package history

import (
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/store"
)

// charging marks a segment's samples as charging below 100 %, as a Mac on
// AC does unless it holds the charge.
func charging(ss []store.Sample) []store.Sample {
	for i := range ss {
		ss[i].Charging = ss[i].OnAC && ss[i].Pct < 100
	}
	return ss
}

func chargesOf(t *testing.T, in Input) []ChargeSession {
	t.Helper()
	return Build(in).ChargeSessions
}

func TestChargeToFullThenPluggedInAtFull(t *testing.T) {
	// Battery, plug in at 20 % at 01:00, 100 % at 02:20, unplugged 04:00.
	in := Input{From: midnight, To: time.Unix(at(600), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 60, 40, 20, false),
		charging(seg(60, 141, 20, 100, true)),
		seg(141, 240, 100, 100, true),
		seg(240, 300, 100, 90, false),
	)}
	cs := chargesOf(t, in)
	if len(cs) != 1 {
		t.Fatalf("charge sessions = %+v", cs)
	}
	c := cs[0]
	if c.ID != "0926-0100" || c.Start != at(60) || c.End != at(240) || c.StartPct != 20 || c.EndPct != 100 || c.Ongoing {
		t.Errorf("session = %+v", c)
	}
	// 100 % first at 02:20 (minute 140): 80 min to full, 100 min at full.
	if c.FullAt != at(140) || c.FullUpperBound || c.AtFullMin != 100 || c.HoldMin != 0 || c.DataGap {
		t.Errorf("full at %d (want %d), bound %v, at full %d, hold %d", c.FullAt, at(140), c.FullUpperBound, c.AtFullMin, c.HoldMin)
	}
}

func TestChargeFullAfterASleepIsAnUpperBound(t *testing.T) {
	// Plug in at 30 %, charge 10 min, sleep 3 h on the charger, wake at 100 %.
	in := Input{From: midnight, To: time.Unix(at(400), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 10, 35, 30, false),
		charging(seg(10, 20, 30, 39, true)),
		seg(200, 230, 100, 100, true),
		seg(230, 240, 100, 99, false),
	)}
	c := chargesOf(t, in)[0]
	// The sleep counts on the charger: 30 min at full after waking.
	if c.MaxPct != 100 || c.MaxAt != at(200) || !c.MaxUpperBound {
		t.Errorf("max %d at %d bound %v; want 100 at the wake, an upper bound", c.MaxPct, c.MaxAt, c.MaxUpperBound)
	}
	if c.FullAt != at(200) || !c.FullUpperBound || c.AtFullMin != 30 {
		t.Errorf("full at %d bound %v at full %d; want %d true 30", c.FullAt, c.FullUpperBound, c.AtFullMin, at(200))
	}
}

func TestChargeHeldBelowFull(t *testing.T) {
	// Plug in at 50 %, charge to 80 %, then 45 min plugged in not charging
	// (Optimized Charging), unplug at 80 %.
	in := Input{From: midnight, To: time.Unix(at(400), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 10, 55, 50, false),
		charging(seg(10, 50, 50, 80, true)),
		seg(50, 95, 80, 80, true), // Charging false: held
		seg(95, 100, 80, 79, false),
	)}
	c := chargesOf(t, in)[0]
	if c.FullAt != 0 || c.HoldMin != 45 || c.HoldPct != 80 || c.EndPct != 80 || c.Ongoing {
		t.Errorf("session = %+v; want no full, held 45 min at 80%%", c)
	}
	// 80 % first at 00:49, after 39 min on the charger.
	if c.MaxPct != 80 || c.MaxAt != at(49) || c.MaxUpperBound {
		t.Errorf("max %d at %d bound %v; want 80 at %d", c.MaxPct, c.MaxAt, c.MaxUpperBound, at(49))
	}
}

func TestChargeOngoingAndAlreadyFull(t *testing.T) {
	// Plugged in full at 01:00, unplugged 02:00; plugged in at 40 % at
	// 03:00 and still charging at 03:30.
	in := Input{From: midnight, To: time.Unix(at(211), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 60, 100, 100, false),
		seg(60, 120, 100, 100, true),
		seg(120, 180, 100, 70, false),
		charging(seg(180, 211, 40, 64, true)),
	)}
	cs := chargesOf(t, in)
	if len(cs) != 2 {
		t.Fatalf("charge sessions = %+v", cs)
	}
	if full := cs[0]; full.FullAt != full.Start || full.AtFullMin != 60 {
		t.Errorf("already full: %+v", full)
	}
	if c := cs[1]; !c.Ongoing || !c.Charging || c.End != 0 || c.EndPct != 64 || c.FullAt != 0 || c.DataGap {
		t.Errorf("ongoing: %+v", c)
	}
}

func TestChargeAcrossTheRangeStartAndDataGaps(t *testing.T) {
	// Plugged in at 23:00 yesterday; the recorder restarted at 00:30 while
	// the Mac was on the charger; unplugged 01:00. The range starts at
	// midnight. A charge that ended yesterday is not in it.
	in := Input{From: midnight, To: time.Unix(at(120), 0), FirstSampleTS: at(-240),
		RunStarts: []int64{at(30)},
		Samples: cat(
			seg(-240, -200, 60, 40, false),
			charging(seg(-200, -180, 40, 55, true)), // yesterday 20:40–21:00
			seg(-180, -60, 55, 30, false),
			charging(seg(-60, -10, 30, 70, true)),
			charging(seg(40, 60, 90, 99, true)),
			seg(60, 120, 99, 90, false),
		)}
	cs := chargesOf(t, in)
	if len(cs) != 1 {
		t.Fatalf("charge sessions = %+v, want only the one that ends in the range", cs)
	}
	if c := cs[0]; c.ID != "0925-2300" || c.Start != at(-60) || c.End != at(60) || !c.DataGap || c.StartPct != 30 || c.EndPct != 99 {
		t.Errorf("session = %+v", c)
	}
}

func TestChargeNeedsAPlugEvent(t *testing.T) {
	// The first sample ever is on AC: when it was plugged in is unknown.
	in := Input{From: midnight, To: time.Unix(at(120), 0), FirstSampleTS: at(0), Samples: cat(
		charging(seg(0, 30, 50, 70, true)),
		seg(30, 60, 70, 60, false),
	)}
	if cs := chargesOf(t, in); len(cs) != 0 {
		t.Errorf("charge sessions = %+v, want none", cs)
	}
}

func TestChargeStaleRecorderIsADataGap(t *testing.T) {
	// Still on the charger at the last sample, 2 h ago, at 100 %.
	in := Input{From: midnight, To: time.Unix(at(300), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 10, 50, 45, false),
		charging(seg(10, 100, 45, 100, true)),
		seg(100, 180, 100, 100, true),
	)}
	c := chargesOf(t, in)[0]
	// At full from 01:39 (the charge's last sample) to the last sample at
	// 02:59, not to now.
	if !c.Ongoing || !c.DataGap || c.FullAt != at(99) || c.AtFullMin != 80 {
		t.Errorf("session = %+v; want ongoing, data gap, 80 min at full", c)
	}
}

func TestChargeJustAfterAWakeIsNotCurrent(t *testing.T) {
	// Charging from 60 % at 22:00, asleep on the charger overnight, woke a
	// minute ago with no new sample yet: not a data gap, but the newest
	// percent is 9 h old.
	in := Input{From: midnight, To: time.Unix(at(600), 0), FirstSampleTS: at(-140), WokeAt: at(599), Samples: cat(
		seg(-140, -120, 70, 62, false),
		charging(seg(-120, -60, 60, 61, true)),
	)}
	c := chargesOf(t, in)[0]
	if !c.Ongoing || c.DataGap || c.Current || !c.Charging {
		t.Errorf("session = %+v; want ongoing, charging, no data gap, not current", c)
	}
	in.Samples = append(in.Samples, charging(seg(599, 600, 100, 100, true))...)
	if c := chargesOf(t, in)[0]; !c.Current {
		t.Errorf("with a sample a minute ago: %+v, want current", c)
	}
}

func TestChargeDriftAfterFullIsNotAHold(t *testing.T) {
	// Full at 01:00, then macOS lets it drift to 97 % on the charger before
	// topping up: time at full, not a hold.
	in := Input{From: midnight, To: time.Unix(at(400), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 10, 70, 60, false),
		charging(seg(10, 61, 60, 100, true)),
		seg(61, 120, 100, 97, true), // not charging, drifting
		seg(120, 130, 97, 95, false),
	)}
	c := chargesOf(t, in)[0]
	if c.HoldMin != 0 || c.FullAt != at(60) || c.AtFullMin != 60 {
		t.Errorf("session = %+v; want no hold, full at 01:00, 60 min at full (unplugged 02:00)", c)
	}
}

func TestAPastRangeHoldsOnlyWhatStartedInIt(t *testing.T) {
	// The range ends at 02:00; samples run on to 05:00, as they do for a
	// past day. The charge at 01:00 and the session from 01:30 are in it;
	// the charge at 03:00 and the session from 04:00 are not.
	in := Input{From: midnight, To: time.Unix(at(120), 0), FirstSampleTS: at(0), Samples: cat(
		seg(0, 60, 80, 60, false),
		charging(seg(60, 90, 60, 80, true)),
		seg(90, 180, 80, 50, false),
		charging(seg(180, 240, 50, 90, true)),
		seg(240, 300, 90, 80, false),
	)}
	r := Build(in)
	if len(r.ChargeSessions) != 1 || r.ChargeSessions[0].Start != at(60) {
		t.Errorf("charges = %+v", r.ChargeSessions)
	}
	if n := len(r.Sessions); n != 2 || r.Sessions[n-1].Start != at(90) || r.Sessions[n-1].End != at(180) {
		t.Errorf("sessions = %+v", r.Sessions)
	}
	if r.Lasted == nil || r.Lasted.Ongoing {
		t.Errorf("lasted = %+v, want the 01:30 session", r.Lasted)
	}
}
