package ui

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jufianto/batlog/internal/charge"
	"github.com/jufianto/batlog/internal/health"
	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/status"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/top"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var (
	wib = time.FixedZone("WIB", 7*3600)
	// noon on Sat 26 Sep 2026.
	noon     = time.Date(2026, 9, 26, 12, 0, 0, 0, wib)
	midnight = time.Date(2026, 9, 26, 0, 0, 0, 0, wib)
)

func at(min int) int64 { return midnight.Unix() + int64(min)*60 }

// seg is one sample a minute over [from, to) minutes after midnight, the
// percent moving linearly from p0 to p1.
func seg(from, to, p0, p1 int, ac bool) []store.Sample {
	var ss []store.Sample
	n := to - from
	for k := range n {
		p := p0
		if n > 1 {
			p = p0 + (p1-p0)*k/(n-1)
		}
		ss = append(ss, store.Sample{TS: at(from + k), Pct: p, OnAC: ac, Charging: ac && p < 100, Watts: 6})
	}
	return ss
}

// day: AC until 23:10 yesterday, battery until 01:00, charging 90 → 100 %
// until 02:00, battery until 03:00, asleep until 11:00 with a dark wake at
// 07:00, then on battery until noon.
func day() []store.Sample {
	var ss []store.Sample
	for _, s := range [][]store.Sample{
		seg(-60, -50, 100, 100, true),
		seg(-50, 60, 100, 90, false),
		seg(60, 120, 90, 100, true),
		seg(120, 180, 100, 95, false),
		seg(420, 421, 95, 95, false),
		seg(660, 720, 94, 88, false),
	} {
		ss = append(ss, s...)
	}
	return ss
}

type fake struct {
	samples []store.Sample
	apps    *Apps
	failAll error
	calls   map[string]int
	ranges  []Range
}

func newFake() *fake {
	brave, ws := 8.0, 3.0
	zero := 0.0
	return &fake{samples: day(), calls: map[string]int{}, apps: &Apps{Recorded: true, First: at(-60), Rows: []top.Row{
		{App: "Brave Browser", Share: 0.5, BatteryPct: &brave},
		{App: "Xcode", Share: 0.33, BatteryPct: &zero},
		{App: "WindowServer", System: true, Share: 0.17, BatteryPct: &ws},
	}}}
}

func (f *fake) Live(context.Context) (Live, error) {
	f.calls["live"]++
	if f.failAll != nil {
		return Live{}, f.failAll
	}
	drain, est, macos, cycles, hp := 5.6, 940, 900, 396, 90.0
	cond := "Normal"
	return Live{
		Status: status.Report{Percent: 88, Watts: ptr(5.0), Drain: &drain, EstMinutes: &est, MacOSMinutes: &macos,
			Worst: &status.Offender{App: "Brave Browser", Share: 0.75}},
		Health:  health.Report{HealthPct: &hp, Cycles: &cycles, Condition: &cond},
		FirstTS: f.samples[0].TS, NewestTS: f.samples[len(f.samples)-1].TS,
	}, nil
}

func ptr[T any](v T) *T { return &v }

func (f *fake) History(_ context.Context, r Range) (History, error) {
	f.calls["history"]++
	f.ranges = append(f.ranges, r)
	if f.failAll != nil {
		return History{}, f.failAll
	}
	in := history.Input{From: r.From, To: r.To, Samples: f.samples, FirstSampleTS: f.samples[0].TS}
	c := charge.Learn([charge.Bands]charge.BandStat{})
	return History{Result: history.Build(in), Samples: f.samples, Curve: &c}, nil
}

func (f *fake) Apps(context.Context, Range) (Apps, error) {
	f.calls["apps"]++
	return *f.apps, f.failAll
}

func (f *fake) AppSeries(_ context.Context, app string, _ Range) ([]Point, error) {
	f.calls["series"]++
	return []Point{{at(15), 10}, {at(30), 30}, {at(675), 60}}, nil
}

func (f *fake) Session(_ context.Context, id string) (string, error) {
	f.calls["session"]++
	return "⚡ session " + id + " · 02:00 → 03:00 · 59m awake · 100% → 95% (5.1 %/hr)\n" +
		" #   APP             SHARE   BATTERY COST\n 1   Brave Browser   60%     ≈ 3% of battery\n", nil
}

func (f *fake) Report(context.Context, Range) (string, error) {
	f.calls["report"]++
	return "📊 batlog report · today, Sat 26 Sep\n\n── drain ─────────\navg 5.7 %/hr\n\n── habits & health ──\n" +
		"⚠ battery spends 89% of time above 90% — consider Optimized Charging or unplugging earlier\n", nil
}

func (f *fake) Health(context.Context) (HealthDays, error) {
	f.calls["health"]++
	var rows []store.HealthRow
	for i, mah := range []int{4500, 4510, 4490, 4480, 4495, 4470} {
		rows = append(rows, store.HealthRow{Day: time.Date(2026, 9, 21+i, 0, 0, 0, 0, wib).Format("2006-01-02"), RawMaxMAh: mah, DesignMAh: 5000})
	}
	return HealthDays{Text: "health         89.4%   (4 470 / 5 000 mAh design)\ncycles         396\n", Rows: rows}, nil
}

func snap(f *fake, color bool, w, h int, keys ...string) string {
	return Snapshot(context.Background(), f, Options{Color: color, Now: func() time.Time { return noon }}, w, h, keys...)
}

// Every screen, at the smallest size and a roomy one, without colour.
func TestGolden(t *testing.T) {
	screens := map[string][]string{
		"battery":        nil,
		"battery-week":   {"w"},
		"session":        {"enter"},
		"charge":         {"down", "enter"},
		"apps":           {"2"},
		"app":            {"2", "enter"},
		"report":         {"3"},
		"health":         {"4"},
		"help":           {"?"},
		"yesterday":      {"["},
		"battery-scroll": {"G"},
	}
	for name, keys := range screens {
		for _, size := range [][2]int{{80, 24}, {120, 40}} {
			got := snap(newFake(), false, size[0], size[1], keys...)
			file := filepath.Join("testdata", name+"-"+strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1])+".golden")
			if *update {
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/ui -update)", err)
			}
			if got != string(want) {
				t.Errorf("%s differs; got:\n%s", file, got)
			}
		}
	}
}

// Colour or not, every line is exactly the window's width. Colour changes
// nothing but the escape codes, except in the battery chart: without it, AC
// is ▓ and asleep ░ so the power source still shows.
func TestEveryLineFillsTheWindow(t *testing.T) {
	for _, keys := range [][]string{nil, {"w"}, {"enter"}, {"down", "enter"}, {"2"}, {"2", "enter"}, {"3"}, {"4"}, {"?"}} {
		for _, size := range [][2]int{{80, 24}, {100, 30}, {160, 50}} {
			plain := snap(newFake(), false, size[0], size[1], keys...)
			colour := snap(newFake(), true, size[0], size[1], keys...)
			chart := len(keys) == 0 || keys[0] != "2" && keys[0] != "3" && keys[0] != "4" && keys[0] != "?"
			if !chart && ansi.Strip(colour) != plain {
				t.Errorf("%v at %v: colour changes the text", keys, size)
			}
			for _, screen := range []string{plain, colour} {
				lines := strings.Split(screen, "\n")
				if len(lines) != size[1] {
					t.Errorf("%v at %v: %d lines", keys, size, len(lines))
				}
				for i, l := range lines {
					if w := ansi.StringWidth(l); w != size[0] {
						t.Errorf("%v at %v: line %d is %d wide: %q", keys, size, i, w, l)
					}
				}
			}
		}
	}
}

func TestTooSmall(t *testing.T) {
	if got := snap(newFake(), false, 79, 30); got != "make the window at least 80×24 (now 79×30)" {
		t.Errorf("got %q", got)
	}
}

func TestRangesStepAndStop(t *testing.T) {
	f := newFake()
	// The first sample is 23:00 yesterday: one step back is yesterday,
	// a second one has no data before it.
	got := snap(f, false, 120, 40, "[", "[")
	if !strings.Contains(got, "1 Battery · Fri 25 Sep") || !strings.Contains(got, "no data before Fri 25 Sep") {
		t.Errorf("got:\n%s", got)
	}
	last := f.ranges[len(f.ranges)-1]
	if !last.From.Equal(midnight.AddDate(0, 0, -1)) || !last.To.Equal(midnight) {
		t.Errorf("yesterday = %v → %v", last.From, last.To)
	}
	// ] goes back to today, and stops there.
	got = snap(newFake(), false, 120, 40, "[", "]", "]")
	if !strings.Contains(got, "1 Battery · today") {
		t.Errorf("got:\n%s", got)
	}
}

func TestMakeRange(t *testing.T) {
	week := MakeRange(noon, true, 0)
	prev := MakeRange(noon, true, 1)
	if !prev.To.Equal(week.From) || !prev.From.Equal(week.From.AddDate(0, 0, -7)) {
		t.Errorf("last week %v → %v, this week from %v", prev.From, prev.To, week.From)
	}
	for r, want := range map[Range]string{
		MakeRange(noon, false, 0): "today",
		week:                      "last 7 days",
		MakeRange(noon, false, 2): "Thu 24 Sep",
		prev:                      "13 – 19 Sep",
		MakeRange(noon, true, 3):  "30 Aug – 05 Sep",
	} {
		if got := r.Label(); got != want {
			t.Errorf("%v → %v: %q, want %q", r.From, r.To, got, want)
		}
	}
}

func TestRefreshKeepsTheSelectionByID(t *testing.T) {
	f := newFake()
	m := New(context.Background(), f, Options{Now: func() time.Time { return noon }, static: true})
	tm := drain(m, func() tea.Msg { return tea.WindowSizeMsg{Width: 120, Height: 40} })
	tm = drain(tm, m.Init())
	tm = drain(tm, func() tea.Msg { return keyPress("down") })
	before := tm.(Model).bat.id
	// A new charge at 11:30 now sorts above the selected row.
	f.samples = append(day()[:len(day())-30], seg(690, 720, 90, 95, true)...)
	tm = drain(tm, tm.(Model).loadView())
	after := tm.(Model)
	if after.bat.id != before || ids(batItems(*after.hist))[after.bat.cur] != before {
		t.Errorf("selected %q at %d, want %q kept", after.bat.id, after.bat.cur, before)
	}
}

func TestStaleRangeDataIsDropped(t *testing.T) {
	m := New(context.Background(), newFake(), Options{Now: func() time.Time { return noon }})
	m.rng = MakeRange(noon, true, 0)
	old := MakeRange(noon, false, 0)
	m = m.gotData(dataMsg{view: BatteryView, key: old.key(), hist: &History{}})
	if m.hist != nil {
		t.Error("a load for today was shown while the week is selected")
	}
}

func TestErrorsShowWhenNothingElseCan(t *testing.T) {
	f := newFake()
	f.failAll = errors.New("database is locked")
	if got := snap(f, false, 120, 40); !strings.Contains(got, " database is locked") {
		t.Errorf("first load failed silently:\n%s", got)
	}

	// With data on screen, one failure is retried quietly; three are shown.
	m := New(context.Background(), newFake(), Options{Now: func() time.Time { return noon }})
	m = m.gotData(dataMsg{view: BatteryView, key: m.rng.key(), hist: &History{}})
	for i := 1; i <= failLimit; i++ {
		m = m.gotData(dataMsg{view: BatteryView, key: m.rng.key(), err: errors.New("database is locked")})
		if shown := m.note != ""; shown != (i == failLimit) {
			t.Errorf("failure %d shown = %v", i, shown)
		}
	}
	if m.hist == nil {
		t.Error("the last good data was dropped")
	}

	m = m.gotData(dataMsg{view: BatteryView, key: m.rng.key(), hist: &History{}, err: Warning("reading the charge curve: busy")})
	if m.note != "warning: reading the charge curve: busy" || m.hist == nil {
		t.Errorf("warning: note %q", m.note)
	}
}

func TestDetailsLoadOnce(t *testing.T) {
	f := newFake()
	snap(f, false, 120, 40, "enter", "esc", "enter")
	if f.calls["session"] != 1 {
		t.Errorf("session read %d times, want once", f.calls["session"])
	}
	// A charge has no session read; switching views reads each view once.
	f = newFake()
	snap(f, false, 120, 40, "down", "enter", "2", "1", "2")
	if f.calls["session"] != 0 || f.calls["history"] != 1 || f.calls["apps"] != 1 {
		t.Errorf("calls = %v", f.calls)
	}
}
