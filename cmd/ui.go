package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/health"
	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/ui"
)

// isTerminal and runUI are swapped by tests.
var (
	isTerminal = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
	runUI      = ui.Run
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "A full-screen view: status, sessions and charges, apps, report and health",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error {
		if jsonOut {
			return usageError{errors.New("ui is interactive; use the other commands for JSON")}
		}
		if !isTerminal() {
			return errors.New("batlog ui needs a terminal")
		}
		color := wantColor(os.Stdout) && os.Getenv("TERM") != "dumb"
		return runUI(cmd.Context(), uiSource{}, ui.Options{Color: color, Now: now})
	},
}

func init() {
	rootCmd.AddCommand(uiCmd)
}

// uiSource reads what the UI shows through the functions the other
// commands use. Each call opens the database read-only for its own read.
type uiSource struct{}

// warned turns what a command would print to stderr into a ui.Warning.
func warned(errw *bytes.Buffer) error {
	if s := strings.TrimSpace(errw.String()); s != "" {
		s = strings.ReplaceAll(strings.TrimPrefix(s, "warning: "), "\nwarning: ", "; ")
		return ui.Warning(s)
	}
	return nil
}

func openDB() (*store.DB, bool, error) {
	p, err := dbPath()
	if err != nil || !store.Exists(p) {
		return nil, false, nil
	}
	db, err := store.Open(p, true)
	if err != nil {
		return nil, false, err
	}
	return db, true, nil
}

func (uiSource) Live(ctx context.Context) (ui.Live, error) {
	snap, err := readBattery(ctx)
	if err != nil {
		return ui.Live{}, err
	}
	var errw bytes.Buffer
	t := now()
	r, _ := buildStatus(ctx, &errw, snap, t)
	l := ui.Live{Status: r, Health: health.Build(snap.Health)}
	if _, woke, ok := wokeAt(); ok {
		l.WokeAt = woke.Unix()
	}
	db, ok, err := openDB()
	if err != nil || !ok {
		return l, err
	}
	defer db.Close()
	if _, l.FirstTS, err = db.SampleStats(ctx); err != nil {
		return l, err
	}
	if s, ok, err := db.LastSampleBefore(ctx, t.Unix()+1); err != nil {
		return l, err
	} else if ok {
		l.NewestTS = s.TS
	}
	return l, warned(&errw)
}

func (uiSource) History(ctx context.Context, r ui.Range) (ui.History, error) {
	db, ok, err := openDB()
	if err != nil || !ok {
		return ui.History{}, err
	}
	defer db.Close()
	in, err := loadHistory(ctx, db, r.From, r.To)
	if err != nil {
		return ui.History{}, fmt.Errorf("reading history: %w", err)
	}
	h := ui.History{Result: history.Build(in), Samples: in.Samples, RunStarts: in.RunStarts}
	var errw bytes.Buffer
	if r.Current() && chargingNow(h.Result) {
		h.Curve = learnCurve(ctx, db, &errw, r.To)
	}
	return h, warned(&errw)
}

func (uiSource) Apps(ctx context.Context, r ui.Range) (ui.Apps, error) {
	db, first, ok, err := openEnergyDB(ctx)
	if err != nil || !ok {
		return ui.Apps{}, err
	}
	defer db.Close()
	in, err := loadHistory(ctx, db, r.From, r.To)
	if err != nil {
		return ui.Apps{}, fmt.Errorf("reading history: %w", err)
	}
	rk, err := rankRange(ctx, db, in, r.From.Unix(), r.To.Unix(), first)
	if err != nil {
		return ui.Apps{}, err
	}
	return ui.Apps{Recorded: true, First: first, Rows: rk.rows, Since: rk.since,
		CoveredPctUsed: rk.covered.PctUsed, Short: rk.covered.BatterySec+rk.covered.ACSec < 30*60}, nil
}

func (uiSource) AppSeries(ctx context.Context, app string, r ui.Range) ([]ui.Point, error) {
	db, ok, err := openDB()
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	es, err := db.EnergyBetween(ctx, store.Bucket(r.From.Unix()), r.ChartEnd().Unix())
	if err != nil {
		return nil, fmt.Errorf("reading app energy: %w", err)
	}
	var pts []ui.Point
	for _, e := range es {
		if e.App == app {
			pts = append(pts, ui.Point{TS: e.TS, J: e.Energy})
		}
	}
	return pts, nil
}

func (uiSource) Session(ctx context.Context, id string) (string, error) {
	var out, errw bytes.Buffer
	if err := runTopSession(ctx, &out, &errw, id, now()); err != nil {
		return "", err
	}
	return out.String(), warned(&errw)
}

func (uiSource) Report(ctx context.Context, r ui.Range) (string, error) {
	kind, to := "daily", r.To
	switch {
	case r.Week:
		kind = "weekly"
	case !r.Current():
		kind = "since" // a past day
	}
	if !r.Current() {
		// A past range ends at midnight; a second before keeps the
		// report's dates on the range's last day.
		to = to.Add(-time.Second)
	}
	var out, errw bytes.Buffer
	switch err := runReport(ctx, &out, &errw, kind, timeRange{From: r.From, To: to}); {
	case errors.Is(err, errNoHistory):
		return "no history yet — run 'batlog daemon install'\n", nil
	case err != nil:
		return "", err
	}
	return out.String(), warned(&errw)
}

func (uiSource) Health(ctx context.Context) (ui.HealthDays, error) {
	var out, errw bytes.Buffer
	if err := runHealth(ctx, &out, &errw, true, false, false); err != nil {
		return ui.HealthDays{}, err
	}
	// Drop the title: the view has its own.
	_, text, _ := strings.Cut(out.String(), "\n")
	h := ui.HealthDays{Text: text}
	db, ok, err := openDB()
	if err != nil || !ok {
		return h, err
	}
	defer db.Close()
	if h.Rows, err = db.HealthSince(ctx, ""); err != nil {
		return h, fmt.Errorf("reading health history: %w", err)
	}
	return h, warned(&errw)
}

var _ ui.Source = uiSource{}
