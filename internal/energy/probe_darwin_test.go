//go:build darwin

package energy

import (
	"os"
	"testing"
	"time"
)

// TestReadLive checks the field indexes against the running kernel: this
// test's own coalition must be found and its CPU energy must rise while it
// burns CPU, and no counter may fall.
func TestReadLive(t *testing.T) {
	self, err := coalitionOf(os.Getpid())
	if err != nil || self == 0 {
		t.Fatalf("own coalition = %d, %v", self, err)
	}
	none := func(uint64) bool { return false }
	before, err := Read(none)
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
	}
	after, err := Read(func(uint64) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	find := func(rs []Reading, c uint64) (Reading, bool) {
		for _, r := range rs {
			if r.Coalition == c {
				return r, true
			}
		}
		return Reading{}, false
	}
	b, ok1 := find(before, self)
	a, ok2 := find(after, self)
	if !ok1 || !ok2 {
		t.Fatalf("own coalition %d missing from a read", self)
	}
	if len(b.Members) == 0 || len(a.Members) != 0 {
		t.Errorf("members: %d unnamed, %d named; paths are only for unnamed coalitions", len(b.Members), len(a.Members))
	}
	// 300 ms of one busy core is well over 10 mJ on any Apple Silicon or Intel Mac.
	if a.CPU < b.CPU+10e6 {
		t.Errorf("own CPU energy rose %d nJ over 300 ms busy; field index moved?", a.CPU-b.CPU)
	}
	for _, r := range after {
		if p, ok := find(before, r.Coalition); ok && (r.CPU < p.CPU || r.GPU < p.GPU || r.ANE < p.ANE) {
			t.Errorf("coalition %d went backwards: %+v -> %+v", r.Coalition, p, r)
		}
	}
	if len(before) < 5 {
		t.Errorf("only %d coalitions; a Mac has dozens", len(before))
	}
}
