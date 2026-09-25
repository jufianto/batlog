package history

import (
	"math"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/pmset"
	"github.com/jufianto/batlog/internal/store"
)

// midnight of the day under test; every time below is minutes from here.
var midnight = time.Date(2026, 9, 26, 0, 0, 0, 0, time.Local)

func at(min int) int64 { return midnight.Unix() + int64(min)*60 }

// seg returns one sample a minute for [from, to), pct moving linearly from
// p0 at the first sample to p1 at the last.
func seg(from, to, p0, p1 int, onAC bool) []store.Sample {
	n := to - from
	out := make([]store.Sample, n)
	for k := 0; k < n; k++ {
		p := p0
		if n > 1 {
			p = int(math.Round(float64(p0) + float64(p1-p0)*float64(k)/float64(n-1)))
		}
		out[k] = store.Sample{TS: at(from + k), Pct: p, OnAC: onAC}
	}
	return out
}

func cat(parts ...[]store.Sample) []store.Sample {
	var out []store.Sample
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// day: AC until 23:10 yesterday, battery across midnight until 01:00, AC
// until 02:00, battery 02:00–03:00, asleep 8 h, battery again 11:00–12:00.
func day() []store.Sample {
	return cat(
		seg(-60, -50, 100, 100, true),
		seg(-50, 60, 100, 90, false),
		seg(60, 120, 90, 100, true),
		seg(120, 180, 100, 95, false),
		seg(660, 720, 94, 88, false),
	)
}

func today() Input {
	return Input{From: midnight, To: time.Unix(at(719), 0), Samples: day(), FirstSampleTS: at(-60)}
}

func TestHeadlineAnswersAcrossMidnightAndOngoing(t *testing.T) {
	r := Build(today())
	if r.FirstCharge == nil || r.FirstCharge.TS != at(60) || r.FirstCharge.Pct != 90 || r.FirstCharge.Source != "batlog" {
		t.Errorf("FirstCharge = %+v, want 01:00 at 90%%", r.FirstCharge)
	}
	if r.LastUnplug == nil || r.LastUnplug.TS != at(120) || r.LastUnplug.Pct != 100 {
		t.Errorf("LastUnplug = %+v, want 02:00 at 100%%", r.LastUnplug)
	}
	if r.Lasted == nil || !r.Lasted.Ongoing || r.Lasted.Minutes != 118 {
		t.Errorf("Lasted = %+v, want ongoing 118 min", r.Lasted)
	}
	if len(r.Sessions) != 2 {
		t.Fatalf("sessions = %+v", r.Sessions)
	}
	s1, s2 := r.Sessions[0], r.Sessions[1]
	// Started yesterday 23:10: kept whole, not clipped at midnight.
	if s1.Start != at(-50) || s1.End != at(60) || s1.Ongoing || *s1.AwakeMin != 110 || s1.StartPct != 100 || s1.EndPct != 90 {
		t.Errorf("session 1 = %+v awake=%d", s1, *s1.AwakeMin)
	}
	if s1.Drain == nil || *s1.Drain != 5.5 {
		t.Errorf("session 1 drain = %v, want 5.5 (10 %% over 110 min)", s1.Drain)
	}
	if s2.Start != at(120) || !s2.Ongoing || s2.End != 0 || *s2.AwakeMin != 118 || s2.EndPct != 88 || s2.DataGap {
		t.Errorf("session 2 = %+v", s2)
	}
}

func TestSleepGapDoesNotInflateBatteryLife(t *testing.T) {
	s2 := Build(today()).Sessions[1]
	// 02:00–03:00 and 11:00–12:00 awake; the 8 h between was sleep. Drain
	// counts only the awake drops (5 + 6 %), not the 1 % lost asleep.
	if *s2.AwakeMin != 118 {
		t.Errorf("awake = %d min, want 118, not 600", *s2.AwakeMin)
	}
	if s2.Drain == nil || *s2.Drain != 5.6 {
		t.Errorf("drain = %v, want 5.6 (11 %% over 118 min)", s2.Drain)
	}
}

func TestTotalsAreClippedToTheRange(t *testing.T) {
	tot := Build(today()).Totals
	want := Totals{BatterySec: (60 + 118) * 60, ACSec: 60 * 60, SleepSec: 481 * 60}
	if tot != want {
		t.Errorf("totals = %+v, want %+v", tot, want)
	}
}

func TestGapContainingARecorderStartIsADataGap(t *testing.T) {
	in := today()
	in.RunStarts = []int64{at(300)} // the daemon restarted during the gap
	r := Build(in)
	if !r.Sessions[1].DataGap {
		t.Error("session 2 must be marked (data gap)")
	}
	if r.Totals.GapSec != 481*60 || r.Totals.SleepSec != 0 {
		t.Errorf("totals = %+v, want the 481 min as a data gap, not sleep", r.Totals)
	}
	// A start at the gap's first sample belongs to the run before it.
	in.RunStarts = []int64{at(179)}
	if Build(in).Sessions[1].DataGap {
		t.Error("a start at the gap's opening sample is not inside the gap")
	}
}

func TestStaleOngoingSessionIsADataGap(t *testing.T) {
	in := today()
	in.To = time.Unix(at(719)+11*60, 0) // newest sample 11 min old
	if s := Build(in).Sessions[1]; !s.Ongoing || !s.DataGap {
		t.Errorf("session = %+v, want ongoing with a data gap", s)
	}
}

func TestShortSessionHasNoDrain(t *testing.T) {
	in := Input{From: midnight, To: time.Unix(at(10), 0), FirstSampleTS: at(0),
		Samples: cat(seg(0, 3, 100, 100, true), seg(3, 6, 100, 99, false), seg(6, 11, 99, 100, true))}
	r := Build(in)
	if len(r.Sessions) != 1 || r.Sessions[0].Drain != nil {
		t.Errorf("3-minute session drain = %v, want nil", r.Sessions[0].Drain)
	}
	if r.Lasted == nil || r.Lasted.Ongoing || r.Lasted.Minutes != 3 {
		t.Errorf("Lasted = %+v, want the completed 3 min session", r.Lasted)
	}
}

func TestPmsetFallbackIsTaggedAndSeparate(t *testing.T) {
	// Daemon installed at 01:00; pmset knows the hour before.
	in := Input{From: midnight, To: time.Unix(at(119), 0), FirstSampleTS: at(60),
		Samples: seg(60, 120, 97, 94, false),
		Pmset: []pmset.Reading{
			{TS: at(-30), OnAC: false, Pct: 80},
			{TS: at(10), OnAC: true, Pct: 70},   // plugged
			{TS: at(30), OnAC: false, Pct: 100}, // unplugged
			{TS: at(50), OnAC: false, Pct: 98},
			{TS: at(70), OnAC: true, Pct: 96}, // after the first sample: ignored
		}}
	r := Build(in)
	if len(r.Events) != 2 || r.Events[0].Source != "pmset" || !r.Events[0].Plugged || r.Events[1].TS != at(30) {
		t.Errorf("events = %+v", r.Events)
	}
	if r.FirstCharge == nil || r.FirstCharge.TS != at(10) || r.FirstCharge.Source != "pmset" {
		t.Errorf("FirstCharge = %+v", r.FirstCharge)
	}
	if len(r.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want a pmset one and a batlog one, not merged", r.Sessions)
	}
	p, b := r.Sessions[0], r.Sessions[1]
	if p.Source != "pmset" || p.Start != at(30) || p.End != at(60) || p.AwakeMin != nil || p.Drain != nil || p.StartPct != 100 || p.EndPct != 98 {
		t.Errorf("pmset session = %+v", p)
	}
	if b.Source != "batlog" || b.Start != at(60) || !b.Ongoing {
		t.Errorf("batlog session = %+v", b)
	}
	if r.Lasted == nil || !r.Lasted.Ongoing || r.Lasted.Minutes != 59 {
		t.Errorf("Lasted = %+v, want from batlog data only", r.Lasted)
	}
}

func TestEventsAscendingAcrossSources(t *testing.T) {
	in := Input{From: midnight, To: time.Unix(at(200), 0), FirstSampleTS: at(100),
		Samples: cat(seg(100, 150, 80, 90, true), seg(150, 201, 90, 85, false)),
		Pmset:   []pmset.Reading{{TS: at(5), OnAC: true, Pct: 50}, {TS: at(20), OnAC: false, Pct: 60}}}
	r := Build(in)
	for i := 1; i < len(r.Events); i++ {
		if r.Events[i].TS < r.Events[i-1].TS {
			t.Fatalf("events not ascending: %+v", r.Events)
		}
	}
	if len(r.Events) != 2 || r.Events[1].Source != "batlog" {
		t.Errorf("events = %+v, want the pmset unplug then the batlog unplug", r.Events)
	}
}

func TestNoData(t *testing.T) {
	r := Build(Input{From: midnight, To: time.Unix(at(600), 0)})
	if len(r.Events) != 0 || len(r.Sessions) != 0 || r.FirstCharge != nil || r.Lasted != nil || r.Totals != (Totals{}) {
		t.Errorf("result = %+v", r)
	}
}

func TestSessionsBeforeTheRangeAreDropped(t *testing.T) {
	in := today()
	in.From = time.Unix(at(100), 0) // --since 01:40: session 1 ended at 01:00
	r := Build(in)
	if len(r.Sessions) != 1 || r.Sessions[0].Start != at(120) {
		t.Errorf("sessions = %+v, want only the one from 02:00", r.Sessions)
	}
	if r.FirstCharge != nil {
		t.Errorf("FirstCharge = %+v, the 01:00 plug is before the range", r.FirstCharge)
	}
}

func TestPmsetEndingOnACHasNoSession(t *testing.T) {
	in := Input{From: midnight, To: time.Unix(at(119), 0), FirstSampleTS: at(60),
		Samples: seg(60, 120, 90, 100, true),
		Pmset:   []pmset.Reading{{TS: at(-30), OnAC: false, Pct: 80}, {TS: at(10), OnAC: true, Pct: 70}}}
	r := Build(in)
	if len(r.Sessions) != 0 {
		t.Errorf("sessions = %+v, want none: pmset saw only a plug", r.Sessions)
	}
	if len(r.Events) != 1 || !r.Events[0].Plugged || r.Events[0].Source != SourcePmset {
		t.Errorf("events = %+v", r.Events)
	}
}
