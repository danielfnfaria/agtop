//go:build darwin

package proc

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// signal revalidates the process identity immediately before the kill: Darwin has no pidfd, so
// this is the closest to a race-free signal the platform allows. A reused pid fails the start
// revalidation and is never signaled.
func signal(pid int, sig syscall.Signal, want Info) error {
	return revalidateAndKill(pid, sig, want, Lookup, unix.Kill)
}
