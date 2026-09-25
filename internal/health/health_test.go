package health

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

func ip(v int) *int         { return &v }
func fp(v float64) *float64 { return &v }

var appleSilicon = battery.Health{Cycles: ip(388), DesignMAh: ip(6249), RawMaxMAh: ip(5424), NominalMAh: ip(5576),
	TempC: fp(30.91), VoltageV: fp(13.1), FailureStatus: ip(0)}

func TestBuildAppleSilicon(t *testing.T) {
	r := Build(appleSilicon)
	if r.HealthPct == nil || *r.HealthPct != 86.8 {
		t.Errorf("HealthPct = %v, want 86.8 (5424/6249)", r.HealthPct)
	}
	if r.AppleHealthPct == nil || *r.AppleHealthPct != 89.2 {
		t.Errorf("AppleHealthPct = %v, want 89.2 (5576/6249)", r.AppleHealthPct)
	}
	if r.Condition == nil || *r.Condition != "Normal" || r.NeedsService {
		t.Errorf("Condition = %v NeedsService = %v, want Normal/false", r.Condition, r.NeedsService)
	}
	if len(r.MissingKeys) != 0 {
		t.Errorf("MissingKeys = %v", r.MissingKeys)
	}
}

func TestBuildWithoutNominalOmitsAppleFigure(t *testing.T) {
	h := appleSilicon
	h.NominalMAh = nil
	r := Build(h)
	if r.AppleHealthPct != nil || r.HealthPct == nil {
		t.Errorf("AppleHealthPct = %v HealthPct = %v", r.AppleHealthPct, r.HealthPct)
	}
}

func TestBuildMissingCapacityNamesTheKey(t *testing.T) {
	h := appleSilicon
	h.DesignMAh = nil
	r := Build(h)
	if r.HealthPct != nil || r.AppleHealthPct != nil {
		t.Errorf("no design capacity: HealthPct = %v AppleHealthPct = %v", r.HealthPct, r.AppleHealthPct)
	}
	if !reflect.DeepEqual(r.MissingKeys, []string{"DesignCapacity"}) || r.HealthNote != "ioreg has no DesignCapacity" {
		t.Errorf("MissingKeys = %v HealthNote = %q", r.MissingKeys, r.HealthNote)
	}
	r = Build(battery.Health{})
	if !reflect.DeepEqual(r.MissingKeys, []string{"AppleRawMaxCapacity", "DesignCapacity"}) ||
		r.HealthNote != "ioreg has no AppleRawMaxCapacity or DesignCapacity" {
		t.Errorf("MissingKeys = %v HealthNote = %q", r.MissingKeys, r.HealthNote)
	}
	if r.Condition != nil {
		t.Errorf("no PermanentFailureStatus key: Condition = %v, want nil", *r.Condition)
	}
}

func TestBuildDesignCapacityZeroSaysSo(t *testing.T) {
	// ioreg has the key, so "ioreg has no DesignCapacity" would send the
	// user looking for a bug that is not there.
	h := appleSilicon
	h.DesignMAh = ip(0)
	r := Build(h)
	if r.HealthPct != nil || r.AppleHealthPct != nil {
		t.Errorf("design 0 must not divide: HealthPct = %v", r.HealthPct)
	}
	if r.HealthNote != "DesignCapacity is 0" {
		t.Errorf("HealthNote = %q", r.HealthNote)
	}
}

func TestBuildFailureStatusNeedsService(t *testing.T) {
	h := appleSilicon
	h.FailureStatus = ip(4)
	r := Build(h)
	if r.Condition == nil || *r.Condition != "Service recommended" || !r.NeedsService {
		t.Errorf("Condition = %v NeedsService = %v", r.Condition, r.NeedsService)
	}
}

func rows(design int, pts ...any) []store.HealthRow {
	var out []store.HealthRow
	for i := 0; i < len(pts); i += 2 {
		out = append(out, store.HealthRow{Day: pts[i].(string), RawMaxMAh: pts[i+1].(int), DesignMAh: design})
	}
	return out
}

func TestTrendLinearDecline(t *testing.T) {
	tr, span := Trend(rows(1000, "2026-06-01", 880, "2026-07-01", 877, "2026-07-31", 874))
	if tr == nil {
		t.Fatal("trend must be computed from 3 rows over 60 days")
	}
	if span != 60 || tr.Days != 60 || tr.FromPct != 88.0 || tr.ToPct != 87.4 || tr.LastDay != "2026-07-31" {
		t.Errorf("trend = %+v span = %d", *tr, span)
	}
	if math.Abs(tr.PctPerMonth-(-0.3)) > 1e-9 {
		t.Errorf("PctPerMonth = %v, want -0.3", tr.PctPerMonth)
	}
}

func TestTrendMatchesManualLeastSquares(t *testing.T) {
	// x = 0, 10, 20 days; y = 90.0, 89.0, 89.5 %. Mean x 10, mean y 89.5;
	// Σ(dx·dy) = -5, Σdx² = 200 → -0.025 %/day → -0.75 %/month.
	tr, _ := Trend(rows(1000, "2026-07-01", 900, "2026-07-11", 890, "2026-07-21", 895))
	if tr == nil || tr.PctPerMonth != -0.75 {
		t.Errorf("trend = %+v, want -0.75 %%/month", tr)
	}
}

func TestTrendNeedsTwoRowsSevenDaysApart(t *testing.T) {
	if tr, span := Trend(nil); tr != nil || span != 0 {
		t.Errorf("no rows: %v %d", tr, span)
	}
	if tr, span := Trend(rows(1000, "2026-07-01", 900)); tr != nil || span != 0 {
		t.Errorf("one row: %v %d", tr, span)
	}
	if tr, span := Trend(rows(1000, "2026-07-01", 900, "2026-07-07", 899)); tr != nil || span != 6 {
		t.Errorf("6-day span: %v %d, want nil 6", tr, span)
	}
	if tr, span := Trend(rows(1000, "2026-07-01", 900, "2026-07-08", 899)); tr == nil || span != 7 {
		t.Errorf("7-day span: %v %d, want a trend", tr, span)
	}
}

func TestTrendFlatIsZeroNotNegativeZero(t *testing.T) {
	tr, _ := Trend(rows(1000, "2026-07-01", 900, "2026-07-20", 900))
	if tr == nil || tr.PctPerMonth != 0 || math.Signbit(tr.PctPerMonth) {
		t.Errorf("flat trend = %+v, want +0", tr)
	}
}

func TestSinceIsNinetyDaysBeforeToday(t *testing.T) {
	today := time.Date(2026, 9, 26, 23, 30, 0, 0, time.Local)
	if got := Since(today); got != "2026-06-28" {
		t.Errorf("Since = %q, want 2026-06-28", got)
	}
}

func TestTrendSkipsMalformedDays(t *testing.T) {
	// Text ordering puts "2026-9-2" after every real date and "" before
	// them; neither may wipe out months of good rows.
	rs := rows(1000, "", 999, "2026-06-01", 880, "2026-07-01", 877, "2026-07-31", 874, "2026-9-2", 1)
	tr, span := Trend(rs)
	if tr == nil || span != 60 || tr.FromPct != 88.0 || tr.ToPct != 87.4 || math.Abs(tr.PctPerMonth-(-0.3)) > 1e-9 {
		t.Errorf("trend = %+v span = %d, want the three valid rows only", tr, span)
	}
}
