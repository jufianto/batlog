package battery

import (
	"errors"
	"fmt"
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
	// Charging at 99%: AvgTimeToFull is macOS's time to full.
	if !s.HasMacOSToFull || s.MacOSToFull != 10 {
		t.Errorf("MacOSToFull = %d (has=%v), want 10", s.MacOSToFull, s.HasMacOSToFull)
	}
	if s.RawCurrentMAh != 5316 || s.RawMaxMAh != 5424 {
		t.Errorf("raw mAh = %d/%d, want 5316/5424", s.RawCurrentMAh, s.RawMaxMAh)
	}
}

func TestParseAppleSiliconOnBattery(t *testing.T) {
	// Real capture while discharging: Amperage is negative (-1533 mA).
	s, err := Parse(fixture(t, "Mac16,8-15.7.3-battery.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Percent != 69 || s.OnAC || s.Charging || s.FullyCharged {
		t.Errorf("Percent/OnAC/Charging/FullyCharged = %d/%v/%v/%v, want 69/false/false/false", s.Percent, s.OnAC, s.Charging, s.FullyCharged)
	}
	if !s.HasWatts || math.Abs(s.Watts-18.145) > 0.001 {
		t.Errorf("Watts = %v (has=%v), want 18.145 (11836 mV × |-1533| mA / 1e6)", s.Watts, s.HasWatts)
	}
	if !s.HasMacOSMinutes || s.MacOSMinutes != 119 {
		t.Errorf("MacOSMinutes = %d (has=%v), want 119", s.MacOSMinutes, s.HasMacOSMinutes)
	}
	if s.HasMacOSToFull {
		t.Errorf("on battery: MacOSToFull = %d, want none (AvgTimeToFull is 65535)", s.MacOSToFull)
	}
	if s.RawCurrentMAh != 3593 || s.RawMaxMAh != 5447 {
		t.Errorf("raw mAh = %d/%d, want 3593/5447", s.RawCurrentMAh, s.RawMaxMAh)
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

func intp(v int) *int         { return &v }
func f64p(v float64) *float64 { return &v }
func eqInt(a, b *int) bool    { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func eqF(a, b *float64) bool  { return (a == nil) == (b == nil) && (a == nil || math.Abs(*a-*b) < 1e-9) }

func TestParseHealthFields(t *testing.T) {
	cases := []struct {
		file string
		want Health
	}{
		{"Mac16,8-15.7.3.plist", Health{Cycles: intp(388), DesignMAh: intp(6249), RawMaxMAh: intp(5424), NominalMAh: intp(5576),
			TempC: f64p(30.91), VoltageV: f64p(13.1), FailureStatus: intp(0)}},
		{"Mac16,8-15.7.3-battery.plist", Health{Cycles: intp(389), DesignMAh: intp(6249), RawMaxMAh: intp(5447), NominalMAh: intp(5599),
			TempC: f64p(31.11), VoltageV: f64p(11.836), FailureStatus: intp(0)}},
		{"intel-nominal-synthetic.plist", Health{Cycles: intp(512), DesignMAh: intp(5800), RawMaxMAh: intp(5100), NominalMAh: intp(4950),
			TempC: f64p(30.12), VoltageV: f64p(11.8), FailureStatus: intp(4)}},
		// No NominalChargeCapacity and no PermanentFailureStatus: both stay nil.
		{"intel-synthetic.plist", Health{Cycles: intp(512), DesignMAh: intp(5800), RawMaxMAh: intp(5100),
			TempC: f64p(30.12), VoltageV: f64p(11.8)}},
	}
	for _, c := range cases {
		s, err := Parse(fixture(t, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		h, w := s.Health, c.want
		if !eqInt(h.Cycles, w.Cycles) || !eqInt(h.DesignMAh, w.DesignMAh) || !eqInt(h.RawMaxMAh, w.RawMaxMAh) ||
			!eqInt(h.NominalMAh, w.NominalMAh) || !eqInt(h.FailureStatus, w.FailureStatus) ||
			!eqF(h.TempC, w.TempC) || !eqF(h.VoltageV, w.VoltageV) {
			t.Errorf("%s: Health = %s, want %s", c.file, fmtHealth(h), fmtHealth(w))
		}
	}
}

func TestParseHealthMissingKeysStayNil(t *testing.T) {
	src := `<?xml version="1.0"?><plist version="1.0"><array><dict>
<key>CurrentCapacity</key><integer>50</integer>
<key>MaxCapacity</key><integer>100</integer>
</dict></array></plist>`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if h := s.Health; h != (Health{}) {
		t.Errorf("Health = %s, want all nil", fmtHealth(h))
	}
}

func fmtHealth(h Health) string {
	i := func(p *int) any {
		if p == nil {
			return "nil"
		}
		return *p
	}
	f := func(p *float64) any {
		if p == nil {
			return "nil"
		}
		return *p
	}
	return fmt.Sprintf("{cycles:%v design:%v raw:%v nominal:%v temp:%v volt:%v fail:%v}",
		i(h.Cycles), i(h.DesignMAh), i(h.RawMaxMAh), i(h.NominalMAh), f(h.TempC), f(h.VoltageV), i(h.FailureStatus))
}

func TestUnrecognisedErrorIsTheSpecsExactText(t *testing.T) {
	_, err := Parse([]byte("not a plist"))
	if err == nil || err.Error() != "cannot read battery (ioreg output not recognised)" {
		t.Errorf("error = %q", err)
	}
}
