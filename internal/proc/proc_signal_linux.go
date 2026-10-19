//go:build linux

package proc

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// signal pins the process with a pidfd and only then signals it, closing the window between the
// identity check and the kill. The pidfd refers to the exact process that held pid at open time:
// if it exits and the pid is reused, the revalidation below rejects the pid and nothing is sent.
func signal(pid int, sig syscall.Signal, want Info) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			// Kernel without pidfd: fall back to revalidating immediately before the (racy) kill.
			return revalidateAndKill(pid, sig, want, Lookup, unix.Kill)
		}
		if errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return err
	}
	defer unix.Close(fd)
	// The pidfd pins the process; confirm the pid still names that process, so a pid reused
	// between the first Lookup and the open is never signaled.
	now, err := Lookup(pid)
	if err != nil {
		return err
	}
	if !SameStart(want, now) {
		return ErrIdentityChanged
	}
	return notFoundIfGone(unix.PidfdSendSignal(fd, sig, nil, 0))
}
