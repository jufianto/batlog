// Package pmset reads power-source history from `pmset -g log`. batlog uses
// it only as F3's fallback for time before the daemon's first sample: the
// log takes about two seconds to print and keeps only about a week.
package pmset

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reading is the power source and charge at one log line.
type Reading struct {
	TS   int64
	OnAC bool
	Pct  int
}

// macOS writes the source four ways: "Using AC(Charge: 100)",
// "Using Batt (Charge:100%)", "Using BATT (Charge:10%)" and so on.
var line = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{4}) .*?Using (?i:(AC|Batt))\s*\(Charge:\s*(\d+)%?\)`)

const stampLayout = "2006-01-02 15:04:05 -0700"

// Parse returns every power-source reading in the log, oldest first.
func Parse(r io.Reader) ([]Reading, error) {
	var out []Reading
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := line.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		at, err := time.Parse(stampLayout, m[1])
		if err != nil {
			continue
		}
		pct, _ := strconv.Atoi(m[3])
		out = append(out, Reading{TS: at.Unix(), OnAC: strings.EqualFold(m[2], "AC"), Pct: pct})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("pmset log: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, nil
}

// Changes keeps the readings where the source differs from the one before:
// plug and unplug events. The first reading has no known prior state.
func Changes(rs []Reading) []Reading {
	var out []Reading
	for i := 1; i < len(rs); i++ {
		if rs[i].OnAC != rs[i-1].OnAC {
			out = append(out, rs[i])
		}
	}
	return out
}

// Read runs `pmset -g log` and parses it.
func Read(ctx context.Context) ([]Reading, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pmset", "-g", "log")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pmset: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pmset: %w", err)
	}
	rs, perr := Parse(out)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("pmset: %w", err)
	}
	return rs, perr
}
