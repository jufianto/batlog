package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/energy"
	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/top"
)

// liveWait is the gap between the two reads of `top --live`; tests skip it.
var liveWait = func() { time.Sleep(time.Second) }

var (
	topLive, topToday, topWeek bool
	topSince, topSession       string
	topN                       int
)

const topFootnote = "shares are of app energy (kernel counters); battery cost is an estimate"

var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Which apps used the most energy: live, today, this week or in one battery session",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error {
		ranges := 0
		for _, set := range []bool{topToday, topWeek, topSince != "", topLive, topSession != ""} {
			if set {
				ranges++
			}
		}
		if ranges > 1 {
			return usageError{errors.New("use only one of --live, --today, --week, --since and --session")}
		}
		if topN < 1 {
			return usageError{errors.New("-n must be at least 1")}
		}
		ctx, out, errw := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		t := now()
		switch {
		case topLive:
			return runTopLive(out, jsonOut, t)
		case topSession != "":
			if topSession != "last" {
				if _, err := history.IDDay(topSession, t); err != nil {
					return usageError{fmt.Errorf("--session %q: %w", topSession, err)}
				}
			}
			return runTopSession(ctx, out, errw, topSession, t)
		}
		rg, err := parseRange(topToday, topWeek, topSince, t)
		if err != nil {
			return usageError{err}
		}
		label := "today"
		switch {
		case topWeek:
			label = "last 7 days"
		case topSince != "":
			label = "since " + rg.From.Format("Mon 02 Jan 15:04")
		}
		return runTopRange(ctx, out, errw, rg, label, !topWeek && topSince == "")
	},
}

func init() {
	f := topCmd.Flags()
	f.BoolVar(&topLive, "live", false, "energy over the next second, without the daemon")
	f.BoolVar(&topToday, "today", false, "since local midnight (the default once the daemon has recorded app energy)")
	f.BoolVar(&topWeek, "week", false, "the last seven days, from midnight six days ago")
	f.StringVar(&topSince, "since", "", "a duration back (3d, 12h) or a date (2026-06-01)")
	f.StringVar(&topSession, "session", "", "a battery session ID from `batlog history`, or last")
	f.IntVarP(&topN, "number", "n", 10, "how many apps to show")
	rootCmd.AddCommand(topCmd)
}

// openEnergyDB opens the database read-only and says when app energy
// recording began; ok is false when there is no app energy at all.
func openEnergyDB(ctx context.Context) (db *store.DB, first int64, ok bool, err error) {
	p, err := dbPath()
	if err != nil || !store.Exists(p) {
		return nil, 0, false, nil
	}
	if db, err = store.Open(p, true); err != nil {
		return nil, 0, false, err
	}
	if first, err = db.FirstEnergyTS(ctx); err != nil || first == 0 {
		db.Close()
		return nil, 0, false, err
	}
	return db, first, true, nil
}

// loadHistory reads what history.Build needs for from..to.
func loadHistory(ctx context.Context, db *store.DB, from, to time.Time) (history.Input, error) {
	in := history.Input{From: from, To: to}
	if _, woke, ok := wokeAt(); ok {
		in.WokeAt = woke.Unix()
	}
	err := loadSamples(ctx, db, &in)
	return in, err
}

// runTopRange ranks apps over rg. Without any recorded app energy, today's
// view (the default) falls back to live; a --week or --since view has
// nothing to show and says so.
func runTopRange(ctx context.Context, out, errw io.Writer, rg timeRange, label string, liveFallback bool) error {
	db, first, ok, err := openEnergyDB(ctx)
	if err != nil {
		return err
	}
	if !ok {
		if !liveFallback {
			return errors.New("no app energy recorded yet: `batlog daemon install` starts recording")
		}
		fmt.Fprintln(errw, "no app energy recorded yet (`batlog daemon install` records it); showing the last second")
		return runTopLive(out, jsonOut, rg.To)
	}
	defer db.Close()
	in, err := loadHistory(ctx, db, rg.From, rg.To)
	if err != nil {
		return fmt.Errorf("reading history: %w", err)
	}
	tot := history.Build(in).Totals
	rk, err := rankRange(ctx, db, in, rg.From.Unix(), rg.To.Unix(), first)
	if err != nil {
		return err
	}
	if jsonOut {
		return writeTopJSON(out, rg.From.Unix(), rg.To.Unix(), tot, rk, nil)
	}
	fmt.Fprintf(out, "⚡ top energy · %s   (%s awake, %s on battery, %d%% used)\n",
		label, fmtDuration(int((tot.BatterySec+tot.ACSec)/60)), fmtDuration(int(tot.BatterySec/60)), tot.PctUsed)
	renderTop(out, rk, first, rg.To, topN)
	return nil
}

// ranked is a range's rows and the part of it app energy covers.
type ranked struct {
	rows []top.Row
	// since is when app energy recording began, when that is inside the
	// range; 0 when it covers the whole range.
	since int64
	// covered are the range's totals from since on. Battery cost spreads
	// only covered.PctUsed: the percent used before recording began has no
	// app energy to be attributed to.
	covered history.Totals
}

func rankRange(ctx context.Context, db *store.DB, in history.Input, from, to, first int64) (ranked, error) {
	var rk ranked
	cin := in
	cin.Pmset = nil
	// first is the first bucket's start; recording began somewhere in it.
	// A range that reaches into that bucket may predate recording, so it
	// gets the note (at worst for 15 minutes too many).
	if store.Bucket(from) <= first {
		rk.since = first
	}
	cin.From = time.Unix(max(from, first), 0).In(in.To.Location())
	cin.To = time.Unix(to, 0).In(in.To.Location())
	rk.covered = history.Build(cin).Totals
	buckets, err := db.EnergyBetween(ctx, store.Bucket(from), to)
	if err != nil {
		return rk, fmt.Errorf("reading app energy: %w", err)
	}
	// top.Build weighs a bucket by its share of samples in the range, so it
	// needs all of every overlapping bucket's samples: in.Samples can start
	// inside the first one (history loads from the last AC sample).
	samples, err := db.SamplesBetween(ctx, store.Bucket(from), store.Bucket(to-1)+store.BucketSec)
	if err != nil {
		return rk, fmt.Errorf("reading samples: %w", err)
	}
	rk.rows = top.Build(buckets, samples, from, to, rk.covered.PctUsed)
	return rk, nil
}

func runTopSession(ctx context.Context, out, errw io.Writer, id string, t time.Time) error {
	db, first, ok, err := openEnergyDB(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no app energy recorded yet: `batlog daemon install` starts recording")
	}
	defer db.Close()

	in, s, err := findSession(ctx, db, errw, id, t)
	if err != nil {
		return err
	}
	if s == nil {
		if id == "last" {
			return errors.New("no battery session in the last 90 days — see batlog history")
		}
		return fmt.Errorf("no battery session %s in the last 90 days — see batlog history", id)
	}

	end := s.End
	if s.Ongoing {
		end = t.Unix()
	}
	sin := in
	sin.From, sin.To, sin.Pmset = time.Unix(s.Start, 0).In(t.Location()), time.Unix(end, 0).In(t.Location()), nil
	tot := history.Build(sin).Totals
	var rk ranked
	if s.Source == history.SourceBatlog {
		if rk, err = rankRange(ctx, db, in, s.Start, end, first); err != nil {
			return err
		}
	}
	heavy, heavyOK := top.Heaviest(in.Samples, s.Start, end)
	if jsonOut {
		sj := &topSessionJSON{ID: s.ID, Start: s.Start, Ongoing: s.Ongoing}
		if !s.Ongoing {
			sj.End = &s.End
		}
		if heavyOK {
			sj.Heaviest = &heaviestJSON{heavy.Start, heavy.End, round1(heavy.AvgWatts)}
		}
		return writeTopJSON(out, s.Start, end, tot, rk, sj)
	}

	span := clock(s.Start, t) + " → now"
	if !s.Ongoing {
		span = clock(s.Start, t) + " → " + clock(s.End, t)
	}
	parts := []string{"⚡ session " + s.ID, span}
	if s.AwakeMin != nil {
		parts = append(parts, fmtDuration(*s.AwakeMin)+" awake")
	}
	drain := fmt.Sprintf("%d%% → %d%%", s.StartPct, s.EndPct)
	if s.Drain != nil {
		drain += fmt.Sprintf(" (%.1f %%/hr)", *s.Drain)
	}
	fmt.Fprintln(out, joinDot(append(parts, drain)))
	if len(rk.rows) == 0 {
		fmt.Fprintf(out, "no app energy for this session (recording started %s)\n", clock(first, t))
		return nil
	}
	renderTop(out, rk, first, t, topN)
	if heavyOK {
		fmt.Fprintf(out, "heaviest 30 min: %s → %s · %.1f W average\n",
			clock(heavy.Start, t), time.Unix(heavy.End, 0).In(t.Location()).Format("15:04"), heavy.AvgWatts)
	}
	return nil
}

// findSession looks a session up by ID, or the newest one for "last", and
// returns the history input it was built from. s is nil when not found.
// IDs are unique within any history that holds their start day, so an ID's
// history runs from that day to now.
func findSession(ctx context.Context, db *store.DB, errw io.Writer, id string, t time.Time) (history.Input, *history.Session, error) {
	if id == "last" {
		// The newest session is almost always within the week; 90 days is
		// all the raw data there is.
		var in history.Input
		for _, days := range []int{7, 90} {
			var err error
			if in, err = loadHistory(ctx, db, t.AddDate(0, 0, -days), t); err != nil {
				return in, nil, fmt.Errorf("reading history: %w", err)
			}
			if ss := history.Build(in).Sessions; len(ss) > 0 {
				return in, &ss[len(ss)-1], nil
			}
		}
		return in, nil, nil
	}
	from, err := history.IDDay(id, t)
	if err != nil {
		return history.Input{}, nil, err
	}
	in, err := loadHistory(ctx, db, from, t)
	if err != nil {
		return in, nil, fmt.Errorf("reading history: %w", err)
	}
	if in.FirstSampleTS == 0 || from.Unix() < in.FirstSampleTS {
		// A session from before the daemon lives in pmset's log.
		if in.Pmset, err = readPmset(ctx); err != nil {
			if ctx.Err() != nil {
				return in, nil, ctx.Err()
			}
			fmt.Fprintf(errw, "warning: no pmset fallback: %v\n", err)
		}
	}
	for _, s := range history.Build(in).Sessions {
		if s.ID == id {
			return in, &s, nil
		}
	}
	return in, nil, nil
}

func runTopLive(out io.Writer, asJSON bool, t time.Time) error {
	tr := &energy.Tracker{}
	rs, err := readEnergy(tr.Named)
	if err != nil {
		return err
	}
	start := time.Now()
	tr.Update(start, rs)
	liveWait()
	if rs, err = readEnergy(tr.Named); err != nil {
		return err
	}
	ds, err := tr.Update(time.Now(), rs)
	if err != nil {
		return err
	}
	rows := top.Live(ds)
	if asJSON {
		j := liveJSON{Range: rangeJSON{t.Unix(), t.Unix() + 1}, Live: true, Rows: []liveRowJSON{}}
		for _, r := range rows[:min(topN, len(rows))] {
			j.Rows = append(j.Rows, liveRowJSON{r.App, round4(r.Share), r.System, round1(r.EnergyJ)})
		}
		return json.NewEncoder(out).Encode(j)
	}
	fmt.Fprintln(out, "⚡ top energy · live (1 s)")
	if len(rows) == 0 {
		fmt.Fprintln(out, "no app used measurable energy in that second")
		return nil
	}
	tw := newTable(out)
	fmt.Fprintln(tw, " #\tAPP\tSHARE")
	for i, r := range rows[:min(topN, len(rows))] {
		fmt.Fprintf(tw, " %d\t%s\t%s\n", i+1, appLabel(r), pct(r.Share))
	}
	tw.Flush()
	return nil
}

func renderTop(out io.Writer, rk ranked, first int64, t time.Time, n int) {
	rows := rk.rows
	if len(rows) == 0 {
		fmt.Fprintln(out, "no app energy recorded for this range")
		fmt.Fprintf(out, "recording started %s\n", clock(first, t))
		return
	}
	tw := newTable(out)
	fmt.Fprintln(tw, " #\tAPP\tSHARE\tBATTERY COST")
	named := false
	for i, r := range rows[:min(n, len(rows))] {
		cost := "—"
		if r.BatteryPct != nil {
			switch p := *r.BatteryPct; {
			case p > 0 && p < 0.5:
				cost = "< 1%"
			default:
				cost = fmt.Sprintf("≈ %.0f%%", p)
			}
			if !named {
				cost += " of battery"
				named = true
			}
		}
		fmt.Fprintf(tw, " %d\t%s\t%s\t%s\n", i+1, appLabel(r), pct(r.Share), cost)
	}
	tw.Flush()
	if rk.since != 0 {
		fmt.Fprintf(out, "app energy from about %s only, when recording started; battery cost covers the %d%% used since\n",
			clock(rk.since, t), rk.covered.PctUsed)
	}
	if rk.covered.BatterySec+rk.covered.ACSec < 30*60 {
		fmt.Fprintln(out, "short window — shares may be noisy")
	}
	fmt.Fprintln(out, topFootnote)
}

func appLabel(r top.Row) string {
	if r.System {
		return r.App + " ⚙"
	}
	return r.App
}

func pct(share float64) string {
	if share > 0 && share < 0.005 {
		return "< 1%"
	}
	return fmt.Sprintf("%.0f%%", share*100)
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

type topRowJSON struct {
	App           string   `json:"app"`
	Share         float64  `json:"share"`
	EstBatteryPct *float64 `json:"est_battery_pct"`
	IsSystem      bool     `json:"is_system"`
	EnergyJ       float64  `json:"energy_j"`
}

type heaviestJSON struct {
	Start    int64   `json:"start"`
	End      int64   `json:"end"`
	AvgWatts float64 `json:"avg_watts"`
}

type topSessionJSON struct {
	ID       string        `json:"id"`
	Start    int64         `json:"start"`
	End      *int64        `json:"end"`
	Ongoing  bool          `json:"ongoing"`
	Heaviest *heaviestJSON `json:"heaviest"`
}

// topJSON is the schema from docs/specs/F4-top.md.
type topJSON struct {
	Range      rangeJSON `json:"range"`
	AwakeMin   int64     `json:"awake_min"`
	BatteryMin int64     `json:"battery_min"`
	PctUsed    int       `json:"pct_used"`
	// EnergySince is when app energy recording began, when inside the
	// range; est_battery_pct then sums to CostPctUsed, not PctUsed.
	EnergySince *int64          `json:"energy_since"`
	CostPctUsed int             `json:"cost_pct_used"`
	Rows        []topRowJSON    `json:"rows"`
	Session     *topSessionJSON `json:"session,omitempty"`
}

type liveRowJSON struct {
	App      string  `json:"app"`
	Share    float64 `json:"share"`
	IsSystem bool    `json:"is_system"`
	EnergyJ  float64 `json:"energy_j"`
}

type liveJSON struct {
	Range rangeJSON     `json:"range"`
	Live  bool          `json:"live"`
	Rows  []liveRowJSON `json:"rows"`
}

func writeTopJSON(out io.Writer, from, to int64, tot history.Totals, rk ranked, s *topSessionJSON) error {
	j := topJSON{Range: rangeJSON{from, to}, AwakeMin: (tot.BatterySec + tot.ACSec) / 60,
		BatteryMin: tot.BatterySec / 60, PctUsed: tot.PctUsed, CostPctUsed: rk.covered.PctUsed, Rows: []topRowJSON{}, Session: s}
	if rk.since != 0 {
		j.EnergySince = &rk.since
	}
	rows := rk.rows
	for _, r := range rows[:min(topN, len(rows))] {
		j.Rows = append(j.Rows, toTopRowJSON(r))
	}
	return json.NewEncoder(out).Encode(j)
}

func toTopRowJSON(r top.Row) topRowJSON {
	var est *float64
	if r.BatteryPct != nil {
		v := round1(*r.BatteryPct)
		est = &v
	}
	return topRowJSON{r.App, round4(r.Share), est, r.System, round1(r.EnergyJ)}
}
