package report

import (
	"testing"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/store"
)

func ptr[T any](v T) *T { return &v }

// sess is a completed batlog session from start (seconds) with awake
// minutes and a drain.
func sess(start int64, startPct, awake int, drain float64) history.Session {
	return history.Session{Start: start, StartPct: startPct, End: start + int64(awake)*60,
		AwakeMin: ptr(awake), Drain: ptr(drain), Source: history.SourceBatlog}
}

func TestEffectiveLifeMatchesAManualCalculation(t *testing.T) {
	ss := []history.Session{
		sess(1000, 100, 120, 20), // 100 / 20 %/hr = 5 h = 300 min
		sess(2000, 85, 90, 12.5), // 8 h = 480 min
		sess(3000, 95, 60, 25),   // 4 h = 240 min
		sess(4000, 79, 120, 10),  // started below 80 %: skipped
		sess(5000, 100, 29, 10),  // under 30 min awake: skipped
		sess(500, 100, 120, 10),  // started before the range: skipped
	}
	ongoing := sess(5100, 100, 120, 10)
	ongoing.Ongoing = true
	ss = append(ss, ongoing, sess(5500, 100, 120, 10)) // the last starts after the range
	l, n, ok := EffectiveLife(ss, 1000, 5500)
	// (300 + 480 + 240) / 3 = 340 min
	if !ok || n != 3 || l.Minutes != 340 || l.Sessions != 3 {
		t.Errorf("life = %+v, n = %d, ok = %v; want 340 min over 3", l, n, ok)
	}
}

func TestEffectiveLifeNeedsTwoSessions(t *testing.T) {
	_, n, ok := EffectiveLife([]history.Session{sess(1000, 100, 120, 20)}, 0, 2000)
	if ok || n != 1 {
		t.Errorf("n = %d, ok = %v; want 1, false", n, ok)
	}
}

func TestWorstAndLongestAndAvg(t *testing.T) {
	ss := []history.Session{
		sess(1000, 100, 120, 20),
		sess(2000, 100, 29, 90), // too short to be the worst
		sess(3000, 100, 45, 24.1),
		{Start: 4000, Source: history.SourcePmset},
	}
	if w := Worst(ss, 0); w == nil || w.Start != 3000 {
		t.Errorf("worst = %+v, want the 24.1 %%/hr session", w)
	}
	if l := Longest(ss, 0); l == nil || l.Start != 1000 {
		t.Errorf("longest = %+v, want the 120 min session", l)
	}
	if Worst(nil, 0) != nil || Longest(nil, 0) != nil {
		t.Error("no sessions must give nil")
	}
	// Sessions that began before the range belong to the report before.
	if w, l := Worst(ss, 1001), Longest(ss, 1001); w == nil || w.Start != 3000 || l == nil || l.Start != 3000 {
		t.Errorf("from 1001: worst %+v longest %+v, want the 3000 session for both", w, l)
	}
	if d, ok := AvgDrain(history.Totals{BatterySec: 2 * 3600, PctUsed: 29}); !ok || d != 14.5 {
		t.Errorf("avg = %v %v, want 14.5", d, ok)
	}
	if _, ok := AvgDrain(history.Totals{BatterySec: 299, PctUsed: 1}); ok {
		t.Error("under 5 min on battery must have no average")
	}
}

func TestFlagsFireOnlyAboveTheirThresholds(t *testing.T) {
	cases := []struct {
		name string
		h    Habits
		days int
		want string // the flag ID, or "" for none
	}{
		{"above 90 at 50%", Habits{Above90Share: ptr(0.5)}, 1, ""},
		{"above 90 at 50.1%", Habits{Above90Share: ptr(0.501)}, 1, "above_90"},
		{"plug-in median 15%", Habits{PlugInMedian: ptr(15.0)}, 1, ""},
		{"plug-in median 14.5%", Habits{PlugInMedian: ptr(14.5)}, 1, "runs_low"},
		{"no median", Habits{}, 1, ""},
		{"4 h at 100% in a day", Habits{MinAt100OnAC: 240}, 1, ""},
		{"4 h 01 at 100% in a day", Habits{MinAt100OnAC: 241}, 1, "pinned_100"},
		{"28 h over 7 days", Habits{MinAt100OnAC: 28 * 60}, 7, ""},
		{"28 h 07 over 7 days", Habits{MinAt100OnAC: 28*60 + 7}, 7, "pinned_100"},
	}
	for _, c := range cases {
		fs := Flags(c.h, c.days)
		switch {
		case c.want == "" && len(fs) != 0:
			t.Errorf("%s: flags = %+v, want none", c.name, fs)
		case c.want != "" && (len(fs) != 1 || fs[0].ID != c.want):
			t.Errorf("%s: flags = %+v, want %s", c.name, fs, c.want)
		}
	}
	fs := Flags(Habits{Above90Share: ptr(0.8), PlugInMedian: ptr(12.5), MinAt100OnAC: 300}, 1)
	want := []string{
		"battery spends 80% of time above 90% — consider Optimized Charging or unplugging earlier",
		"you often run very low (median 12.5%) — deep discharges add wear",
		"sat at 100% for 5.0h/day",
	}
	if len(fs) != 3 {
		t.Fatalf("flags = %+v", fs)
	}
	for i, f := range fs {
		if f.Message != want[i] {
			t.Errorf("flag %d = %q, want %q", i, f.Message, want[i])
		}
	}
}

// minutes builds one sample a minute from minute a to b (exclusive) at a
// fixed percent and source.
func minutes(a, b, pct int, onAC bool) []store.Sample {
	var ss []store.Sample
	for m := a; m < b; m++ {
		ss = append(ss, store.Sample{TS: int64(m) * 60, Pct: pct, OnAC: onAC})
	}
	return ss
}

func TestHabits(t *testing.T) {
	var samples []store.Sample
	samples = append(samples, minutes(0, 60, 100, true)...)    // 1 h at 100 % on AC
	samples = append(samples, minutes(60, 120, 50, false)...)  // 1 h at 50 %
	samples = append(samples, minutes(120, 150, 10, false)...) // 30 min at 10 %
	samples = append(samples, minutes(150, 180, 100, true)...) // 30 min at 100 % on AC…
	// …asleep on the charger 6 h, then 30 more minutes at 100 %.
	samples = append(samples, minutes(540, 570, 100, true)...)

	r := history.Result{
		Sessions: []history.Session{sess(3600, 100, 90, 60), sess(99999, 100, 10, 5)},
		Events: []history.Event{
			{TS: 3600, Plugged: false, Pct: 100, Source: history.SourceBatlog},
			{TS: 9000, Plugged: true, Pct: 10, Source: history.SourceBatlog},
			{TS: 50000, Plugged: false, Pct: 96, Source: history.SourceBatlog},
			{TS: 60000, Plugged: true, Pct: 20, Source: history.SourceBatlog},
		},
	}
	h := BuildHabits(r, samples, nil, 0, 600*60)
	// Awake: 0–179 and 540–569 = 208 min (the sleep and the last sample
	// have no awake interval); above 90: 60 + 29 + 29 = 118; below 20: 30.
	if h.Completed != 2 || *h.PlugInMedian != 15 || *h.UnplugMedian != 98 {
		t.Errorf("completed %d, medians %v %v", h.Completed, *h.PlugInMedian, *h.UnplugMedian)
	}
	if got := *h.Above90Share; got != 118.0/208 {
		t.Errorf("above 90 = %v, want %v", got, 118.0/208)
	}
	if got := *h.Below20Share; got != 30.0/208 {
		t.Errorf("below 20 = %v, want %v", got, 30.0/208)
	}
	// 0–59 (59 min; the next sample is on battery), then 150–569 through
	// the sleep: 419 min.
	if h.MaxMinAt100OnAC != 419 || h.MinAt100OnAC != 59+419 {
		t.Errorf("at 100 on AC: longest %d total %d, want 419, 478", h.MaxMinAt100OnAC, h.MinAt100OnAC)
	}

	// A recorder start in the sleep breaks the stretch: nothing is known then.
	h = BuildHabits(r, samples, []int64{300 * 60}, 0, 600*60)
	if h.MaxMinAt100OnAC != 59 || h.MinAt100OnAC != 59+29+29 {
		t.Errorf("with a data gap: longest %d total %d, want 59, 117", h.MaxMinAt100OnAC, h.MinAt100OnAC)
	}

	// One completed session: no medians.
	r.Sessions = r.Sessions[:1]
	if h := BuildHabits(r, samples, nil, 0, 600*60); h.PlugInMedian != nil || h.UnplugMedian != nil {
		t.Error("medians need two completed sessions")
	}
	if h := BuildHabits(history.Result{}, nil, nil, 0, 1); h.Above90Share != nil || h.MaxMinAt100OnAC != 0 {
		t.Errorf("no samples: %+v", h)
	}
	// 29 awake minutes are too few for the shares.
	if h := BuildHabits(history.Result{}, minutes(0, 30, 100, true), nil, 0, 3600); h.Above90Share != nil || h.Below20Share != nil {
		t.Errorf("29 awake minutes: shares %v %v, want none", h.Above90Share, h.Below20Share)
	}
	// A session that began before the range is not counted as completed in it.
	if h := BuildHabits(r, samples, nil, 3601, 600*60); h.Completed != 0 {
		t.Errorf("completed from 3601 = %d, want 0", h.Completed)
	}
}

func TestCharging(t *testing.T) {
	const m = 60
	cs := []history.ChargeSession{
		// Began before the range: the previous report's.
		{Start: -100 * m, StartPct: 10, FullAt: -10 * m, AtFullMin: 500},
		// 20 → 100 in 90 min, left on 60 min at full.
		{Start: 0, StartPct: 20, EndPct: 100, FullAt: 90 * m, AtFullMin: 60},
		// 30 → 100 in 70 min, 120 min at full, 20 min held at 80 % first.
		{Start: 300 * m, StartPct: 30, EndPct: 100, FullAt: 370 * m, AtFullMin: 120, HoldMin: 20},
		// Woke on the charger full: an upper bound, not a time to full.
		{Start: 600 * m, StartPct: 40, EndPct: 100, FullAt: 900 * m, FullUpperBound: true, AtFullMin: 10},
		// Unplugged at 96 %.
		{Start: 1000 * m, StartPct: 50, EndPct: 95, MaxPct: 96},
		// Plugged in full: at full, but not reached.
		{Start: 1100 * m, StartPct: 100, EndPct: 100, FullAt: 1100 * m, AtFullMin: 30},
		// Ongoing and full for 600 min so far: the longest, not in the median.
		{Start: 1200 * m, StartPct: 60, EndPct: 100, FullAt: 1250 * m, AtFullMin: 600, Ongoing: true},
	}
	c := BuildCharging(cs, 0)
	if c.Charges != 6 || c.ReachedFull != 4 || c.MaxAtFull != 600 || c.NotChargingMin != 20 {
		t.Errorf("charging = %+v", c)
	}
	// Starts 20 30 40 50 100 60 → 45; exact times to full 90 70 50 → 70;
	// finished at full 60 120 10 30 → 45.
	if *c.StartMedian != 45 || *c.ToFullMedian != 70 || *c.AtFullMedian != 45 {
		t.Errorf("medians: start %v, to full %v, at full %v", *c.StartMedian, *c.ToFullMedian, *c.AtFullMedian)
	}
	if len(c.StoppedBelow) != 1 || c.StoppedBelow[0] != 96 {
		t.Errorf("stopped below = %v", c.StoppedBelow)
	}

	if e := BuildCharging(cs[:1], 0); e.Charges != 0 || e.StartMedian != nil || e.ToFullMedian != nil || e.AtFullMedian != nil {
		t.Errorf("only an earlier charge: %+v", e)
	}
}
