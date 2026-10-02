// Package energy reads per-app energy from the kernel's coalition counters
// (ADR-0006) and turns successive reads into per-app deltas. The probe is
// darwin-only; naming and the Tracker are pure so they test anywhere.
package energy

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// ErrUnsupported is returned by Read where there are no coalition counters.
var ErrUnsupported = errors.New("per-app energy needs macOS")

// ErrImplausible means a read was refused by the sanity check.
var ErrImplausible = errors.New("implausible energy reading")

// maxWatts: one read claiming more than this across all apps is a moved
// struct field after a macOS update, not a laptop (ADR-0006).
const maxWatts = 200

// forgetAfter is how many reads a coalition may be missing before its
// baseline is dropped: a day of ticks. A coalition can outlive its members
// (lingering children) and regain one later; forgetting it then would count
// its whole life in one tick. Coalition ids are never reused, and a
// baseline is ~80 bytes, so keeping them long costs nothing.
const forgetAfter = 24 * 60

// Other is the row small deltas are folded into (Fold).
const Other = "(other)"

// minDeltaNJ: one tick's delta below this (0.1 J, ~2 mW over a minute) is
// folded into Other. On a developer's Mac ~200 apps move every minute but
// ~20 pass this, and the rest are ~1 % of the energy (measured 2026-09-29),
// so folding keeps app_energy ~5× smaller without changing any ranking.
const minDeltaNJ = 100_000_000

// uint64 indexes in xnu's struct coalition_resource_usage (not public API;
// testdata holds a real reply). The kernel does not report the struct's
// size, so a macOS that moved these fields is caught only by the Tracker's
// sanity check (ADR-0006).
const (
	cruEnergy    = 11 // CPU energy, nJ
	cruANEEnergy = 39 // Neural Engine, nJ
	cruGPUEnergy = 41 // nJ; 40 is phys_footprint, in bytes
	cruFields    = cruGPUEnergy + 1
)

// FromUsage reads one coalition's counters out of a coalition_info reply.
func FromUsage(coalition uint64, cu []uint64) (Reading, error) {
	if len(cu) < cruFields {
		return Reading{}, fmt.Errorf("coalition_info: %d fields, want at least %d", len(cu), cruFields)
	}
	return Reading{Coalition: coalition, CPU: cu[cruEnergy], GPU: cu[cruGPUEnergy], ANE: cu[cruANEEnergy]}, nil
}

// Reading is one coalition's cumulative counters.
type Reading struct {
	Coalition     uint64
	CPU, GPU, ANE uint64   // nanojoules since the coalition was created
	Members       []Member // only for coalitions the caller has not named
}

// Member is one process of a coalition.
type Member struct {
	PID  int
	Comm string
	Path string // executable; empty when unreadable
}

// Delta is the energy one app used between two reads.
type Delta struct {
	App           string
	System        bool
	CPU, GPU, ANE uint64 // nanojoules
}

// Name names a coalition the way Activity Monitor does, roughly, by the
// app bundle its members run from. Among the bundles seen, the best is:
//  1. installed in an Applications folder (/Applications, ~/Applications,
//     /System/Applications, Safari's cryptex), so a Chromium that an
//     agent's Playwright runs from node_modules never names the agent;
//  2. then one whose own main executable (X.app/Contents/MacOS/Y) is a
//     member, over a bundle only helpers run from;
//  3. then the one most members run from, then the lowest pid.
//
// Only a member's outermost bundle counts, and not when it sits inside a
// framework: Homebrew's Python.framework/…/Python.app is how python runs,
// not an app. Without a bundle, the name is the lowest pid's executable, or
// its command when the path is unreadable. A tool that installs each
// version as its own file (Claude Code's ~/.local/share/claude/versions/
// 2.1.287) is named by the nearest folder above that is neither a version
// nor a container like versions/ or bin/: "claude", whatever the version.
//
// system is true for bundles under /System/Library or /Library/Apple
// (Dock, NotificationCenter; Finder excepted), and for bundle-less
// coalitions whose readable paths are all system paths, or that have none
// readable (root-owned).
func Name(members []Member) (name string, system bool) {
	ms := slices.Clone(members)
	sort.Slice(ms, func(i, j int) bool { return ms[i].PID < ms[j].PID })

	type cand struct {
		installed, main bool
		count           int
		sysLib          bool
		order           int // first seen, i.e. lowest pid
	}
	cands := map[string]*cand{}
	for _, m := range ms {
		b, ok := appBundle(m.Path)
		if !ok {
			continue
		}
		c := cands[b.name]
		if c == nil {
			c = &cand{order: len(cands)}
			cands[b.name] = c
		}
		c.installed = c.installed || b.installed
		c.main = c.main || b.main
		c.sysLib = c.sysLib || b.sysLib
		c.count++
	}
	var best *cand
	for n, c := range cands {
		switch {
		case best == nil,
			c.installed != best.installed && c.installed,
			c.installed == best.installed && c.main != best.main && c.main,
			c.installed == best.installed && c.main == best.main && (c.count > best.count || c.count == best.count && c.order < best.order):
			best, name = c, n
		}
	}
	if best != nil {
		return name, best.sysLib && name != "Finder"
	}

	system = true
	for _, m := range ms {
		if m.Path != "" && !systemPath(m.Path) {
			system = false
		}
	}
	for _, m := range ms {
		switch {
		case m.Path != "":
			return exeName(m.Path), system
		case m.Comm != "":
			return m.Comm, system
		}
	}
	return "", system
}

var versionLike = regexp.MustCompile(`^v?\d+(\.\d+)+([-+_.][0-9A-Za-z.]+)?$`)

// containerDirs hold executables without naming them.
var containerDirs = map[string]bool{"versions": true, "version": true, "bin": true, "libexec": true}

// exeName is an executable's file name, or for a version-named file the
// nearest folder above it that names the tool.
func exeName(path string) string {
	parts := strings.Split(path, "/")
	base := parts[len(parts)-1]
	if !versionLike.MatchString(base) {
		return base
	}
	for i := len(parts) - 2; i >= 0; i-- {
		if p := parts[i]; p != "" && !containerDirs[p] && !versionLike.MatchString(p) {
			return p
		}
	}
	return base
}

type bundleInfo struct {
	name            string
	installed, main bool
	sysLib          bool // under /System/Library or /Library/Apple
}

// appBundle finds the outermost .app bundle in an executable path. ok is
// false without one, or when it is inside a framework.
func appBundle(path string) (b bundleInfo, ok bool) {
	i := strings.Index(path, ".app/")
	if i < 0 {
		return b, false
	}
	dir := path[:strings.LastIndex(path[:i], "/")+1]
	if strings.Contains(dir, ".framework/") {
		return b, false
	}
	rest := path[i+len(".app/"):]
	b.name = path[len(dir):i]
	b.main = strings.HasPrefix(rest, "Contents/MacOS/") && !strings.Contains(rest[len("Contents/MacOS/"):], "/")
	b.installed = strings.Contains(dir, "/Applications/")
	b.sysLib = strings.HasPrefix(dir, "/System/Library/") || strings.HasPrefix(dir, "/Library/Apple/")
	return b, true
}

func systemPath(p string) bool {
	if strings.HasPrefix(p, "/usr/local/") {
		return false // Intel Homebrew and installer packages
	}
	for _, prefix := range []string{"/System/", "/usr/", "/bin/", "/sbin/", "/Library/Apple/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

type app struct {
	name   string
	system bool
}

type coalition struct {
	app
	cpu, gpu, ane uint64
	missing       int // consecutive reads without it
	warned        bool
}

// Tracker turns cumulative reads into deltas. The zero value is ready; the
// first Update is a baseline.
type Tracker struct {
	// Logf, if set, gets one line the first time a coalition's counter goes
	// backwards.
	Logf func(format string, args ...any)

	seen  map[uint64]*coalition
	last  time.Time
	based bool
}

// Named reports whether the tracker already knows a coalition's name, so
// the probe can skip reading its members' paths.
func (t *Tracker) Named(c uint64) bool {
	_, ok := t.seen[c]
	return ok
}

// Update takes a read and returns each app's energy since the previous one,
// sorted by app name, zero rows left out:
//   - the first read is a baseline and returns nothing;
//   - a coalition first seen later is counted from zero (it was created
//     since, as coalition ids are never reused);
//   - a counter that went backwards re-baselines that coalition;
//   - more than maxWatts over the time since the previous read drops the
//     whole read with ErrImplausible and moves the baseline to it.
func (t *Tracker) Update(now time.Time, rs []Reading) ([]Delta, error) {
	if t.seen == nil {
		t.seen = map[uint64]*coalition{}
	}
	first := !t.based
	elapsed := now.Sub(t.last).Seconds()
	t.based, t.last = true, now

	sums := map[app]*Delta{}
	var total float64 // float: garbage fields must not wrap under the limit
	present := map[uint64]bool{}
	for _, r := range rs {
		present[r.Coalition] = true
		c, known := t.seen[r.Coalition]
		if !known {
			name, system := Name(r.Members)
			if name == "" {
				name = "(unknown)"
			}
			c = &coalition{app: app{name, system}}
			t.seen[r.Coalition] = c
			if first {
				c.cpu, c.gpu, c.ane = r.CPU, r.GPU, r.ANE
				continue
			}
		}
		c.missing = 0
		if r.CPU < c.cpu || r.GPU < c.gpu || r.ANE < c.ane {
			if !c.warned && t.Logf != nil {
				t.Logf("energy counters of %s went backwards; skipping it once", c.name)
			}
			c.warned = true
			c.cpu, c.gpu, c.ane = r.CPU, r.GPU, r.ANE
			continue
		}
		d := Delta{CPU: r.CPU - c.cpu, GPU: r.GPU - c.gpu, ANE: r.ANE - c.ane}
		c.cpu, c.gpu, c.ane = r.CPU, r.GPU, r.ANE
		if d.CPU+d.GPU+d.ANE == 0 {
			continue
		}
		s := sums[c.app]
		if s == nil {
			s = &Delta{App: c.name, System: c.system}
			sums[c.app] = s
		}
		s.CPU += d.CPU
		s.GPU += d.GPU
		s.ANE += d.ANE
		total += float64(d.CPU) + float64(d.GPU) + float64(d.ANE)
	}
	for id, c := range t.seen {
		if !present[id] {
			if c.missing++; c.missing > forgetAfter {
				delete(t.seen, id)
			}
		}
	}
	if first {
		return nil, nil
	}
	if w := total / 1e9 / max(elapsed, 1); w > maxWatts {
		return nil, fmt.Errorf("%w: %.0f W over %.0f s", ErrImplausible, w, max(elapsed, 1))
	}
	if len(sums) == 0 {
		return nil, nil
	}
	out := make([]Delta, 0, len(sums))
	for _, d := range sums {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].App != out[j].App {
			return out[i].App < out[j].App
		}
		return !out[i].System
	})
	return out, nil
}

// Fold merges each delta under 0.1 J into one Other row, tagged system so
// it is never named the worst offender. Deltas stay sorted by app.
func Fold(ds []Delta) []Delta {
	var out []Delta
	other := Delta{App: Other, System: true}
	for _, d := range ds {
		if d.CPU+d.GPU+d.ANE >= minDeltaNJ {
			out = append(out, d)
			continue
		}
		other.CPU += d.CPU
		other.GPU += d.GPU
		other.ANE += d.ANE
	}
	if other.CPU+other.GPU+other.ANE == 0 {
		return out
	}
	i := sort.Search(len(out), func(i int) bool { return out[i].App >= Other })
	return slices.Insert(out, i, other)
}
