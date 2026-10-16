package discovery

import (
	"time"

	"golang.org/x/sys/unix"
)

// SystemUptimeMs is the time since the machine booted, in ms.
func SystemUptimeMs() int64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0
	}
	boot := time.Unix(tv.Sec, int64(tv.Usec)*1000)
	return time.Since(boot).Milliseconds()
}
