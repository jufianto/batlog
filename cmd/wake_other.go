//go:build !darwin

package cmd

import "time"

// lastWake is unknown off macOS; batlog only records on Macs, but the tests
// and the build run on Linux CI.
func lastWake() (slept, woke time.Time, ok bool) { return time.Time{}, time.Time{}, false }
