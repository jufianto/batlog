package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "batlog.db")
	db, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestMigrateCreatesSchemaAndIsIdempotent(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	for _, table := range []string{"samples", "app_energy", "health", "meta", "daily_rollup"} {
		if err := db.Exec(ctx, "SELECT 1 FROM "+table+" LIMIT 1"); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate must be a no-op, got %v", err)
	}
	var v string
	if err := db.sql.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&v); err != nil || v != "2" {
		t.Fatalf("schema_version = %q, %v; want \"1\"", v, err)
	}
}

func TestSamplesSinceReturnsOrderedRowsInWindow(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	rows := []Sample{
		{TS: 1000, Pct: 70, OnAC: false, Charging: false, Watts: 8.1},
		{TS: 1060, Pct: 69, OnAC: false, Charging: false, Watts: 8.4},
		{TS: 1120, Pct: 69, OnAC: true, Charging: true, Watts: 40},
	}
	for _, r := range rows {
		if err := db.Exec(ctx, `INSERT INTO samples(ts,pct,on_ac,charging,watts) VALUES(?,?,?,?,?)`,
			r.TS, r.Pct, r.OnAC, r.Charging, r.Watts); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.SamplesSince(ctx, 1060)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TS != 1060 || got[1].TS != 1120 {
		t.Fatalf("SamplesSince = %+v", got)
	}
	if got[1].OnAC != true || got[1].Charging != true || got[1].Watts != 40 {
		t.Errorf("bool/real columns round-trip wrong: %+v", got[1])
	}
}

func TestEnergySince(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	for _, r := range []AppEnergy{{1000, "Code", 20}, {1060, "Google Chrome", 30}, {1060, "Code", 5}} {
		if err := db.Exec(ctx, `INSERT INTO app_energy(ts,app,energy) VALUES(?,?,?)`, r.TS, r.App, r.Energy); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.EnergySince(ctx, 1060)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].App != "Code" || got[1].App != "Google Chrome" {
		t.Fatalf("EnergySince = %+v", got)
	}
}

func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	_, path := openTemp(t)
	ro, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := ro.Exec(context.Background(), `INSERT INTO meta(key,value) VALUES('x','y')`); err == nil {
		t.Fatal("write through a read-only handle must fail")
	}
}

func TestOpenNonDatabaseFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batlog.db")
	if err := os.WriteFile(path, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, true); err == nil {
		t.Fatal("Open must fail on a file that is not a database")
	}
}

func TestExists(t *testing.T) {
	_, path := openTemp(t)
	if !Exists(path) {
		t.Error("Exists must be true for the created database")
	}
	if Exists(filepath.Join(t.TempDir(), "nope.db")) {
		t.Error("Exists must be false for a missing file")
	}
}

func TestOpenPathWithSpacesAndReadOnlyReopen(t *testing.T) {
	// The real path is ~/Library/Application Support/batlog/batlog.db.
	dir := filepath.Join(t.TempDir(), "Application Support", "batlog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "batlog.db")
	db, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if !Exists(path) {
		t.Fatalf("database not created at %q", path)
	}
	ro, err := Open(path, true)
	if err != nil {
		t.Fatalf("read-only reopen of a path with spaces: %v", err)
	}
	ro.Close()
}

func TestHealthSinceSkipsIncompleteRowsAndOrdersByDay(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	rows := []struct {
		day         string
		raw, design any
	}{
		{"2026-06-01", 5500, 6249}, // before the window
		{"2026-07-10", 5480, 6249},
		{"2026-07-03", 5490, 6249}, // inserted out of order
		{"2026-07-05", nil, 6249},  // no raw capacity that day
		{"2026-07-06", 5485, nil},  // no design capacity
	}
	for _, r := range rows {
		if err := db.Exec(ctx, `INSERT INTO health(day, raw_max_mah, design_mah) VALUES(?,?,?)`, r.day, r.raw, r.design); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.HealthSince(ctx, "2026-07-01")
	if err != nil {
		t.Fatal(err)
	}
	want := []HealthRow{{"2026-07-03", 5490, 6249}, {"2026-07-10", 5480, 6249}}
	if len(got) != len(want) {
		t.Fatalf("HealthSince = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func sip(v int) *int         { return &v }
func sfp(v float64) *float64 { return &v }
func ssp(v string) *string   { return &v }

func TestWriteTickStoresSampleAndLastTick(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	if err := db.WriteTick(ctx, Tick{TS: 1000, Pct: 67, Watts: sfp(8.4), RawCurMAh: sip(3600), RawMaxMAh: sip(5424)}); err != nil {
		t.Fatal(err)
	}
	// No watts or raw capacities: stored as NULL, never as 0.
	if err := db.WriteTick(ctx, Tick{TS: 1060, Pct: 66, OnAC: true, Charging: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.SamplesSince(ctx, 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("SamplesSince = %+v, %v", got, err)
	}
	if got[0].Pct != 67 || got[0].Watts != 8.4 || !got[1].OnAC || !got[1].Charging {
		t.Errorf("samples = %+v", got)
	}
	var nulls int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples WHERE ts=1060 AND watts IS NULL AND raw_cur_mah IS NULL AND raw_max_mah IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("missing values must be NULL: %d, %v", nulls, err)
	}
	if v, ok, err := db.Meta(ctx, "last_tick"); err != nil || !ok || v != "1060" {
		t.Errorf("last_tick = %q %v %v", v, ok, err)
	}
}

func TestWriteTickSameSecondTwiceIsHarmless(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := db.WriteTick(ctx, Tick{TS: 1000, Pct: 67}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if n, _, _ := db.SampleStats(ctx); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

func TestWriteTickUpsertsHealthDay(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	day := func(cycles, raw int) *HealthDay {
		return &HealthDay{Day: "2026-09-26", Cycles: sip(cycles), RawMaxMAh: sip(raw), NominalMAh: sip(5576),
			DesignMAh: sip(6249), TempC: sfp(30.9), Condition: ssp("Normal")}
	}
	if err := db.WriteTick(ctx, Tick{TS: 1000, Pct: 60, Health: day(388, 5424)}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTick(ctx, Tick{TS: 1060, Pct: 60, Health: day(389, 5435)}); err != nil {
		t.Fatal(err)
	}
	var n, cycles, raw int
	var cond string
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*), MAX(cycles), MAX(raw_max_mah), MAX(condition) FROM health`).Scan(&n, &cycles, &raw, &cond); err != nil {
		t.Fatal(err)
	}
	if n != 1 || cycles != 389 || raw != 5435 || cond != "Normal" {
		t.Errorf("health rows=%d cycles=%d raw=%d cond=%q, want 1 row with the second write", n, cycles, raw, cond)
	}
	rows, err := db.HealthSince(ctx, "2026-09-01")
	if err != nil || len(rows) != 1 || rows[0] != (HealthRow{"2026-09-26", 5435, 6249}) {
		t.Errorf("HealthSince = %+v, %v", rows, err)
	}
}

func TestMetaAndSampleStatsOnEmptyDatabase(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	if _, ok, err := db.Meta(ctx, "last_tick"); ok || err != nil {
		t.Errorf("Meta on empty db: ok=%v err=%v", ok, err)
	}
	n, oldest, err := db.SampleStats(ctx)
	if n != 0 || oldest != 0 || err != nil {
		t.Errorf("SampleStats = %d %d %v", n, oldest, err)
	}
	for _, ts := range []int64{2000, 1000, 3000} {
		if err := db.WriteTick(ctx, Tick{TS: ts, Pct: 50}); err != nil {
			t.Fatal(err)
		}
	}
	if n, oldest, _ = db.SampleStats(ctx); n != 3 || oldest != 1000 {
		t.Errorf("SampleStats = %d %d, want 3 1000", n, oldest)
	}
}

func TestOpenPathWithURICharacters(t *testing.T) {
	// SQLite reads "file:" DSNs as URIs: an unescaped '#' ends the path,
	// '?' starts parameters and '%' starts an escape.
	for _, name := range []string{"a#b", "c%20d", "e?f", "g h"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "batlog.db")
			db, err := Open(path, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteTick(context.Background(), Tick{TS: 1, Pct: 5}); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if !Exists(path) {
				t.Fatalf("database not at %q", path)
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 {
				t.Errorf("stray files created next to %q: %v", name, entries)
			}
			ro, err := Open(path, true)
			if err != nil {
				t.Fatalf("read-only reopen: %v", err)
			}
			if n, _, _ := ro.SampleStats(context.Background()); n != 1 {
				t.Errorf("read-only handle sees %d samples", n)
			}
			ro.Close()
		})
	}
}

func TestReadOnlyHandlesWaitLessThanTheOneSecondBudget(t *testing.T) {
	if got := dsn("/x/batlog.db", true); !strings.Contains(got, "busy_timeout(500)") || !strings.Contains(got, "mode=ro") {
		t.Errorf("read-only dsn = %q, want busy_timeout(500) and mode=ro", got)
	}
	if got := dsn("/x/batlog.db", false); !strings.Contains(got, "busy_timeout(5000)") || !strings.Contains(got, "journal_mode(WAL)") {
		t.Errorf("writer dsn = %q", got)
	}
}

func TestHealthUpsertKeepsKnownValues(t *testing.T) {
	// A later same-day write with a partial ioreg read must not blank out
	// what the first write recorded.
	db, _ := openTemp(t)
	ctx := context.Background()
	full := &HealthDay{Day: "2026-09-26", Cycles: sip(388), RawMaxMAh: sip(5424), NominalMAh: sip(5576),
		DesignMAh: sip(6249), TempC: sfp(30.9), Condition: ssp("Normal")}
	if err := db.WriteTick(ctx, Tick{TS: 1, Pct: 5, Health: full}); err != nil {
		t.Fatal(err)
	}
	partial := &HealthDay{Day: "2026-09-26", Cycles: sip(389)}
	if err := db.WriteTick(ctx, Tick{TS: 2, Pct: 5, Health: partial}); err != nil {
		t.Fatal(err)
	}
	var cycles, raw, design int
	var cond string
	if err := db.sql.QueryRowContext(ctx, `SELECT cycles, raw_max_mah, design_mah, condition FROM health`).Scan(&cycles, &raw, &design, &cond); err != nil {
		t.Fatalf("a column was blanked: %v", err)
	}
	if cycles != 389 || raw != 5424 || design != 6249 || cond != "Normal" {
		t.Errorf("cycles=%d raw=%d design=%d cond=%q", cycles, raw, design, cond)
	}
}

func TestRunStartsAndSampleLookups(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var v string
	db.sql.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&v)
	if v != "2" {
		t.Fatalf("schema_version = %q, want 2 (runs table)", v)
	}
	for _, ts := range []int64{100, 500, 900} {
		if err := db.RecordRunStart(ctx, ts); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordRunStart(ctx, 500); err != nil {
		t.Errorf("recording the same start twice must be harmless: %v", err)
	}
	got, err := db.RunStartsBetween(ctx, 200, 900)
	if err != nil || len(got) != 2 || got[0] != 500 || got[1] != 900 {
		t.Errorf("RunStartsBetween = %v, %v", got, err)
	}

	for _, s := range []Tick{{TS: 1000, Pct: 90, OnAC: true}, {TS: 1060, Pct: 89}, {TS: 1120, Pct: 88}, {TS: 1180, Pct: 88, OnAC: true}} {
		db.WriteTick(ctx, s)
	}
	if s, ok, err := db.LastSampleBefore(ctx, 1180); err != nil || !ok || s.TS != 1120 {
		t.Errorf("LastSampleBefore(1180) = %+v %v %v", s, ok, err)
	}
	if _, ok, _ := db.LastSampleBefore(ctx, 1000); ok {
		t.Error("nothing before the first sample")
	}
	if s, ok, err := db.LastSampleBeforeWithState(ctx, 1180, true); err != nil || !ok || s.TS != 1000 {
		t.Errorf("last AC sample before 1180 = %+v %v %v", s, ok, err)
	}
}

func TestMigratingAVersionOneDatabaseAddsRuns(t *testing.T) {
	// The live database was created at schema 1; the daemon's next start
	// must upgrade it in place without touching its samples.
	path := filepath.Join(t.TempDir(), "batlog.db")
	db, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	body, _ := migrations.ReadFile("migrations/0001_init.sql")
	db.Exec(ctx, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	db.Exec(ctx, string(body))
	db.Exec(ctx, `INSERT INTO meta VALUES('schema_version','1')`)
	db.WriteTick(ctx, Tick{TS: 1, Pct: 50})
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordRunStart(ctx, 5); err != nil {
		t.Errorf("runs table missing after upgrade: %v", err)
	}
	if n, _, _ := db.SampleStats(ctx); n != 1 {
		t.Errorf("samples after upgrade = %d", n)
	}
	db.Close()
}
