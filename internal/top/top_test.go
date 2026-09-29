package top

import (
	"fmt"
	"math"
	"testing"

	"github.com/jufianto/batlog/internal/energy"
	"github.com/jufianto/batlog/internal/store"
)

// minute samples [from, to) of a bucket-aligned clock; ts in seconds.
func minutes(from, to int64, onAC bool, watts float64) []store.Sample {
	var out []store.Sample
	for ts := from; ts < to; ts += 60 {
		out = append(out, store.Sample{TS: ts, OnAC: onAC, Watts: watts})
	}
	return out
}

func e(ts int64, app string, j float64) store.AppEnergy {
	return store.AppEnergy{TS: ts, App: app, Energy: j}
}

func TestSharesAndBatteryCost(t *testing.T) {
	// Buckets 0 and 900. Bucket 0 is on AC, bucket 900 on battery; the
	// range starts partway into bucket 0 and ends partway into bucket 900.
	samples := append(minutes(0, 900, true, 20), minutes(900, 1800, false, 10)...)
	buckets := []store.AppEnergy{
		e(0, "Xcode", 600), e(0, "Slack", 200), // only part of each is in range
		e(900, "Brave Browser", 400), e(900, "Slack", 200), e(900, "WindowServer", 200),
	}
	buckets[4].System = true
	rows := Build(buckets, samples, 450, 1350, 10)
	const w1 = 8.0 / 15

	want := []struct {
		app    string
		share  float64
		cost   float64
		system bool
	}{
		// Bucket 0 has 7 of its 15 samples in range (08:00–14:00 of it),
		// bucket 900 has 8: Xcode 280, Slack 200, Brave 213⅓, WindowServer
		// 106⅔ → 800 J. Only bucket 900 was on battery: 426⅔ J.
		{"Xcode", 280.0 / 800, 0, false},
		{"Brave Browser", 400 * w1 / 800, 5, false},
		{"Slack", 200.0 / 800, 2.5, false},
		{"WindowServer", 200 * w1 / 800, 2.5, true},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	var shares, costs float64
	for i, w := range want {
		r := rows[i]
		if r.App != w.app || r.System != w.system || math.Abs(r.Share-w.share) > 1e-9 || r.BatteryPct == nil || math.Abs(*r.BatteryPct-w.cost) > 1e-9 {
			t.Errorf("row %d = %+v (cost %v), want %+v", i, r, deref(r.BatteryPct), w)
		}
		shares += r.Share
		costs += deref(r.BatteryPct)
	}
	if math.Abs(shares-1) > 1e-9 || math.Abs(costs-10) > 1e-9 {
		t.Errorf("shares sum %v, costs sum %v; want 1 and the percent used", shares, costs)
	}
	if math.Abs(rows[0].EnergyJ-280) > 1e-9 {
		t.Errorf("EnergyJ = %v, want the in-range joules", rows[0].EnergyJ)
	}
}

func deref(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

func TestNoBatteryTimeHasNoCost(t *testing.T) {
	rows := Build([]store.AppEnergy{e(0, "Xcode", 10)}, minutes(0, 900, true, 30), 0, 900, 0)
	if len(rows) != 1 || rows[0].BatteryPct != nil || rows[0].Share != 1 {
		t.Errorf("rows = %+v, want share 1 and no battery cost", rows)
	}
}

func TestBucketWithoutSamplesWeighsByTime(t *testing.T) {
	// Asleep for the whole bucket except a tick's energy: no samples at all.
	rows := Build([]store.AppEnergy{e(900, "Mail", 100), e(0, "Notes", 100)}, minutes(0, 900, false, 5), 0, 1350, 3)
	if len(rows) != 2 || rows[0].App != "Notes" || rows[1].App != "Mail" || math.Abs(rows[1].EnergyJ-50) > 1e-9 {
		t.Errorf("rows = %+v, want Notes 100 J then Mail 50 J (half its bucket by time)", rows)
	}
	if rows[1].BatteryPct == nil || *rows[1].BatteryPct != 0 {
		t.Errorf("Mail cost = %v, want 0: no sample says it was on battery", deref(rows[1].BatteryPct))
	}
}

func TestTiesSortByName(t *testing.T) {
	rows := Build([]store.AppEnergy{e(0, "b", 1), e(0, "a", 1), e(0, "c", 2)}, nil, 0, 900, 0)
	var got []string
	for _, r := range rows {
		got = append(got, r.App)
	}
	if fmt.Sprint(got) != "[c a b]" {
		t.Errorf("order = %v", got)
	}
	if Build(nil, nil, 0, 900, 5) != nil {
		t.Error("no energy, no rows")
	}
}

func TestHeaviest(t *testing.T) {
	ss := append(minutes(0, 1800, false, 8), minutes(1800, 3600, false, 25)...)
	ss = append(ss, minutes(3600, 5400, false, 12)...)
	h, ok := Heaviest(ss, 0, 5400)
	if !ok || h.Start != 1800 || h.End != 3600 || h.AvgWatts != 25 {
		t.Errorf("Heaviest = %+v %v, want 1800-3600 at 25 W", h, ok)
	}

	// A sleep gap breaks a window; AC samples are not battery drain.
	ss = append(minutes(0, 1200, false, 30), minutes(4000, 5200, false, 30)...)
	ss = append(ss, minutes(5200, 7000, true, 60)...)
	if h, ok := Heaviest(ss, 0, 7000); ok {
		t.Errorf("Heaviest = %+v, want none: no 30 awake minutes on battery", h)
	}
}

func TestLive(t *testing.T) {
	rows := Live([]energy.Delta{{App: "Zoom", CPU: 3e9, GPU: 1e9}, {App: "kernel_task", System: true, CPU: 1e9}})
	if len(rows) != 2 || rows[0].App != "Zoom" || rows[0].Share != 0.8 || rows[0].EnergyJ != 4 || rows[0].BatteryPct != nil || !rows[1].System {
		t.Errorf("Live = %+v", rows)
	}
}
