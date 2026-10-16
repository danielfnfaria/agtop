package discovery

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// maxPID is the largest accepted pid: it fits a 32-bit pid_t.
const maxPID = 0x7fffffff

// IsValidPID reports whether pid is a positive pid_t.
func IsValidPID(pid int) bool { return pid > 0 && pid <= maxPID }

// IsPidAlive reports whether the process exists, even if it belongs to another user.
func IsPidAlive(pid int) bool { return pidAlive(unix.Kill, pid) }

// pidAlive signals 0: ESRCH means gone, EPERM means alive but not ours. An invalid pid never
// reaches kill (0 and negatives would signal process groups).
func pidAlive(kill func(pid int, sig syscall.Signal) error, pid int) bool {
	if !IsValidPID(pid) {
		return false
	}
	err := kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
