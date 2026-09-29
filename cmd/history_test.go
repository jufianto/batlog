package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/pmset"
	"github.com/jufianto/batlog/internal/store"
)

// histMidnight is the day under test; histAt counts minutes from it.
var histMidnight = time.Date(2026, 9, 26, 0, 0, 0, 0, time.Local)

func histAt(min int) int64 { return histMidnight.Unix() + int64(min)*60 }

type histFix struct {
	db        string
	pmsetRuns int
}

// stubHistory seeds a database with one tick a minute over each segment
// {from, to, p0, p1, onAC} and makes now histAt(nowMin).
func stubHistory(t *testing.T, nowMin int, segs [][5]int, runs []int64, pm []pmset.Reading, pmErr error) *histFix {
	t.Helper()
	f := &histFix{db: filepath.Join(t.TempDir(), "batlog.db")}
	if segs != nil {
		db, err := store.Open(f.db, false)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		for _, sg := range segs {
			n := sg[1] - sg[0]
			for k := 0; k < n; k++ {
				p := sg[2]
				if n > 1 {
					p = sg[2] + (sg[3]-sg[2])*k/(n-1)
				}
				if err := db.WriteTick(ctx, store.Tick{TS: histAt(sg[0] + k), Pct: p, OnAC: sg[4] == 1}); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, r := range runs {
			if err := db.RecordRunStart(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
	}
	stubStatus(t, battery.Snapshot{}, f.db)
	oldNow, oldPm, oldWoke := now, readPmset, wokeAt
	now = func() time.Time { return time.Unix(histAt(nowMin), 0) }
	readPmset = func(context.Context) ([]pmset.Reading, error) { f.pmsetRuns++; return pm, pmErr }
	wokeAt = func() (time.Time, time.Time, bool) { return time.Time{}, time.Time{}, false }
	t.Cleanup(func() { now, readPmset, wokeAt = oldNow, oldPm, oldWoke })
	return f
}

// daySegs: AC until 23:10 yesterday, battery across midnight until 01:00,
// AC until 02:00, battery 02:00–03:00, asleep 8 h, battery 11:00–12:00.
var daySegs = [][5]int{
	{-60, -50, 100, 100, 1},
	{-50, 60, 100, 90, 0},
	{60, 120, 90, 100, 1},
	{120, 180, 100, 95, 0},
	{660, 720, 94, 88, 0},
}

func TestHistoryTodayHuman(t *testing.T) {
	f := stubHistory(t, 719, daySegs, nil, nil, nil)
	got, err := run(t, "history")
	if err != nil {
		t.Fatal(err)
	}
	want := "📅 Today, Sat 26 Sep\n" +
		"first charge       01:00  (at 90%)\n" +
		"last on battery    02:00  (at 100%)\n" +
		"battery lasted     ongoing, 1h 58m so far\n" +
		"\n" +
		"battery sessions\n" +
		"  0925-2310   yesterday 23:10 → 01:00   1h 50m awake    100% → 90%   5.5 %/hr\n" +
		"  0926-0200   02:00 → now               1h 58m so far   100% → 88%   5.6 %/hr   (ongoing)\n" +
		"\n" +
		"on battery 2h 58m · on AC 1h 00m · asleep 8h 01m\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if f.pmsetRuns != 0 {
		t.Error("pmset must not run when the daemon covers the whole range")
	}
}

func TestHistoryDataGapShown(t *testing.T) {
	stubHistory(t, 719, daySegs, []int64{histAt(300)}, nil, nil)
	got, _ := run(t, "history")
	for _, w := range []string{"(ongoing) (data gap)", "· no data 8h 01m", "asleep 0m"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
}

type histOut struct {
	Range       struct{ From, To int64 }
	FirstCharge *struct {
		TS     int64  `json:"ts"`
		Type   string `json:"type"`
		Pct    int    `json:"pct"`
		Source string `json:"source"`
	} `json:"first_charge"`
	BatteryLasted *struct {
		Minutes int  `json:"minutes"`
		Ongoing bool `json:"ongoing"`
	} `json:"battery_lasted"`
	Sessions []map[string]any `json:"sessions"`
	Totals   map[string]int   `json:"totals"`
}

func TestHistoryJSON(t *testing.T) {
	stubHistory(t, 719, daySegs, nil, nil, nil)
	got, err := run(t, "history", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var j histOut
	if err := json.Unmarshal([]byte(got), &j); err != nil {
		t.Fatalf("%v: %s", err, got)
	}
	if j.Range.From != histMidnight.Unix() || j.Range.To != histAt(719) {
		t.Errorf("range = %+v", j.Range)
	}
	if j.FirstCharge == nil || j.FirstCharge.Type != "plugged" || j.FirstCharge.Source != "batlog" || j.FirstCharge.TS != histAt(60) {
		t.Errorf("first_charge = %+v", j.FirstCharge)
	}
	if j.BatteryLasted == nil || !j.BatteryLasted.Ongoing || j.BatteryLasted.Minutes != 118 {
		t.Errorf("battery_lasted = %+v", j.BatteryLasted)
	}
	if len(j.Sessions) != 2 {
		t.Fatalf("sessions = %v", j.Sessions)
	}
	keys := []string{"id", "start", "end", "awake_minutes", "start_pct", "end_pct", "drain_pct_per_hr", "ongoing", "data_gap", "source"}
	for _, k := range keys {
		if _, ok := j.Sessions[1][k]; !ok {
			t.Errorf("session missing %q", k)
		}
	}
	if j.Sessions[0]["id"] != "0925-2310" || j.Sessions[1]["id"] != "0926-0200" {
		t.Errorf("ids = %v, %v", j.Sessions[0]["id"], j.Sessions[1]["id"])
	}
	if j.Sessions[1]["end"] != nil || j.Sessions[0]["end"] != float64(histAt(60)) {
		t.Errorf("ends = %v, %v; want 01:00 and null", j.Sessions[0]["end"], j.Sessions[1]["end"])
	}
	want := map[string]int{"battery_min": 178, "ac_min": 60, "sleep_min": 481, "gap_min": 0}
	for k, v := range want {
		if j.Totals[k] != v {
			t.Errorf("totals[%s] = %d, want %d", k, j.Totals[k], v)
		}
	}
}

func TestHistoryEventsJSONAscending(t *testing.T) {
	stubHistory(t, 719, daySegs, nil, nil, nil)
	got, err := run(t, "history", "--events", "--json", "--since", "2d")
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		Range  map[string]int64 `json:"range"`
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(got), &j); err != nil {
		t.Fatal(err)
	}
	var types []string
	last := 0.0
	for _, e := range j.Events {
		if len(e) != 4 {
			t.Errorf("event %v, want exactly ts, type, pct, source", e)
		}
		ts := e["ts"].(float64)
		if ts < last {
			t.Errorf("events not ascending: %v", j.Events)
		}
		last = ts
		types = append(types, e["type"].(string))
	}
	if strings.Join(types, ",") != "unplugged,plugged,unplugged" {
		t.Errorf("types = %v", types)
	}
}

func TestHistoryEventsHuman(t *testing.T) {
	stubHistory(t, 719, daySegs, nil, nil, nil)
	got, _ := run(t, "history", "--events")
	want := "📅 Today, Sat 26 Sep\n" +
		"  01:00   plugged     90%\n" +
		"  02:00   unplugged   100%\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestHistoryPmsetFallback(t *testing.T) {
	// Installed at 01:00; pmset knows the hour before.
	f := stubHistory(t, 119, [][5]int{{60, 120, 97, 94, 0}}, nil, []pmset.Reading{
		{TS: histAt(-30), OnAC: false, Pct: 80},
		{TS: histAt(10), OnAC: true, Pct: 70},
		{TS: histAt(30), OnAC: false, Pct: 100},
		{TS: histAt(50), OnAC: false, Pct: 98},
	}, nil)
	got, err := run(t, "history")
	if err != nil {
		t.Fatal(err)
	}
	if f.pmsetRuns != 1 {
		t.Errorf("pmset ran %d times, want 1", f.pmsetRuns)
	}
	for _, w := range []string{
		"first charge       00:10  (at 70%)  (pmset)\n",
		"last on battery    00:30  (at 100%)  (pmset)\n",
		"  0926-0030   00:30 → 01:00   —            100% → 98%              (pmset)\n",
		"  0926-0100   01:00 → now     59m so far   97% → 94%    3.1 %/hr   (ongoing)\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
}

func TestHistoryPmsetFailureIsAWarning(t *testing.T) {
	stubHistory(t, 119, [][5]int{{60, 120, 97, 94, 0}}, nil, nil, errors.New("exit status 1"))
	got, err := run(t, "history")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "warning: no pmset fallback: exit status 1") || !strings.Contains(got, "(ongoing)") {
		t.Errorf("got\n%s", got)
	}
}

func TestHistoryNoData(t *testing.T) {
	stubHistory(t, 600, nil, nil, nil, nil)
	got, err := run(t, "history")
	if err != nil {
		t.Fatal(err)
	}
	want := "📅 Today, Sat 26 Sep\nno charge/discharge events in this range\nno history yet: `batlog daemon install` starts recording\n"
	if got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestHistoryBeforeInstallSaysWhereHistoryStarts(t *testing.T) {
	stubHistory(t, 600, [][5]int{{540, 601, 100, 100, 1}}, nil, nil, nil)
	got, _ := run(t, "history")
	want := "📅 Today, Sat 26 Sep\n" +
		"no charge/discharge events in this range\n" +
		"history starts Sat 26 Sep 09:00\n" +
		"\n" +
		"on battery 0m · on AC 1h 00m · asleep 0m\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestHistoryRanges(t *testing.T) {
	n := time.Date(2026, 9, 26, 14, 30, 0, 0, time.Local)
	cases := []struct {
		week  bool
		since string
		from  time.Time
		title string
	}{
		{false, "", time.Date(2026, 9, 26, 0, 0, 0, 0, time.Local), "Today, Sat 26 Sep"},
		{true, "", time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local), "Last 7 days, Sun 20 Sep → Sat 26 Sep"},
		{false, "3d", n.AddDate(0, 0, -3), "Since Wed 23 Sep 14:30"},
		{false, "12h", n.Add(-12 * time.Hour), "Since Sat 26 Sep 02:30"},
		{false, "2026-06-01", time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local), "Since Mon 01 Jun 00:00"},
	}
	for _, c := range cases {
		rg, err := parseRange(false, c.week, c.since, n)
		if err != nil || !rg.From.Equal(c.from) || !rg.To.Equal(n) || rg.Title != c.title {
			t.Errorf("week=%v since=%q: %+v, %v", c.week, c.since, rg, err)
		}
	}
}

func TestHistoryUsageErrors(t *testing.T) {
	stubHistory(t, 600, nil, nil, nil, nil)
	for _, args := range [][]string{
		{"history", "--today", "--week"},
		{"history", "--week", "--since", "3d"},
		{"history", "--since", "3w"},
		{"history", "--since", "-3d"},
		{"history", "--since", "0h"},
		{"history", "--since", "d"},
		{"history", "--since", "2026-13-01"},
		{"history", "--since", "2026-09-27"},
		{"history", "extra"},
	} {
		_, err := run(t, args...)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
}

func TestHistorySessionFromTheFirstSampleEver(t *testing.T) {
	// Installed on battery at 23:10 yesterday, never on AC before midnight.
	stubHistory(t, 119, [][5]int{{-50, 60, 100, 90, 0}, {60, 120, 90, 100, 1}}, nil, nil, nil)
	got, _ := run(t, "history")
	if !strings.Contains(got, "  yesterday 23:10 → 01:00   1h 50m awake   100% → 90%   5.5 %/hr\n") {
		t.Errorf("got\n%s", got)
	}
}

func TestHistoryEventsSaysSoWhenThereAreNone(t *testing.T) {
	// On battery since yesterday: a session today, but no event.
	stubHistory(t, 119, [][5]int{{-60, -50, 100, 100, 1}, {-50, 120, 100, 80, 0}}, nil, nil, nil)
	got, _ := run(t, "history", "--events")
	if got != "📅 Today, Sat 26 Sep\nno charge/discharge events in this range\n" {
		t.Errorf("got\n%s", got)
	}
}

func TestHistoryPmsetBeforeInstall(t *testing.T) {
	f := stubHistory(t, 120, nil, nil, []pmset.Reading{
		{TS: histAt(-30), OnAC: true, Pct: 80}, {TS: histAt(10), OnAC: false, Pct: 100}, {TS: histAt(100), OnAC: false, Pct: 90},
	}, nil)
	got, err := run(t, "history")
	if err != nil {
		t.Fatal(err)
	}
	if f.pmsetRuns != 1 || !strings.Contains(got, "last on battery    00:10  (at 100%)  (pmset)") ||
		!strings.Contains(got, "  00:10 → now   —   100% → 90%      (ongoing) (pmset)\n") ||
		!strings.HasSuffix(got, "\nno history yet: `batlog daemon install` starts recording\n") {
		t.Errorf("pmset ran %d times; got\n%s", f.pmsetRuns, got)
	}
}

func TestHistoryRunStartBeforeTheRange(t *testing.T) {
	// Went on battery 23:10, daemon restarted 23:50 inside the gap that
	// crosses midnight: the session still carries (data gap).
	stubHistory(t, 119, [][5]int{{-60, -50, 100, 100, 1}, {-50, -30, 100, 98, 0}, {60, 120, 97, 94, 0}},
		[]int64{histAt(-10)}, nil, nil)
	got, _ := run(t, "history")
	if !strings.Contains(got, "(ongoing) (data gap)") {
		t.Errorf("got\n%s", got)
	}
}

func TestHistorySinceTooFarBack(t *testing.T) {
	stubHistory(t, 600, nil, nil, nil, nil)
	for _, v := range []string{"3000000h", "99999999999999d", "36501d"} {
		_, err := run(t, "history", "--since", v)
		if err == nil || !strings.Contains(err.Error(), "want a duration") {
			t.Errorf("--since %s: err = %v, want the usage message", v, err)
		}
	}
}

func TestHistoryUsesTheWakeTime(t *testing.T) {
	stubHistory(t, 1200, daySegs, nil, nil, nil)
	wokeAt = func() (time.Time, time.Time, bool) {
		return time.Unix(histAt(720), 0), time.Unix(histAt(1199), 0), true
	}
	got, _ := run(t, "history")
	if strings.Contains(got, "data gap") || !strings.Contains(got, "asleep 16h") {
		t.Errorf("got\n%s", got)
	}
}

func TestHistoryRangesWhereDSTSkipsMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago") // 6 Sep 2026 starts at 01:00
	if err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 9, 6, 1, 0, 0, 0, loc)
	for _, c := range []struct {
		n            time.Time
		week         bool
		since, title string
	}{
		{time.Date(2026, 9, 6, 14, 30, 0, 0, loc), false, "", "Today, Sun 06 Sep"},
		{time.Date(2026, 9, 12, 14, 30, 0, 0, loc), true, "", "Last 7 days, Sun 06 Sep → Sat 12 Sep"},
		{time.Date(2026, 9, 12, 14, 30, 0, 0, loc), false, "2026-09-06", "Since Sun 06 Sep 01:00"},
	} {
		rg, err := parseRange(false, c.week, c.since, c.n)
		if err != nil || !rg.From.Equal(day1) || rg.Title != c.title {
			t.Errorf("week=%v since=%q: from %s title %q (%v), want %s", c.week, c.since, rg.From, rg.Title, err, day1)
		}
	}
	// 01:30 on 6 Sep is "yesterday" on the 7th, not two days back.
	if got := clock(time.Date(2026, 9, 6, 1, 30, 0, 0, loc).Unix(), time.Date(2026, 9, 7, 9, 0, 0, 0, loc)); got != "yesterday 01:30" {
		t.Errorf("clock = %q", got)
	}
	if got := clock(time.Date(2026, 9, 5, 23, 30, 0, 0, loc).Unix(), time.Date(2026, 9, 7, 9, 0, 0, 0, loc)); got != "Sat 05 Sep 23:30" {
		t.Errorf("clock = %q", got)
	}
}
