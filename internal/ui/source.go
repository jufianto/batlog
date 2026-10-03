// Package ui is `batlog ui` (docs/specs/F8-ui.md): a full-screen view of the
// battery over the data F1–F6 read. It never reads the store itself: the
// command hands it a Source that calls the same code the other commands do,
// so every number matches theirs.
package ui

import (
	"context"

	"github.com/jufianto/batlog/internal/charge"
	"github.com/jufianto/batlog/internal/health"
	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/status"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/top"
)

// Live is one battery reading and how fresh the recorder's data is.
type Live struct {
	Status   status.Report
	Health   health.Report
	FirstTS  int64 // the first sample; 0 without history
	NewestTS int64 // the newest sample
	WokeAt   int64 // the last wake; 0 when unknown
}

// History is F3 over a range, with the samples it was built from for the
// chart.
type History struct {
	Result    history.Result
	Samples   []store.Sample
	RunStarts []int64
	Curve     *charge.Curve // set while a charge has a time to full to estimate
}

// Apps is F4's ranking over a range.
type Apps struct {
	Recorded bool  // any app energy at all
	First    int64 // when app energy recording began
	Rows     []top.Row
	// Since is when recording began, when that is inside the range; its
	// battery cost covers only the percent used from then on.
	Since          int64
	CoveredPctUsed int
	Short          bool // under 30 awake minutes: shares may be noisy
}

// HealthDays is F2 with its trend, as `health --trend` prints it, and the
// daily rows for the chart.
type HealthDays struct {
	Text string
	Rows []store.HealthRow
}

// Point is one app's energy in one bucket.
type Point struct {
	TS int64
	J  float64
}

// Source is everything the UI reads. Each method calls what its CLI
// command calls.
type Source interface {
	Live(ctx context.Context) (Live, error)
	History(ctx context.Context, r Range) (History, error)
	Apps(ctx context.Context, r Range) (Apps, error)
	AppSeries(ctx context.Context, app string, r Range) ([]Point, error)
	// Session is `top --session <id>` as it prints.
	Session(ctx context.Context, id string) (string, error)
	// Report is `report` for the range as it prints.
	Report(ctx context.Context, r Range) (string, error)
	Health(ctx context.Context) (HealthDays, error)
}

// Warning is returned with a usable value: the read worked, with a caveat
// the key line shows.
type Warning string

func (w Warning) Error() string { return string(w) }
