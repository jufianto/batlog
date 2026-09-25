package store

import (
	"context"
	"os"
	"path/filepath"
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
	if err := db.sql.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&v); err != nil || v != "1" {
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
