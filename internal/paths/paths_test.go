package paths

import (
	"path/filepath"
	"testing"
)

func TestBatlogHomeOverridesEverything(t *testing.T) {
	t.Setenv("BATLOG_HOME", "/tmp/bl-test")

	home, err := Home()
	if err != nil || home != "/tmp/bl-test" {
		t.Fatalf("Home() = %q, %v", home, err)
	}
	db, _ := DB()
	if db != filepath.Join("/tmp/bl-test", "batlog.db") {
		t.Fatalf("DB() = %q", db)
	}
	log, _ := Log()
	if log != filepath.Join("/tmp/bl-test", "daemon.log") {
		t.Fatalf("Log() = %q", log)
	}
}

func TestDefaultsFollowApplicationSupport(t *testing.T) {
	t.Setenv("BATLOG_HOME", "")
	t.Setenv("HOME", "/Users/example")

	home, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/Users/example", "Library", "Application Support", "batlog")
	if home != want {
		t.Fatalf("Home() = %q, want %q", home, want)
	}
	log, _ := Log()
	wantLog := filepath.Join("/Users/example", "Library", "Logs", "batlog", "daemon.log")
	if log != wantLog {
		t.Fatalf("Log() = %q, want %q", log, wantLog)
	}
}

func TestLaunchAgentIgnoresBatlogHome(t *testing.T) {
	// launchd only reads ~/Library/LaunchAgents, whatever BATLOG_HOME says.
	t.Setenv("BATLOG_HOME", "/tmp/bl-test")
	t.Setenv("HOME", "/Users/example")
	p, err := LaunchAgent()
	if err != nil || p != "/Users/example/Library/LaunchAgents/dev.jufi.batlog.plist" {
		t.Fatalf("LaunchAgent() = %q, %v", p, err)
	}
}
