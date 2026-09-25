package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/launchd"
	"github.com/jufianto/batlog/internal/store"
)

type fakeCtl struct {
	loaded       bool
	bootstrapErr error
	calls        []string
}

func (f *fakeCtl) Loaded() bool { f.calls = append(f.calls, "print"); return f.loaded }
func (f *fakeCtl) Bootstrap(p string) error {
	f.calls = append(f.calls, "bootstrap "+filepath.Base(p))
	if f.bootstrapErr != nil {
		return f.bootstrapErr
	}
	f.loaded = true
	return nil
}
func (f *fakeCtl) Bootout() error { f.calls = append(f.calls, "bootout"); f.loaded = false; return nil }

type daemonEnv struct {
	dir, db, log, agent string
	ctl                 *fakeCtl
}

func stubDaemon(t *testing.T) *daemonEnv {
	t.Helper()
	dir := t.TempDir()
	e := &daemonEnv{
		dir:   dir,
		db:    filepath.Join(dir, "Application Support", "batlog", "batlog.db"),
		log:   filepath.Join(dir, "Logs", "batlog", "daemon.log"),
		agent: filepath.Join(dir, "LaunchAgents", "dev.jufi.batlog.plist"),
		ctl:   &fakeCtl{},
	}
	stubStatus(t, battery.Snapshot{Percent: 67, Watts: 8.4, HasWatts: true}, e.db)
	oldAgent, oldLog, oldCtl, oldExe, oldHome := agentPath, logPath, newCtl, executable, homeDir
	agentPath = func() (string, error) { return e.agent, nil }
	logPath = func() (string, error) { return e.log, nil }
	newCtl = func() daemonCtl { return e.ctl }
	executable = func() (string, error) { return "/opt/bin/batlog", nil }
	homeDir = func() (string, error) { return dir, nil }
	t.Setenv("BATLOG_HOME", "")
	t.Setenv("XPC_SERVICE_NAME", "") // as in a terminal, not under launchd
	t.Cleanup(func() { agentPath, logPath, newCtl, executable, homeDir = oldAgent, oldLog, oldCtl, oldExe, oldHome })
	return e
}

func (e *daemonEnv) seedTicks(t *testing.T, ts ...int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.db), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(e.db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, v := range ts {
		if err := db.WriteTick(ctx, store.Tick{TS: v, Pct: 60}); err != nil {
			t.Fatal(err)
		}
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		jsonOut = false
		daemonOnce = false
		daemonFollow = false
	})
	err := rootCmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestInstallWritesPlistDatabaseAndBootstraps(t *testing.T) {
	e := stubDaemon(t)
	out, err := run(t, "daemon", "install")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(e.agent)
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if bin, _ := launchd.Program(data); bin != "/opt/bin/batlog" {
		t.Errorf("plist runs %q", bin)
	}
	if strings.Contains(string(data), "EnvironmentVariables") {
		t.Error("BATLOG_HOME unset: plist must not carry an environment")
	}
	db, err := store.Open(e.db, true)
	if err != nil {
		t.Fatalf("database not created: %v", err)
	}
	v, _, _ := db.Meta(context.Background(), "schema_version")
	db.Close()
	if v != "1" {
		t.Errorf("schema_version = %q", v)
	}
	if _, err := os.Stat(filepath.Dir(e.log)); err != nil {
		t.Errorf("log directory not created: %v", err)
	}
	if got := strings.Join(e.ctl.calls, ","); got != "print,bootstrap dev.jufi.batlog.plist" {
		t.Errorf("launchctl calls = %s", got)
	}
	for _, want := range []string{"installed", "~/LaunchAgents/dev.jufi.batlog.plist", "~/Application Support/batlog/batlog.db", "~/Logs/batlog/daemon.log"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	e := stubDaemon(t)
	e.ctl.loaded = true
	if _, err := run(t, "daemon", "install"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.ctl.calls, ","); got != "print,bootout,bootstrap dev.jufi.batlog.plist" {
		t.Errorf("already loaded: calls = %s, want bootout before bootstrap", got)
	}
}

func TestInstallPassesBatlogHome(t *testing.T) {
	e := stubDaemon(t)
	t.Setenv("BATLOG_HOME", "/data dir")
	if _, err := run(t, "daemon", "install"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(e.agent)
	if !strings.Contains(string(data), "<key>BATLOG_HOME</key>") || !strings.Contains(string(data), "<string>/data dir</string>") {
		t.Errorf("plist must pass BATLOG_HOME through:\n%s", data)
	}
}

func TestInstallBootstrapFailureKeepsPlist(t *testing.T) {
	e := stubDaemon(t)
	e.ctl.bootstrapErr = errors.New("launchctl bootstrap: Bootstrap failed: 5: Input/output error (exit status 5)")
	_, err := run(t, "daemon", "install")
	if err == nil || !strings.Contains(err.Error(), "Bootstrap failed: 5") {
		t.Fatalf("err = %v, want launchctl's message", err)
	}
	if _, err := os.Stat(e.agent); err != nil {
		t.Error("plist must be left in place for inspection")
	}
}

func TestInstallRefusesGoRunBinary(t *testing.T) {
	e := stubDaemon(t)
	executable = func() (string, error) { return "/var/folders/x/T/go-build123/b001/exe/batlog", nil }
	_, err := run(t, "daemon", "install")
	if err == nil || !strings.Contains(err.Error(), "go run") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(e.agent); !errors.Is(err, os.ErrNotExist) || len(e.ctl.calls) != 0 {
		t.Errorf("nothing may be written or loaded: calls=%v", e.ctl.calls)
	}
}

func TestUninstallKeepsData(t *testing.T) {
	e := stubDaemon(t)
	if _, err := run(t, "daemon", "install"); err != nil {
		t.Fatal(err)
	}
	e.ctl.calls = nil
	out, err := run(t, "daemon", "uninstall")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.ctl.calls, ","); got != "print,bootout" {
		t.Errorf("calls = %s", got)
	}
	if _, err := os.Stat(e.agent); !errors.Is(err, os.ErrNotExist) {
		t.Error("plist must be deleted")
	}
	if _, err := os.Stat(e.db); err != nil {
		t.Error("database must be kept")
	}
	if !strings.Contains(out, "data kept at ~/Application Support/batlog — delete it yourself if you want it gone") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUninstallWhenNotInstalled(t *testing.T) {
	stubDaemon(t)
	out, err := run(t, "daemon", "uninstall")
	if err != nil || !strings.Contains(out, "not installed") {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestDaemonStatusStates(t *testing.T) {
	cases := []struct {
		name      string
		ageSec    int64 // age of last tick; <0 = no ticks
		loaded    bool
		install   bool
		binGone   bool
		wantState string
		wantLine  string
	}{
		{"not installed", -1, false, false, false, "not_installed", "○ batlog daemon: not installed"},
		{"running", 38, true, true, false, "running", "● batlog daemon: running   (last tick 38 s ago)"},
		{"stale", 300, true, true, false, "stale", "● batlog daemon: stale   (last tick 5 min ago)"},
		{"dead", 1200, true, true, false, "dead", "● batlog daemon: dead   (last tick 20 min ago)"},
		{"waiting", -1, true, true, false, "waiting", "● batlog daemon: waiting for the first tick"},
		{"not loaded", 38, false, true, false, "not_loaded", "● batlog daemon: installed but not loaded"},
		{"binary moved", 1200, true, true, true, "dead", "● batlog daemon: dead   (binary missing: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := stubDaemon(t)
			bin := filepath.Join(e.dir, "batlog-bin")
			if !c.binGone {
				os.WriteFile(bin, []byte("#!"), 0o755)
			}
			if c.install {
				os.MkdirAll(filepath.Dir(e.agent), 0o755)
				data, _ := launchd.Plist("dev.jufi.batlog", bin, e.log, nil)
				os.WriteFile(e.agent, data, 0o644)
			}
			if c.ageSec >= 0 {
				e.seedTicks(t, testNow.Unix()-c.ageSec-120, testNow.Unix()-c.ageSec)
			}
			e.ctl.loaded = c.loaded
			out, err := run(t, "daemon", "status")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(out, c.wantLine) {
				t.Errorf("first line:\n%s\nwant prefix %q", out, c.wantLine)
			}
			if (c.binGone || c.wantState == "not_loaded") && !strings.Contains(out, "re-run 'batlog daemon install'") {
				t.Errorf("missing re-install hint:\n%s", out)
			}
			js, err := run(t, "daemon", "status", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var j map[string]any
			if err := json.Unmarshal([]byte(js), &j); err != nil {
				t.Fatalf("bad JSON %q: %v", js, err)
			}
			if j["state"] != c.wantState {
				t.Errorf("json state = %v, want %s", j["state"], c.wantState)
			}
		})
	}
}

func TestDaemonStatusDetails(t *testing.T) {
	e := stubDaemon(t)
	if _, err := run(t, "daemon", "install"); err != nil {
		t.Fatal(err)
	}
	executableFile := filepath.Join(e.dir, "bin")
	os.WriteFile(executableFile, nil, 0o755)
	data, _ := launchd.Plist("dev.jufi.batlog", executableFile, e.log, nil)
	os.WriteFile(e.agent, data, 0o644)
	e.seedTicks(t, testNow.Unix()-2*86400, testNow.Unix()-60, testNow.Unix()-38)
	out, err := run(t, "daemon", "status")
	if err != nil {
		t.Fatal(err)
	}
	oldest := time.Unix(testNow.Unix()-2*86400, 0).Format("02 Jan 2006")
	for _, want := range []string{
		"plist      ~/LaunchAgents/dev.jufi.batlog.plist   (loaded)\n",
		"database   ~/Application Support/batlog/batlog.db   ",
		"samples    3 since " + oldest + "\n",
		"log        ~/Logs/batlog/daemon.log\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestRunOnceWritesOneTick(t *testing.T) {
	e := stubDaemon(t)
	out, err := run(t, "daemon", "run", "--once")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	db, err := store.Open(e.db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n, _, _ := db.SampleStats(context.Background())
	if n != 1 {
		t.Errorf("samples = %d, want exactly 1", n)
	}
	if rows, _ := db.HealthSince(context.Background(), "1900-01-01"); len(rows) != 0 {
		// The stub snapshot has no capacities, so HealthSince skips its row.
		t.Errorf("health rows = %v", rows)
	}
	if strings.Contains(out, "another recorder") {
		t.Errorf("fresh database must not warn:\n%s", out)
	}
}

func TestRunWarnsWhenAnotherRecorderIsActive(t *testing.T) {
	e := stubDaemon(t)
	e.seedTicks(t, testNow.Unix()-12)
	out, err := run(t, "daemon", "run", "--once")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "warning: another recorder wrote a tick 12 s ago") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunOnceProbeFailureExits1(t *testing.T) {
	stubDaemon(t)
	readBattery = func(context.Context) (battery.Snapshot, error) {
		return battery.Snapshot{}, errors.New("ioreg: exit status 1")
	}
	_, err := run(t, "daemon", "run", "--once")
	var ue usageError
	if err == nil || errors.As(err, &ue) || !strings.Contains(err.Error(), "ioreg") {
		t.Errorf("err = %v, want an operational error naming ioreg", err)
	}
}

func TestLogsPrintsAndFollows(t *testing.T) {
	e := stubDaemon(t)
	out, err := run(t, "daemon", "logs")
	if err != nil || !strings.Contains(out, "no log yet at ~/Logs/batlog/daemon.log") {
		t.Errorf("missing log: out=%q err=%v", out, err)
	}
	os.MkdirAll(filepath.Dir(e.log), 0o755)
	os.WriteFile(e.log, []byte("started\n"), 0o644)
	if out, _ := run(t, "daemon", "logs"); out != "started\n" {
		t.Errorf("logs = %q", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf syncBuffer
	done := make(chan error, 1)
	go func() { done <- followLog(ctx, &buf, e.log, 5*time.Millisecond) }()
	time.Sleep(20 * time.Millisecond)
	f, _ := os.OpenFile(e.log, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString("tick skipped: x\n")
	f.Close()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "tick skipped: x") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("follow returned %v", err)
	}
	if got := buf.String(); got != "started\ntick skipped: x\n" {
		t.Errorf("followed = %q", got)
	}
}

func TestDaemonUsageErrors(t *testing.T) {
	stubDaemon(t)
	for _, args := range [][]string{{"daemon", "bogus"}, {"daemon", "status", "extra"}} {
		_, err := run(t, args...)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want usage error", args, err)
		}
	}
}

func TestRunUnderLaunchdDoesNotWarnAfterRestart(t *testing.T) {
	// After a crash, launchd restarts the recorder within seconds; the
	// recent tick is its own predecessor's, not a second recorder.
	e := stubDaemon(t)
	e.seedTicks(t, testNow.Unix()-12)
	t.Setenv("XPC_SERVICE_NAME", "dev.jufi.batlog")
	out, err := run(t, "daemon", "run", "--once")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "another recorder") {
		t.Errorf("launchd restart must not warn:\n%s", out)
	}
}

func TestInstallKeepsTheSymlinkPath(t *testing.T) {
	// Homebrew runs /opt/homebrew/bin/batlog → ../Cellar/batlog/1.0/bin/batlog.
	// The plist must name the stable link: `brew upgrade` deletes the
	// versioned target, and launchd would have nothing to start.
	e := stubDaemon(t)
	cellar := filepath.Join(e.dir, "Cellar", "batlog", "1.0", "bin", "batlog")
	link := filepath.Join(e.dir, "brewbin", "batlog")
	os.MkdirAll(filepath.Dir(cellar), 0o755)
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.WriteFile(cellar, []byte("#!"), 0o755)
	if err := os.Symlink(cellar, link); err != nil {
		t.Fatal(err)
	}
	executable = func() (string, error) { return link, nil }
	if _, err := run(t, "daemon", "install"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(e.agent)
	if bin, _ := launchd.Program(data); bin != link {
		t.Errorf("plist runs %q, want the symlink %q", bin, link)
	}
}

func TestInstallRefusesGoRunBinaryBehindSymlink(t *testing.T) {
	e := stubDaemon(t)
	target := filepath.Join(e.dir, "go-build123", "exe", "batlog")
	link := filepath.Join(e.dir, "batlog")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("#!"), 0o755)
	os.Symlink(target, link)
	executable = func() (string, error) { return link, nil }
	if _, err := run(t, "daemon", "install"); err == nil || !strings.Contains(err.Error(), "go run") {
		t.Errorf("err = %v, want refusal of a go run build behind a link", err)
	}
}
