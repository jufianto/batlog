package status

import (
	"math"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

var now = time.Unix(2_000_000, 0)

// samples builds one row per minute ending at now, oldest first.
func samples(pcts []int, onAC bool) []store.Sample {
	n := len(pcts)
	out := make([]store.Sample, n)
	for i, p := range pcts {
		out[i] = store.Sample{TS: now.Unix() - int64(60*(n-1-i)), Pct: p, OnAC: onAC}
	}
	return out
}

var onBattery = battery.Snapshot{Percent: 67, Watts: 8.4, HasWatts: true, MacOSMinutes: 312, HasMacOSMinutes: true}

func TestDrainIsLeastSquaresSlope(t *testing.T) {
	r := Build(onBattery, samples([]int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}, false), nil, now)
	if r.Drain == nil {
		t.Fatal("Drain must be set with 10 on-battery samples")
	}
	// slope of this sequence = -40/82.5 %/min = -29.09 %/hr
	if math.Abs(*r.Drain-29.1) > 0.05 {
		t.Errorf("Drain = %v, want 29.1", *r.Drain)
	}
	if r.EstMinutes == nil || *r.EstMinutes != 138 {
		t.Errorf("EstMinutes = %v, want 138 (67 / 29.09 × 60)", r.EstMinutes)
	}
	if r.MacOSMinutes == nil || *r.MacOSMinutes != 312 {
		t.Errorf("MacOSMinutes = %v, want 312", r.MacOSMinutes)
	}
	if !r.HasData || r.Collecting || r.EstOver12h {
		t.Errorf("flags: HasData=%v Collecting=%v EstOver12h=%v", r.HasData, r.Collecting, r.EstOver12h)
	}
}

func TestFewerThanThreeSamplesIsCollecting(t *testing.T) {
	r := Build(onBattery, samples([]int{68, 67}, false), nil, now)
	if r.Drain != nil || !r.Collecting || !r.HasData {
		t.Errorf("Drain=%v Collecting=%v HasData=%v", r.Drain, r.Collecting, r.HasData)
	}
}

func TestSleepGapKeepsOnlyPostWakeSamples(t *testing.T) {
	s := samples([]int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}, false)
	// Push the first eight samples 91 s further into the past: the gap before
	// index 8 becomes 151 s, leaving two post-wake rows.
	for i := 0; i < 8; i++ {
		s[i].TS -= 91
	}
	r := Build(onBattery, s, nil, now)
	if r.Drain != nil || !r.Collecting {
		t.Errorf("after a sleep gap only 2 samples remain: Drain=%v Collecting=%v", r.Drain, r.Collecting)
	}
}

func TestGapOfExactlyNinetySecondsIsNotSleep(t *testing.T) {
	// Review Focus 4.
	s := samples([]int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}, false)
	for i := 0; i < 8; i++ {
		s[i].TS -= 30 // gap before index 8 is exactly 90 s
	}
	r := Build(onBattery, s, nil, now)
	if r.Drain == nil {
		t.Fatal("a 90 s gap must not cut the window")
	}
}

func TestOnACSamplesAreDropped(t *testing.T) {
	s := samples([]int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}, false)
	for i := 0; i < 8; i++ {
		s[i].OnAC = true
	}
	r := Build(onBattery, s, nil, now)
	if r.Drain != nil || !r.Collecting {
		t.Errorf("only 2 on-battery rows: Drain=%v Collecting=%v", r.Drain, r.Collecting)
	}
}

func TestConstantPercentIsOverTwelveHours(t *testing.T) {
	// Review Focus 3: slope 0 must render as "> 12h", not be omitted.
	r := Build(onBattery, samples([]int{67, 67, 67, 67, 67, 67}, false), nil, now)
	if r.Drain == nil || *r.Drain != 0 {
		t.Fatalf("Drain = %v, want 0", r.Drain)
	}
	if !r.EstOver12h || r.EstMinutes != nil {
		t.Errorf("EstOver12h=%v EstMinutes=%v", r.EstOver12h, r.EstMinutes)
	}
}

func TestRisingPercentOnBatteryIsOmitted(t *testing.T) {
	r := Build(onBattery, samples([]int{66, 66, 67, 67, 68, 68}, false), nil, now)
	if r.Drain != nil || r.EstMinutes != nil || r.EstOver12h || r.Collecting {
		t.Errorf("a negative drain is not a drain: %+v", r)
	}
}

func TestOnACHasNoDrainButKeepsWorst(t *testing.T) {
	onAC := battery.Snapshot{Percent: 82, OnAC: true, Charging: true, Watts: 41.8, HasWatts: true, MacOSMinutes: 14, HasMacOSMinutes: true}
	energy := []store.AppEnergy{{TS: now.Unix() - 60, App: "Google Chrome", Energy: 30}, {TS: now.Unix(), App: "Code", Energy: 20}, {TS: now.Unix(), App: "Google Chrome", Energy: 8}}
	r := Build(onAC, samples([]int{80, 81, 82, 82}, true), energy, now)
	if r.Drain != nil || r.EstMinutes != nil || r.Collecting {
		t.Errorf("no drain on AC: %+v", r)
	}
	if r.MacOSMinutes != nil {
		t.Errorf("macOS time-to-full is not a time-left estimate; want nil on AC")
	}
	if r.Worst == nil || r.Worst.App != "Google Chrome" || math.Abs(r.Worst.Share-38.0/58.0) > 1e-9 {
		t.Errorf("Worst = %+v, want Google Chrome at 38/58", r.Worst)
	}
}

func TestNoDataAtAll(t *testing.T) {
	r := Build(onBattery, nil, nil, now)
	if r.HasData || r.Collecting || r.Drain != nil || r.Worst != nil {
		t.Errorf("%+v", r)
	}
	if r.Watts == nil || *r.Watts != 8.4 || r.MacOSMinutes == nil {
		t.Errorf("live fields must survive without daemon data: %+v", r)
	}
	if r.TS != now.Unix() {
		t.Errorf("TS = %d", r.TS)
	}
}

func TestMissingLiveFieldsAreNil(t *testing.T) {
	r := Build(battery.Snapshot{Percent: 50}, nil, nil, now)
	if r.Watts != nil || r.MacOSMinutes != nil {
		t.Errorf("optional snapshot fields must map to nil: %+v", r)
	}
}

// withAC marks samples[from:to] as taken on AC.
func withAC(s []store.Sample, from, to int) []store.Sample {
	for i := from; i < to; i++ {
		s[i].OnAC = true
	}
	return s
}

func TestUnplugAfterChargingIsCollecting(t *testing.T) {
	// 5 min on battery, 4 min charging, unplugged 1 min ago: only one
	// on-battery row since the unplug, so the spec's "just unplugged" case.
	s := withAC(samples([]int{60, 59, 58, 57, 56, 58, 60, 62, 64, 64}, false), 5, 9)
	r := Build(onBattery, s, nil, now)
	if r.Drain != nil || !r.Collecting {
		t.Errorf("just unplugged: Drain=%v Collecting=%v, want nil/true", r.Drain, r.Collecting)
	}
}

func TestDrainIgnoresBatteryRowsFromBeforeAC(t *testing.T) {
	// 80→78 on battery, held at 78 on AC, then flat on battery since the
	// unplug. Only the post-unplug rows may be fitted: drain 0, not a
	// slope across the AC stretch.
	s := withAC(samples([]int{80, 79, 78, 78, 78, 78, 78, 78, 78, 78}, false), 3, 7)
	r := Build(onBattery, s, nil, now)
	if r.Drain == nil || *r.Drain != 0 {
		t.Errorf("Drain = %v, want 0 from the 3 post-unplug rows", r.Drain)
	}
}

func TestStaleNewestSampleIsTreatedAsSleep(t *testing.T) {
	// Just woke (or the daemon stopped): the newest sample is more than
	// 90 s old, so everything in the window is from before the gap.
	s := samples([]int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}, false)
	for i := range s {
		s[i].TS -= 91
	}
	r := Build(onBattery, s, nil, now)
	if r.Drain != nil || !r.Collecting {
		t.Errorf("stale window: Drain=%v Collecting=%v, want nil/true", r.Drain, r.Collecting)
	}
	for i := range s {
		s[i].TS++ // newest now exactly 90 s old: not a gap
	}
	if r := Build(onBattery, s, nil, now); r.Drain == nil {
		t.Error("a newest sample exactly 90 s old must still count")
	}
}

func TestOverTwelveHoursIsDecidedByTheEstimate(t *testing.T) {
	// 67 % at ~3.3 %/hr is about 20 h: that is "> 12h", and JSON keeps the
	// number. The old rule (rate < 0.5 %/hr) printed "20h 29m".
	r := Build(onBattery, samples([]int{67, 67, 67, 67, 67, 67, 67, 67, 67, 66}, false), nil, now)
	if r.Drain == nil || r.EstMinutes == nil || *r.EstMinutes <= 720 || !r.EstOver12h {
		t.Errorf("Drain=%v EstMinutes=%v EstOver12h=%v, want an estimate over 720 min flagged > 12h", r.Drain, r.EstMinutes, r.EstOver12h)
	}
	// 3 % at 29.1 %/hr is 6 minutes: a real number, not "> 12h".
	low := onBattery
	low.Percent = 3
	r = Build(low, samples([]int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}, false), nil, now)
	if r.EstMinutes == nil || *r.EstMinutes != 6 || r.EstOver12h {
		t.Errorf("EstMinutes=%v EstOver12h=%v, want 6 and false", r.EstMinutes, r.EstOver12h)
	}
}
