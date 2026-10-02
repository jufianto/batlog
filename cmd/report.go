package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/health"
	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/localday"
	"github.com/jufianto/batlog/internal/report"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/top"
)

var (
	reportDaily, reportWeekly bool
	reportSince               string
)

// reportTopN is how many apps the report lists.
const reportTopN = 5

var errNoHistory = errors.New("report needs daemon history — run 'batlog daemon install'")

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "A digest: battery life, drain, top apps, charging habits and health",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error {
		n := 0
		for _, set := range []bool{reportDaily, reportWeekly, reportSince != ""} {
			if set {
				n++
			}
		}
		if n > 1 {
			return usageError{errors.New("use only one of --daily, --weekly and --since")}
		}
		t := now()
		rg, err := parseRange(reportDaily, reportWeekly, reportSince, t)
		if err != nil {
			return usageError{err}
		}
		kind := "daily"
		switch {
		case reportWeekly:
			kind = "weekly"
		case reportSince != "":
			kind = "since"
		}
		return runReport(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), kind, rg)
	},
}

func init() {
	f := reportCmd.Flags()
	f.BoolVar(&reportDaily, "daily", false, "today, since local midnight (the default)")
	f.BoolVar(&reportWeekly, "weekly", false, "the last seven days, compared with the seven before")
	f.StringVar(&reportSince, "since", "", "a duration back (3d, 12h) or a date (2026-06-01)")
	rootCmd.AddCommand(reportCmd)
}

// reportData is everything both renderings print.
type reportData struct {
	kind     string
	rg       timeRange
	hist     history.Result
	life     *report.Life // weekly and since only
	lifeN    int          // qualifying sessions, when life is nil
	prev     *report.Life // the week before, weekly only
	avg      *float64
	worst    *history.Session
	worstApp *top.Row
	energy   bool // any app energy recorded at all
	first    int64
	apps     ranked
	habits   report.Habits
	flags    []report.Flag
	health   *reportHealth
}

type reportHealth struct {
	pct   float64
	day   string
	trend *health.TrendResult
}

func runReport(ctx context.Context, out, errw io.Writer, kind string, rg timeRange) error {
	p, err := dbPath()
	if err != nil || !store.Exists(p) {
		return errNoHistory
	}
	db, err := store.Open(p, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if n, _, err := db.SampleStats(ctx); err != nil {
		return fmt.Errorf("reading history: %w", err)
	} else if n == 0 {
		return errNoHistory
	}

	from, to := rg.From.Unix(), rg.To.Unix()
	in, err := loadHistory(ctx, db, rg.From, rg.To)
	if err != nil {
		return fmt.Errorf("reading history: %w", err)
	}
	d := reportData{kind: kind, rg: rg, hist: history.Build(in)}
	ss := d.hist.Sessions

	if kind != "daily" {
		if l, n, ok := report.EffectiveLife(ss, from, to); ok {
			d.life = &l
		} else {
			d.lifeN = n
		}
	}
	if kind == "weekly" && d.life != nil {
		prevFrom := localday.Shift(rg.From, -7)
		pin, err := loadHistory(ctx, db, prevFrom, rg.From)
		if err != nil {
			return fmt.Errorf("reading history: %w", err)
		}
		if l, _, ok := report.EffectiveLife(history.Build(pin).Sessions, prevFrom.Unix(), from); ok {
			d.prev = &l
		}
	}
	if a, ok := report.AvgDrain(d.hist.Totals); ok {
		d.avg = &a
	}
	d.worst = report.Worst(ss, from)
	d.habits = report.BuildHabits(d.hist, in.Samples, in.RunStarts, from, to)
	// The days the range touches; a DST week is still seven.
	days := 1
	switch kind {
	case "weekly":
		days = 7
	case "since":
		days = max(1, int(math.Ceil(float64(to-from)/86400)))
	}
	d.flags = report.Flags(d.habits, days)

	// App energy and health each degrade on their own: a failed read is a
	// warning and an empty section, never an empty report.
	if d.first, err = db.FirstEnergyTS(ctx); err != nil {
		fmt.Fprintf(errw, "warning: reading app energy: %v\n", err)
	} else if d.first != 0 {
		d.energy = true
		if d.apps, err = rankRange(ctx, db, in, from, to, d.first); err != nil {
			fmt.Fprintf(errw, "warning: %v\n", err)
		}
		if d.worst != nil {
			end := d.worst.End
			if d.worst.Ongoing {
				end = to
			}
			wk, err := rankRange(ctx, db, in, d.worst.Start, end, d.first)
			if err != nil {
				fmt.Fprintf(errw, "warning: %v\n", err)
			}
			for i := range wk.rows {
				if !wk.rows[i].System {
					d.worstApp = &wk.rows[i]
					break
				}
			}
		}
	}
	if rows, err := db.HealthSince(ctx, health.Since(rg.To)); err != nil {
		fmt.Fprintf(errw, "warning: reading health history: %v\n", err)
	} else if n := len(rows); n > 0 {
		last := rows[n-1]
		tr, _ := health.Trend(rows)
		d.health = &reportHealth{pct: round1(float64(last.RawMaxMAh) * 100 / float64(last.DesignMAh)), day: last.Day, trend: tr}
	}

	if jsonOut {
		return writeReportJSON(out, d)
	}
	renderReport(out, d)
	return nil
}

func renderReport(w io.Writer, d reportData) {
	t := d.rg.To
	title := "today, " + t.Format("Mon 02 Jan")
	switch d.kind {
	case "weekly":
		title = d.rg.From.Format("02 Jan") + " – " + t.Format("02 Jan")
	case "since":
		title = "since " + d.rg.From.Format("Mon 02 Jan 15:04")
	}
	fmt.Fprintln(w, "📊 batlog report · "+title)

	section(w, "battery life")
	fmt.Fprintln(w, totalsLine(d.hist.Totals))
	if l := report.Longest(d.hist.Sessions, d.rg.From.Unix()); l != nil {
		tag := ""
		if l.Ongoing {
			tag = ", ongoing"
		}
		fmt.Fprintf(w, "longest session %s (%s%s)\n", fmtDuration(*l.AwakeMin), l.ID, tag)
	}
	if len(d.hist.Sessions) == 0 {
		fmt.Fprintln(w, "no battery sessions in this range")
	} else {
		renderSessions(w, d.hist.Sessions, t)
	}
	if d.kind != "daily" {
		if d.life == nil {
			fmt.Fprintf(w, "est. full-charge life: not enough sessions yet (%d; needs %d that start at %d%% or more and run %dm or more)\n",
				d.lifeN, report.MinSessions, report.MinLifeStartPct, report.MinSessionMin)
		} else {
			line := "est. full-charge life " + fmtDuration(d.life.Minutes)
			if d.prev != nil {
				line += " (" + lifeChange(d.life.Minutes-d.prev.Minutes) + " vs last week)"
			}
			fmt.Fprintln(w, line)
		}
	}

	section(w, "drain")
	var drain []string
	if d.avg != nil {
		drain = append(drain, fmt.Sprintf("avg %.1f %%/hr", *d.avg))
	}
	if s := d.worst; s != nil {
		worst := fmt.Sprintf("worst %.1f %%/hr (%s, %s)", *s.Drain, s.ID, clock(s.Start, t))
		if a := d.worstApp; a != nil {
			worst += fmt.Sprintf(" — top app %s (%s)", a.App, pct(a.Share))
		} else if d.energy {
			worst += " — no app energy recorded for it"
		}
		drain = append(drain, worst)
	}
	if len(drain) == 0 {
		fmt.Fprintln(w, "no time on battery in this range")
	} else {
		fmt.Fprintln(w, joinDot(drain))
	}

	section(w, "top apps")
	if !d.energy {
		fmt.Fprintln(w, "no app energy recorded yet")
	} else {
		renderTop(w, d.apps, d.first, t, reportTopN)
	}

	section(w, "habits & health")
	h := d.habits
	if h.PlugInMedian != nil && h.UnplugMedian != nil {
		fmt.Fprintf(w, "you typically plug in at %s%% and unplug at %s%%\n",
			report.FmtPct(*h.PlugInMedian), report.FmtPct(*h.UnplugMedian))
	} else {
		fmt.Fprintf(w, "charging habits: not enough sessions yet (%d)\n", h.Completed)
	}
	var parts []string
	if h.Above90Share != nil {
		parts = append(parts, "above 90% "+pct(*h.Above90Share)+" of the time", "below 20% "+pct(*h.Below20Share))
	}
	if h.MaxMinAt100OnAC > 0 {
		parts = append(parts, "longest at 100% on AC "+fmtDuration(h.MaxMinAt100OnAC))
	}
	if len(parts) > 0 {
		fmt.Fprintln(w, joinDot(parts))
	}
	for _, f := range d.flags {
		fmt.Fprintln(w, "⚠ "+f.Message)
	}
	if hh := d.health; hh != nil {
		line := fmt.Sprintf("health %.1f%%", hh.pct)
		if hh.trend != nil {
			line += fmt.Sprintf(" (%s %%/month)", signed(hh.trend.PctPerMonth))
		}
		fmt.Fprintln(w, line)
	}
}

// section prints a rule with a title, 50 columns wide.
func section(w io.Writer, title string) {
	fmt.Fprintln(w, "── "+title+" "+strings.Repeat("─", max(0, 50-4-len([]rune(title)))))
}

// lifeChange prints a change in battery life with an arrow.
func lifeChange(min int) string {
	switch {
	case min > 0:
		return "▲ " + fmtDuration(min)
	case min < 0:
		return "▼ " + fmtDuration(-min)
	}
	return "no change"
}

type lifeJSON struct {
	Minutes  int `json:"minutes"`
	Sessions int `json:"sessions"`
}

type reportBatteryJSON struct {
	BatteryMin int64         `json:"battery_min"`
	ACMin      int64         `json:"ac_min"`
	SleepMin   int64         `json:"sleep_min"`
	GapMin     int64         `json:"gap_min"`
	Longest    *string       `json:"longest_session"`
	Sessions   []sessionJSON `json:"sessions"`
	// EffectiveLife is null on a daily report and with too few sessions;
	// its change is null without a previous week to compare.
	EffectiveLife       *lifeJSON `json:"effective_life"`
	EffectiveLifeChange *int      `json:"effective_life_change_min"`
}

type worstAppJSON struct {
	App   string  `json:"app"`
	Share float64 `json:"share"`
}

type worstJSON struct {
	SessionID     string        `json:"session_id"`
	DrainPctPerHr float64       `json:"drain_pct_per_hr"`
	TopApp        *worstAppJSON `json:"top_app"`
}

type drainJSON struct {
	AvgPctPerHr *float64   `json:"avg_pct_per_hr"`
	Worst       *worstJSON `json:"worst"`
}

type flagJSON struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type habitsJSON struct {
	CompletedSessions int        `json:"completed_sessions"`
	PlugInMedianPct   *float64   `json:"plug_in_median_pct"`
	UnplugMedianPct   *float64   `json:"unplug_median_pct"`
	TimeAbove90Share  *float64   `json:"time_above_90_share"`
	TimeBelow20Share  *float64   `json:"time_below_20_share"`
	MaxMinutesAt100   int        `json:"max_minutes_at_100_on_ac"`
	Flags             []flagJSON `json:"flags"`
}

type reportHealthJSON struct {
	HealthPct float64    `json:"health_pct"`
	Day       string     `json:"day"`
	Trend     *trendJSON `json:"trend"`
}

// reportJSON is the schema from docs/specs/F6-report.md.
type reportJSON struct {
	Range       rangeJSON         `json:"range"`
	Kind        string            `json:"kind"`
	Battery     reportBatteryJSON `json:"battery"`
	Drain       drainJSON         `json:"drain"`
	TopApps     []topRowJSON      `json:"top_apps"`
	EnergySince *int64            `json:"energy_since"`
	Habits      habitsJSON        `json:"habits"`
	Health      *reportHealthJSON `json:"health"`
}

func writeReportJSON(out io.Writer, d reportData) error {
	tot := d.hist.Totals
	j := reportJSON{
		Range: rangeJSON{d.rg.From.Unix(), d.rg.To.Unix()},
		Kind:  d.kind,
		Battery: reportBatteryJSON{BatteryMin: tot.BatterySec / 60, ACMin: tot.ACSec / 60,
			SleepMin: tot.SleepSec / 60, GapMin: tot.GapSec / 60, Sessions: []sessionJSON{}},
		Drain:   drainJSON{AvgPctPerHr: d.avg},
		TopApps: []topRowJSON{},
		Habits: habitsJSON{CompletedSessions: d.habits.Completed, PlugInMedianPct: d.habits.PlugInMedian,
			UnplugMedianPct: d.habits.UnplugMedian, TimeAbove90Share: roundPtr(d.habits.Above90Share, 4),
			TimeBelow20Share: roundPtr(d.habits.Below20Share, 4), MaxMinutesAt100: d.habits.MaxMinAt100OnAC,
			Flags: []flagJSON{}},
	}
	if l := report.Longest(d.hist.Sessions, d.rg.From.Unix()); l != nil {
		j.Battery.Longest = &l.ID
	}
	for _, s := range d.hist.Sessions {
		j.Battery.Sessions = append(j.Battery.Sessions, toSessionJSON(s))
	}
	if d.life != nil {
		j.Battery.EffectiveLife = &lifeJSON{d.life.Minutes, d.life.Sessions}
		if d.prev != nil {
			c := d.life.Minutes - d.prev.Minutes
			j.Battery.EffectiveLifeChange = &c
		}
	}
	if s := d.worst; s != nil {
		j.Drain.Worst = &worstJSON{SessionID: s.ID, DrainPctPerHr: *s.Drain}
		if a := d.worstApp; a != nil {
			j.Drain.Worst.TopApp = &worstAppJSON{a.App, round4(a.Share)}
		}
	}
	rows := d.apps.rows
	for _, r := range rows[:min(reportTopN, len(rows))] {
		j.TopApps = append(j.TopApps, toTopRowJSON(r))
	}
	if d.apps.since != 0 {
		j.EnergySince = &d.apps.since
	}
	for _, f := range d.flags {
		j.Habits.Flags = append(j.Habits.Flags, flagJSON{f.ID, f.Message})
	}
	if hh := d.health; hh != nil {
		j.Health = &reportHealthJSON{HealthPct: hh.pct, Day: hh.day}
		if t := hh.trend; t != nil {
			j.Health.Trend = &trendJSON{FromPct: t.FromPct, ToPct: t.ToPct, PctPerMonth: t.PctPerMonth, Days: t.Days, LastDay: t.LastDay}
		}
	}
	return json.NewEncoder(out).Encode(j)
}
