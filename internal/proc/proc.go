// Package proc reads the identity of a process from the operating system (Darwin and Linux):
// its name, command line, owner and real start time. The closer uses it to make sure a pid still
// belongs to the session it is about to terminate, and to signal it without a reused pid.
package proc

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"time"
)

// ErrNotFound is returned by Lookup when the process does not exist.
var ErrNotFound = errors.New("process not found")

// ErrIdentityChanged is returned by Signal when the pid no longer identifies the process that
// was validated (it exited and the pid was reused, or the process was replaced).
var ErrIdentityChanged = errors.New("process identity changed")

// Info describes a process at a moment.
type Info struct {
	PID  int
	PPID int
	// Name is the process name (comm), usually the base name of the executable.
	Name string
	// Args is the full command line (argv); empty when unavailable.
	Args []string
	// Owner is the real user id of the process.
	Owner int
	// Start is the process start time in UTC without a zone. It is a reserve (secondary): the
	// closer prefers the registry startedAt and only uses Start to reject a reused pid. A zero
	// Start means unknown.
	Start time.Time
}

// StartMillis is Start as epoch ms (0 when unknown).
func (i Info) StartMillis() int64 {
	if i.Start.IsZero() {
		return 0
	}
	return i.Start.UnixMilli()
}

// SameStart reports whether a and b read the same process start: two reads of one process agree,
// a reused pid does not. Unknown starts (zero) never match.
func SameStart(a, b Info) bool {
	return !a.Start.IsZero() && a.Start.Equal(b.Start)
}

// Signaler sends a signal to the process at pid, but only after re-confirming that the pid still
// identifies the process described by want. It is the closer's last line of defence against a
// reused pid (TOCTOU): on Linux it pins the process with a pidfd; on Darwin it re-reads the
// identity immediately before the kill.
type Signaler interface {
	Signal(pid int, sig syscall.Signal, want Info) error
}

// OSSignal is the Signaler over the real system.
type OSSignal struct{}

// Signal implements Signaler.
func (OSSignal) Signal(pid int, sig syscall.Signal, want Info) error {
	return signal(pid, sig, want)
}

// revalidateAndKill re-reads the process identity and, only if it still matches want, signals pid
// through kill. It is the Darwin path and the Linux fallback when pidfd is unavailable.
func revalidateAndKill(pid int, sig syscall.Signal, want Info,
	lookup func(int) (Info, error), kill func(int, syscall.Signal) error) error {
	now, err := lookup(pid)
	if err != nil {
		return err
	}
	if !SameStart(want, now) {
		return ErrIdentityChanged
	}
	return notFoundIfGone(kill(pid, sig))
}

// notFoundIfGone turns ESRCH (the process exited meanwhile) into ErrNotFound, keeping the cause.
func notFoundIfGone(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}

// MatchesExecutable reports whether a process is the program name: its name (comm) is name, or
// the base name of its executable path (exe, when known) is. Discovery and the closer share it.
func MatchesExecutable(name, comm, exe string) bool {
	return comm == name || (exe != "" && filepath.Base(exe) == name)
}

// Process is one running process as List reports it.
type Process struct {
	PID int
	// Name is the process name (comm); Exe the executable path when readable.
	Name string
	Exe  string
	// Cwd is the current working directory; empty when unreadable (another user's process).
	Cwd string
	// Start is the process start time in UTC; zero when unknown.
	Start time.Time
}

// List enumerates the live processes whose name (comm) or executable base name is name, with
// their cwd and start time. Processes that cannot be inspected are skipped, not reported.
func List(name string) ([]Process, error) {
	return list(name)
}
