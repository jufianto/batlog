// Package paths resolves where batlog keeps its data (ADR-0005).
package paths

import (
	"os"
	"path/filepath"
)

const envHome = "BATLOG_HOME"

// Home is the data directory: $BATLOG_HOME if set, otherwise
// ~/Library/Application Support/batlog.
func Home() (string, error) {
	if h := os.Getenv(envHome); h != "" {
		// Absolute, because the daemon runs from "/" and must find the
		// same directory the installer's shell meant.
		return filepath.Abs(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "batlog"), nil
}

// DB is the SQLite database path.
func DB() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "batlog.db"), nil
}

// Log is the daemon log path. With BATLOG_HOME set it sits beside the
// database so tests and power users get one self-contained directory.
func Log() (string, error) {
	if os.Getenv(envHome) != "" {
		h, err := Home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, "daemon.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "batlog", "daemon.log"), nil
}

// AgentLabel is the launchd label of the recorder (ADR-0004).
const AgentLabel = "dev.jufi.batlog"

// LaunchAgent is where the recorder's plist lives. launchd only looks in
// ~/Library/LaunchAgents, so BATLOG_HOME does not move it.
func LaunchAgent() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", AgentLabel+".plist"), nil
}
