package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/energy"
	"github.com/jufianto/batlog/internal/store"
)

// stubTop seeds daySegs (see history_test.go), app energy per bucket
// {minute of the bucket start, app, joules, system}, and a live probe.
func stubTop(t *testing.T, nowMin int, energyAt ...[4]any) *histFix {
	t.Helper()
	f := stubHistory(t, nowMin, daySegs, nil, nil, nil)
	db, err := store.Open(f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, e := range energyAt {
		d := store.EnergyDelta{App: e[1].(string), CPU: uint64(e[2].(float64) * 1e9), System: e[3].(bool)}
		if err := db.AddEnergy(context.Background(), histAt(e[0].(int)), []store.EnergyDelta{d}); err != nil {
			t.Fatal(err)
		}
	}
	stubLive(t)
	return f
}

// stubLive makes the live probe see Zoom use 3 J and kernel_task 1 J.
func stubLive(t *testing.T) {
	oldRead, oldWait := readEnergy, liveWait
	calls := 0
	readEnergy = func(named func(uint64) bool) ([]energy.Reading, error) {
		calls++
		k := uint64(calls)
		zoom := energy.Reading{Coalition: 1, CPU: k * 3e9}
		kern := energy.Reading{Coalition: 2, CPU: k * 1e9}
		if !named(1) {
			zoom.Members = []energy.Member{{PID: 10, Path: "/Applications/zoom.us.app/Contents/MacOS/zoom.us"}}
			kern.Members = []energy.Member{{PID: 0, Comm: "kernel_task"}}
		}
		return []energy.Reading{zoom, kern}, nil
	}
	liveWait = func() {}
	t.Cleanup(func() { readEnergy, liveWait = oldRead, oldWait })
}

// dayEnergy: Xcode on AC at 01:00, Brave and WindowServer on battery at 02:00.
var dayEnergy = [][4]any{
	{60, "Xcode", 200.0, false},
	{120, "Brave Browser", 300.0, false},
	{120, "WindowServer", 100.0, true},
}

func TestTopTodayHuman(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	got, err := run(t, "top")
	if err != nil {
		t.Fatal(err)
	}
	want := "⚡ top energy · today   (3h 58m awake, 2h 58m on battery, 17% used)\n" +
		" #   APP              SHARE   BATTERY COST\n" +
		" 1   Brave Browser    50%     ≈ 8% of battery\n" +
		" 2   Xcode            33%     ≈ 0%\n" +
		" 3   WindowServer ⚙   17%     ≈ 3%\n" +
		// 00:00–01:00 used 6% with no app energy recorded: nobody is charged for it.
		"app energy from about 01:00 only, when recording started; battery cost covers the 11% used since\n" +
		topFootnote + "\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTopJSONAndLimit(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	out, err := run(t, "top", "--today", "-n", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		PctUsed     int    `json:"pct_used"`
		CostPctUsed int    `json:"cost_pct_used"`
		EnergySince *int64 `json:"energy_since"`
		Rows        []struct {
			App      string   `json:"app"`
			Share    float64  `json:"share"`
			Est      *float64 `json:"est_battery_pct"`
			IsSystem bool     `json:"is_system"`
			EnergyJ  float64  `json:"energy_j"`
		} `json:"rows"`
		Session *struct{} `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if len(j.Rows) != 2 || j.Rows[0].App != "Brave Browser" || j.Rows[0].Share != 0.5 || j.Rows[0].EnergyJ != 300 ||
		j.Rows[1].Est == nil || *j.Rows[1].Est != 0 || j.Session != nil ||
		j.PctUsed != 17 || j.CostPctUsed != 11 || j.EnergySince == nil || *j.EnergySince != histAt(60) {
		t.Errorf("json = %s", out)
	}
}

func TestTopSession(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	got, err := run(t, "top", "--session", "0926-0200")
	if err != nil {
		t.Fatal(err)
	}
	want := "⚡ session 0926-0200 · 02:00 → now · 1h 58m awake · 100% → 88% (5.6 %/hr)\n" +
		" #   APP              SHARE   BATTERY COST\n" +
		" 1   Brave Browser    75%     ≈ 8% of battery\n" +
		" 2   WindowServer ⚙   25%     ≈ 3%\n" +
		topFootnote + "\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	last, err := run(t, "top", "--session", "last")
	if err != nil || last != got {
		t.Errorf("--session last = %v\n%s\nwant the same as the newest session's ID", err, last)
	}
}

func TestTopSessionAcrossMidnightHasNoEnergyBeforeRecording(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	got, err := run(t, "top", "--session", "0925-2310")
	if err != nil {
		t.Fatal(err)
	}
	want := "⚡ session 0925-2310 · yesterday 23:10 → 01:00 · 1h 50m awake · 100% → 90% (5.5 %/hr)\n" +
		"no app energy for this session (recording started 01:00)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTopErrors(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	var ue usageError
	for _, args := range [][]string{
		{"top", "--live", "--today"},
		{"top", "--session", "last", "--week"},
		{"top", "--session", "yesterday"},
		{"top", "-n", "0"},
	} {
		if _, err := run(t, args...); !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
	_, err := run(t, "top", "--session", "0926-0930")
	if err == nil || errors.As(err, &ue) || !strings.Contains(err.Error(), "no battery session 0926-0930") {
		t.Errorf("unknown session: err = %v, want an operational error", err)
	}
}

func TestTopWithoutEnergyFallsBackToLive(t *testing.T) {
	stubTop(t, 719) // samples, but no app energy yet
	got, err := run(t, "top")
	if err != nil {
		t.Fatal(err)
	}
	want := "no app energy recorded yet (`batlog daemon install` records it); showing the last second\n" +
		"⚡ top energy · live (1 s)\n" +
		" #   APP             SHARE\n" +
		" 1   zoom.us         75%\n" +
		" 2   kernel_task ⚙   25%\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTopLiveJSON(t *testing.T) {
	stubLive(t)
	stubStatus(t, battery.Snapshot{}, "/nonexistent/batlog.db")
	out, err := run(t, "top", "--live", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"live":true`) || !strings.Contains(out, `{"app":"zoom.us","share":0.75,"is_system":false,"energy_j":3}`) || strings.Contains(out, "est_battery_pct") {
		t.Errorf("json = %s", out)
	}
}

func TestTopSinceWeighsAStraddledBucketByAllItsSamples(t *testing.T) {
	// --since 10h at 11:59 starts at 01:59, inside the 01:45 bucket (on AC):
	// 1 of its 15 samples is in range, so 10 of Xcode's 150 J count.
	stubTop(t, 719, [4]any{105, "Xcode", 150.0, false}, [4]any{660, "Brave Browser", 100.0, false})
	out, err := run(t, "top", "--since", "10h", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		Rows []struct {
			App     string  `json:"app"`
			EnergyJ float64 `json:"energy_j"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if len(j.Rows) != 2 || j.Rows[0].App != "Brave Browser" || j.Rows[0].EnergyJ != 100 || j.Rows[1].EnergyJ != 10 {
		t.Errorf("rows = %+v, want Brave 100 J then Xcode 10 J", j.Rows)
	}
}

func TestTopWeekWithoutEnergyDoesNotGoLive(t *testing.T) {
	stubTop(t, 719)
	_, err := run(t, "top", "--week")
	var ue usageError
	if err == nil || errors.As(err, &ue) || !strings.Contains(err.Error(), "no app energy recorded yet") {
		t.Errorf("err = %v, want an operational error, not a live view", err)
	}
}
