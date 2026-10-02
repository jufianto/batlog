package cmd

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/store"
)

var (
	exportCSV, exportForce bool
	exportSince, exportOut string
)

var errNothingToExport = errors.New("nothing to export — run 'batlog daemon install'")

var exportCmd = &cobra.Command{
	Use:       "export <samples|apps|health>",
	Short:     "Your raw data as CSV (the default) or JSON, for your own charts",
	ValidArgs: []string{"samples", "apps", "health"},
	Args: usageArgs(func(_ *cobra.Command, args []string) error {
		if len(args) != 1 || (args[0] != "samples" && args[0] != "apps" && args[0] != "health") {
			return errors.New("export what? one of samples, apps or health")
		}
		return nil
	}),
	RunE: func(cmd *cobra.Command, args []string) error {
		if exportCSV && jsonOut {
			return usageError{errors.New("use only one of --csv and --json")}
		}
		var from int64
		if exportSince != "" {
			f, err := parseSince(exportSince, now())
			if err != nil {
				return usageError{err}
			}
			from = f.Unix()
		}
		return runExport(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], from, exportOut, exportForce, jsonOut)
	},
}

func init() {
	f := exportCmd.Flags()
	f.StringVar(&exportSince, "since", "", "a duration back (3d, 12h) or a date (2026-06-01); default everything")
	f.BoolVar(&exportCSV, "csv", false, "CSV with a header row (the default)")
	f.StringVarP(&exportOut, "output", "o", "", "write to this file instead of stdout")
	f.BoolVar(&exportForce, "force", false, "overwrite the -o file if it exists")
	rootCmd.AddCommand(exportCmd)
}

func runExport(ctx context.Context, stdout, errw io.Writer, table string, from int64, outPath string, force, asJSON bool) (err error) {
	p, err := dbPath()
	if err != nil || !store.Exists(p) {
		return errNothingToExport
	}
	db, err := store.Open(p, true)
	if err != nil {
		return err
	}
	defer db.Close()
	n, oldest, err := db.SampleStats(ctx)
	if err != nil {
		return fmt.Errorf("reading samples: %w", err)
	}
	rolled, err := db.RollupDays(ctx)
	if err != nil {
		return fmt.Errorf("reading rollups: %w", err)
	}
	if n == 0 && rolled == 0 {
		return errNothingToExport
	}

	out := stdout
	if outPath != "" {
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		// err is the named result: the deferred cleanup must see the
		// export's error, not a shadow.
		var f *os.File
		f, err = os.OpenFile(outPath, flags, 0o644)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s exists: add --force to overwrite it", outPath)
		}
		if err != nil {
			return err
		}
		defer func() {
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				os.Remove(outPath) // never leave half an export behind
			}
		}()
		out = f
	}

	w := newExportWriter(out, asJSON)
	loc := now().Location()
	switch table {
	case "samples":
		w.header("ts", "pct", "on_ac", "charging", "watts", "raw_cur_mah", "raw_max_mah")
		err = db.EachSample(ctx, from, func(s store.RawSample) error {
			return w.row(sampleRow{s.TS, s.Pct, s.OnAC, s.Charging, s.Watts, s.RawCurMAh, s.RawMaxMAh},
				isoTime(s.TS, loc), strconv.Itoa(s.Pct), strconv.FormatBool(s.OnAC), strconv.FormatBool(s.Charging),
				csvFloat(s.Watts), csvInt(s.RawCurMAh), csvInt(s.RawMaxMAh))
		})
	case "apps":
		w.header("ts", "app", "is_system", "cpu_j", "gpu_j", "ane_j")
		err = db.EachEnergy(ctx, from, func(e store.RawEnergy) error {
			cpu, gpu, ane := joules(e.CPU), joules(e.GPU), joules(e.ANE)
			return w.row(appRow{e.TS, e.App, e.System, cpu, gpu, ane},
				isoTime(e.TS, loc), e.App, strconv.FormatBool(e.System), fmtFloat(cpu), fmtFloat(gpu), fmtFloat(ane))
		})
	case "health":
		day := ""
		if from != 0 {
			day = time.Unix(from, 0).In(loc).Format("2006-01-02")
		}
		w.header("day", "cycles", "raw_max_mah", "nominal_mah", "design_mah", "health_pct", "temp_c", "condition")
		err = db.EachHealth(ctx, day, func(h store.HealthDay) error {
			var hp *float64
			if h.RawMaxMAh != nil && h.DesignMAh != nil && *h.DesignMAh > 0 {
				v := round1(float64(*h.RawMaxMAh) * 100 / float64(*h.DesignMAh))
				hp = &v
			}
			cond := ""
			if h.Condition != nil {
				cond = *h.Condition
			}
			return w.row(healthRow{h.Day, h.Cycles, h.RawMaxMAh, h.NominalMAh, h.DesignMAh, hp, h.TempC, h.Condition},
				h.Day, csvInt(h.Cycles), csvInt(h.RawMaxMAh), csvInt(h.NominalMAh), csvInt(h.DesignMAh),
				csvFloat(hp), csvFloat(h.TempC), cond)
		})
	}
	if err != nil {
		return fmt.Errorf("exporting %s: %w", table, err)
	}
	if err := w.close(); err != nil {
		return err
	}
	// Days past the raw window live on only as daily totals.
	if table != "health" && rolled > 0 && (from == 0 || from < oldest) {
		fmt.Fprintf(errw, "note: %d days older than 90 days are rolled up and not in this export; `batlog export health` has every day\n", rolled)
	}
	return nil
}

type sampleRow struct {
	TS        int64    `json:"ts"`
	Pct       int      `json:"pct"`
	OnAC      bool     `json:"on_ac"`
	Charging  bool     `json:"charging"`
	Watts     *float64 `json:"watts"`
	RawCurMAh *int     `json:"raw_cur_mah"`
	RawMaxMAh *int     `json:"raw_max_mah"`
}

type appRow struct {
	TS       int64   `json:"ts"`
	App      string  `json:"app"`
	IsSystem bool    `json:"is_system"`
	CPUJ     float64 `json:"cpu_j"`
	GPUJ     float64 `json:"gpu_j"`
	ANEJ     float64 `json:"ane_j"`
}

type healthRow struct {
	Day        string   `json:"day"`
	Cycles     *int     `json:"cycles"`
	RawMaxMAh  *int     `json:"raw_max_mah"`
	NominalMAh *int     `json:"nominal_mah"`
	DesignMAh  *int     `json:"design_mah"`
	HealthPct  *float64 `json:"health_pct"`
	TempC      *float64 `json:"temp_c"`
	Condition  *string  `json:"condition"`
}

// exportWriter streams rows as RFC 4180 CSV or as a JSON array with one
// object per line.
type exportWriter struct {
	bw     *bufio.Writer
	csv    *csv.Writer
	asJSON bool
	rows   int
	err    error
}

func newExportWriter(w io.Writer, asJSON bool) *exportWriter {
	e := &exportWriter{bw: bufio.NewWriter(w), asJSON: asJSON}
	if !asJSON {
		e.csv = csv.NewWriter(e.bw)
	}
	return e
}

func (e *exportWriter) header(cols ...string) {
	if e.asJSON {
		_, e.err = e.bw.WriteString("[")
		return
	}
	e.err = e.csv.Write(cols)
}

// row writes obj as JSON, or the cells as CSV.
func (e *exportWriter) row(obj any, cells ...string) error {
	if e.err != nil {
		return e.err
	}
	if !e.asJSON {
		return e.csv.Write(cells)
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	sep := ",\n"
	if e.rows == 0 {
		sep = "\n"
	}
	e.rows++
	if _, err := e.bw.WriteString(sep); err != nil {
		return err
	}
	_, err = e.bw.Write(b)
	return err
}

func (e *exportWriter) close() error {
	if e.err != nil {
		return e.err
	}
	if e.asJSON {
		end := "\n]\n"
		if e.rows == 0 {
			end = "]\n"
		}
		if _, err := e.bw.WriteString(end); err != nil {
			return err
		}
	} else {
		e.csv.Flush()
		if err := e.csv.Error(); err != nil {
			return err
		}
	}
	return e.bw.Flush()
}

// isoTime is ISO 8601 in local time with its offset, which spreadsheets
// read as a date.
func isoTime(ts int64, loc *time.Location) string {
	return time.Unix(ts, 0).In(loc).Format(time.RFC3339)
}

func joules(nj int64) float64 { return float64(nj) / 1e9 }

func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func csvFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return fmtFloat(*v)
}

func csvInt(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}
