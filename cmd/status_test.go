package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

var testNow = time.Unix(2_000_000, 0)

type statusOut struct {
	TS              int64    `json:"ts"`
	Percent         int      `json:"percent"`
	OnAC            bool     `json:"on_ac"`
	Charging        bool     `json:"charging"`
	Watts           *float64 `json:"watts"`
	DrainPctPerHr   *float64 `json:"drain_pct_per_hr"`
	EstMinutesLeft  *int     `json:"est_minutes_left"`
	MacOSEstMinutes *int     `json:"macos_est_minutes"`
	WorstOffender   *struct {
		App         string  `json:"app"`
		EnergyShare float64 `json:"energy_share"`
	} `json:"worst_offender"`
}

func stubStatus(t *testing.T, snap battery.Snapshot, dbFile string) {
	t.Helper()
	oldRead, oldDB, oldNow := readBattery, dbPath, now
	readBattery = func(context.Context) (battery.Snapshot, error) { return snap, nil }
	dbPath = func() (string, error) { return dbFile, nil }
	now = func() time.Time { return testNow }
	t.Cleanup(func() { readBattery, dbPath, now = oldRead, oldDB, oldNow })
}

func seededDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "batlog.db")
	db, err := store.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pcts := []int{70, 70, 69, 69, 68, 68, 67, 67, 66, 66}
	for i, p := range pcts {
		ts := testNow.Unix() - int64(60*(len(pcts)-1-i))
		if err := db.Exec(ctx, `INSERT INTO samples(ts,pct,on_ac,charging,watts) VALUES(?,?,0,0,8.4)`, ts, p); err != nil {
			t.Fatal(err)
		}
	}
	for i, e := range []struct {
		app string
		en  float64
	}{{"Google Chrome", 30}, {"Code", 20}, {"Google Chrome", 8}} {
		// Distinct ts per row: (ts, app) is the primary key.
		if err := db.Exec(ctx, `INSERT INTO app_energy(ts,app,energy) VALUES(?,?,?)`, testNow.Unix()-int64(60*i), e.app, e.en); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestStatusJSONWithDaemonData(t *testing.T) {
	stubStatus(t, battery.Snapshot{Percent: 67, Watts: 8.44, HasWatts: true, MacOSMinutes: 312, HasMacOSMinutes: true}, seededDB(t))
	var out, errw bytes.Buffer
	if err := runStatus(context.Background(), &out, &errw, true); err != nil {
		t.Fatal(err)
	}
	var got statusOut
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
	if got.TS != testNow.Unix() || got.Percent != 67 || got.OnAC || got.Charging {
		t.Errorf("live fields: %+v", got)
	}
	if got.Watts == nil || *got.Watts != 8.4 {
		t.Errorf("watts = %v, want 8.4 (rounded to one decimal)", got.Watts)
	}
	if got.DrainPctPerHr == nil || *got.DrainPctPerHr != 29.1 {
		t.Errorf("drain = %v, want 29.1", got.DrainPctPerHr)
	}
	if got.EstMinutesLeft == nil || *got.EstMinutesLeft != 138 {
		t.Errorf("est_minutes_left = %v, want 138", got.EstMinutesLeft)
	}
	if got.MacOSEstMinutes == nil || *got.MacOSEstMinutes != 312 {
		t.Errorf("macos_est_minutes = %v, want 312", got.MacOSEstMinutes)
	}
	if got.WorstOffender == nil || got.WorstOffender.App != "Google Chrome" || got.WorstOffender.EnergyShare != 0.66 {
		t.Errorf("worst_offender = %+v, want Google Chrome 0.66", got.WorstOffender)
	}
	if errw.Len() != 0 {
		t.Errorf("unexpected stderr: %s", errw.String())
	}
}

func TestStatusJSONWithoutDatabaseHasNulls(t *testing.T) {
	stubStatus(t, battery.Snapshot{Percent: 99, OnAC: true, Charging: true, Watts: 14.017, HasWatts: true, MacOSMinutes: 10, HasMacOSMinutes: true},
		filepath.Join(t.TempDir(), "missing.db"))
	var out, errw bytes.Buffer
	if err := runStatus(context.Background(), &out, &errw, true); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{`"drain_pct_per_hr":null`, `"est_minutes_left":null`, `"macos_est_minutes":null`, `"worst_offender":null`, `"watts":14`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON %s lacks %s", s, want)
		}
	}
}

func TestStatusHumanOnACAndTip(t *testing.T) {
	stubStatus(t, battery.Snapshot{Percent: 82, OnAC: true, Charging: true, Watts: 41.8, HasWatts: true}, filepath.Join(t.TempDir(), "missing.db"))
	var out, errw bytes.Buffer
	if err := runStatus(context.Background(), &out, &errw, false); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.HasPrefix(s, "⚡ 82%  ·  AC  ·  charging  ·  41.8 W\n") {
		t.Errorf("first line: %q", s)
	}
	// "drain" alone would also match the tip line; check the value lines.
	if strings.Contains(s, "%/hr") || strings.Contains(s, "collecting") || strings.Contains(s, "est. left") {
		t.Errorf("no drain or estimate on AC: %q", s)
	}
	if !strings.Contains(s, "tip: run 'batlog daemon install' for drain analysis") {
		t.Errorf("missing tip: %q", s)
	}
}

func TestStatusHumanOnBatteryWithData(t *testing.T) {
	stubStatus(t, battery.Snapshot{Percent: 67, Watts: 8.4, HasWatts: true, MacOSMinutes: 312, HasMacOSMinutes: true}, seededDB(t))
	var out, errw bytes.Buffer
	if err := runStatus(context.Background(), &out, &errw, false); err != nil {
		t.Fatal(err)
	}
	want := "🔋 67%  ·  on battery  ·  discharging  ·  8.4 W\n" +
		"drain       29.1 %/hr   (last 10 min)\n" +
		"est. left   2h 18m  (batlog) · 5h 12m (macOS)\n" +
		"worst now   Google Chrome  (66% of energy)\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestStatusHumanCollecting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batlog.db")
	db, err := store.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, p := range []int{68, 67} {
		if err := db.Exec(ctx, `INSERT INTO samples(ts,pct,on_ac,charging) VALUES(?,?,0,0)`, testNow.Unix()-int64(60*(1-i)), p); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	stubStatus(t, battery.Snapshot{Percent: 67}, path)
	var out, errw bytes.Buffer
	if err := runStatus(ctx, &out, &errw, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "drain       collecting…") {
		t.Errorf("got %q", out.String())
	}
	if strings.Contains(out.String(), "tip:") {
		t.Errorf("tip must not show when daemon data exists: %q", out.String())
	}
}

func TestStatusCorruptDatabaseWarnsAndStillPrints(t *testing.T) {
	// Review Focus 5.
	path := filepath.Join(t.TempDir(), "batlog.db")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubStatus(t, battery.Snapshot{Percent: 55, Watts: 7, HasWatts: true}, path)
	var out, errw bytes.Buffer
	if err := runStatus(context.Background(), &out, &errw, false); err != nil {
		t.Fatalf("a bad database must not fail status: %v", err)
	}
	if !strings.HasPrefix(out.String(), "🔋 55%") {
		t.Errorf("live fields missing: %q", out.String())
	}
	if !strings.Contains(errw.String(), "warning") {
		t.Errorf("expected a warning on stderr, got %q", errw.String())
	}
	if strings.Contains(out.String(), "tip:") {
		t.Errorf("the database exists, so 'install the daemon' is the wrong advice:\n%s", out.String())
	}
}

func TestStatusProbeErrorPropagates(t *testing.T) {
	stubStatus(t, battery.Snapshot{}, filepath.Join(t.TempDir(), "missing.db"))
	readBattery = func(context.Context) (battery.Snapshot, error) { return battery.Snapshot{}, battery.ErrNoBattery }
	var out, errw bytes.Buffer
	err := runStatus(context.Background(), &out, &errw, false)
	if err == nil || err.Error() != "no battery found on this machine" {
		t.Errorf("err = %v", err)
	}
}

func TestFmtDuration(t *testing.T) {
	for m, want := range map[int]string{340: "5h 40m", 59: "59m", 60: "1h 0m", 0: "0m"} {
		if got := fmtDuration(m); got != want {
			t.Errorf("fmtDuration(%d) = %q, want %q", m, got, want)
		}
	}
}

func TestStatusTipAfterUninstallKeptTheData(t *testing.T) {
	// uninstall keeps the database; with nothing recording, the tip is
	// exactly the right advice. Only a broken database suppresses it.
	path := filepath.Join(t.TempDir(), "batlog.db")
	db, err := store.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	db.Migrate(context.Background())
	db.Exec(context.Background(), `INSERT INTO samples(ts,pct,on_ac,charging) VALUES(?,60,0,0)`, testNow.Unix()-86400)
	db.Close()
	stubStatus(t, battery.Snapshot{Percent: 60}, path)
	var out, errw bytes.Buffer
	if err := runStatus(context.Background(), &out, &errw, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tip: run 'batlog daemon install' for drain analysis") {
		t.Errorf("no recent samples in a healthy database: want the tip\n%s", out.String())
	}
}
