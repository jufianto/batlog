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
		return h, nil
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
	if h := os.Getenv(envHome); h != "" {
		return filepath.Join(h, "daemon.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "batlog", "daemon.log"), nil
}
