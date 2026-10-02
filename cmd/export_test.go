package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/store"
)

// stubExport seeds three samples (the last with every optional column),
// two apps' energy and one health day.
func stubExport(t *testing.T) *histFix {
	t.Helper()
	f := stubHistory(t, 719, [][5]int{{0, 2, 80, 79, 0}}, nil, nil, nil)
	db, err := store.Open(f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	watts, cur, maxMAh, design, cycles := 7.25, 4000, 5000, 6000, 12
	cond := "Normal"
	tick := store.Tick{TS: histAt(2), Pct: 78, Charging: true, OnAC: true, Watts: &watts, RawCurMAh: &cur, RawMaxMAh: &maxMAh,
		Health: &store.HealthDay{Day: "2026-09-26", Cycles: &cycles, RawMaxMAh: &maxMAh, DesignMAh: &design, Condition: &cond},
		Energy: []store.EnergyDelta{
			{App: `Say "hi", Inc.`, CPU: 1_500_000_000, GPU: 250_000_000},
			{App: "WindowServer", System: true, CPU: 2e9},
		}}
	if err := db.WriteTick(ctx, tick); err != nil {
		t.Fatal(err)
	}
	return f
}

func iso(min int) string { return time.Unix(histAt(min), 0).Format(time.RFC3339) }

func TestExportSamplesCSV(t *testing.T) {
	stubExport(t)
	got, err := run(t, "export", "samples")
	if err != nil {
		t.Fatal(err)
	}
	want := "ts,pct,on_ac,charging,watts,raw_cur_mah,raw_max_mah\n" +
		iso(0) + ",80,false,false,,,\n" +
		iso(1) + ",79,false,false,,,\n" +
		iso(2) + ",78,true,true,7.25,4000,5000\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got, _ := run(t, "export", "samples", "--since", "2h"); got != "ts,pct,on_ac,charging,watts,raw_cur_mah,raw_max_mah\n" {
		t.Errorf("empty range: %q, want the header only", got)
	}
}

func TestExportAppsQuotesNamesPerRFC4180(t *testing.T) {
	stubExport(t)
	got, err := run(t, "export", "apps", "--csv")
	if err != nil {
		t.Fatal(err)
	}
	b := iso(0) // the bucket that holds 00:02
	want := "ts,app,is_system,cpu_j,gpu_j,ane_j\n" +
		b + `,"Say ""hi"", Inc.",false,1.5,0.25,0` + "\n" +
		b + ",WindowServer,true,2,0,0\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestExportJSON(t *testing.T) {
	stubExport(t)
	out, err := run(t, "export", "samples", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if len(rows) != 3 || rows[0]["ts"] != float64(histAt(0)) || rows[0]["watts"] != nil || rows[2]["raw_max_mah"] != float64(5000) {
		t.Errorf("samples = %s", out)
	}

	out, err = run(t, "export", "apps", "--json")
	if err != nil || !strings.Contains(out, `{"ts":`+jsonInt(histAt(0))+`,"app":"WindowServer","is_system":true,"cpu_j":2,"gpu_j":0,"ane_j":0}`) {
		t.Errorf("apps: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 {
		t.Errorf("apps rows = %d, %v; want 2", len(rows), err)
	}

	out, err = run(t, "export", "health", "--json")
	want := `[` + "\n" + `{"day":"2026-09-26","cycles":12,"raw_max_mah":5000,"nominal_mah":null,"design_mah":6000,"health_pct":83.3,"temp_c":null,"condition":"Normal"}` + "\n]\n"
	if err != nil || out != want {
		t.Errorf("health: %v\n%s\nwant:\n%s", err, out, want)
	}
	if out, _ := run(t, "export", "apps", "--json", "--since", "2h"); out != "[]\n" {
		t.Errorf("empty range: %q, want []", out)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestExportHealthCSV(t *testing.T) {
	stubExport(t)
	got, err := run(t, "export", "health")
	want := "day,cycles,raw_max_mah,nominal_mah,design_mah,health_pct,temp_c,condition\n" +
		"2026-09-26,12,5000,,6000,83.3,,Normal\n"
	if err != nil || got != want {
		t.Errorf("%v\ngot:\n%s\nwant:\n%s", err, got, want)
	}
}

func TestExportToFile(t *testing.T) {
	stubExport(t)
	path := filepath.Join(t.TempDir(), "samples.csv")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "export", "samples", "-o", path)
	var ue usageError
	if err == nil || errors.As(err, &ue) || !strings.Contains(err.Error(), "--force") {
		t.Errorf("existing file: err = %v, want an operational error naming --force", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me" {
		t.Errorf("existing file changed to %q", b)
	}
	if out, err := run(t, "export", "samples", "-o", path, "--force"); err != nil || out != "" {
		t.Fatalf("--force: %v, stdout %q", err, out)
	}
	if b, _ := os.ReadFile(path); !strings.HasPrefix(string(b), "ts,pct,") || strings.Count(string(b), "\n") != 4 {
		t.Errorf("file = %q", b)
	}
}

func TestExportNotesRolledUpDays(t *testing.T) {
	f := stubExport(t)
	db, err := store.Open(f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(context.Background(), `INSERT INTO daily_rollup(day) VALUES('2026-06-01'), ('2026-06-02')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out, err := run(t, "export", "apps")
	if err != nil || !strings.Contains(out, "note: 2 days older than 90 days are rolled up") {
		t.Errorf("%v\n%s", err, out)
	}
	if out, _ := run(t, "export", "health"); strings.Contains(out, "note:") {
		t.Errorf("health is complete; got a note:\n%s", out)
	}
	if out, _ := run(t, "export", "samples", "--since", "1h"); strings.Contains(out, "note:") {
		t.Errorf("a range inside the raw window needs no note:\n%s", out)
	}
}

func TestExportErrors(t *testing.T) {
	for name, segs := range map[string][][5]int{"no database": nil, "no samples": {}} {
		stubHistory(t, 719, segs, nil, nil, nil)
		_, err := run(t, "export", "samples")
		var ue usageError
		if !errors.Is(err, errNothingToExport) || errors.As(err, &ue) {
			t.Errorf("%s: err = %v, want errNothingToExport (exit 1)", name, err)
		}
	}
	stubExport(t)
	var ue usageError
	for _, args := range [][]string{
		{"export"},
		{"export", "bogus"},
		{"export", "samples", "apps"},
		{"export", "samples", "--csv", "--json"},
		{"export", "samples", "--since", "soon"},
	} {
		if _, err := run(t, args...); !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
}
