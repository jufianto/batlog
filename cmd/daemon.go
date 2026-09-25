package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/batlog/internal/launchd"
	"github.com/jufianto/batlog/internal/paths"
	"github.com/jufianto/batlog/internal/recorder"
	"github.com/jufianto/batlog/internal/store"
)

// daemonCtl is the part of launchd.Client the commands use.
type daemonCtl interface {
	Loaded() bool
	Bootstrap(plistPath string) error
	Bootout() error
}

// Swapped by tests so nothing touches the real launchd or ~/Library.
var (
	agentPath  = paths.LaunchAgent
	logPath    = paths.Log
	homeDir    = os.UserHomeDir
	newCtl     = func() daemonCtl { return launchd.New(os.Getuid(), paths.AgentLabel) }
	executable = func() (string, error) {
		p, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(p)
	}
	tickEvery = time.Minute
)

const (
	logMaxBytes  = 5 << 20 // truncate the log above this at startup
	logKeepBytes = 1 << 20 // keeping this much of its tail
	busyTick     = 30 * time.Second
)

var (
	daemonOnce   bool
	daemonFollow bool
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Install, remove and inspect the background recorder",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

func init() {
	sub := []*cobra.Command{
		{Use: "install", Short: "Start recording every 60 s, now and at every login", RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstall(cmd.OutOrStdout())
		}},
		{Use: "uninstall", Short: "Stop recording; your data is kept", RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd.OutOrStdout())
		}},
		{Use: "status", Short: "Is the recorder installed, loaded and writing?", RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonStatus(cmd.Context(), cmd.OutOrStdout(), jsonOut)
		}},
		{Use: "logs", Short: "Print the recorder's log; -f follows it", RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogs(cmd.Context(), cmd.OutOrStdout(), daemonFollow)
		}},
		{Use: "run", Short: "The recorder loop that launchd runs (use --once to debug)", RunE: func(cmd *cobra.Command, _ []string) error {
			return runRecorder(cmd.Context(), cmd.OutOrStdout(), daemonOnce)
		}},
	}
	for _, c := range sub {
		c.Args = usageArgs(cobra.NoArgs)
		daemonCmd.AddCommand(c)
	}
	sub[3].Flags().BoolVarP(&daemonFollow, "follow", "f", false, "keep printing new lines")
	sub[4].Flags().BoolVar(&daemonOnce, "once", false, "write one tick and exit")
	rootCmd.AddCommand(daemonCmd)
}

func runInstall(out io.Writer) error {
	bin, err := executable()
	if err != nil {
		return fmt.Errorf("find batlog binary: %w", err)
	}
	if strings.Contains(bin, "/go-build") {
		return fmt.Errorf("install from a built binary, not 'go run' (%s is temporary)", bin)
	}
	dbp, err := dbPath()
	if err != nil {
		return err
	}
	if err := createDatabase(dbp); err != nil {
		return err
	}
	logp, err := logPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logp), 0o755); err != nil {
		return err
	}
	var env map[string]string
	if h := os.Getenv("BATLOG_HOME"); h != "" {
		env = map[string]string{"BATLOG_HOME": h}
	}
	data, err := launchd.Plist(paths.AgentLabel, bin, logp, env)
	if err != nil {
		return err
	}
	ap, err := agentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ap), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(ap, data, 0o644); err != nil {
		return err
	}

	ctl := newCtl()
	if ctl.Loaded() {
		// Re-installing also repoints a plist at a moved binary.
		if err := ctl.Bootout(); err != nil {
			return err
		}
	}
	if err := ctl.Bootstrap(ap); err != nil {
		return err // plist stays for inspection
	}
	fmt.Fprintln(out, "✓ batlog daemon installed: recording every 60 s, now and at every login")
	fmt.Fprintf(out, "binary     %s\n", bin)
	fmt.Fprintf(out, "plist      %s\n", tilde(ap))
	fmt.Fprintf(out, "database   %s\n", tilde(dbp))
	fmt.Fprintf(out, "log        %s\n", tilde(logp))
	return nil
}

func createDatabase(dbp string) error {
	if err := os.MkdirAll(filepath.Dir(dbp), 0o755); err != nil {
		return err
	}
	db, err := store.Open(dbp, false)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Migrate(context.Background())
}

func runUninstall(out io.Writer) error {
	ap, err := agentPath()
	if err != nil {
		return err
	}
	ctl := newCtl()
	loaded := ctl.Loaded()
	_, statErr := os.Stat(ap)
	if !loaded && errors.Is(statErr, fs.ErrNotExist) {
		fmt.Fprintln(out, "batlog daemon is not installed")
		return nil
	}
	if loaded {
		if err := ctl.Bootout(); err != nil {
			return err
		}
	}
	if err := os.Remove(ap); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	dbp, _ := dbPath()
	fmt.Fprintln(out, "✓ batlog daemon removed; nothing is recording now")
	fmt.Fprintf(out, "data kept at %s — delete it yourself if you want it gone\n", tilde(filepath.Dir(dbp)))
	return nil
}

// daemonState is everything `daemon status` reports.
type daemonState struct {
	State         string  `json:"state"` // running|stale|dead|waiting|not_loaded|not_installed
	Installed     bool    `json:"installed"`
	Loaded        bool    `json:"loaded"`
	LastTickAgeS  *int64  `json:"last_tick_age_s"`
	Binary        *string `json:"binary"`
	BinaryExists  bool    `json:"binary_exists"`
	Plist         string  `json:"plist"`
	Database      string  `json:"database"`
	DatabaseBytes *int64  `json:"database_bytes"`
	Samples       int     `json:"samples"`
	OldestTS      *int64  `json:"oldest_ts"`
	Log           string  `json:"log"`
}

func collectDaemonState(ctx context.Context) (daemonState, error) {
	var s daemonState
	var err error
	if s.Plist, err = agentPath(); err != nil {
		return s, err
	}
	if s.Database, err = dbPath(); err != nil {
		return s, err
	}
	if s.Log, err = logPath(); err != nil {
		return s, err
	}
	if data, err := os.ReadFile(s.Plist); err == nil {
		s.Installed = true
		if bin, err := launchd.Program(data); err == nil {
			s.Binary = &bin
			_, err := os.Stat(bin)
			s.BinaryExists = err == nil
		}
	}
	s.Loaded = newCtl().Loaded()

	if st, err := os.Stat(s.Database); err == nil {
		size := st.Size()
		if wal, err := os.Stat(s.Database + "-wal"); err == nil {
			size += wal.Size()
		}
		s.DatabaseBytes = &size
		if db, err := store.Open(s.Database, true); err == nil {
			if n, oldest, err := db.SampleStats(ctx); err == nil {
				s.Samples = n
				if n > 0 {
					s.OldestTS = &oldest
				}
			}
			if v, ok, err := db.Meta(ctx, "last_tick"); err == nil && ok {
				if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
					age := now().Unix() - ts
					s.LastTickAgeS = &age
				}
			}
			db.Close()
		}
	}

	switch {
	case !s.Installed:
		s.State = "not_installed"
	case s.Binary != nil && !s.BinaryExists:
		s.State = "dead"
	case !s.Loaded:
		s.State = "not_loaded"
	case s.LastTickAgeS == nil:
		s.State = "waiting"
	case *s.LastTickAgeS < 120:
		s.State = "running"
	case *s.LastTickAgeS <= 600:
		s.State = "stale"
	default:
		s.State = "dead"
	}
	return s, nil
}

func runDaemonStatus(ctx context.Context, out io.Writer, asJSON bool) error {
	s, err := collectDaemonState(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(s)
	}

	age := ""
	if s.LastTickAgeS != nil {
		age = "   (last tick " + fmtAge(*s.LastTickAgeS) + ")"
	}
	hint := ""
	switch {
	case s.State == "not_installed":
		fmt.Fprintln(out, "○ batlog daemon: not installed")
		hint = "run 'batlog daemon install' to start recording"
	case s.Binary != nil && !s.BinaryExists:
		fmt.Fprintf(out, "● batlog daemon: dead   (binary missing: %s)\n", *s.Binary)
		hint = "re-run 'batlog daemon install'"
	case s.State == "not_loaded":
		fmt.Fprintln(out, "● batlog daemon: installed but not loaded")
		hint = "re-run 'batlog daemon install'"
	case s.State == "waiting":
		fmt.Fprintln(out, "● batlog daemon: waiting for the first tick")
	default:
		fmt.Fprintf(out, "● batlog daemon: %s%s\n", s.State, age)
		if s.State == "dead" {
			hint = "check 'batlog daemon logs', then re-run 'batlog daemon install'"
		}
	}
	if hint != "" {
		fmt.Fprintf(out, "hint       %s\n", hint)
	}

	loaded := "not loaded"
	if s.Loaded {
		loaded = "loaded"
	}
	if s.Installed {
		fmt.Fprintf(out, "plist      %s   (%s)\n", tilde(s.Plist), loaded)
	} else {
		fmt.Fprintf(out, "plist      %s   (missing)\n", tilde(s.Plist))
	}
	if s.DatabaseBytes != nil {
		fmt.Fprintf(out, "database   %s   %s\n", tilde(s.Database), humanBytes(*s.DatabaseBytes))
	} else {
		fmt.Fprintf(out, "database   %s   (not created yet)\n", tilde(s.Database))
	}
	if s.OldestTS != nil {
		fmt.Fprintf(out, "samples    %s since %s\n", thousands(s.Samples), time.Unix(*s.OldestTS, 0).Format("02 Jan 2006"))
	} else {
		fmt.Fprintln(out, "samples    none yet")
	}
	fmt.Fprintf(out, "log        %s\n", tilde(s.Log))
	return nil
}

func runRecorder(ctx context.Context, out io.Writer, once bool) error {
	logger := log.New(out, "", log.LstdFlags)
	if lp, err := logPath(); err == nil {
		if cut, err := recorder.TruncateLog(lp, logMaxBytes, logKeepBytes); err != nil {
			logger.Printf("warning: could not truncate log: %v", err)
		} else if cut {
			logger.Printf("log was over %d MB; kept the last %d MB", logMaxBytes>>20, logKeepBytes>>20)
		}
	}
	dbp, err := dbPath()
	if err != nil {
		return err
	}
	if err := createDatabase(dbp); err != nil {
		return err
	}
	db, err := store.Open(dbp, false)
	if err != nil {
		return err
	}
	defer db.Close()

	// launchd sets XPC_SERVICE_NAME to the agent's label. Under launchd a
	// recent tick is the previous instance's (crash restart, re-install), so
	// the warning is only for a recorder started by hand.
	underLaunchd := os.Getenv("XPC_SERVICE_NAME") == paths.AgentLabel
	if v, ok, err := db.Meta(ctx, "last_tick"); err == nil && ok && !underLaunchd {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			if age := now().Unix() - ts; age >= 0 && time.Duration(age)*time.Second < busyTick {
				logger.Printf("warning: another recorder wrote a tick %d s ago; is the daemon already running?", age)
			}
		}
	}

	rec := &recorder.Recorder{DB: db, Read: readBattery, Now: now, Log: logger}
	if once {
		if err := rec.Tick(ctx); err != nil {
			return err
		}
		logger.Printf("tick written to %s", dbp)
		return nil
	}
	logger.Printf("batlog daemon started (pid %d, every %s, database %s)", os.Getpid(), tickEvery, dbp)
	err = rec.Run(ctx, tickEvery)
	logger.Printf("batlog daemon stopped")
	return err
}

func runLogs(ctx context.Context, out io.Writer, follow bool) error {
	lp, err := logPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(lp); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(out, "no log yet at %s\n", tilde(lp))
		return nil
	}
	if follow {
		return followLog(ctx, out, lp, 500*time.Millisecond)
	}
	f, err := os.Open(lp)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(out, f)
	return err
}

// followLog prints the log, then new lines as they arrive, until ctx ends.
// If the file shrinks (truncated at daemon start) it starts again from the top.
func followLog(ctx context.Context, out io.Writer, path string, every time.Duration) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var offset int64
	for {
		if st, err := f.Stat(); err == nil && st.Size() < offset {
			offset = 0
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		n, err := io.Copy(out, f)
		if err != nil {
			return err
		}
		offset += n
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// tilde shortens paths under the home directory to ~/…
func tilde(p string) string {
	home, err := homeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func fmtAge(sec int64) string {
	switch {
	case sec < 120:
		return fmt.Sprintf("%d s ago", sec)
	case sec < 2*3600:
		return fmt.Sprintf("%d min ago", sec/60)
	case sec < 2*86400:
		return fmt.Sprintf("%d h ago", sec/3600)
	}
	return fmt.Sprintf("%d days ago", sec/86400)
}

func humanBytes(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
