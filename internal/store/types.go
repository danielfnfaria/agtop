// Package store wires discovery, tailers, the transcript normalizer and the reducer into an
// immutable snapshot of the live sessions.
//
// A single goroutine (the actor) owns every piece of mutable state: periodic scans, watcher
// events and requests (start, refresh, stop) reach it as messages in one FIFO mailbox, so
// reductions never interleave. Each emission publishes a fresh Snapshot through an
// atomic.Pointer and a notify channel; snapshots and everything they reference are never
// mutated after being published.
package store

import (
	"io"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/source"
	"github.com/danielfnfaria/agtop/internal/source/claudecode"
)

const (
	// DefaultDiscoveryIntervalMs is the wait between the end of a scan and the next one.
	DefaultDiscoveryIntervalMs = 2_000
	// DefaultStaleMs is how long a session that left the live set stays visible.
	DefaultStaleMs = 30_000
	// DefaultEmitThrottleMs is the minimum interval between emissions.
	DefaultEmitThrottleMs = 100
	// DefaultMaxErrors is how many errors the snapshot keeps.
	DefaultMaxErrors = 50
)

// Liveness tells whether a session is still in the live set.
type Liveness string

const (
	Alive Liveness = "alive"
	// Stale: left the registry or the process died; goes away after StaleMs.
	Stale Liveness = "stale"
)

// ProfileStatus is the summary of a profile (no file content).
type ProfileStatus struct {
	Label    string
	Provider domain.ProviderID
	Dir      string
	Source   config.ProfileSource
	// LiveCount is the live sessions of the last scan.
	LiveCount          int
	SessionsDirMissing bool
	// Unsupported: no source reads the provider of the profile, so it is never scanned.
	Unsupported bool
	// SkippedEntries are the entries of sessions/ that are not <pid>.json.
	SkippedEntries int
	// Issues counts the invalid registry files by reason.
	Issues map[discovery.IssueReason]int
	// Usage is the plan usage of <dir>/agtop/usage.json; nil without a valid file.
	Usage *domain.UsageView
}

// AgentContext is the context of an agent: tokens in use, its window and the %.
type AgentContext struct {
	Used int
	domain.ContextUsage
}

// AgentView is an agent as the UI shows it.
type AgentView struct {
	// Type is subagent_type / agentType; "unknown" until seen.
	Type         string
	Description  string
	Model        string
	IsBackground bool
	Status       domain.AgentStatus
	// FailureReason is why it failed, when known.
	FailureReason string
	// StartedAt and FinishedAt are epoch ms; 0 is unknown.
	StartedAt  int64
	FinishedAt int64
	Totals     domain.TokenTotals
	// PendingTools are tool_use without a tool_result, oldest first.
	PendingTools []domain.PendingEntry
	// Activity is what it is doing now; nil unless active.
	Activity *domain.AgentActivity
	Context  AgentContext
}

// SessionView is a session ready to show: the reduced state plus the registry and discovery.
// Empty strings and zero times are "absent".
type SessionView struct {
	// Key is "<profileLabel>:<sessionId>".
	Key          string
	SessionID    string
	Provider     domain.ProviderID
	ProfileLabel string
	PID          int
	Kind         string
	Activity     discovery.Activity
	// RawStatus is the registry status as given, only with ActivityUnknown.
	RawStatus string
	Liveness  Liveness
	// HasTranscript reports whether a transcript file was located for the session.
	HasTranscript bool
	// StaleSince is the epoch ms when the session stopped being alive.
	StaleSince int64
	// StartedAt is the startedAt of sessions/<pid>.json (epoch ms).
	StartedAt int64
	// DisplayTitle is customTitle > aiTitle > registry name > last cwd segment > sessionId.
	DisplayTitle  string
	Cwd           string
	ProjectName   string
	GitBranch     string
	Version       string
	Model         *domain.ModelInfo
	Effort        string
	ContextUsed   int
	ContextWindow domain.ContextWindow
	// ContextPercent is ContextUsed over ContextWindow.Size, in %.
	ContextPercent float64
	Totals         domain.TokenTotals
	PendingTools   []domain.PendingEntry
	// LastActivityAt is the epoch ms of the most recent line with a timestamp.
	LastActivityAt int64
	// PRs are the PR links seen in the session and its agents, oldest first.
	PRs []domain.PullRequestRef
	// Agents by key: agentId when known, else the toolUseId of the launch.
	Agents map[string]*AgentView
	// ParseErrors are transcript lines that were not valid JSON objects.
	ParseErrors int
}

// Snapshot is an immutable view of the store.
type Snapshot struct {
	// Sessions in a stable order: by startedAt (else when the store first saw the session),
	// ties by key. Activity never moves a session.
	Sessions []*SessionView
	Profiles []ProfileStatus
	// ScannedAt is the epoch ms of the start of the last scan; 0 before the first.
	ScannedAt int64
	// Errors are the latest I/O errors (operation, path and code; never content), oldest first.
	Errors []string
	// ActivityByProfile has one entry per profile, in Profiles order (even when empty): present
	// sessions plus removed ones, pruned to the window that ends now.
	ActivityByProfile []domain.ProfileHistogram
}

// Tailer reads the lines appended to a file (see tailer.Tailer).
type Tailer = claudecode.Tailer

// Clock is the time source of the store: Now is epoch ms; AfterFunc runs f (on any goroutine)
// after ms and returns a function that cancels it.
type Clock interface {
	Now() int64
	AfterFunc(ms int64, f func()) (stop func())
}

// Deps are the injectable collaborators of the store.
type Deps struct {
	Discovery discovery.Deps
	// Sources are the providers the store reads; default: Claude Code over Discovery and NewTailer.
	Sources    []source.Source
	NewTailer  func(path string) (Tailer, error)
	NewWatcher watcher.Factory
	Clock      Clock
	// Log receives the diagnostics of the store (never transcript content); default io.Discard.
	Log io.Writer
}

// Options configure a store; DefaultOptions has the defaults.
type Options struct {
	Profiles []config.ProfileDir
	// DiscoveryIntervalMs is the wait between the end of a scan and the next one.
	DiscoveryIntervalMs int64
	// StaleMs is how long a session that left the live set stays visible.
	StaleMs int64
	// EmitThrottleMs is the minimum interval between emissions.
	EmitThrottleMs int64
	MaxErrors      int
	// Watch opens the watchers of sessions/, agtop/ and the live sessions; without it only the
	// periodic scan sees changes.
	Watch bool
}

// DefaultOptions are the options of a store over profiles.
func DefaultOptions(profiles []config.ProfileDir) Options {
	return Options{
		Profiles:            profiles,
		DiscoveryIntervalMs: DefaultDiscoveryIntervalMs,
		StaleMs:             DefaultStaleMs,
		EmitThrottleMs:      DefaultEmitThrottleMs,
		MaxErrors:           DefaultMaxErrors,
		Watch:               true,
	}
}
