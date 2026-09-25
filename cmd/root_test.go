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
