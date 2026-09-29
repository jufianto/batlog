//go:build darwin

package energy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// xnu bsd/sys/proc_info.h and osfmk/mach/coalition.h.
const (
	procInfoCallPidinfo        = 2
	procPidPathInfo            = 11
	procPidCoalitionInfo       = 20
	procPidPathInfoMaxSize     = 4 * 1024
	coalitionInfoResourceUsage = 1
)

// Read lists every process's resource coalition and reads each coalition's
// counters. Members' executable paths are read only for coalitions that
// named reports false, since naming is the only use for them.
func Read(named func(coalition uint64) bool) ([]Reading, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	members := map[uint64][]Member{}
	for _, kp := range kps {
		pid := int(kp.Proc.P_pid)
		c, err := coalitionOf(pid)
		if err != nil || c == 0 {
			continue // exited since the list, or no resource coalition
		}
		if named(c) {
			if _, ok := members[c]; !ok {
				members[c] = nil
			}
			continue
		}
		members[c] = append(members[c], Member{PID: pid, Comm: unix.ByteSliceToString(kp.Proc.P_comm[:]), Path: pidPath(pid)})
	}
	out := make([]Reading, 0, len(members))
	for c, ms := range members {
		cu, err := coalitionUsage(c)
		if err != nil {
			continue // the coalition emptied since the list
		}
		r, err := FromUsage(c, cu)
		if err != nil {
			return nil, err
		}
		r.Members = ms
		out = append(out, r)
	}
	return out, nil
}

func procInfo(call, pid, flavor int, buf []byte) error {
	_, _, e := unix.Syscall6(unix.SYS_PROC_INFO, uintptr(call), uintptr(pid), uintptr(flavor),
		0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if e != 0 {
		return e
	}
	return nil
}

// coalitionOf returns a process's resource coalition id (type 0 of the pair).
func coalitionOf(pid int) (uint64, error) {
	buf := make([]byte, 5*8)
	if err := procInfo(procInfoCallPidinfo, pid, procPidCoalitionInfo, buf); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf), nil
}

func pidPath(pid int) string {
	buf := make([]byte, procPidPathInfoMaxSize)
	// The kernel returns 0 on success, not a length (libproc strlen()s it).
	if err := procInfo(procInfoCallPidinfo, pid, procPidPathInfo, buf); err != nil {
		return ""
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf)
}

func coalitionUsage(cid uint64) ([]uint64, error) {
	buf := make([]byte, 128*8)
	size := uintptr(len(buf))
	id := cid
	_, _, e := unix.Syscall6(unix.SYS_COALITION_INFO, coalitionInfoResourceUsage,
		uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if e != 0 {
		return nil, e
	}
	// The kernel copies min(its struct, our buffer) and leaves size as we
	// passed it, so the length says nothing about the layout.
	out := make([]uint64, min(size, uintptr(len(buf)))/8)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(buf[i*8:])
	}
	return out, nil
}
