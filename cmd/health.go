package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/health"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/textfmt"
)

var healthTrend bool

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Real capacity vs design, cycles, temperature, condition; --trend for decline per month",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()
		return runHealth(cmd.Context(), out, cmd.ErrOrStderr(), healthTrend, jsonOut, wantColor(out))
	},
}

func init() {
	healthCmd.Flags().BoolVar(&healthTrend, "trend", false, "show the health change over the last 90 days")
	rootCmd.AddCommand(healthCmd)
}

func runHealth(ctx context.Context, out, errw io.Writer, withTrend, asJSON, color bool) error {
	snap, err := readBattery(ctx)
	if err != nil {
		return err
	}
	r := health.Build(snap.Health)

	var trend *health.TrendResult
	span := 0
	if withTrend {
		trend, span = loadTrend(ctx, errw)
	}
	if asJSON {
		return writeHealthJSON(out, r, trend)
	}
	renderHealth(out, r, withTrend, trend, span, color, now())
	return nil
}

// loadTrend reads the daemon's daily health rows. A missing database is
// "no history yet"; one that fails to open is a warning, never an error.
func loadTrend(ctx context.Context, errw io.Writer) (*health.TrendResult, int) {
	p, err := dbPath()
	if err != nil || !store.Exists(p) {
		return nil, 0
	}
	db, err := store.Open(p, true)
	if err != nil {
		fmt.Fprintf(errw, "warning: %v\n", err)
		return nil, 0
	}
	defer db.Close()
	rows, err := db.HealthSince(ctx, health.Since(now()))
	if err != nil {
		fmt.Fprintf(errw, "warning: reading health history: %v\n", err)
		return nil, 0
	}
	return health.Trend(rows)
}

type healthJSON struct {
	HealthPct      *float64   `json:"health_pct"`
	AppleHealthPct *float64   `json:"apple_health_pct"`
	RawMaxMAh      *int       `json:"raw_max_mah"`
	NominalMAh     *int       `json:"nominal_mah"`
	DesignMAh      *int       `json:"design_mah"`
	CycleCount     *int       `json:"cycle_count"`
	TemperatureC   *float64   `json:"temperature_c"`
	VoltageV       *float64   `json:"voltage_v"`
	Condition      *string    `json:"condition"`
	Trend          *trendJSON `json:"trend"`
}

type trendJSON struct {
	FromPct     float64 `json:"from_pct"`
	ToPct       float64 `json:"to_pct"`
	PctPerMonth float64 `json:"pct_per_month"`
	Days        int     `json:"days"`
	LastDay     string  `json:"last_day"`
}

func writeHealthJSON(out io.Writer, r health.Report, t *health.TrendResult) error {
	j := healthJSON{
		HealthPct:      r.HealthPct,
		AppleHealthPct: r.AppleHealthPct,
		RawMaxMAh:      r.RawMaxMAh,
		NominalMAh:     r.NominalMAh,
		DesignMAh:      r.DesignMAh,
		CycleCount:     r.Cycles,
		TemperatureC:   roundPtr(r.TempC, 1),
		VoltageV:       roundPtr(r.VoltageV, 2),
		Condition:      r.Condition,
	}
	if t != nil {
		j.Trend = &trendJSON{FromPct: t.FromPct, ToPct: t.ToPct, PctPerMonth: t.PctPerMonth, Days: t.Days, LastDay: t.LastDay}
	}
	return json.NewEncoder(out).Encode(j)
}

// staleTrendDays: a trend whose newest row is older than this says so.
const staleTrendDays = 3

func renderHealth(w io.Writer, r health.Report, withTrend bool, t *health.TrendResult, span int, color bool, today time.Time) {
	line := func(label, value string) { fmt.Fprintf(w, "%-15s%s\n", label, value) }

	fmt.Fprintln(w, "🔎 Battery health")
	if r.HealthPct != nil {
		line("health", fmt.Sprintf("%.1f%%   (%s / %s mAh design)", *r.HealthPct, thousands(*r.RawMaxMAh), thousands(*r.DesignMAh)))
	} else {
		line("health", fmt.Sprintf("unknown   (%s)", r.HealthNote))
	}
	if r.AppleHealthPct != nil {
		line("Apple reports", fmt.Sprintf("%.1f%%   (smoothed)", *r.AppleHealthPct))
	}
	if r.Cycles != nil {
		line("cycles", strconv.Itoa(*r.Cycles))
	}
	if r.TempC != nil {
		line("temperature", fmt.Sprintf("%.1f °C", *r.TempC))
	}
	if r.VoltageV != nil {
		line("voltage", fmt.Sprintf("%.2f V", *r.VoltageV))
	}
	if r.Condition != nil {
		c := *r.Condition
		if r.NeedsService {
			c = fmt.Sprintf("%s (status %d)", c, *r.FailureStatus)
			if color {
				c = "\x1b[31m" + c + "\x1b[0m"
			}
		}
		line("condition", c)
	}

	if !withTrend {
		return
	}
	fmt.Fprintln(w)
	if t == nil {
		fmt.Fprintf(w, "trend: not enough history yet (have %d days, need %d)\n", span, health.MinSpanDays)
		return
	}
	stale := ""
	if last, err := time.ParseInLocation("2006-01-02", t.LastDay, today.Location()); err == nil &&
		today.Sub(last) > staleTrendDays*24*time.Hour {
		stale = "   (last recorded " + last.Format("02 Jan") + ")"
	}
	fmt.Fprintf(w, "trend (%d days)   %.1f%% → %.1f%%   ≈ %s %%/month%s\n", t.Days, t.FromPct, t.ToPct, signed(t.PctPerMonth), stale)
}

func signed(v float64) string { return textfmt.Signed(v) }

func thousands(n int) string { return textfmt.Thousands(n) }

func roundPtr(v *float64, places int) *float64 {
	if v == nil {
		return nil
	}
	p := math.Pow(10, float64(places))
	r := math.Round(*v*p) / p
	return &r
}

// wantColor is true when writing to a terminal and NO_COLOR is unset.
func wantColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
