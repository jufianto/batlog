// Package cmd holds the cobra commands. Behaviour lives in internal/;
// commands only wire probes, the store and rendering together.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Set by goreleaser through -ldflags "-X github.com/jufianto/batlog/cmd.version=...".
var version = "dev"

// jsonOut is the persistent --json flag every command honours.
var jsonOut bool

// usageError wraps flag and argument errors so Execute can exit with 2.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

var rootCmd = &cobra.Command{
	Use:           "batlog",
	Short:         "Record your Mac's battery and per-app energy, and answer questions about them",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Runnable so that an unknown subcommand fails argument validation
	// (exit 2) instead of cobra's plain "unknown command" error (exit 1).
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

// usageArgs marks positional-argument errors as usage errors (exit 2).
func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "print machine-readable JSON instead of text")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError{err}
	})
}

// Execute runs the CLI. Exit codes: 0 success, 1 operational error, 2 usage error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "batlog:", err)
		var ue usageError
		if errors.As(err, &ue) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
