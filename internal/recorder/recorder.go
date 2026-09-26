// Package recorder is the daemon's loop: once a minute, read the battery and
// write one tick to the store (docs/specs/F5-daemon.md).
package recorder

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/health"
	"github.com/jufianto/batlog/internal/rollup"
	"github.com/jufianto/batlog/internal/store"
)

// probeTimeout bounds one battery read, per the F5 spec.
const probeTimeout = 10 * time.Second

// Recorder writes ticks. Read and Now are injectable for tests.
type Recorder struct {
	DB   *store.DB
	Read func(context.Context) (battery.Snapshot, error)
	Now  func() time.Time
	Log  *log.Logger
	// Rollup prunes raw rows older than 90 days; nil means rollup.Run.
	Rollup func(context.Context, *store.DB, time.Time) (rollup.Result, error)

	lastHealthDay string // local date of the last health row this process wrote
}

// Tick reads the battery and writes one sample. The first successful tick of
// each local calendar day, and the first after the process starts, also
// writes that day's health row and rolls up raw rows older than 90 days.
func (r *Recorder) Tick(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	s, err := r.Read(pctx)
	if err != nil {
		return err
	}
	now := r.Now()
	day := now.Format("2006-01-02")
	withHealth := day != r.lastHealthDay
	if err := r.DB.WriteTick(ctx, TickFrom(s, now, withHealth)); err != nil {
		return err
	}
	if withHealth {
		r.lastHealthDay = day
		r.rollup(ctx, now)
	}
	return nil
}

// rollup logs what it did; a failure never fails the tick.
func (r *Recorder) rollup(ctx context.Context, now time.Time) {
	run := r.Rollup
	if run == nil {
		run = rollup.Run
	}
	res, err := run(ctx, r.DB, now)
	switch {
	case res.Days == 1:
		r.Log.Printf("rolled up 1 day (%s)", res.First)
	case res.Days > 1:
		r.Log.Printf("rolled up %d days (%s → %s)", res.Days, res.First, res.Last)
	}
	if err != nil && ctx.Err() == nil {
		r.Log.Printf("rollup failed: %v", err)
	}
}

// Start records that this recorder process began, so history can tell a
// gap it caused (daemon down, Mac off) from one caused by sleep.
func (r *Recorder) Start(ctx context.Context) error {
	return r.DB.RecordRunStart(ctx, r.Now().Unix())
}

// Run records its start, then ticks now and every interval until ctx is
// cancelled. A failed tick is logged and skipped; the loop never exits on a
// data error.
func (r *Recorder) Run(ctx context.Context, every time.Duration) error {
	if err := r.Start(ctx); err != nil && ctx.Err() == nil {
		r.Log.Printf("could not record start: %v", err)
	}
	tick := func() {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.Log.Printf("tick skipped: %v", err)
		}
	}
	tick()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			tick()
		}
	}
}

// TickFrom maps a battery snapshot to the row the store writes. Values the
// probe did not report stay nil so they are stored as NULL.
func TickFrom(s battery.Snapshot, now time.Time, withHealth bool) store.Tick {
	t := store.Tick{TS: now.Unix(), Pct: s.Percent, OnAC: s.OnAC, Charging: s.Charging}
	if s.HasWatts {
		w := s.Watts
		t.Watts = &w
	}
	if s.RawCurrentMAh > 0 {
		v := s.RawCurrentMAh
		t.RawCurMAh = &v
	}
	if s.RawMaxMAh > 0 {
		v := s.RawMaxMAh
		t.RawMaxMAh = &v
	}
	if withHealth {
		h := s.Health
		t.Health = &store.HealthDay{
			Day:        now.Format("2006-01-02"),
			Cycles:     h.Cycles,
			RawMaxMAh:  h.RawMaxMAh,
			NominalMAh: h.NominalMAh,
			DesignMAh:  h.DesignMAh,
			TempC:      h.TempC,
			Condition:  health.Build(h).Condition,
		}
	}
	return t
}

// TruncatedMarker is the line TruncateLog leaves after the kept tail, so
// `logs -f` can resume after it instead of replaying the tail.
const TruncatedMarker = "--- batlog: log truncated here ---"

// TruncateLog cuts a log larger than max bytes down to its last keep bytes,
// starting at a line boundary, followed by TruncatedMarker. It works in
// place because launchd holds the file open in append mode; replacing the
// file would orphan launchd's writes.
func TruncateLog(path string, max, keep int64) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() <= max {
		return false, err
	}
	tail := make([]byte, keep)
	if _, err := f.ReadAt(tail, st.Size()-keep); err != nil {
		return false, err
	}
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	tail = append(tail, TruncatedMarker+"\n"...)
	if err := f.Truncate(0); err != nil {
		return false, err
	}
	_, err = f.WriteAt(tail, 0)
	return err == nil, err
}
