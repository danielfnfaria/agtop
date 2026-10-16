package discovery

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// SystemUptimeMs is the time since the machine booted, in ms.
func SystemUptimeMs() int64 {
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if secs, err := strconv.ParseFloat(f[0], 64); err == nil {
				return int64(secs * 1000)
			}
		}
	}
	var si unix.Sysinfo_t
	if unix.Sysinfo(&si) != nil {
		return 0
	}
	return int64(si.Uptime) * 1000
}
