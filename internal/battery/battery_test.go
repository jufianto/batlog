package battery

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseAppleSilicon(t *testing.T) {
	s, err := Parse(fixture(t, "Mac16,8-15.7.3.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Percent != 99 {
		t.Errorf("Percent = %d, want 99 (MaxCapacity==100 means CurrentCapacity is already a percent)", s.Percent)
	}
	if !s.OnAC || !s.Charging || s.FullyCharged {
		t.Errorf("OnAC/Charging/FullyCharged = %v/%v/%v, want true/true/false", s.OnAC, s.Charging, s.FullyCharged)
	}
	if !s.HasWatts || math.Abs(s.Watts-14.017) > 0.001 {
		t.Errorf("Watts = %v (has=%v), want 14.017 (13100 mV × 1070 mA / 1e6)", s.Watts, s.HasWatts)
	}
	if !s.HasMacOSMinutes || s.MacOSMinutes != 10 {
		t.Errorf("MacOSMinutes = %d (has=%v), want 10", s.MacOSMinutes, s.HasMacOSMinutes)
	}
	if s.RawCurrentMAh != 5316 || s.RawMaxMAh != 5424 {
		t.Errorf("raw mAh = %d/%d, want 5316/5424", s.RawCurrentMAh, s.RawMaxMAh)
	}
}

func TestParseIntelUsesRawMilliampHours(t *testing.T) {
	s, err := Parse(fixture(t, "intel-synthetic.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Percent != 83 {
		t.Errorf("Percent = %d, want 83 (4213/5100 rounded)", s.Percent)
	}
	if s.OnAC || s.Charging {
		t.Errorf("expected on battery and not charging")
	}
	// Review Focus 1: negative amperage decodes as int64; watts use |A|.
	if !s.HasWatts || math.Abs(s.Watts-14.75) > 0.001 {
		t.Errorf("Watts = %v, want 14.75 (11800 mV × |-1250| mA / 1e6)", s.Watts)
	}
	if s.HasMacOSMinutes {
		t.Errorf("TimeRemaining 65535 must be reported as unknown")
	}
}

func TestParsePercentFallsBackToCurrentOverMaxWhenRawKeysMissing(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><array><dict>
<key>CurrentCapacity</key><integer>2500</integer>
<key>MaxCapacity</key><integer>5000</integer>
</dict></array></plist>`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Percent != 50 {
		t.Errorf("Percent = %d, want 50", s.Percent)
	}
	if s.HasWatts || s.HasMacOSMinutes {
		t.Errorf("missing keys must leave watts and time unknown")
	}
}

func TestParseEmptyOutputIsNoBattery(t *testing.T) {
	// Review Focus 2: on a desktop Mac ioreg prints nothing at all.
	for _, src := range []string{"", "\n", `<?xml version="1.0"?><plist version="1.0"><array/></plist>`} {
		_, err := Parse([]byte(src))
		if !errors.Is(err, ErrNoBattery) {
			t.Errorf("Parse(%q) error = %v, want ErrNoBattery", src, err)
		}
	}
}

func TestParseBatteryInstalledFalseIsNoBattery(t *testing.T) {
	src := `<?xml version="1.0"?><plist version="1.0"><array><dict>
<key>BatteryInstalled</key><false/>
<key>CurrentCapacity</key><integer>0</integer>
<key>MaxCapacity</key><integer>100</integer>
</dict></array></plist>`
	_, err := Parse([]byte(src))
	if !errors.Is(err, ErrNoBattery) {
		t.Errorf("error = %v, want ErrNoBattery", err)
	}
}

func TestParseGarbageIsUnrecognised(t *testing.T) {
	for _, src := range []string{"not a plist", `<?xml version="1.0"?><plist version="1.0"><array><dict><key>Foo</key><integer>1</integer></dict></array></plist>`} {
		_, err := Parse([]byte(src))
		if !errors.Is(err, ErrUnrecognised) {
			t.Errorf("Parse(%q) error = %v, want ErrUnrecognised", src, err)
		}
	}
}
