package launchd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
)

func TestPlistHasEverythingLaunchdNeeds(t *testing.T) {
	data, err := Plist("dev.jufi.batlog", "/opt/bin/batlog", "/Users/x/Library/Logs/batlog/daemon.log", map[string]string{"BATLOG_HOME": "/data"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if _, err := plist.Unmarshal(data, &got); err != nil {
		t.Fatalf("not a valid plist: %v\n%s", err, data)
	}
	want := map[string]any{
		"Label":                "dev.jufi.batlog",
		"ProgramArguments":     []any{"/opt/bin/batlog", "daemon", "run"},
		"RunAtLoad":            true,
		"KeepAlive":            true,
		"ProcessType":          "Background",
		"StandardOutPath":      "/Users/x/Library/Logs/batlog/daemon.log",
		"StandardErrorPath":    "/Users/x/Library/Logs/batlog/daemon.log",
		"EnvironmentVariables": map[string]any{"BATLOG_HOME": "/data"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plist =\n%#v\nwant\n%#v", got, want)
	}
}

func TestPlistOmitsEmptyEnvironment(t *testing.T) {
	data, _ := Plist("l", "/b", "/log", nil)
	if strings.Contains(string(data), "EnvironmentVariables") {
		t.Errorf("no env given, but plist has EnvironmentVariables:\n%s", data)
	}
}

func TestProgramReadsTheBinaryBack(t *testing.T) {
	data, _ := Plist("l", "/Applications/My Tools/batlog", "/log", nil)
	bin, err := Program(data)
	if err != nil || bin != "/Applications/My Tools/batlog" {
		t.Errorf("Program = %q, %v", bin, err)
	}
	if _, err := Program([]byte("garbage")); err == nil {
		t.Error("Program must fail on a broken plist")
	}
}

type fake struct {
	calls [][]string
	fail  int // fail this many calls before succeeding
	out   string
}

func (f *fake) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.fail > 0 {
		f.fail--
		return []byte(f.out), errors.New("exit status 5")
	}
	return nil, nil
}

func client(f *fake, slept *[]time.Duration) Client {
	return Client{UID: 501, Label: "dev.jufi.batlog", Run: f.run, Sleep: func(ctx context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return ctx.Err()
	}}
}

func TestBootstrapRetriesThenSucceeds(t *testing.T) {
	f := &fake{fail: 2, out: "Bootstrap failed: 5: Input/output error"}
	var slept []time.Duration
	if err := client(f, &slept).Bootstrap(context.Background(), "/p.plist"); err != nil {
		t.Fatalf("third attempt succeeds, got %v", err)
	}
	want := []string{"bootstrap", "gui/501", "/p.plist"}
	if len(f.calls) != 3 || !reflect.DeepEqual(f.calls[0], want) {
		t.Errorf("calls = %v", f.calls)
	}
	if len(slept) != 2 || slept[0] != time.Second {
		t.Errorf("slept = %v, want two 1 s pauses", slept)
	}
}

func TestBootstrapGivesUpWithLaunchctlsMessage(t *testing.T) {
	f := &fake{fail: 99, out: "Bootstrap failed: 5: Input/output error\n"}
	var slept []time.Duration
	err := client(f, &slept).Bootstrap(context.Background(), "/p.plist")
	if err == nil || !strings.Contains(err.Error(), "Bootstrap failed: 5: Input/output error") {
		t.Fatalf("err = %v, want launchctl's own message", err)
	}
	if len(f.calls) != 3 {
		t.Errorf("attempts = %d, want 3", len(f.calls))
	}
}

func TestBootoutAndLoaded(t *testing.T) {
	f := &fake{}
	var slept []time.Duration
	c := client(f, &slept)
	bg := context.Background()
	if !c.Loaded(bg) {
		t.Error("print succeeded, so the agent is loaded")
	}
	if err := c.Bootout(bg); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"print", "gui/501/dev.jufi.batlog"}, {"bootout", "gui/501/dev.jufi.batlog"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	f.fail = 1
	if c.Loaded(bg) {
		t.Error("print failed, so the agent is not loaded")
	}
}

func TestEnvReadsTheEnvironmentBack(t *testing.T) {
	data, _ := Plist("l", "/b", "/log", map[string]string{"BATLOG_HOME": "/d"})
	if env, err := Env(data); err != nil || env["BATLOG_HOME"] != "/d" {
		t.Errorf("Env = %v, %v", env, err)
	}
	data, _ = Plist("l", "/b", "/log", nil)
	if env, err := Env(data); err != nil || len(env) != 0 {
		t.Errorf("no env: Env = %v, %v", env, err)
	}
}

func TestBootstrapStopsRetryingWhenCancelled(t *testing.T) {
	f := &fake{fail: 99, out: "Bootstrap failed: 5: Input/output error"}
	var slept []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := client(f, &slept).Bootstrap(ctx, "/p.plist")
	if !errors.Is(err, context.Canceled) || len(f.calls) > 1 {
		t.Errorf("err = %v after %d calls, want context.Canceled without retrying", err, len(f.calls))
	}
}
