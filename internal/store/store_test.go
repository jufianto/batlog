package store

import (
	"context"
	"fmt"
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
	for _, table := range []string{"samples", "apps", "app_energy", "health", "meta", "daily_rollup", "runs"} {
		if err := db.Exec(ctx, "SELECT 1 FROM "+table+" LIMIT 1"); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate must be a no-op, got %v", err)
	}
	var v string
	if err := db.sql.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&v); err != nil || v != "3" {
		t.Fatalf("schema_version = %q, %v; want \"3\"", v, err)
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

func TestBucket(t *testing.T) {
	for ts, want := range map[int64]int64{0: 0, 899: 0, 900: 900, -1: -900, 1_759_000_123: 1_758_999_600} {
		if got := Bucket(ts); got != want {
			t.Errorf("Bucket(%d) = %d, want %d", ts, got, want)
		}
	}
}

func TestWriteTickAddsEnergyToBuckets(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	ticks := []Tick{
		{TS: 1000, Pct: 70, Energy: []EnergyDelta{{App: "Code", CPU: 20e9}, {App: "WindowServer", System: true, CPU: 1e9, GPU: 2e9}}},
		{TS: 1060, Pct: 70, Energy: []EnergyDelta{{App: "Code", CPU: 5e9, GPU: 1e9, ANE: 1e9}}},
		{TS: 1800, Pct: 69, Energy: []EnergyDelta{{App: "Google Chrome", CPU: 30e9}}},
	}
	for _, tk := range ticks {
		if err := db.WriteTick(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_energy`).Scan(&n); err != nil || n != 3 {
		t.Errorf("app_energy rows = %d, %v; want 3 (Code's two ticks share bucket 900)", n, err)
	}
	var cpu, gpu, ane int64
	if err := db.sql.QueryRowContext(ctx, `SELECT cpu_nj, gpu_nj, ane_nj FROM app_energy
		JOIN apps ON apps.id = app_id WHERE name = 'Code' AND ts = 900`).Scan(&cpu, &gpu, &ane); err != nil ||
		cpu != 25e9 || gpu != 1e9 || ane != 1e9 {
		t.Errorf("Code bucket = %d/%d/%d, %v; want 25e9/1e9/1e9", cpu, gpu, ane, err)
	}

	got, err := db.EnergySince(ctx, 0)
	want := []AppEnergy{{900, "Code", false, 27}, {900, "WindowServer", true, 3}, {1800, "Google Chrome", false, 30}}
	if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("EnergySince(0) = %+v, %v; want %+v", got, err, want)
	}
	if got, _ := db.EnergyBetween(ctx, 900, 1800); len(got) != 2 || got[1].App != "WindowServer" {
		t.Errorf("EnergyBetween(900, 1800) = %+v, want bucket 900 only", got)
	}
	if ts, err := db.FirstEnergyTS(ctx); ts != 900 || err != nil {
		t.Errorf("FirstEnergyTS = %d, %v", ts, err)
	}
	if got, _ := db.EnergySince(ctx, 1800); len(got) != 1 || got[0].App != "Google Chrome" {
		t.Errorf("EnergySince(1800) = %+v, want only the 1800 bucket", got)
	}
	if sums, err := db.EnergySums(ctx, 0, 1800); err != nil || fmt.Sprint(sums) != "map[Code:27 WindowServer:3]" {
		t.Errorf("EnergySums = %v, %v", sums, err)
	}
}

func TestEnergySinceOnASchemaTwoDatabase(t *testing.T) {
	db, path := openTemp(t)
	ctx := context.Background()
	if err := db.Exec(ctx, "DROP TABLE app_energy; DROP TABLE apps"); err != nil {
		t.Fatal(err)
	}
	ro, err := Open(path, true) // status reads without migrating
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if got, err := ro.EnergySince(ctx, 0); err != nil || got != nil {
		t.Errorf("EnergySince = %v, %v; want none before the apps table", got, err)
	}
	if ts, err := ro.FirstEnergyTS(ctx); err != nil || ts != 0 {
		t.Errorf("FirstEnergyTS = %d, %v", ts, err)
	}
}

func TestMigrationReplacesTopEnergyTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// A schema-2 database, as every install before ADR-0006 has.
	for _, q := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES('schema_version', '2')`,
		`CREATE TABLE samples (ts INTEGER PRIMARY KEY, pct INTEGER NOT NULL, on_ac INTEGER NOT NULL, charging INTEGER NOT NULL, watts REAL, raw_cur_mah INTEGER, raw_max_mah INTEGER)`,
		`INSERT INTO samples(ts, pct, on_ac, charging) VALUES(1000, 70, 0, 0)`,
		`CREATE TABLE app_energy (ts INTEGER NOT NULL, app TEXT NOT NULL, energy REAL NOT NULL, cpu_pct REAL, is_system INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (ts, app))`,
		`CREATE INDEX app_energy_app_ts ON app_energy (app, ts)`,
		`CREATE TABLE daily_rollup (day TEXT PRIMARY KEY, min_battery INTEGER NOT NULL DEFAULT 0, min_ac INTEGER NOT NULL DEFAULT 0, min_asleep INTEGER NOT NULL DEFAULT 0, pct_consumed REAL, app_energy TEXT)`,
		`CREATE TABLE health (day TEXT PRIMARY KEY, cycles INTEGER, raw_max_mah INTEGER, nominal_mah INTEGER, design_mah INTEGER, temp_c REAL, condition TEXT)`,
		`CREATE TABLE runs (started INTEGER PRIMARY KEY)`,
	} {
		if err := db.Exec(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTick(ctx, Tick{TS: 1060, Pct: 70, Energy: []EnergyDelta{{App: "Code", CPU: 1e9}}}); err != nil {
		t.Fatalf("WriteTick after migrating: %v", err)
	}
	if n, _, _ := db.SampleStats(ctx); n != 2 {
		t.Errorf("samples = %d, want the old row kept", n)
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
	if v != "3" {
		t.Fatalf("schema_version = %q, want 3", v)
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

func TestRunStartsOnASchemaOneDatabase(t *testing.T) {
	db, path := openTemp(t)
	ctx := context.Background()
	if err := db.Exec(ctx, "DROP TABLE runs"); err != nil {
		t.Fatal(err)
	}
	ro, err := Open(path, true) // history reads without migrating
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	got, err := ro.RunStartsBetween(ctx, 0, 1<<40)
	if err != nil || got != nil {
		t.Errorf("RunStartsBetween = %v, %v; want none from a database before the runs table", got, err)
	}
}

func TestRollupStorage(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	for _, tk := range []Tick{{TS: 100, Pct: 90}, {TS: 160, Pct: 89, OnAC: true}, {TS: 220, Pct: 89}, {TS: 280, Pct: 88}} {
		if err := db.WriteTick(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []struct {
		ts  int64
		app string
		j   float64
	}{{100, "Brave", 2.5}, {160, "Brave", 1.5}, {160, "Slack", 1.0}, {280, "Brave", 9.0}} {
		// Real buckets start on multiples of 900; any ts works for the store.
		if err := db.Exec(ctx, `INSERT INTO apps(name) VALUES(?) ON CONFLICT DO NOTHING`, e.app); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(ctx, `INSERT INTO app_energy(ts, app_id, cpu_nj) SELECT ?, id, ? FROM apps WHERE name = ?`,
			e.ts, int64(e.j*1e9), e.app); err != nil {
			t.Fatal(err)
		}
	}
	if ts, err := db.OldestRawTS(ctx); err != nil || ts != 100 {
		t.Errorf("OldestRawTS = %d, %v", ts, err)
	}
	ss, err := db.SamplesBetween(ctx, 100, 220)
	if err != nil || len(ss) != 2 || ss[1].TS != 160 || !ss[1].OnAC {
		t.Errorf("SamplesBetween = %+v, %v", ss, err)
	}
	if s, ok, err := db.FirstSampleFrom(ctx, 161); err != nil || !ok || s.TS != 220 {
		t.Errorf("FirstSampleFrom = %+v %v %v", s, ok, err)
	}
	sums, err := db.EnergySums(ctx, 100, 220)
	if err != nil || len(sums) != 2 || sums["Brave"] != 4.0 || sums["Slack"] != 1.0 {
		t.Errorf("EnergySums = %v, %v", sums, err)
	}

	if _, ok, err := db.RollupCarry(ctx); ok || err != nil {
		t.Errorf("carry before any rollup: %v %v", ok, err)
	}
	day := RollupDay{Day: "1970-01-01", MinBattery: 2, MinAC: 1, MinAsleep: 0, PctConsumed: 1, AppEnergy: sums}
	if err := db.WriteRollup(ctx, day, 100, 220, Sample{TS: 160, Pct: 89, OnAC: true}); err != nil {
		t.Fatal(err)
	}
	if ss, _ := db.SamplesBetween(ctx, 0, 1000); len(ss) != 2 || ss[0].TS != 220 {
		t.Errorf("samples left = %+v, want 220 and 280", ss)
	}
	if sums, _ := db.EnergySums(ctx, 0, 1000); len(sums) != 1 || sums["Brave"] != 9.0 {
		t.Errorf("app_energy left = %v", sums)
	}
	if c, ok, err := db.RollupCarry(ctx); err != nil || !ok || c != (Sample{TS: 160, Pct: 89, OnAC: true}) {
		t.Errorf("carry = %+v %v %v", c, ok, err)
	}

	// The same day again merges instead of replacing.
	more := RollupDay{Day: "1970-01-01", MinBattery: 3, MinAsleep: 4, PctConsumed: 2, AppEnergy: map[string]float64{"Brave": 1, "Zoom": 2}}
	if err := db.WriteRollup(ctx, more, 220, 280, Sample{TS: 220, Pct: 89}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.Rollup(ctx, "1970-01-01")
	want := RollupDay{Day: "1970-01-01", MinBattery: 5, MinAC: 1, MinAsleep: 4, PctConsumed: 3,
		AppEnergy: map[string]float64{"Brave": 5, "Slack": 1, "Zoom": 2}}
	if err != nil || !ok || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Rollup = %+v, want %+v (%v)", got, want, err)
	}
	// An older carry never replaces a newer one.
	if err := db.WriteRollup(ctx, RollupDay{Day: "1969-12-31"}, 0, 50, Sample{TS: 40}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := db.RollupCarry(ctx); c.TS != 220 {
		t.Errorf("carry = %+v, want 220 kept", c)
	}
	if r, ok, _ := db.Rollup(ctx, "1969-12-31"); !ok || r.AppEnergy != nil {
		t.Errorf("empty day = %+v %v, want a row with NULL app_energy", r, ok)
	}
}
