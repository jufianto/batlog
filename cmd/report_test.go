package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jufianto/batlog/internal/store"
)

// seedHealth adds daily health rows {day, raw max mAh} with a 5 000 mAh design.
func seedHealth(t *testing.T, path string, rows ...[2]any) {
	t.Helper()
	db, err := store.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	design := 5000
	for i, r := range rows {
		raw := r[1].(int)
		tick := store.Tick{TS: histAt(-2000 - i), Pct: 100, OnAC: true,
			Health: &store.HealthDay{Day: r[0].(string), RawMaxMAh: &raw, DesignMAh: &design}}
		if err := db.WriteTick(context.Background(), tick); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReportDailyHuman(t *testing.T) {
	f := stubTop(t, 719, dayEnergy...)
	seedHealth(t, f.db, [2]any{"2026-08-26", 4500}, [2]any{"2026-09-25", 4480})
	got, err := run(t, "report")
	if err != nil {
		t.Fatal(err)
	}
	want := "📊 batlog report · today, Sat 26 Sep\n" +
		"── battery life ──────────────────────────────────\n" +
		"on battery 2h 58m · on AC 1h 00m · asleep 8h 01m\n" +
		"longest session 1h 58m (0926-0200, ongoing)\n" +
		"  0925-2310   yesterday 23:10 → 01:00   1h 50m awake    100% → 90%   5.5 %/hr\n" +
		"  0926-0200   02:00 → now               1h 58m so far   100% → 88%   5.6 %/hr   (ongoing)\n" +
		"── drain ─────────────────────────────────────────\n" +
		"avg 5.7 %/hr · worst 5.6 %/hr (0926-0200, 02:00) — top app Brave Browser (75%)\n" +
		"── top apps ──────────────────────────────────────\n" +
		" #   APP              SHARE   BATTERY COST\n" +
		" 1   Brave Browser    50%     ≈ 8% of battery\n" +
		" 2   Xcode            33%     ≈ 0%\n" +
		" 3   WindowServer ⚙   17%     ≈ 3%\n" +
		"app energy from about 01:00 only, when recording started; battery cost covers the 11% used since\n" +
		topFootnote + "\n" +
		"── habits & health ───────────────────────────────\n" +
		// 0925-2310 began yesterday: it is listed, but yesterday's to judge.
		"charging habits: not enough sessions yet (0)\n" +
		"above 90% 89% of the time · below 20% 0%\n" +
		"⚠ battery spends 89% of time above 90% — consider Optimized Charging or unplugging earlier\n" +
		// 90.0 % → 89.6 % over 30 days.
		"health 89.6% (−0.40 %/month)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReportJSON(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	out, err := run(t, "report", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var j map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	for _, k := range []string{"range", "kind", "battery", "drain", "top_apps", "energy_since", "habits", "health"} {
		if _, ok := j[k]; !ok {
			t.Errorf("missing %q in %s", k, out)
		}
	}
	for _, want := range []string{
		`"kind":"daily"`,
		`"effective_life":null`,
		`"worst":{"session_id":"0926-0200","drain_pct_per_hr":5.6,"top_app":{"app":"Brave Browser","share":0.75}}`,
		`"plug_in_median_pct":null`,
		`"flags":[{"id":"above_90",`,
		`"health":null`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("json lacks %s:\n%s", want, out)
		}
	}
}

func TestReportNeedsDaemonHistory(t *testing.T) {
	for name, segs := range map[string][][5]int{"no database": nil, "no samples": {}} {
		stubHistory(t, 719, segs, nil, nil, nil)
		_, err := run(t, "report")
		var ue usageError
		if !errors.Is(err, errNoHistory) || errors.As(err, &ue) {
			t.Errorf("%s: err = %v, want errNoHistory (exit 1)", name, err)
		}
	}
}

func TestReportUsageErrors(t *testing.T) {
	stubTop(t, 719, dayEnergy...)
	var ue usageError
	for _, args := range [][]string{
		{"report", "--daily", "--weekly"},
		{"report", "--weekly", "--since", "3d"},
		{"report", "--since", "soon"},
	} {
		if _, err := run(t, args...); !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
}

func TestReportDegradesWithoutEnergyOrHealth(t *testing.T) {
	stubTop(t, 719) // samples only
	got, err := run(t, "report")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "── top apps ──────────────────────────────────────\nno app energy recorded yet\n") ||
		!strings.Contains(got, "avg 5.7 %/hr · worst 5.6 %/hr (0926-0200, 02:00)\n") ||
		strings.Contains(got, "\nhealth ") || !strings.Contains(got, "── habits & health") {
		t.Errorf("got:\n%s", got)
	}
}

// weekSegs: two sessions this week and two the week before, each followed
// by a plug-in at the percent it ended on.
//
//	this week   100→80 over 120 min = 10 %/hr → 600 min; 100→85 over 60 min = 15 %/hr → 400 min
//	last week   100→76 over 120 min = 12 %/hr → 500 min;  90→80 over 60 min = 10 %/hr → 600 min
//
// Effective life: (600+400)/2 = 500 min now, (500+600)/2 = 550 before: ▼ 50m.
var weekSegs = [][5]int{
	{-15010, -15000, 100, 100, 1}, {-15000, -14880, 100, 76, 0}, {-14880, -14870, 76, 76, 1},
	{-13010, -13000, 90, 90, 1}, {-13000, -12940, 90, 80, 0}, {-12940, -12930, 80, 80, 1},
	{-7210, -7200, 100, 100, 1}, {-7200, -7080, 100, 80, 0}, {-7080, -7070, 80, 80, 1},
	{-5770, -5760, 100, 100, 1}, {-5760, -5700, 100, 85, 0}, {-5700, -5690, 85, 85, 1},
}

func TestReportWeeklyEffectiveLife(t *testing.T) {
	stubHistory(t, 719, weekSegs, nil, nil, nil)
	got, err := run(t, "report", "--weekly")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"📊 batlog report · 20 Sep – 26 Sep\n",
		"est. full-charge life 8h 20m (▼ 50m vs last week)\n",
		"you typically plug in at 82.5% and unplug at 100%\n",
		"── top apps ──────────────────────────────────────\nno app energy recorded yet\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q in:\n%s", want, got)
		}
	}
	out, err := run(t, "report", "--weekly", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"effective_life":{"minutes":500,"sessions":2},"effective_life_change_min":-50`) {
		t.Errorf("json = %s", out)
	}

	// --since has the life but nothing to compare with.
	got, err = run(t, "report", "--since", "6d")
	if err != nil || !strings.Contains(got, "est. full-charge life 8h 20m\n") {
		t.Errorf("--since 6d: %v\n%s", err, got)
	}
}

func TestReportWeeklyNeedsTwoSessionsForLife(t *testing.T) {
	stubHistory(t, 719, weekSegs[6:9], nil, nil, nil)
	got, err := run(t, "report", "--weekly")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"est. full-charge life: not enough sessions yet (1; needs 2 that start at 80% or more and run 30m or more)\n",
		"charging habits: not enough sessions yet (1)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q in:\n%s", want, got)
		}
	}
}
