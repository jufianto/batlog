package recorder

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

func ip(v int) *int         { return &v }
func fp(v float64) *float64 { return &v }

var snap = battery.Snapshot{Percent: 67, Watts: 8.4, HasWatts: true, RawCurrentMAh: 3600, RawMaxMAh: 5424,
	Health: battery.Health{Cycles: ip(388), DesignMAh: ip(6249), RawMaxMAh: ip(5424), NominalMAh: ip(5576),
		TempC: fp(30.91), VoltageV: fp(13.1), FailureStatus: ip(0)}}

func TestTickFromSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 26, 23, 59, 30, 0, time.Local)
	tk := TickFrom(snap, now, true)
	if tk.TS != now.Unix() || tk.Pct != 67 || tk.Watts == nil || *tk.Watts != 8.4 || *tk.RawCurMAh != 3600 || *tk.RawMaxMAh != 5424 {
		t.Errorf("tick = %+v", tk)
	}
	h := tk.Health
	if h == nil || h.Day != "2026-09-26" || *h.Cycles != 388 || *h.NominalMAh != 5576 || *h.TempC != 30.91 || *h.Condition != "Normal" {
		t.Errorf("health = %+v", h)
	}
	if TickFrom(snap, now, false).Health != nil {
		t.Error("withHealth=false must not write a health row")
	}
}

func TestTickFromMissingValuesAreNil(t *testing.T) {
	tk := TickFrom(battery.Snapshot{Percent: 50}, time.Unix(1000, 0), true)
	if tk.Watts != nil || tk.RawCurMAh != nil || tk.RawMaxMAh != nil {
		t.Errorf("missing probe values must be nil: %+v", tk)
	}
	if tk.Health.Condition != nil || tk.Health.Cycles != nil {
		t.Errorf("missing health keys must be nil: %+v", *tk.Health)
	}
	s := snap
	s.Health.FailureStatus = ip(4)
	if c := TickFrom(s, time.Unix(1000, 0), true).Health.Condition; c == nil || *c != "Service recommended" {
		t.Errorf("condition = %v", c)
	}
}

type env struct {
	rec   *Recorder
	db    *store.DB
	logs  *bytes.Buffer
	clock time.Time
	reads int
	fail  map[int]bool // read number → fail
	mu    sync.Mutex
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "batlog.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, logs: &bytes.Buffer{}, clock: time.Date(2026, 9, 26, 23, 58, 0, 0, time.Local), fail: map[int]bool{}}
	e.rec = &Recorder{
		DB: db,
		Read: func(ctx context.Context) (battery.Snapshot, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if _, ok := ctx.Deadline(); !ok {
				t.Error("battery probe must run with a timeout")
			}
			e.reads++
			if e.fail[e.reads] {
				return battery.Snapshot{}, errors.New("ioreg: exit status 1")
			}
			s := snap
			s.Health.Cycles = ip(388 + e.reads) // lets tests see which tick wrote health
			return s, nil
		},
		Now: func() time.Time {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.clock = e.clock.Add(time.Minute)
			return e.clock
		},
		Log: log.New(e.logs, "", 0),
	}
	return e
}

func (e *env) health(t *testing.T) map[string]int {
	t.Helper()
	rows, err := e.db.HealthSince(context.Background(), "2000-01-01")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Day]++
	}
	return out
}

func TestTickWritesHealthOncePerDay(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ { // 23:59, 00:00 (next day), 00:01
		if err := e.rec.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	n, _, _ := e.db.SampleStats(ctx)
	if n != 3 {
		t.Errorf("samples = %d, want 3", n)
	}
	h := e.health(t)
	if len(h) != 2 || h["2026-09-26"] != 1 || h["2026-09-27"] != 1 {
		t.Errorf("health days = %v, want one row for each of the two days", h)
	}
	if v, ok, _ := e.db.Meta(ctx, "last_tick"); !ok || v == "" {
		t.Error("last_tick not written")
	}
}

func TestFailedTickWritesNothingAndRetriesHealth(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fail[1] = true
	if err := e.rec.Tick(ctx); err == nil {
		t.Fatal("probe failure must surface as an error")
	}
	if n, _, _ := e.db.SampleStats(ctx); n != 0 {
		t.Errorf("failed tick wrote %d samples", n)
	}
	if err := e.rec.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if h := e.health(t); h["2026-09-26"] != 1 {
		t.Errorf("health must be written by the first successful tick: %v", h)
	}
}

func TestRunKeepsGoingAfterErrorsAndStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	e.fail[2] = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.rec.Run(ctx, 5*time.Millisecond) }()
	deadline := time.After(5 * time.Second)
	for {
		// Wait for completed writes, not reads: a read's write may still be
		// in flight when the loop is cancelled.
		if n, _, _ := e.db.SampleStats(context.Background()); n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("loop did not tick")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v on cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	if n, _, _ := e.db.SampleStats(context.Background()); n < 3 {
		t.Errorf("samples = %d, want at least 3 (4+ reads, one failed)", n)
	}
	if !strings.Contains(e.logs.String(), "tick skipped: ioreg: exit status 1") {
		t.Errorf("log = %q", e.logs.String())
	}
}

func TestTruncateLogKeepsWholeTrailingLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("line-" + strings.Repeat("x", 5) + "\n") // 11 bytes each
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cut, err := TruncateLog(path, 100, 40)
	if err != nil || !cut {
		t.Fatalf("TruncateLog = %v, %v", cut, err)
	}
	got, _ := os.ReadFile(path)
	kept := strings.TrimSuffix(string(got), TruncatedMarker+"\n")
	if len(kept) > 40 || kept == string(got) || !strings.HasPrefix(kept, "line-") || !strings.HasSuffix(kept, "xxxxx\n") {
		t.Errorf("after cut: %q, want at most 40 bytes of whole lines, then the marker", got)
	}
	if cut, _ := TruncateLog(path, 100, 40); cut {
		t.Error("a small log must be left alone")
	}
	if cut, err := TruncateLog(filepath.Join(t.TempDir(), "none.log"), 100, 40); cut || err != nil {
		t.Errorf("missing log: %v %v", cut, err)
	}
}

func TestTruncateLogInPlaceKeepsAppendersWorking(t *testing.T) {
	// launchd holds the log open in append mode; the file must be cut in
	// place, not replaced, or its writes would go to a deleted inode.
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("old-line\n", 50)), 0o644); err != nil {
		t.Fatal(err)
	}
	appender, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer appender.Close()
	if _, err := TruncateLog(path, 100, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := appender.WriteString("new-line\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(got), "old-line\n"+TruncatedMarker+"\nnew-line\n") || len(got) > 30+len(TruncatedMarker)+1+9 {
		t.Errorf("after cut and append: %q", got)
	}
}

func TestTruncateLogLeavesAMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	os.WriteFile(path, []byte(strings.Repeat("line-xxxxx\n", 20)), 0o644)
	if _, err := TruncateLog(path, 100, 40); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(got), "line-xxxxx\n"+TruncatedMarker+"\n") {
		t.Errorf("after cut: %q, want the kept tail then the marker line", got)
	}
}

func TestRunRecordsItsStart(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.rec.Run(ctx, time.Hour) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if n, _, _ := e.db.SampleStats(context.Background()); n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no first tick")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	starts, err := e.db.RunStartsBetween(context.Background(), 0, 1<<62)
	if err != nil || len(starts) != 1 {
		t.Fatalf("run starts = %v, %v; want exactly one", starts, err)
	}
	if _, oldest, _ := e.db.SampleStats(context.Background()); starts[0] > oldest {
		t.Errorf("start %d recorded after the first tick %d", starts[0], oldest)
	}
}
