// Package energy reads per-app energy from the kernel's coalition counters
// (ADR-0006) and turns successive reads into per-app deltas. The probe is
// darwin-only; naming and the Tracker are pure so they test anywhere.
package energy

import (
	"errors"
	"fmt"
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
// baseline is dropped. Coalition ids are never reused, so keeping it longer
// only costs memory; forgetting too soon would count its whole life again
// if a read merely failed.
const forgetAfter = 60

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

// Name names a coalition the way Activity Monitor does, roughly:
//  1. the member that is an app's own main executable
//     (X.app/Contents/MacOS/Y, X the outermost bundle), lowest pid first;
//  2. else the most common outermost bundle among members;
//  3. else the lowest pid's executable name, or its command.
//
// system is true when no member is in an app bundle and every readable
// path is a system path (or none is readable: root-owned).
func Name(members []Member) (name string, system bool) {
	ms := slices.Clone(members)
	sort.Slice(ms, func(i, j int) bool { return ms[i].PID < ms[j].PID })
	for _, m := range ms {
		i := strings.Index(m.Path, ".app/Contents/MacOS/")
		if i >= 0 && strings.Index(m.Path, ".app/") == i && !strings.Contains(m.Path[i+len(".app/Contents/MacOS/"):], "/") {
			return bundle(m.Path), false
		}
	}
	count := map[string]int{}
	best := ""
	for _, m := range ms {
		if b := bundle(m.Path); b != "" {
			count[b]++
			if count[b] > count[best] {
				best = b
			}
		}
	}
	if best != "" {
		return best, false
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
			return m.Path[strings.LastIndex(m.Path, "/")+1:], system
		case m.Comm != "":
			return m.Comm, system
		}
	}
	return "", system
}

// bundle is the outermost .app bundle's name in an executable path.
func bundle(path string) string {
	i := strings.Index(path, ".app/")
	if i < 0 {
		return ""
	}
	return path[strings.LastIndex(path[:i], "/")+1 : i]
}

func systemPath(p string) bool {
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
	var total uint64
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
		total += d.CPU + d.GPU + d.ANE
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
	if w := float64(total) / 1e9 / max(elapsed, 1); w > maxWatts {
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
