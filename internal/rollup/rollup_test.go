package rollup

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/store"
)

func local(m time.Month, d, h, min int) time.Time {
	return time.Date(2026, m, d, h, min, 0, 0, time.Local)
}

// seed writes one tick a minute over [from, to), pct moving p0 → p1.
func seed(t *testing.T, db *store.DB, from, to time.Time, p0, p1 int, onAC bool) {
	t.Helper()
	n := int(to.Sub(from) / time.Minute)
	for k := 0; k < n; k++ {
		p := p0
		if n > 1 {
			p = p0 + (p1-p0)*k/(n-1)
		}
		if err := db.WriteTick(context.Background(), store.Tick{TS: from.Add(time.Duration(k) * time.Minute).Unix(), Pct: p, OnAC: onAC}); err != nil {
			t.Fatal(err)
		}
	}
}

// fixture: battery 22:00–23:00 on 29 Sep, asleep until 07:00, battery
// 07:00–08:00, AC 08:00–09:00, asleep until 10:00 on 1 Oct, AC to 10:05.
func fixture(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "batlog.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	seed(t, db, local(9, 29, 22, 0), local(9, 29, 23, 0), 50, 44, false)
	seed(t, db, local(9, 30, 7, 0), local(9, 30, 8, 0), 44, 38, false)
	seed(t, db, local(9, 30, 8, 0), local(9, 30, 9, 0), 38, 45, true)
	seed(t, db, local(10, 1, 10, 0), local(10, 1, 10, 5), 60, 61, true)
	for i, e := range []float64{3, 2} {
		if err := db.Exec(ctx, `INSERT INTO app_energy(ts, app, energy) VALUES(?, 'Brave', ?)`,
			local(9, 29, 22, i).Unix(), e); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

var (
	wantSep29 = store.RollupDay{Day: "2026-09-29", MinBattery: 59, MinAsleep: 61, PctConsumed: 6,
		AppEnergy: map[string]float64{"Brave": 5}}
	wantSep30 = store.RollupDay{Day: "2026-09-30", MinBattery: 60, MinAC: 59, MinAsleep: 420 + 901, PctConsumed: 6}
)

func check(t *testing.T, db *store.DB, want ...store.RollupDay) {
	t.Helper()
	for _, w := range want {
		got, ok, err := db.Rollup(context.Background(), w.Day)
		if err != nil || !ok || fmt.Sprint(got) != fmt.Sprint(w) {
			t.Errorf("rollup %s = %+v (%v, %v), want %+v", w.Day, got, ok, err, w)
		}
	}
}

func TestRollsUpDaysOlderThan90AndPrunesThem(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	res, err := Run(ctx, db, local(12, 30, 12, 0)) // cutoff 1 Oct 00:00
	if err != nil {
		t.Fatal(err)
	}
	if res.Days != 2 || res.First != "2026-09-29" || res.Last != "2026-09-30" {
		t.Errorf("result = %+v", res)
	}
	check(t, db, wantSep29, wantSep30)
	if r, _, _ := db.Rollup(ctx, "2026-09-30"); r.AppEnergy != nil {
		t.Errorf("app_energy = %v, want NULL for a day without rows", r.AppEnergy)
	}
	if ts, _ := db.OldestRawTS(ctx); ts != local(10, 1, 10, 0).Unix() {
		t.Errorf("oldest raw row = %v, want 1 Oct 10:00 kept", time.Unix(ts, 0))
	}
	if res, err := Run(ctx, db, local(12, 30, 12, 0)); err != nil || res.Days != 0 {
		t.Errorf("second run = %+v, %v; want nothing to do", res, err)
	}
	check(t, db, wantSep29, wantSep30)
}

func TestDayByDayMatchesOneRun(t *testing.T) {
	// A sleep that crosses midnight is split right even when the day before
	// was rolled up (and deleted) by an earlier run.
	db := fixture(t)
	ctx := context.Background()
	if res, err := Run(ctx, db, local(12, 29, 12, 0)); err != nil || res.Days != 1 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	if res, err := Run(ctx, db, local(12, 30, 12, 0)); err != nil || res.Days != 1 {
		t.Fatalf("second run = %+v, %v", res, err)
	}
	check(t, db, wantSep29, wantSep30)
}

func TestDataGapIsNotSleep(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	if err := db.RecordRunStart(ctx, local(9, 30, 12, 0).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, db, local(12, 30, 12, 0)); err != nil {
		t.Fatal(err)
	}
	w := wantSep30
	w.MinAsleep = 420 // the 901 min after 08:59 were the daemon being down
	check(t, db, wantSep29, w)
}

func TestNothingToDo(t *testing.T) {
	db := fixture(t)
	if res, err := Run(context.Background(), db, local(12, 28, 23, 59)); err != nil || res.Days != 0 {
		t.Errorf("result = %+v, %v; 29 Sep is not yet 90 days old", res, err)
	}
	empty, err := store.Open(filepath.Join(t.TempDir(), "e.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	empty.Migrate(context.Background())
	if res, err := Run(context.Background(), empty, local(12, 30, 0, 0)); err != nil || res.Days != 0 {
		t.Errorf("empty database: %+v, %v", res, err)
	}
}
