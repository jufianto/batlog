//go:build darwin

// Spike: can pure Go (no cgo, no root) read per-process energy and the
// responsible-app coalition, the way Activity Monitor groups apps?
// Throwaway, like sample.sh: the answer is the deliverable.
//
//	go run ./scripts/spike/energy probe
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// xnu bsd/sys/proc_info.h and libproc.
const (
	procInfoCallPidinfo    = 2
	procInfoCallPidrusage  = 9
	procPidPathInfo        = 11
	procPidCoalitionInfo   = 20
	rusageInfoV6           = 6
	procPidPathInfoMaxSize = 4 * 1024
)

// rusage_info_v6: a 16-byte uuid, then uint64 fields in this order.
const (
	riUserTime       = 0
	riSystemTime     = 1
	riPkgIdleWkups   = 2
	riInterruptWkups = 3
	riBilledEnergy   = 31
	riServicedEnergy = 32
	riEnergyNJ       = 40 // v6
	riPEnergyNJ      = 41 // v6: on performance cores
	riFields         = 56
)

type rusage [riFields]uint64

func procInfo(call, pid, flavor int, arg uint64, buf []byte) (int, error) {
	r, _, e := unix.Syscall6(unix.SYS_PROC_INFO, uintptr(call), uintptr(pid), uintptr(flavor),
		uintptr(arg), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}

func pidRusage(pid int) (rusage, error) {
	buf := make([]byte, 16+riFields*8)
	if _, err := procInfo(procInfoCallPidrusage, pid, rusageInfoV6, 0, buf); err != nil {
		return rusage{}, err
	}
	var r rusage
	for i := range r {
		r[i] = binary.LittleEndian.Uint64(buf[16+i*8:])
	}
	return r, nil
}

// coalition returns the resource coalition id (type 0) and jetsam (type 1).
func coalition(pid int) (uint64, uint64, error) {
	buf := make([]byte, 5*8)
	if _, err := procInfo(procInfoCallPidinfo, pid, procPidCoalitionInfo, 0, buf); err != nil {
		return 0, 0, err
	}
	return binary.LittleEndian.Uint64(buf), binary.LittleEndian.Uint64(buf[8:]), nil
}

func pidPath(pid int) string {
	buf := make([]byte, procPidPathInfoMaxSize)
	// The kernel returns 0 on success, not a length (libproc strlen()s it).
	if _, err := procInfo(procInfoCallPidinfo, pid, procPidPathInfo, 0, buf); err != nil {
		return ""
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf)
}

// coalition_info(COALITION_INFO_RESOURCE_USAGE): struct
// coalition_resource_usage, uint64 fields: tasks_started, tasks_exited,
// time_nonempty, cpu_time, interrupt_wakeups, platform_idle_wakeups,
// bytesread, byteswritten, gpu_time, cpu_time_billed_to_me,
// cpu_time_billed_to_others, energy, ...
const (
	coalitionInfoResourceUsage = 1
	cruTasksStarted            = 0
	cruTasksExited             = 1
	cruCPUTime                 = 3
	cruEnergy                  = 11 // CPU energy, nJ
	cruEnergyBilledToMe        = 20 // CPU energy daemons spent for us (vouchers), nJ
	cruANEEnergy               = 39 // Neural Engine, nJ
	cruGPUEnergy               = 41 // nJ (40 is phys_footprint)
)

func coalitionUsage(cid uint64) ([]uint64, error) {
	buf := make([]byte, 128*8)
	size := uintptr(len(buf))
	id := cid
	_, _, e := unix.Syscall6(unix.SYS_COALITION_INFO, coalitionInfoResourceUsage,
		uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if e != 0 {
		return nil, e
	}
	out := make([]uint64, size/8)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(buf[i*8:])
	}
	return out, nil
}

type proc struct {
	pid, ppid  int
	comm, path string
	coal       uint64
	ru         rusage
}

func list() ([]proc, map[string]int) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysctl kern.proc.all:", err)
		os.Exit(1)
	}
	errs := map[string]int{}
	var out []proc
	for _, kp := range kps {
		pid := int(kp.Proc.P_pid)
		p := proc{pid: pid, ppid: int(kp.Eproc.Ppid), comm: unix.ByteSliceToString(kp.Proc.P_comm[:])}
		ru, err := pidRusage(pid)
		if err != nil {
			errs["rusage "+errName(err)]++
			continue
		}
		p.ru = ru
		if c, _, err := coalition(pid); err == nil {
			p.coal = c
		} else {
			errs["coalition "+errName(err)]++
		}
		p.path = pidPath(pid)
		out = append(out, p)
	}
	return out, errs
}

func errName(err error) string {
	var e unix.Errno
	if errors.As(err, &e) {
		return unix.ErrnoName(e)
	}
	return err.Error()
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "fields" {
		var cid uint64
		fmt.Sscan(os.Args[2], &cid)
		cu, err := coalitionUsage(cid)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("coalition %d: %d fields\n", cid, len(cu))
		for i, v := range cu {
			fmt.Printf("%3d %d\n", i, v)
		}
		return
	}
	switch {
	case len(os.Args) >= 2 && os.Args[1] == "sample":
		sample()
		return
	case len(os.Args) >= 2 && os.Args[1] == "rank":
		rank()
		return
	case len(os.Args) < 2 || os.Args[1] != "probe":
		fmt.Fprintln(os.Stderr, "usage: energy probe | fields <coalition> | sample | rank [hours] [n]")
		os.Exit(2)
	}
	// Offset check: burn ~300 ms of CPU and see our own user time move.
	self := os.Getpid()
	before, err := pidRusage(self)
	if err != nil {
		fmt.Println("self rusage failed:", err)
		os.Exit(1)
	}
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
	}
	after, _ := pidRusage(self)
	fmt.Printf("self: user_time +%d, energy_nj +%d, billed_energy +%d (after 300 ms busy)\n",
		after[riUserTime]-before[riUserTime], after[riEnergyNJ]-before[riEnergyNJ], after[riBilledEnergy]-before[riBilledEnergy])

	ps, errs := list()
	fmt.Printf("processes readable: %d; errors: %v\n\n", len(ps), errs)
	sort.Slice(ps, func(i, j int) bool { return ps[i].ru[riEnergyNJ] > ps[j].ru[riEnergyNJ] })
	fmt.Printf("%-7s %-7s %-10s %14s %14s  %s\n", "PID", "PPID", "COALITION", "ENERGY_J", "BILLED", "PATH")
	for _, p := range ps[:min(20, len(ps))] {
		name := p.path
		if name == "" {
			name = "(" + p.comm + ")"
		}
		if len(name) > 70 {
			name = "…" + name[len(name)-69:]
		}
		fmt.Printf("%-7d %-7d %-10d %14.1f %14d  %s\n", p.pid, p.ppid, p.coal, float64(p.ru[riEnergyNJ])/1e9, p.ru[riBilledEnergy], name)
	}

	// Coalition totals against the sum of their live members.
	type agg struct {
		name   string
		sumNJ  uint64
		n      int
		minPID int
	}
	coals := map[uint64]*agg{}
	for _, p := range ps {
		a := coals[p.coal]
		if a == nil {
			a = &agg{minPID: p.pid}
			coals[p.coal] = a
		}
		a.sumNJ += p.ru[riEnergyNJ]
		a.n++
		if a.name == "" {
			a.name = leaderName([]proc{p})
		}
	}
	ids := make([]uint64, 0, len(coals))
	for id := range coals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return coals[ids[i]].sumNJ > coals[ids[j]].sumNJ })
	fmt.Printf("\n%-10s %-28s %5s %12s %12s %6s %6s\n", "COALITION", "APP", "LIVE", "LIVE_SUM_J", "COAL_J", "START", "EXIT")
	for _, id := range ids[:min(12, len(ids))] {
		a := coals[id]
		cu, err := coalitionUsage(id)
		var coalJ, st, ex string
		if err != nil {
			coalJ = "err " + errName(err)
		} else {
			coalJ = fmt.Sprintf("%.1f", float64(cu[cruEnergy])/1e9)
			st, ex = fmt.Sprint(cu[cruTasksStarted]), fmt.Sprint(cu[cruTasksExited])
		}
		fmt.Printf("%-10d %-28s %5d %12.1f %12s %6s %6s\n", id, a.name, a.n, float64(a.sumNJ)/1e9, coalJ, st, ex)
	}
}

// appName is the outermost .app bundle in an executable path.
func appName(path string) string {
	i := strings.Index(path, ".app/")
	if i < 0 {
		return ""
	}
	return path[strings.LastIndex(path[:i], "/")+1 : i]
}

// leaderName names a coalition the way Activity Monitor does, roughly: the
// member that is an app's own main executable (X.app/Contents/MacOS/Y with
// no bundle inside another), lowest pid first; else the most common
// outermost bundle; else the lowest pid's command.
func leaderName(members []proc) string {
	sort.Slice(members, func(i, j int) bool { return members[i].pid < members[j].pid })
	for _, m := range members {
		i := strings.Index(m.path, ".app/Contents/MacOS/")
		if i >= 0 && strings.Index(m.path, ".app/") == i && !strings.Contains(m.path[i+20:], "/") {
			return appName(m.path)
		}
	}
	count := map[string]int{}
	best := ""
	for _, m := range members {
		if n := appName(m.path); n != "" {
			count[n]++
			if count[n] > count[best] {
				best = n
			}
		}
	}
	if best != "" {
		return best
	}
	if len(members) > 0 {
		return "(" + members[0].comm + ")"
	}
	return ""
}

const outPath = "batlog-energy.tsv"

// sample appends, every 60 s, one row per coalition whose counters moved:
//
//	ts  coalition  name  cpu_nj  gpu_nj  ane_nj  billed_to_me_nj  tasks_started
//
// Counters are cumulative for the coalition's life, exited members
// included. Rows with coalition 0 are the sampler's own cost (its process
// energy, cumulative), so rank can report what sampling costs.
func sample() {
	home, _ := os.UserHomeDir()
	path := home + "/" + outPath
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if st, _ := f.Stat(); st.Size() == 0 {
		fmt.Fprintln(f, "ts\tcoalition\tname\tcpu_nj\tgpu_nj\tane_nj\tbilled_to_me_nj\ttasks_started")
	}
	os.WriteFile("/tmp/batlog-energy.pid", []byte(fmt.Sprint(os.Getpid())), 0o644)
	fmt.Printf("sampling every 60s -> %s (pid %d)\n", path, os.Getpid())
	names := map[uint64]string{}
	last := map[uint64][4]uint64{}
	for {
		ts := time.Now().Unix()
		members := map[uint64][]proc{}
		kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
		if err != nil {
			fmt.Fprintln(os.Stderr, "sysctl:", err)
		}
		for _, kp := range kps {
			pid := int(kp.Proc.P_pid)
			c, _, err := coalition(pid)
			if err != nil || c == 0 {
				continue
			}
			if _, named := names[c]; named {
				members[c] = nil // known: no need for paths
				continue
			}
			members[c] = append(members[c], proc{pid: pid, comm: unix.ByteSliceToString(kp.Proc.P_comm[:]), path: pidPath(pid)})
		}
		var b strings.Builder
		for c, ms := range members {
			if _, named := names[c]; !named {
				names[c] = leaderName(ms)
			}
			cu, err := coalitionUsage(c)
			if err != nil {
				continue
			}
			v := [4]uint64{cu[cruEnergy], cu[cruGPUEnergy], cu[cruANEEnergy], cu[cruEnergyBilledToMe]}
			if v == last[c] {
				continue
			}
			last[c] = v
			fmt.Fprintf(&b, "%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\n", ts, c, names[c], v[0], v[1], v[2], v[3], cu[cruTasksStarted])
		}
		if self, err := pidRusage(os.Getpid()); err == nil {
			fmt.Fprintf(&b, "%d\t0\t(sampler)\t%d\t0\t0\t0\t0\n", ts, self[riEnergyNJ])
		}
		f.WriteString(b.String())
		time.Sleep(time.Until(time.Unix(ts+60, 0)))
	}
}

// rank sums each app's energy over the last hours from consecutive rows of
// the same coalition (a coalition first seen after the first tick counts
// from zero: it was created since).
func rank() {
	hours, n := 12, 15
	if len(os.Args) > 2 {
		fmt.Sscan(os.Args[2], &hours)
	}
	if len(os.Args) > 3 {
		fmt.Sscan(os.Args[3], &n)
	}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/" + outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	var firstTS, lastTS int64
	prev := map[uint64][4]uint64{}
	seen := map[uint64]bool{}
	type sums struct{ cpu, gpu, ane, billed float64 }
	apps := map[string]*sums{}
	var sampler float64
	var samplerPrev uint64
	for i, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) != 8 {
			continue
		}
		var ts int64
		var c uint64
		var v [4]uint64
		fmt.Sscan(f[0], &ts)
		fmt.Sscan(f[1], &c)
		for k := 0; k < 4; k++ {
			fmt.Sscan(f[3+k], &v[k])
		}
		if firstTS == 0 {
			firstTS = ts
		}
		if c == 0 {
			if ts >= since && samplerPrev != 0 && v[0] >= samplerPrev {
				sampler += float64(v[0] - samplerPrev)
			}
			samplerPrev = v[0]
			continue
		}
		p, had := prev[c]
		if !had && ts == firstTS {
			prev[c], seen[c] = v, true
			continue
		}
		prev[c] = v
		if ts < since {
			continue
		}
		lastTS = ts
		a := apps[f[2]]
		if a == nil {
			a = &sums{}
			apps[f[2]] = a
		}
		d := func(k int) float64 {
			if v[k] < p[k] {
				return 0
			}
			return float64(v[k] - p[k])
		}
		a.cpu += d(0)
		a.gpu += d(1)
		a.ane += d(2)
		a.billed += d(3)
	}
	var total float64
	names := make([]string, 0, len(apps))
	for name, a := range apps {
		total += a.cpu + a.gpu + a.ane + a.billed
		names = append(names, name)
	}
	e := func(name string) float64 { a := apps[name]; return a.cpu + a.gpu + a.ane + a.billed }
	sort.Slice(names, func(i, j int) bool { return e(names[i]) > e(names[j]) })
	fmt.Printf("last %dh · data %s → %s\n\n", hours, time.Unix(max(since, firstTS), 0).Format("15:04"), time.Unix(lastTS, 0).Format("15:04"))
	fmt.Printf("%-3s %-30s %7s %10s   %s\n", "#", "APP", "SHARE", "JOULES", "cpu / gpu / ane / billed-in (J)")
	for i, name := range names[:min(n, len(names))] {
		a := apps[name]
		fmt.Printf("%-3d %-30s %6.1f%% %10.0f   %.0f / %.0f / %.0f / %.0f\n", i+1, name, 100*e(name)/total, e(name)/1e9,
			a.cpu/1e9, a.gpu/1e9, a.ane/1e9, a.billed/1e9)
	}
	fmt.Printf("\nsampler cost: %.1f J = %.3f%% of the %.0f J measured\n", sampler/1e9, 100*sampler/total, total/1e9)
}
