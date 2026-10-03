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

	"github.com/jufianto/batlog/internal/charge"
	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/localday"
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
	midnight := localday.Start(t)
	switch {
	case week:
		from := localday.Shift(t, -6)
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

// maxSince keeps --since well inside time.Duration: a hundred years.
var maxSince = map[string]int{"d": 36500, "h": 36500 * 24}

// parseSince reads 3d, 12h or 2026-06-01 (local midnight).
func parseSince(s string, t time.Time) (time.Time, error) {
	var from time.Time
	// Parsed as a bare date: in t's zone its midnight may not exist.
	if d, err := time.Parse("2006-01-02", s); err == nil {
		from = localday.Start(time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, t.Location()))
	} else {
		unit := s[len(s)-1:]
		v, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || v <= 0 || (unit != "d" && unit != "h") || v > maxSince[unit] {
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
	if _, woke, ok := wokeAt(); ok {
		in.WokeAt = woke.Unix()
	}
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
	var curve *charge.Curve
	if hasDB && chargingNow(r) {
		curve = loadCurve(ctx, errw, rg.To)
	}
	switch {
	case asJSON && events:
		return writeEventsJSON(out, rg, r)
	case asJSON:
		return writeHistoryJSON(out, rg, r, curve)
	}
	fmt.Fprintf(out, "📅 %s\n", rg.Title)
	if len(r.Events) == 0 && (events || len(r.Sessions) == 0 && len(r.ChargeSessions) == 0) {
		fmt.Fprintln(out, "no charge/discharge events in this range")
		switch {
		case !hasDB || in.FirstSampleTS == 0:
			fmt.Fprintln(out, "no history yet: `batlog daemon install` starts recording")
		case rg.From.Unix() < in.FirstSampleTS:
			fmt.Fprintln(out, "history starts "+time.Unix(in.FirstSampleTS, 0).Format("Mon 02 Jan 15:04"))
		}
		if !events && r.Totals != (history.Totals{}) {
			fmt.Fprintln(out)
			fmt.Fprintln(out, totalsLine(r.Totals))
		}
		return nil
	}
	if events {
		renderEvents(out, r.Events, rg.To)
		return nil
	}
	renderHistory(out, r, rg.To, curve)
	fmt.Fprintln(out)
	if in.FirstSampleTS == 0 { // pmset rows only: there are no totals
		fmt.Fprintln(out, "no history yet: `batlog daemon install` starts recording")
	} else {
		fmt.Fprintln(out, totalsLine(r.Totals))
	}
	return nil
}

// loadSamples reads from the start of the run that holds rg.From, so a
// battery or charge session that began before the range keeps its real
// start: from the last AC sample before a battery run, or the last battery
// sample before an AC run.
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
		// The sample before the run is in the other state.
		before, ok, err := db.LastSampleBeforeWithState(ctx, s.TS, !s.OnAC)
		switch {
		case err != nil:
			return err
		case ok:
			start = before.TS
		default:
			start = first
		}
	}
	if in.Samples, err = db.SamplesSince(ctx, start); err != nil {
		return err
	}
	in.RunStarts, err = db.RunStartsBetween(ctx, start, in.To.Unix())
	return err
}

// curveDays is how far back the charge curve learns from.
const curveDays = 30

// chargingNow reports whether the newest charge session has a time to full
// to estimate.
func chargingNow(r history.Result) bool {
	n := len(r.ChargeSessions)
	return n > 0 && estimable(r.ChargeSessions[n-1])
}

// estimable: ongoing, charging below 100 % at a sample from the last 90 s,
// with the recorder writing. An old percent gives no time to full.
func estimable(c history.ChargeSession) bool {
	return c.Ongoing && c.Charging && c.Current && !c.DataGap && c.EndPct < 100
}

// loadCurve learns this Mac's charge curve from the last curveDays. A
// failed read is a warning and no estimate.
func loadCurve(ctx context.Context, errw io.Writer, t time.Time) *charge.Curve {
	p, err := dbPath()
	if err != nil {
		return nil
	}
	db, err := store.Open(p, true)
	if err != nil {
		fmt.Fprintf(errw, "warning: %v\n", err)
		return nil
	}
	defer db.Close()
	return learnCurve(ctx, db, errw, t)
}

// learnCurve is loadCurve on an open database.
func learnCurve(ctx context.Context, db *store.DB, errw io.Writer, t time.Time) *charge.Curve {
	bands, err := db.ChargeBands(ctx, t.AddDate(0, 0, -curveDays).Unix())
	if err != nil {
		fmt.Fprintf(errw, "warning: reading the charge curve: %v\n", err)
		return nil
	}
	var st [charge.Bands]charge.BandStat
	for _, b := range bands {
		if b.Band >= 0 && b.Band < charge.Bands {
			st[b.Band] = charge.BandStat{Gained: b.Gained, Sec: b.Sec}
		}
	}
	c := charge.Learn(st)
	return &c
}

// estToFull is the curve's time to full for an ongoing charging session.
func estToFull(c history.ChargeSession, curve *charge.Curve) *int {
	if curve == nil || !estimable(c) {
		return nil
	}
	m := curve.MinutesToFull(c.EndPct)
	return &m
}

func renderHistory(w io.Writer, r history.Result, t time.Time, curve *charge.Curve) {
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
		renderSessions(w, r.Sessions, t)
	}
	if len(r.ChargeSessions) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "charging sessions")
		renderCharges(w, r.ChargeSessions, t, curve)
	}
}

// minHoldMin is the least not-charging time a charge session mentions.
const minHoldMin = 5

// renderCharges prints one line per charge session: how it got to full,
// how long it then stayed plugged in at 100 %, and any hold below it.
func renderCharges(w io.Writer, cs []history.ChargeSession, t time.Time, curve *charge.Curve) {
	tw := newTable(w)
	for _, c := range cs {
		end := "now"
		if !c.Ongoing {
			end = clock(c.End, t)
		}
		var parts []string
		switch {
		case c.FullAt == c.Start:
			parts = append(parts, "already full")
		case c.FullAt != 0:
			bound := ""
			if c.FullUpperBound {
				bound = "≤ "
			}
			parts = append(parts, "full in "+bound+fmtDuration(int((c.FullAt-c.Start)/60)))
		case !c.Ongoing:
			p := "unplugged before full"
			if c.MaxPct > c.StartPct {
				bound := ""
				if c.MaxUpperBound {
					bound = "≤ "
				}
				p = fmt.Sprintf("%d%% in %s%s · %s", c.MaxPct, bound, fmtDuration(int((c.MaxAt-c.Start)/60)), p)
			}
			parts = append(parts, p)
		case c.Charging:
			p := "charging"
			if m := estToFull(c, curve); m != nil {
				p += " · full in ~" + fmtDuration(*m)
			}
			parts = append(parts, p)
		default:
			parts = append(parts, "not charging")
		}
		if c.FullAt != 0 {
			at := fmtDuration(c.AtFullMin) + " at 100%"
			if c.Ongoing {
				at += " so far"
			}
			parts = append(parts, at)
		}
		if c.HoldMin >= minHoldMin {
			parts = append(parts, fmt.Sprintf("not charging at %d%% for %s", c.HoldPct, fmtDuration(c.HoldMin)))
		}
		var tags []string
		if c.Ongoing {
			tags = append(tags, "(ongoing)")
		}
		if c.DataGap {
			tags = append(tags, "(data gap)")
		}
		fmt.Fprintf(tw, "  %s\t%s → %s\t%d%% → %d%%\t%s\t%s\n",
			c.ID, clock(c.Start, t), end, c.StartPct, c.EndPct, strings.Join(parts, " · "), strings.Join(tags, " "))
	}
	tw.Flush()
}

// renderSessions prints one line per session, as `history` and `report` list them.
func renderSessions(w io.Writer, ss []history.Session, t time.Time) {
	tw := newTable(w)
	for _, s := range ss {
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
		fmt.Fprintf(tw, "  %s\t%s → %s\t%s\t%d%% → %d%%\t%s\t%s\n",
			s.ID, clock(s.Start, t), end, awake, s.StartPct, s.EndPct, drain, strings.Join(tags, " "))
	}
	tw.Flush()
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
	switch day, today := localday.Start(at), localday.Start(t); {
	case !day.Before(today):
		return at.Format("15:04")
	case localday.Next(day).Equal(today):
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
	ID            string   `json:"id"`
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

type chargeJSON struct {
	ID                 string `json:"id"`
	Start              int64  `json:"start"`
	End                *int64 `json:"end"`
	StartPct           int    `json:"start_pct"`
	EndPct             int    `json:"end_pct"`
	FullAt             *int64 `json:"full_at"`
	MinutesToFull      *int   `json:"minutes_to_full"`
	FullIsUpperBound   bool   `json:"full_is_upper_bound"`
	MaxPct             int    `json:"max_pct"`
	MinutesToMaxPct    int    `json:"minutes_to_max_pct"`
	MaxIsUpperBound    bool   `json:"max_is_upper_bound"`
	MinutesAtFull      *int   `json:"minutes_at_full"`
	NotChargingMinutes int    `json:"not_charging_minutes"`
	NotChargingPct     *int   `json:"not_charging_pct"`
	EstMinutesToFull   *int   `json:"est_minutes_to_full"`
	Charging           bool   `json:"charging"`
	Ongoing            bool   `json:"ongoing"`
	DataGap            bool   `json:"data_gap"`
}

// historyJSON is the schema from docs/specs/F3-history.md.
type historyJSON struct {
	Range          rangeJSON     `json:"range"`
	FirstCharge    *eventJSON    `json:"first_charge"`
	LastUnplug     *eventJSON    `json:"last_unplug"`
	BatteryLasted  *lastedJSON   `json:"battery_lasted"`
	Sessions       []sessionJSON `json:"sessions"`
	ChargeSessions []chargeJSON  `json:"charge_sessions"`
	Totals         totalsJSON    `json:"totals"`
}

func toChargeJSON(c history.ChargeSession, curve *charge.Curve) chargeJSON {
	j := chargeJSON{ID: c.ID, Start: c.Start, StartPct: c.StartPct, EndPct: c.EndPct, FullIsUpperBound: c.FullUpperBound,
		MaxPct: c.MaxPct, MinutesToMaxPct: int((c.MaxAt - c.Start) / 60), MaxIsUpperBound: c.MaxUpperBound,
		NotChargingMinutes: c.HoldMin, EstMinutesToFull: estToFull(c, curve), Charging: c.Charging && c.Ongoing,
		Ongoing: c.Ongoing, DataGap: c.DataGap}
	if !c.Ongoing {
		end := c.End
		j.End = &end
	}
	if c.FullAt != 0 {
		at, to, full := c.FullAt, int((c.FullAt-c.Start)/60), c.AtFullMin
		j.FullAt, j.MinutesToFull, j.MinutesAtFull = &at, &to, &full
	}
	if c.HoldMin > 0 {
		p := c.HoldPct
		j.NotChargingPct = &p
	}
	return j
}

func toEventJSON(e *history.Event) *eventJSON {
	if e == nil {
		return nil
	}
	return &eventJSON{TS: e.TS, Type: eventType(*e), Pct: e.Pct, Source: e.Source}
}

func writeHistoryJSON(out io.Writer, rg timeRange, r history.Result, curve *charge.Curve) error {
	j := historyJSON{
		Range:          rangeJSON{rg.From.Unix(), rg.To.Unix()},
		FirstCharge:    toEventJSON(r.FirstCharge),
		LastUnplug:     toEventJSON(r.LastUnplug),
		Sessions:       []sessionJSON{},
		ChargeSessions: []chargeJSON{},
		Totals: totalsJSON{BatteryMin: r.Totals.BatterySec / 60, ACMin: r.Totals.ACSec / 60,
			SleepMin: r.Totals.SleepSec / 60, GapMin: r.Totals.GapSec / 60},
	}
	if r.Lasted != nil {
		j.BatteryLasted = &lastedJSON{r.Lasted.Minutes, r.Lasted.Ongoing}
	}
	for _, s := range r.Sessions {
		j.Sessions = append(j.Sessions, toSessionJSON(s))
	}
	for _, c := range r.ChargeSessions {
		j.ChargeSessions = append(j.ChargeSessions, toChargeJSON(c, curve))
	}
	return json.NewEncoder(out).Encode(j)
}

func toSessionJSON(s history.Session) sessionJSON {
	sj := sessionJSON{ID: s.ID, Start: s.Start, AwakeMinutes: s.AwakeMin, StartPct: s.StartPct, EndPct: s.EndPct,
		DrainPctPerHr: s.Drain, Ongoing: s.Ongoing, DataGap: s.DataGap, Source: s.Source}
	if !s.Ongoing {
		end := s.End
		sj.End = &end
	}
	return sj
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
