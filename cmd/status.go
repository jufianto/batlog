package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/charge"
	"github.com/jufianto/batlog/internal/energy"
	"github.com/jufianto/batlog/internal/paths"
	"github.com/jufianto/batlog/internal/status"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/textfmt"
)

// Swapped by tests so nothing shells out or touches the real data directory.
var (
	readBattery = battery.Read
	readEnergy  = energy.Read
	dbPath      = paths.DB
	now         = time.Now
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Battery level, drain rate, watts and a steady time-left estimate",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonOut)
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(ctx context.Context, out, errw io.Writer, asJSON bool) error {
	snap, err := readBattery(ctx)
	if err != nil {
		return err
	}
	r, dbBroken := buildStatus(ctx, errw, snap, now())
	if asJSON {
		return writeStatusJSON(out, r)
	}
	renderStatus(out, r, dbBroken)
	return nil
}

// buildStatus adds the daemon's history to a battery reading: drain, the
// estimates and the worst app. dbBroken is a database that exists but
// failed to open; the warning went to errw.
func buildStatus(ctx context.Context, errw io.Writer, snap battery.Snapshot, t time.Time) (r status.Report, dbBroken bool) {
	since := t.Add(-status.Window).Unix()

	var samples []store.Sample
	var energy []store.AppEnergy
	// Without the daemon's history the curve is the default one: a time
	// to full is still better than none.
	def := charge.Learn([charge.Bands]charge.BandStat{})
	curve := &def
	if p, err := dbPath(); err == nil && store.Exists(p) {
		// A database that fails to open is a warning, never a reason to hide
		// the live fields. status never creates the database.
		db, err := store.Open(p, true)
		if err != nil {
			fmt.Fprintf(errw, "warning: %v\n", err)
			dbBroken = true
		} else {
			defer db.Close()
			if samples, err = db.SamplesSince(ctx, since); err != nil {
				fmt.Fprintf(errw, "warning: reading samples: %v\n", err)
				samples = nil
			}
			if energy, err = db.EnergySince(ctx, status.EnergyFrom(t)); err != nil {
				fmt.Fprintf(errw, "warning: reading app energy: %v\n", err)
				energy = nil
			}
			if snap.OnAC && snap.Charging && snap.Percent < 100 {
				// A failed read keeps the default curve.
				if c := learnCurve(ctx, db, errw, t); c != nil {
					curve = c
				}
			}
		}
	}

	r = status.Build(snap, samples, energy, t)
	if r.OnAC && r.Charging && r.Percent < 100 && curve != nil {
		m := curve.MinutesToFull(r.Percent)
		r.EstToFull = &m
	}
	return r, dbBroken
}

// statusJSON is the stable schema from docs/specs/F1-status.md.
type statusJSON struct {
	TS              int64         `json:"ts"`
	Percent         int           `json:"percent"`
	OnAC            bool          `json:"on_ac"`
	Charging        bool          `json:"charging"`
	Watts           *float64      `json:"watts"`
	DrainPctPerHr   *float64      `json:"drain_pct_per_hr"`
	EstMinutesLeft  *int          `json:"est_minutes_left"`
	MacOSEstMinutes *int          `json:"macos_est_minutes"`
	EstToFull       *int          `json:"est_minutes_to_full"`
	MacOSToFull     *int          `json:"macos_minutes_to_full"`
	WorstOffender   *offenderJSON `json:"worst_offender"`
}

type offenderJSON struct {
	App         string  `json:"app"`
	EnergyShare float64 `json:"energy_share"`
}

func writeStatusJSON(out io.Writer, r status.Report) error {
	j := statusJSON{
		TS:              r.TS,
		Percent:         r.Percent,
		OnAC:            r.OnAC,
		Charging:        r.Charging,
		Watts:           r.Watts,
		DrainPctPerHr:   r.Drain,
		EstMinutesLeft:  r.EstMinutes,
		MacOSEstMinutes: r.MacOSMinutes,
		EstToFull:       r.EstToFull,
		MacOSToFull:     r.MacOSToFull,
	}
	if r.Worst != nil {
		j.WorstOffender = &offenderJSON{App: r.Worst.App, EnergyShare: round2(r.Worst.Share)}
	}
	enc := json.NewEncoder(out)
	return enc.Encode(j)
}

func renderStatus(w io.Writer, r status.Report, dbBroken bool) {
	icon, source, state := "🔋", "on battery", "discharging"
	if r.OnAC {
		icon, source = "⚡", "AC"
		switch {
		case r.Charging:
			state = "charging"
		case r.FullyCharged || r.Percent >= 100:
			state = "charged"
		default:
			state = "not charging"
		}
	}
	// Watts is the power through the battery, without its direction: what
	// the Mac uses on battery, what goes into the battery while charging.
	// On AC but not charging it is neither, so it is left out.
	if r.Watts != nil {
		switch {
		case !r.OnAC:
			state = fmt.Sprintf("using %.1f W", *r.Watts)
		case r.Charging:
			state = fmt.Sprintf("charging at %.1f W", *r.Watts)
		}
	}
	line := fmt.Sprintf("%s %d%%  ·  %s  ·  %s", icon, r.Percent, source, state)
	fmt.Fprintln(w, line)

	if !r.OnAC {
		switch {
		case r.Drain != nil:
			fmt.Fprintf(w, "drain       %.1f %%/hr   (last 10 min)\n", *r.Drain)
		case r.Collecting:
			fmt.Fprintln(w, "drain       collecting…")
		}
		var parts []string
		if r.Drain != nil {
			est := "> 12h"
			if r.EstMinutes != nil && !r.EstOver12h {
				est = fmtDuration(*r.EstMinutes)
			}
			parts = append(parts, est+"  (batlog)")
		}
		if r.MacOSMinutes != nil {
			parts = append(parts, fmtDuration(*r.MacOSMinutes)+" (macOS)")
		}
		if len(parts) > 0 {
			fmt.Fprintf(w, "est. left   %s\n", joinDot(parts))
		}
	}
	var full []string
	if r.EstToFull != nil {
		full = append(full, fmtDuration(*r.EstToFull)+"  (batlog)")
	}
	if r.MacOSToFull != nil {
		full = append(full, fmtDuration(*r.MacOSToFull)+" (macOS)")
	}
	if len(full) > 0 {
		fmt.Fprintf(w, "est. full   %s\n", joinDot(full))
	}
	if r.Worst != nil {
		fmt.Fprintf(w, "worst now   %s  (%.0f%% of energy)\n", r.Worst.App, r.Worst.Share*100)
	}
	// A database that exists but cannot be opened is not fixed by
	// installing the daemon; the warning on stderr already says what is wrong.
	if !r.HasData && !dbBroken {
		fmt.Fprintln(w, "tip: run 'batlog daemon install' for drain analysis")
	}
}

func fmtDuration(minutes int) string { return textfmt.Duration(minutes) }

func joinDot(parts []string) string { return textfmt.JoinDot(parts) }

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
