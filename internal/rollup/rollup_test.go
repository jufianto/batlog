package rollup

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata"

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
		if err := db.AddEnergy(ctx, local(9, 29, 22, i).Unix(), []store.EnergyDelta{{App: "Brave", CPU: uint64(e * 1e9)}}); err != nil {
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

func newDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "batlog.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDSTDays(t *testing.T) {
	cases := []struct {
		zone string
		day  int // of the month below
		mon  time.Month
		want int // minutes in that day
	}{
		{"Europe/Berlin", 29, time.March, 1380},
		{"Europe/Berlin", 25, time.October, 1500},
		{"America/Santiago", 6, time.September, 1380}, // skips midnight
		{"Atlantic/Azores", 29, time.March, 1380},
		{"Africa/Cairo", 24, time.April, 1380},
	}
	for _, c := range cases {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		db := newDB(t)
		noon := time.Date(2026, c.mon, c.day, 12, 0, 0, 0, loc)
		// On AC, one tick a minute, from two days before to two days after.
		from, to := noon.AddDate(0, 0, -2).Add(-12*time.Hour), noon.AddDate(0, 0, 2).Add(12*time.Hour)
		if err := db.Exec(context.Background(), `WITH RECURSIVE m(ts) AS (SELECT ? UNION ALL SELECT ts + 60 FROM m WHERE ts + 60 < ?)
			INSERT INTO samples(ts, pct, on_ac, charging) SELECT ts, 80, 1, 0 FROM m`, from.Unix(), to.Unix()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		res, err := Run(ctx, db, noon.AddDate(0, 0, 92))
		cancel()
		if err != nil || res.Days < 3 {
			t.Errorf("%s: %+v, %v", c.zone, res, err)
			continue
		}
		for off := -1; off <= 1; off++ {
			day := time.Date(2026, c.mon, c.day+off, 12, 0, 0, 0, loc).Format("2006-01-02")
			want := 1440
			if off == 0 {
				want = c.want
			}
			if r, ok, _ := db.Rollup(context.Background(), day); !ok || r.MinAC != want {
				t.Errorf("%s %s: %+v, want %d min on AC", c.zone, day, r, want)
			}
		}
	}
}

func TestRecorderStartsAcrossMidnight(t *testing.T) {
	// Shut down 23:00 on 30 Sep; restarted either before midnight (lead-in
	// side for 1 Oct) or at 08:00 on 1 Oct (lead-out side for 30 Sep).
	for _, restart := range []time.Time{local(9, 30, 23, 30), local(10, 1, 8, 0)} {
		db := newDB(t)
		seed(t, db, local(9, 30, 22, 0), local(9, 30, 23, 0), 90, 90, true)
		seed(t, db, local(10, 1, 8, 0), local(10, 1, 9, 0), 90, 90, true)
		seed(t, db, local(10, 3, 8, 0), local(10, 3, 8, 5), 90, 90, true)
		if err := db.RecordRunStart(context.Background(), restart.Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(context.Background(), db, local(12, 31, 12, 0)); err != nil { // cutoff 2 Oct
			t.Fatal(err)
		}
		check(t, db,
			store.RollupDay{Day: "2026-09-30", MinAC: 59},
			store.RollupDay{Day: "2026-10-01", MinAC: 59, MinAsleep: 901}) // 08:59 → midnight, no start in it
	}
}

func TestDaysWithoutRawRowsGetTheirSleep(t *testing.T) {
	db := newDB(t)
	seed(t, db, local(9, 1, 20, 0), local(9, 1, 21, 0), 60, 54, false) // lid closed at 21:00
	seed(t, db, local(9, 4, 8, 0), local(9, 4, 8, 10), 52, 52, false)
	res, err := Run(context.Background(), db, local(12, 3, 12, 0)) // cutoff 4 Sep
	if err != nil || res.Days != 3 || res.Last != "2026-09-03" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	check(t, db,
		store.RollupDay{Day: "2026-09-01", MinBattery: 59, MinAsleep: 181, PctConsumed: 6},
		store.RollupDay{Day: "2026-09-02", MinAsleep: 1440},
		store.RollupDay{Day: "2026-09-03", MinAsleep: 1440})
}

func TestUnknownDaysGetNoRow(t *testing.T) {
	// The Mac was off (a recorder start ends the gap): nothing is known.
	db := newDB(t)
	seed(t, db, local(9, 1, 20, 0), local(9, 1, 21, 0), 60, 54, true)
	seed(t, db, local(9, 4, 8, 0), local(9, 4, 8, 10), 52, 52, true)
	db.RecordRunStart(context.Background(), local(9, 4, 8, 0).Unix())
	res, err := Run(context.Background(), db, local(12, 3, 12, 0))
	if err != nil || res.Days != 1 || res.First != "2026-09-01" {
		t.Errorf("result = %+v, %v; want only 1 Sep", res, err)
	}
	if _, ok, _ := db.Rollup(context.Background(), "2026-09-02"); ok {
		t.Error("2 Sep has no data and must have no row")
	}
}

func TestReappearedDayOlderThanTheCarry(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	if _, err := Run(ctx, db, local(12, 30, 12, 0)); err != nil {
		t.Fatal(err)
	}
	// Rows for 28 Sep turn up after 29 and 30 Sep were rolled (a clock change).
	seed(t, db, local(9, 28, 12, 0), local(9, 28, 12, 10), 70, 70, true)
	res, err := Run(ctx, db, local(12, 30, 12, 0))
	if err != nil || res.Days != 1 || res.First != "2026-09-28" {
		t.Errorf("result = %+v, %v", res, err)
	}
	// No lead-in (the carry is newer); asleep 12:09 → midnight.
	check(t, db, store.RollupDay{Day: "2026-09-28", MinAC: 9, MinAsleep: 711}, wantSep29, wantSep30)
}
