package energy

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

var t0 = time.Unix(1_759_000_000, 0)

func member(pid int, path string) Member {
	comm := path[strings.LastIndex(path, "/")+1:]
	return Member{PID: pid, Comm: comm, Path: path}
}

func TestNameAndSystem(t *testing.T) {
	cases := []struct {
		name    string
		members []Member
		want    string
		system  bool
	}{
		{"main executable wins over helpers, whatever the pid order", []Member{
			member(900, "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/140/Helpers/Google Chrome Helper (Renderer).app/Contents/MacOS/Google Chrome Helper (Renderer)"),
			member(901, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		}, "Google Chrome", false},
		{"a nested bundle's main executable is not the app", []Member{
			member(10, "/Applications/Brave Browser.app/Contents/Frameworks/Brave Browser Framework.framework/Helpers/Brave Browser Helper.app/Contents/MacOS/Brave Browser Helper"),
		}, "Brave Browser", false},
		{"children outside any bundle take the most common bundle", []Member{
			member(5, "/opt/homebrew/bin/node"),
			member(6, "/Applications/T3 Code.app/Contents/Resources/bin/rg"),
			member(7, "/Applications/T3 Code.app/Contents/Resources/bin/t3"),
			member(8, "/usr/bin/git"),
		}, "T3 Code", false},
		{"system daemon", []Member{member(150, "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer")}, "WindowServer", true},
		{"system apps with a bundle are apps", []Member{member(600, "/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder")}, "Finder", false},
		{"a user binary is not system", []Member{member(4242, "/Users/me/.local/bin/batlog")}, "batlog", false},
		{"unreadable paths fall back to the command", []Member{{PID: 0, Comm: "kernel_task"}}, "kernel_task", true},
		{"homebrew daemons are not system", []Member{member(77, "/opt/homebrew/opt/postgresql@17/bin/postgres")}, "postgres", false},
		{"/usr/local is not system", []Member{member(78, "/usr/local/bin/ollama")}, "ollama", false},
		// Seen live: T3 Code's coalition was named Python.
		{"a Python.app inside a framework is not an app", []Member{
			member(100, "/opt/homebrew/Cellar/python@3.13/3.13.7/Frameworks/Python.framework/Versions/3.13/Resources/Python.app/Contents/MacOS/Python"),
			member(200, "/Applications/T3 Code.app/Contents/MacOS/T3 Code"),
		}, "T3 Code", false},
		{"an installed app beats a bundle run from a cache", []Member{
			member(100, "/Users/me/Library/Caches/ms-playwright/chromium-1187/chrome-mac/Chromium.app/Contents/MacOS/Chromium"),
			member(101, "/Users/me/Library/Caches/ms-playwright/chromium-1187/chrome-mac/Chromium.app/Contents/Frameworks/Chromium Framework.framework/Helpers/Chromium Helper.app/Contents/MacOS/Chromium Helper"),
			member(300, "/Applications/Warp.app/Contents/MacOS/stable"),
		}, "Warp", false},
		{"~/Applications counts as installed", []Member{
			member(1, "/Users/me/src/app/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"),
			member(2, "/Users/me/Applications/Chrome Apps.localized/Figma.app/Contents/MacOS/app_mode_loader"),
		}, "Figma", false},
		{"system UI agents are system", []Member{member(500, "/System/Library/CoreServices/Dock.app/Contents/MacOS/Dock")}, "Dock", true},
		{"Safari runs from its cryptex", []Member{member(900, "/System/Volumes/Preboot/Cryptexes/App/System/Applications/Safari.app/Contents/MacOS/Safari"),
			member(901, "/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices/com.apple.WebKit.WebContent.xpc/Contents/MacOS/com.apple.WebKit.WebContent")}, "Safari", false},
		{"System Settings is an app", []Member{member(5, "/System/Applications/System Settings.app/Contents/MacOS/System Settings")}, "System Settings", false},
		// Seen live: Claude Code's coalition was named 2.1.285, and its
		// kernel command is the version too.
		{"a version-named executable takes its tool's folder", []Member{
			{PID: 9188, Comm: "2.1.283", Path: "/Users/me/.local/share/claude/versions/2.1.283"},
		}, "claude", false},
		{"version folders and bin/ are skipped", []Member{member(7, "/opt/tools/mytool/v1.4.0/bin/1.4.0-rc1")}, "mytool", false},
		{"a dotted name that is not a version stays", []Member{member(8, "/usr/local/bin/python3.13")}, "python3.13", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, system := Name(c.members)
			if got != c.want || system != c.system {
				t.Errorf("Name = %q, system %v; want %q, %v", got, system, c.want, c.system)
			}
		})
	}
}

func reading(c uint64, cpu, gpu, ane uint64, ms ...Member) Reading {
	return Reading{Coalition: c, CPU: cpu, GPU: gpu, ANE: ane, Members: ms}
}

func TestTracker(t *testing.T) {
	chrome := member(1, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	ws := member(2, "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer")
	var logs []string
	tr := &Tracker{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}

	// First read: a baseline, never the apps' whole lifetime.
	ds, err := tr.Update(t0, []Reading{reading(10, 500e9, 0, 0, chrome), reading(20, 900e9, 50e9, 0, ws)})
	if err != nil || ds != nil {
		t.Fatalf("baseline = %v, %v; want nothing", ds, err)
	}
	if !tr.Named(10) || tr.Named(99) {
		t.Error("Named must know coalitions from the baseline, and only those")
	}

	// A minute later: deltas, named from the cache (no members needed), and
	// a coalition born since counts from zero.
	ds, err = tr.Update(t0.Add(time.Minute), []Reading{
		reading(10, 530e9, 2e9, 1e9), reading(20, 906e9, 50e9, 0), reading(30, 4e9, 0, 0, member(3, "/Applications/Slack.app/Contents/MacOS/Slack")),
	})
	want := []Delta{{"Google Chrome", false, 30e9, 2e9, 1e9}, {"Slack", false, 4e9, 0, 0}, {"WindowServer", true, 6e9, 0, 0}}
	if err != nil || fmt.Sprint(ds) != fmt.Sprint(want) {
		t.Fatalf("Update = %v, %v; want %v", ds, err, want)
	}

	// A counter that goes backwards re-baselines that coalition, logged once.
	for i, cpu := range []uint64{1e9, 0.5e9} {
		ds, err = tr.Update(t0.Add(time.Duration(2+i)*time.Minute), []Reading{reading(10, cpu, 2e9, 1e9), reading(20, 906e9, 50e9, 0), reading(30, uint64(5+i)*1e9, 0, 0)})
		if err != nil || fmt.Sprint(ds) != fmt.Sprint([]Delta{{"Slack", false, 1e9, 0, 0}}) {
			t.Errorf("decrease %d = %v, %v; want only Slack's delta", i, ds, err)
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "Google Chrome") {
		t.Errorf("logs = %q, want one line naming the app", logs)
	}

	// Two coalitions of one app are one row.
	ds, _ = tr.Update(t0.Add(4*time.Minute), []Reading{reading(10, 2e9, 2e9, 1e9), reading(40, 3e9, 0, 0, chrome)})
	if fmt.Sprint(ds) != fmt.Sprint([]Delta{{"Google Chrome", false, 4.5e9, 0, 0}}) {
		t.Errorf("same app = %v", ds)
	}
}

func TestTrackerRejectsImplausibleReads(t *testing.T) {
	app := member(1, "/Applications/Zoom.app/Contents/MacOS/zoom.us")
	tr := &Tracker{}
	tr.Update(t0, []Reading{reading(10, 0, 0, 0, app)})
	// 60 s at 250 W: a moved field, not a laptop.
	ds, err := tr.Update(t0.Add(time.Minute), []Reading{reading(10, 250*60e9, 0, 0)})
	if !errors.Is(err, ErrImplausible) || ds != nil {
		t.Fatalf("Update = %v, %v; want ErrImplausible", ds, err)
	}
	// The baseline moved to the rejected read, so the next one is sane again.
	ds, err = tr.Update(t0.Add(2*time.Minute), []Reading{reading(10, 250*60e9+60e9, 0, 0)})
	if err != nil || len(ds) != 1 || ds[0].CPU != 60e9 {
		t.Errorf("after rejection = %v, %v", ds, err)
	}
	// The limit is over the time since the previous read: 250 W × 60 s
	// again, but spread over 10 minutes, is 25 W.
	ds, err = tr.Update(t0.Add(12*time.Minute), []Reading{reading(10, 2*250*60e9+60e9, 0, 0)})
	if err != nil || len(ds) != 1 || ds[0].CPU != 250*60e9 {
		t.Errorf("over a longer gap = %v, %v", ds, err)
	}
	// Fields full of garbage must not wrap around the limit.
	ds, err = tr.Update(t0.Add(13*time.Minute), []Reading{reading(10, 2*250*60e9+60e9+1<<63, 1<<63, 1<<63)})
	if !errors.Is(err, ErrImplausible) {
		t.Errorf("wrapping read = %v, %v; want ErrImplausible", ds, err)
	}
}

func TestTrackerKeepsVanishedCoalitionsForAWhile(t *testing.T) {
	// A coalition missing from one read (a transient error) must not count
	// its whole life when it comes back.
	app := member(1, "/Applications/Figma.app/Contents/MacOS/Figma")
	tr := &Tracker{}
	tr.Update(t0, []Reading{reading(10, 100e9, 0, 0, app)})
	tr.Update(t0.Add(time.Minute), nil)
	ds, _ := tr.Update(t0.Add(2*time.Minute), []Reading{reading(10, 101e9, 0, 0)})
	if len(ds) != 1 || ds[0].CPU != 1e9 {
		t.Errorf("back after one miss = %v, want a 1e9 delta", ds)
	}
	for i := range forgetAfter + 1 {
		tr.Update(t0.Add(time.Duration(3+i)*time.Minute), nil)
	}
	if tr.Named(10) {
		t.Error("a coalition gone for forgetAfter reads must be forgotten")
	}
}

func TestTrackerUnnamedCoalitionWithoutMembers(t *testing.T) {
	tr := &Tracker{}
	tr.Update(t0, nil)
	ds, _ := tr.Update(t0.Add(time.Minute), []Reading{reading(7, 1e9, 0, 0)})
	if len(ds) != 1 || ds[0].App != "(unknown)" {
		t.Errorf("Update = %v, want an (unknown) row rather than a lost one", ds)
	}
}

func TestFold(t *testing.T) {
	ds := []Delta{
		{App: "Brave Browser", CPU: 5e9},
		{App: "Dock", System: true, CPU: 0.02e9},
		{App: "Slack", CPU: 0.05e9, GPU: 0.04e9},
		{App: "Warp", GPU: 0.1e9},
	}
	want := []Delta{{"(other)", true, 0.07e9, 0.04e9, 0}, {"Brave Browser", false, 5e9, 0, 0}, {"Warp", false, 0, 0.1e9, 0}}
	if got := Fold(ds); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Fold = %v, want %v", got, want)
	}
	if got := Fold(ds[:1]); len(got) != 1 {
		t.Errorf("nothing small, no (other) row: %v", got)
	}
}

// TestFromUsageFixture pins the field indexes to a real reply: CPU energy
// is the largest counter the kernel keeps in nJ, and GPU (41) must not be
// read from phys_footprint (40), which is bytes.
func TestFromUsageFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/coalition-usage-15.7.3.txt")
	if err != nil {
		t.Fatal(err)
	}
	cu := make([]uint64, 128)
	for _, line := range strings.Split(string(data), "\n") {
		var i int
		var v uint64
		if n, _ := fmt.Sscan(line, &i, &v); n == 2 {
			cu[i] = v
		}
	}
	r, err := FromUsage(2403, cu)
	if err != nil {
		t.Fatal(err)
	}
	if r.CPU != 50943023986712 || r.GPU != 1777752491438 || r.ANE != 153181786 || r.Coalition != 2403 {
		t.Errorf("FromUsage = %+v", r)
	}
	if _, err := FromUsage(1, cu[:41]); err == nil {
		t.Error("a reply without the GPU field must be an error")
	}
}
