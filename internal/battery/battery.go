// Package battery reads the AppleSmartBattery IORegistry entry through
// `ioreg -rc AppleSmartBattery -a` and parses it as a plist (ADR-0002).
package battery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"time"

	"howett.net/plist"
)

var (
	// ErrNoBattery means the machine has no AppleSmartBattery entry.
	ErrNoBattery = errors.New("no battery found on this machine")
	// ErrUnrecognised means ioreg printed something the parser cannot use.
	ErrUnrecognised = errors.New("cannot read battery (ioreg output not recognised)")
)

// timeUnknown is what macOS reports in TimeRemaining while it has no estimate.
const timeUnknown = 65535

// probeTimeout bounds the ioreg call so a wedged IOKit cannot hang a command.
const probeTimeout = 10 * time.Second

// Snapshot is one reading of the battery. Fields with a Has* twin are
// optional: the key varies by Mac model and a missing key must never fail
// a command.
type Snapshot struct {
	Percent         int
	OnAC            bool
	Charging        bool
	FullyCharged    bool
	Watts           float64 // valid when HasWatts
	HasWatts        bool
	MacOSMinutes    int // macOS's own time-remaining estimate; valid when HasMacOSMinutes
	HasMacOSMinutes bool
	RawCurrentMAh   int // gauge-measured; 0 when absent
	RawMaxMAh       int
	Health          Health
}

// Health is the detail `batlog health` shows. Every field is nil when its
// ioreg key is absent, which varies by Mac model.
type Health struct {
	Cycles        *int     // CycleCount
	DesignMAh     *int     // DesignCapacity
	RawMaxMAh     *int     // AppleRawMaxCapacity: the gauge's measured full charge
	NominalMAh    *int     // NominalChargeCapacity: Apple's smoothed figure; absent on older Intel
	TempC         *float64 // Temperature is in hundredths of a degree
	VoltageV      *float64 // Voltage is in mV
	FailureStatus *int     // PermanentFailureStatus: 0 means normal
}

// Parse turns `ioreg -rc AppleSmartBattery -a` output into a Snapshot.
func Parse(data []byte) (Snapshot, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Snapshot{}, ErrNoBattery
	}
	var entries []map[string]any
	if _, err := plist.Unmarshal(data, &entries); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", ErrUnrecognised, err)
	}
	if len(entries) == 0 {
		return Snapshot{}, ErrNoBattery
	}
	m := entries[0]
	if installed, ok := boolKey(m, "BatteryInstalled"); ok && !installed {
		return Snapshot{}, ErrNoBattery
	}

	var s Snapshot
	cur, okCur := intKey(m, "CurrentCapacity")
	max, okMax := intKey(m, "MaxCapacity")
	rawCur, okRawCur := intKey(m, "AppleRawCurrentCapacity")
	rawMax, okRawMax := intKey(m, "AppleRawMaxCapacity")
	switch {
	case okCur && okMax && max == 100:
		// Apple Silicon: CurrentCapacity is already a percentage.
		s.Percent = int(cur)
	case okRawCur && okRawMax && rawMax > 0:
		// Intel: capacities are mAh; the raw pair is the gauge's own reading.
		s.Percent = int(math.Round(float64(rawCur) * 100 / float64(rawMax)))
	case okCur && okMax && max > 0:
		s.Percent = int(math.Round(float64(cur) * 100 / float64(max)))
	default:
		return Snapshot{}, ErrUnrecognised
	}
	s.RawCurrentMAh, s.RawMaxMAh = int(rawCur), int(rawMax)
	s.OnAC, _ = boolKey(m, "ExternalConnected")
	s.Charging, _ = boolKey(m, "IsCharging")
	s.FullyCharged, _ = boolKey(m, "FullyCharged")

	if v, okV := intKey(m, "Voltage"); okV {
		if a, okA := intKey(m, "Amperage"); okA {
			// mV × mA / 1e6 = W. Negative amperage means discharging.
			s.Watts = float64(v) * math.Abs(float64(a)) / 1e6
			s.HasWatts = true
		}
	}
	if t, ok := intKey(m, "TimeRemaining"); ok && t >= 0 && t != timeUnknown {
		s.MacOSMinutes, s.HasMacOSMinutes = int(t), true
	}
	s.Health = Health{
		Cycles:        intPtr(m, "CycleCount"),
		DesignMAh:     intPtr(m, "DesignCapacity"),
		RawMaxMAh:     intPtr(m, "AppleRawMaxCapacity"),
		NominalMAh:    intPtr(m, "NominalChargeCapacity"),
		TempC:         scaledPtr(m, "Temperature", 100),
		VoltageV:      scaledPtr(m, "Voltage", 1000),
		FailureStatus: intPtr(m, "PermanentFailureStatus"),
	}
	return s, nil
}

func intPtr(m map[string]any, k string) *int {
	v, ok := intKey(m, k)
	if !ok {
		return nil
	}
	i := int(v)
	return &i
}

func scaledPtr(m map[string]any, k string, div float64) *float64 {
	v, ok := intKey(m, k)
	if !ok {
		return nil
	}
	f := float64(v) / div
	return &f
}

// intKey reads an integer key. The plist library decodes non-negative
// integers as uint64 and negative ones as int64, so both are accepted.
func intKey(m map[string]any, k string) (int64, bool) {
	switch v := m[k].(type) {
	case int64:
		return v, true
	case uint64:
		return int64(v), true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	}
	return 0, false
}

func boolKey(m map[string]any, k string) (bool, bool) {
	v, ok := m[k].(bool)
	return v, ok
}

// Read runs ioreg and parses its output.
func Read(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-rc", "AppleSmartBattery", "-a").Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("ioreg: %w", err)
	}
	return Parse(out)
}
