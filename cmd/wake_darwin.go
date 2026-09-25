package cmd

import (
	"time"

	"golang.org/x/sys/unix"
)

// lastWake reads kern.waketime, the moment the Mac last woke from sleep.
func lastWake() (time.Time, bool) {
	tv, err := unix.SysctlTimeval("kern.waketime")
	if err != nil || tv.Sec == 0 {
		return time.Time{}, false
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), true
}
