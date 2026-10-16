// Package closer validates a session against its registry record (Claude Code) or its process
// identity (opencode: pid, start time and executable) and the live process, and only then sends
// SIGTERM to it. It never signals SIGKILL.
package closer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/proc"
)

// opencodeProcessName is the executable (and comm) of the opencode TUI.
const opencodeProcessName = "opencode"

// startToleranceMs is the most the real process start may differ from the registry startedAt.
const startToleranceMs = 10_000

// Card is what the caller knows about the session to close.
type Card struct {
	// ProfileDir is the absolute path of the profile directory (the one containing sessions/).
	ProfileDir string
	PID        int
	SessionID  string
	// StartedAt is the startedAt of sessions/<pid>.json (epoch ms); 0 is absent.
	StartedAt int64
	Kind      string
	// Provider is the provider of the session; empty means Claude Code.
	Provider domain.ProviderID
	// ProcessStart is the start of the process at PID as discovery saw it (epoch ms), for
	// opencode, which has no registry record; 0 is absent.
	ProcessStart int64
}

// Process is the identity of agtop itself: the processes Close refuses to signal.
type Process struct {
	PID  int
	PPID int
	UID  int
}

// Self is the identity of the running process.
func Self() Process {
	return Process{PID: os.Getpid(), PPID: os.Getppid(), UID: os.Getuid()}
}

// FS reads a registry file.
type FS interface {
	ReadFile(path string) (string, error)
}

// Proc looks up process info.
type Proc interface {
	Lookup(pid int) (proc.Info, error)
}

// Signaler sends a signal to the process at pid, but only after re-confirming that the pid still
// identifies the process described by want (see internal/proc). A returned error means nothing
// was signaled.
type Signaler interface {
	Signal(pid int, sig syscall.Signal, want proc.Info) error
}

// Deps are the injectable collaborators.
type Deps struct {
	FS     FS
	Proc   Proc
	Signal Signaler
	Self   Process
}

// OSFS reads from the operating system.
type OSFS struct{}

func (OSFS) ReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// OSProc looks processes up over the operating system.
type OSProc struct{}

func (OSProc) Lookup(pid int) (proc.Info, error) { return proc.Lookup(pid) }

// NewDeps are the collaborators over the real system.
func NewDeps() Deps {
	return Deps{FS: OSFS{}, Proc: OSProc{}, Signal: proc.OSSignal{}, Self: Self()}
}

// Closer closes a session after validating its record and process.
type Closer struct{ deps Deps }

// New builds a Closer.
func New(deps Deps) *Closer { return &Closer{deps: deps} }

// Errors Close returns, wrapped with the pid or path that caused them.
var (
	ErrInvalidPID    = errors.New("invalid pid")
	ErrSelfOrParent  = errors.New("refusing to signal agtop or its parent")
	ErrRecordGone    = errors.New("session record is gone")
	ErrRecordChanged = errors.New("session record changed")
	ErrNotAlive      = errors.New("process is not alive")
	ErrNotOwner      = errors.New("process is owned by another user")
	ErrNotClaude     = errors.New("process is not Claude Code")
	ErrNotOpencode   = errors.New("process is not opencode")
	ErrStartMismatch = errors.New("process start does not match the session")
	ErrProvider      = errors.New("sessions of this provider cannot be closed")
)

// Close validates the record and the process and, only if every rule holds, sends SIGTERM to
// the pid of the record. For a background session the record pid is the only one signaled: there
// is no process tree walk. An error means no signal was sent.
func (c *Closer) Close(card Card) error {
	if card.PID <= 1 {
		return fmt.Errorf("%w: pid %d", ErrInvalidPID, card.PID)
	}
	self := c.deps.Self
	if card.PID == self.PID || card.PID == self.PPID {
		return fmt.Errorf("%w: pid %d", ErrSelfOrParent, card.PID)
	}
	switch card.Provider {
	case "", domain.ClaudeCode:
	case domain.Opencode:
		return c.closeOpencode(card)
	default:
		return fmt.Errorf("%w: %s", ErrProvider, card.Provider)
	}

	rec, err := c.readRecord(card)
	if err != nil {
		return err
	}
	if rec.PID != card.PID || rec.SessionID != card.SessionID {
		return fmt.Errorf("%w: pid %d", ErrRecordChanged, card.PID)
	}
	// Fail closed: without a startedAt on the card or the record there is no way to detect a
	// reused pid.
	if card.StartedAt <= 0 {
		return fmt.Errorf("%w: pid %d (startedAt is absent)", ErrStartMismatch, card.PID)
	}
	if rec.StartedAt <= 0 {
		return fmt.Errorf("%w: pid %d (record startedAt is absent)", ErrStartMismatch, card.PID)
	}
	if rec.StartedAt != card.StartedAt {
		return fmt.Errorf("%w: pid %d", ErrRecordChanged, card.PID)
	}

	info, err := c.deps.Proc.Lookup(card.PID)
	if err != nil {
		if errors.Is(err, proc.ErrNotFound) {
			return fmt.Errorf("%w: pid %d", ErrNotAlive, card.PID)
		}
		return fmt.Errorf("inspect process %d: %w", card.PID, err)
	}
	if info.Owner != self.UID {
		return fmt.Errorf("%w: pid %d (uid %d, running as %d)", ErrNotOwner, card.PID, info.Owner, self.UID)
	}
	if !isClaude(info) {
		return fmt.Errorf("%w: pid %d runs %q", ErrNotClaude, card.PID, info.Name)
	}
	// Fail closed: an unknown process start cannot be matched to the record.
	started := info.StartMillis()
	if started <= 0 {
		return fmt.Errorf("%w: pid %d (process start is unknown)", ErrStartMismatch, card.PID)
	}
	if diff := started - card.StartedAt; abs64(diff) > startToleranceMs {
		return fmt.Errorf("%w: pid %d (started %dms from the record)", ErrStartMismatch, card.PID, diff)
	}

	if err := c.deps.Signal.Signal(card.PID, syscall.SIGTERM, info); err != nil {
		if errors.Is(err, proc.ErrNotFound) {
			return fmt.Errorf("%w: pid %d", ErrNotAlive, card.PID)
		}
		return fmt.Errorf("signal %d: %w", card.PID, err)
	}
	return nil
}

// closeOpencode closes an opencode session: there is no registry record, so the identity is the
// pid, the process start discovery saw and the executable. Fail closed on any unknown.
func (c *Closer) closeOpencode(card Card) error {
	if card.ProcessStart <= 0 {
		return fmt.Errorf("%w: pid %d (process start is absent)", ErrStartMismatch, card.PID)
	}
	info, err := c.deps.Proc.Lookup(card.PID)
	if err != nil {
		if errors.Is(err, proc.ErrNotFound) {
			return fmt.Errorf("%w: pid %d", ErrNotAlive, card.PID)
		}
		return fmt.Errorf("inspect process %d: %w", card.PID, err)
	}
	if info.Owner != c.deps.Self.UID {
		return fmt.Errorf("%w: pid %d (uid %d, running as %d)", ErrNotOwner, card.PID, info.Owner, c.deps.Self.UID)
	}
	if !isOpencode(info) {
		return fmt.Errorf("%w: pid %d runs %q", ErrNotOpencode, card.PID, info.Name)
	}
	started := info.StartMillis()
	if started <= 0 {
		return fmt.Errorf("%w: pid %d (process start is unknown)", ErrStartMismatch, card.PID)
	}
	if diff := started - card.ProcessStart; abs64(diff) > startToleranceMs {
		return fmt.Errorf("%w: pid %d (started %dms from the session)", ErrStartMismatch, card.PID, diff)
	}
	if err := c.deps.Signal.Signal(card.PID, syscall.SIGTERM, info); err != nil {
		if errors.Is(err, proc.ErrNotFound) {
			return fmt.Errorf("%w: pid %d", ErrNotAlive, card.PID)
		}
		return fmt.Errorf("signal %d: %w", card.PID, err)
	}
	return nil
}

// isOpencode reports whether the process is the opencode binary: its name, or the base name of
// its executable (argv[0] when the command line is known).
func isOpencode(info proc.Info) bool {
	if info.Name == opencodeProcessName {
		return true
	}
	return len(info.Args) > 0 && filepath.Base(info.Args[0]) == opencodeProcessName
}

// isClaude reports whether the process is Claude Code: the "claude" binary, or node/bun running
// the Claude Code entrypoint script. The node/bun check is structural — argv[1] (the script) must
// be cli.js inside a claude-code directory — so an incidental "claude-code" in another argument
// (a path, a flag value, a file name) is never accepted.
func isClaude(info proc.Info) bool {
	if info.Name == "claude" {
		return true
	}
	if info.Name != "node" && info.Name != "bun" {
		return false
	}
	if len(info.Args) < 2 {
		return false
	}
	script := info.Args[1]
	if filepath.Base(script) != "cli.js" {
		return false
	}
	return filepath.Base(filepath.Dir(script)) == "claude-code"
}

type record struct {
	PID       int
	SessionID string
	StartedAt int64
}

// readRecord re-reads sessions/<pid>.json and returns its pid, sessionId and startedAt.
func (c *Closer) readRecord(card Card) (record, error) {
	path := filepath.Join(card.ProfileDir, "sessions", strconv.Itoa(card.PID)+".json")
	text, err := c.deps.FS.ReadFile(path)
	if err != nil {
		return record{}, fmt.Errorf("%w: %v", ErrRecordGone, err)
	}
	var raw map[string]any
	if json.Unmarshal([]byte(text), &raw) != nil {
		return record{}, fmt.Errorf("%w: %s", ErrRecordChanged, path)
	}
	rec := record{
		PID:       intField(raw["pid"]),
		SessionID: stringField(raw["sessionId"]),
		StartedAt: epochMs(raw["startedAt"]),
	}
	if rec.SessionID == "" || rec.PID <= 0 {
		return record{}, fmt.Errorf("%w: %s", ErrRecordChanged, path)
	}
	return rec, nil
}

func stringField(v any) string {
	s, _ := v.(string)
	return s
}

func intField(v any) int {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) {
		return 0
	}
	return int(f)
}

func epochMs(v any) int64 {
	f, ok := v.(float64)
	if !ok || f <= 0 || f > (1<<53) {
		return 0
	}
	return int64(f)
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
