package cmd

import (
	"time"

	"golang.org/x/sys/unix"
)

// lastWake reads kern.sleeptime and kern.waketime: when the Mac last went
// to sleep and when it last woke.
func lastWake() (slept, woke time.Time, ok bool) {
	s, err1 := unix.SysctlTimeval("kern.sleeptime")
	w, err2 := unix.SysctlTimeval("kern.waketime")
	if err1 != nil || err2 != nil || w.Sec == 0 {
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(s.Sec, int64(s.Usec)*1000), time.Unix(w.Sec, int64(w.Usec)*1000), true
}
