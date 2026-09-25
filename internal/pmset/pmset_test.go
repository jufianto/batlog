package pmset

import (
	"strings"
	"testing"
	"time"
)

// Line shapes copied from a real `pmset -g log` on macOS 15.7.
const log = `PM ASL data store: /var/log/powermanagement
Time stamp                Domain              	Message                                                                         	Duration  	Delay
2026-09-19 02:59:43 +0700 Assertions          	Summary- [System: DeclUser SRPrevSleep kCPU kDisp] Using Batt(Charge: 100)          
2026-09-19 02:59:44 +0700 Sleep               	Entering Sleep state due to 'Software Sleep pid=388':TCPKeepAlive=active Using Batt (Charge:100%) 14 secs   
2026-09-19 03:10:00 +0700 Assertions          	PID 385(WindowServer) Created UserIsActive "com.apple.iohideventsystem" 00:00:00
2026-09-25 23:43:57 +0700 DarkWake            	DarkWake from Deep Idle [CDNP] : due to smc.70070000 USB-C_plug Using BATT (Charge:10%) 1 secs    
2026-09-25 23:43:58 +0700 Wake                	DarkWake to FullWake from Deep Idle [CDNVAP] : due to Notification Using AC (Charge:10%)
2026-09-26 02:15:12 +0700 Assertions          	Summary- [System: PrevIdle PrevDisp DeclUser kDisp] Using AC(Charge: 100)          
garbage line Using AC(Charge: 55)
`

func TestParseReadsEveryPowerSourceSpelling(t *testing.T) {
	got, err := Parse(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		at   string
		onAC bool
		pct  int
	}{
		{"2026-09-19 02:59:43 +0700", false, 100},
		{"2026-09-19 02:59:44 +0700", false, 100},
		{"2026-09-25 23:43:57 +0700", false, 10},
		{"2026-09-25 23:43:58 +0700", true, 10},
		{"2026-09-26 02:15:12 +0700", true, 100},
	}
	if len(got) != len(want) {
		t.Fatalf("readings = %+v", got)
	}
	for i, w := range want {
		at, _ := time.Parse("2006-01-02 15:04:05 -0700", w.at)
		if got[i].TS != at.Unix() || got[i].OnAC != w.onAC || got[i].Pct != w.pct {
			t.Errorf("reading %d = %+v, want %s ac=%v %d%%", i, got[i], w.at, w.onAC, w.pct)
		}
	}
}

func TestChangesKeepOnlyTransitions(t *testing.T) {
	rs := []Reading{{TS: 1, OnAC: false, Pct: 100}, {TS: 2, OnAC: false, Pct: 99}, {TS: 3, OnAC: true, Pct: 10},
		{TS: 4, OnAC: true, Pct: 50}, {TS: 5, OnAC: false, Pct: 100}}
	got := Changes(rs)
	if len(got) != 2 || got[0] != (Reading{3, true, 10}) || got[1] != (Reading{5, false, 100}) {
		t.Errorf("Changes = %+v, want plug at 3 and unplug at 5 (the first reading has no known prior state)", got)
	}
	if Changes(nil) != nil {
		t.Error("no readings, no changes")
	}
}

func TestParseSortsOutOfOrderLines(t *testing.T) {
	src := "2026-09-19 03:00:00 +0700 Wake  Using AC (Charge:50%)\n2026-09-19 02:00:00 +0700 Sleep  Using Batt (Charge:60%)\n"
	got, _ := Parse(strings.NewReader(src))
	if len(got) != 2 || got[0].TS > got[1].TS {
		t.Errorf("readings = %+v, want ascending", got)
	}
}
