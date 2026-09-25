package cmd

import (
	"errors"
	"testing"
)

func TestUnknownFlagIsUsageError(t *testing.T) {
	rootCmd.SetArgs([]string{"--bogus"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("expected usageError, got %T: %v", err, err)
	}
}

func TestExtraArgumentsAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"status", "extra"}, {"bogus-command"}} {
		rootCmd.SetArgs(args)
		err := rootCmd.Execute()
		var ue usageError
		if err == nil || !errors.As(err, &ue) {
			t.Errorf("args %v: expected usageError, got %T: %v", args, err, err)
		}
	}
}
