//go:build !darwin && !linux

package proc

import "syscall"

// signal is not supported where Lookup is not (see proc_other.go); the closer fails earlier.
func signal(pid int, sig syscall.Signal, want Info) error {
	return errUnsupported
}
