package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/pmset"
	"github.com/jufianto/batlog/internal/store"
)

// readPmset is swapped by tests; the real one takes about two seconds.
var readPmset = pmset.Read

var (
	histToday, histWeek, histEvents bool
	histSince                       string
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "First charge, last unplug and how long each battery session lasted",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error {
		rg, err := parseRange(histToday, histWeek, histSince, now())
		if err != nil {
			return usageError{err}
		}
		return runHistory(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), rg, histEvents, jsonOut)
	},
}

func init() {
	f := historyCmd.Flags()
	f.BoolVar(&histToday, "today", false, "since local midnight (the default)")
	f.BoolVar(&histWeek, "week", false, "the last seven days, from midnight six days ago")
	f.StringVar(&histSince, "since", "", "a duration back (3d, 12h) or a date (2026-06-01)")
	f.BoolVar(&histEvents, "events", false, "print raw plug and unplug events instead of sessions")
	rootCmd.AddCommand(historyCmd)
}

// timeRange is the span history reports on; To is always now.
type timeRange struct {
	From, To time.Time
	Title    string
}

func parseRange(today, week bool, since string, t time.Time) (timeRange, error) {
	n := 0
	for _, set := range []bool{today, week, since != ""} {
		if set {
			n++
		}
	}
	if n > 1 {
		return timeRange{}, errors.New("use only one of --today, --week and --since")
	}
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	switch {
	case week:
		from := midnight.AddDate(0, 0, -6)
		return timeRange{from, t, "Last 7 days, " + from.Format("Mon 02 Jan") + " → " + t.Format("Mon 02 Jan")}, nil
	case since != "":
		from, err := parseSince(since, t)
		if err != nil {
			return timeRange{}, err
		}
		return timeRange{from, t, "Since " + from.Format("Mon 02 Jan 15:04")}, nil
	}
	return timeRange{midnight, t, "Today, " + t.Format("Mon 02 Jan")}, nil
}

// parseSince reads 3d, 12h or 2026-06-01 (local midnight).
func parseSince(s string, t time.Time) (time.Time, error) {
	var from time.Time
	if d, err := time.ParseInLocation("2006-01-02", s, t.Location()); err == nil {
		from = d
	} else {
		unit := s[len(s)-1:]
		v, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || v <= 0 || (unit != "d" && unit != "h") {
			return time.Time{}, fmt.Errorf("--since %q: want a duration like 3d or 12h, or a date like 2026-06-01", s)
		}
		if unit == "d" {
			from = t.AddDate(0, 0, -v)
		} else {
			from = t.Add(-time.Duration(v) * time.Hour)
		}
	}
	if from.After(t) {
		return time.Time{}, fmt.Errorf("--since %s is in the future", s)
	}
	return from, nil
}

func runHistory(ctx context.Context, out, errw io.Writer, rg timeRange, events, asJSON bool) error {
	in := history.Input{From: rg.From, To: rg.To}
	hasDB := false
	if p, err := dbPath(); err == nil && store.Exists(p) {
		db, err := store.Open(p, true)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := loadSamples(ctx, db, &in); err != nil {
			return fmt.Errorf("reading history: %w", err)
		}
		hasDB = true
	}
	// pmset covers only time the daemon has not seen.
	if in.FirstSampleTS == 0 || rg.From.Unix() < in.FirstSampleTS {
		rs, err := readPmset(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(errw, "warning: no pmset fallback: %v\n", err)
		}
		in.Pmset = rs
	}

	r := history.Build(in)
	switch {
	case asJSON && events:
		return writeEventsJSON(out, rg, r)
	case asJSON:
		return writeHistoryJSON(out, rg, r)
	}
	fmt.Fprintf(out, "📅 %s\n", rg.Title)
	if len(r.Events) == 0 && len(r.Sessions) == 0 {
		fmt.Fprintln(out, "no charge/discharge events in this range")
		switch {
		case !hasDB || in.FirstSampleTS == 0:
			fmt.Fprintln(out, "no history yet: `batlog daemon install` starts recording")
		case rg.From.Unix() < in.FirstSampleTS:
			fmt.Fprintln(out, "history starts "+time.Unix(in.FirstSampleTS, 0).Format("Mon 02 Jan 15:04"))
		}
		if r.Totals != (history.Totals{}) {
			fmt.Fprintln(out)
			fmt.Fprintln(out, totalsLine(r.Totals))
		}
		return nil
	}
	if events {
		renderEvents(out, r.Events, rg.To)
		return nil
	}
	renderHistory(out, r, rg.To)
	return nil
}

// loadSamples reads from the start of the battery run that holds rg.From,
// so a session that began before the range keeps its real start.
func loadSamples(ctx context.Context, db *store.DB, in *history.Input) error {
	_, first, err := db.SampleStats(ctx)
	if err != nil {
		return err
	}
	in.FirstSampleTS = first
	from := in.From.Unix()
	start := from
	s, ok, err := db.LastSampleBefore(ctx, from)
	if err != nil {
		return err
	}
	if ok {
		start = s.TS
		if !s.OnAC {
			ac, ok, err := db.LastSampleBeforeWithState(ctx, s.TS, true)
			switch {
			case err != nil:
				return err
			case ok:
				start = ac.TS
			default:
				start = first
			}
		}
	}
	if in.Samples, err = db.SamplesSince(ctx, start); err != nil {
		return err
	}
	in.RunStarts, err = db.RunStartsBetween(ctx, start, in.To.Unix())
	return err
}

func renderHistory(w io.Writer, r history.Result, t time.Time) {
	line := func(label, value string) { fmt.Fprintf(w, "%-19s%s\n", label, value) }
	at := func(e *history.Event) string {
		if e == nil {
			return "—"
		}
		return fmt.Sprintf("%s  (at %d%%)%s", clock(e.TS, t), e.Pct, pmsetTag(e.Source))
	}
	line("first charge", at(r.FirstCharge))
	line("last on battery", at(r.LastUnplug))
	switch {
	case r.Lasted == nil:
		line("battery lasted", "—")
	case r.Lasted.Ongoing:
		line("battery lasted", "ongoing, "+fmtDuration(r.Lasted.Minutes)+" so far")
	default:
		line("battery lasted", fmtDuration(r.Lasted.Minutes))
	}

	if len(r.Sessions) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "battery sessions")
		tw := newTable(w)
		for _, s := range r.Sessions {
			end, awake, drain := "now", "—", ""
			if !s.Ongoing {
				end = clock(s.End, t)
			}
			if s.AwakeMin != nil {
				awake = fmtDuration(*s.AwakeMin) + " awake"
				if s.Ongoing {
					awake = fmtDuration(*s.AwakeMin) + " so far"
				}
			}
			if s.Drain != nil {
				drain = fmt.Sprintf("%.1f %%/hr", *s.Drain)
			}
			var tags []string
			if s.Ongoing {
				tags = append(tags, "(ongoing)")
			}
			if s.DataGap {
				tags = append(tags, "(data gap)")
			}
			if s.Source == history.SourcePmset {
				tags = append(tags, "(pmset)")
			}
			fmt.Fprintf(tw, "  %s → %s\t%s\t%d%% → %d%%\t%s\t%s\n",
				clock(s.Start, t), end, awake, s.StartPct, s.EndPct, drain, strings.Join(tags, " "))
		}
		tw.Flush()
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, totalsLine(r.Totals))
}

func renderEvents(w io.Writer, events []history.Event, t time.Time) {
	tw := newTable(w)
	for _, e := range events {
		fmt.Fprintf(tw, "  %s\t%s\t%d%%\t%s\n", clock(e.TS, t), eventType(e), e.Pct, strings.TrimSpace(pmsetTag(e.Source)))
	}
	tw.Flush()
}

// table aligns tab-separated columns and drops the padding an empty last
// column leaves at the end of a line.
type table struct {
	w   io.Writer
	buf strings.Builder
	*tabwriter.Writer
}

func newTable(w io.Writer) *table {
	t := &table{w: w}
	t.Writer = tabwriter.NewWriter(&t.buf, 0, 0, 3, ' ', 0)
	return t
}

func (t *table) Flush() {
	t.Writer.Flush()
	for _, l := range strings.SplitAfter(t.buf.String(), "\n") {
		if l != "" {
			fmt.Fprintln(t.w, strings.TrimRight(l, " \n"))
		}
	}
}

func totalsLine(t history.Totals) string {
	parts := []string{
		"on battery " + fmtDuration(int(t.BatterySec/60)),
		"on AC " + fmtDuration(int(t.ACSec/60)),
		"asleep " + fmtDuration(int(t.SleepSec/60)),
	}
	if t.GapSec > 0 {
		parts = append(parts, "no data "+fmtDuration(int(t.GapSec/60)))
	}
	return joinDot(parts)
}

// clock prints a time as 15:04 on t's day, "yesterday 15:04" the day
// before, and "Thu 24 Sep 15:04" further back.
func clock(ts int64, t time.Time) string {
	at := time.Unix(ts, 0).In(t.Location())
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location()) }
	switch d := day(t).Sub(day(at)); {
	case d <= 0:
		return at.Format("15:04")
	case d <= 24*time.Hour+time.Hour: // a DST day is 25 h long
		return at.Format("yesterday 15:04")
	}
	return at.Format("Mon 02 Jan 15:04")
}

func pmsetTag(source string) string {
	if source == history.SourcePmset {
		return "  (pmset)"
	}
	return ""
}

func eventType(e history.Event) string {
	if e.Plugged {
		return "plugged"
	}
	return "unplugged"
}

type rangeJSON struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type eventJSON struct {
	TS     int64  `json:"ts"`
	Type   string `json:"type"`
	Pct    int    `json:"pct"`
	Source string `json:"source"`
}

type sessionJSON struct {
	Start         int64    `json:"start"`
	End           *int64   `json:"end"`
	AwakeMinutes  *int     `json:"awake_minutes"`
	StartPct      int      `json:"start_pct"`
	EndPct        int      `json:"end_pct"`
	DrainPctPerHr *float64 `json:"drain_pct_per_hr"`
	Ongoing       bool     `json:"ongoing"`
	DataGap       bool     `json:"data_gap"`
	Source        string   `json:"source"`
}

type lastedJSON struct {
	Minutes int  `json:"minutes"`
	Ongoing bool `json:"ongoing"`
}

type totalsJSON struct {
	BatteryMin int64 `json:"battery_min"`
	ACMin      int64 `json:"ac_min"`
	SleepMin   int64 `json:"sleep_min"`
	GapMin     int64 `json:"gap_min"`
}

// historyJSON is the schema from docs/specs/F3-history.md.
type historyJSON struct {
	Range         rangeJSON     `json:"range"`
	FirstCharge   *eventJSON    `json:"first_charge"`
	LastUnplug    *eventJSON    `json:"last_unplug"`
	BatteryLasted *lastedJSON   `json:"battery_lasted"`
	Sessions      []sessionJSON `json:"sessions"`
	Totals        totalsJSON    `json:"totals"`
}

func toEventJSON(e *history.Event) *eventJSON {
	if e == nil {
		return nil
	}
	return &eventJSON{TS: e.TS, Type: eventType(*e), Pct: e.Pct, Source: e.Source}
}

func writeHistoryJSON(out io.Writer, rg timeRange, r history.Result) error {
	j := historyJSON{
		Range:       rangeJSON{rg.From.Unix(), rg.To.Unix()},
		FirstCharge: toEventJSON(r.FirstCharge),
		LastUnplug:  toEventJSON(r.LastUnplug),
		Sessions:    []sessionJSON{},
		Totals: totalsJSON{BatteryMin: r.Totals.BatterySec / 60, ACMin: r.Totals.ACSec / 60,
			SleepMin: r.Totals.SleepSec / 60, GapMin: r.Totals.GapSec / 60},
	}
	if r.Lasted != nil {
		j.BatteryLasted = &lastedJSON{r.Lasted.Minutes, r.Lasted.Ongoing}
	}
	for _, s := range r.Sessions {
		sj := sessionJSON{Start: s.Start, AwakeMinutes: s.AwakeMin, StartPct: s.StartPct, EndPct: s.EndPct,
			DrainPctPerHr: s.Drain, Ongoing: s.Ongoing, DataGap: s.DataGap, Source: s.Source}
		if !s.Ongoing {
			end := s.End
			sj.End = &end
		}
		j.Sessions = append(j.Sessions, sj)
	}
	return json.NewEncoder(out).Encode(j)
}

func writeEventsJSON(out io.Writer, rg timeRange, r history.Result) error {
	j := struct {
		Range  rangeJSON   `json:"range"`
		Events []eventJSON `json:"events"`
	}{Range: rangeJSON{rg.From.Unix(), rg.To.Unix()}, Events: []eventJSON{}}
	for i := range r.Events {
		j.Events = append(j.Events, *toEventJSON(&r.Events[i]))
	}
	return json.NewEncoder(out).Encode(j)
}
