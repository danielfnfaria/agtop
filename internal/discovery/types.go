package discovery

import "github.com/danielfnfaria/agtop/internal/config"

// Deps are the injectable collaborators of discovery.
type Deps struct {
	FS FS
	// IsPidAlive reports whether the process exists (even without permission to signal it).
	IsPidAlive func(pid int) bool
	// Now is the epoch in ms.
	Now func() int64
	// UptimeMs is the time since boot in ms. A registry whose startedAt precedes the boot is from
	// a process that is gone, even if the pid exists again.
	UptimeMs func() int64
	// LocateCache remembers where transcripts are; without it every call scans projects/*.
	LocateCache *Cache[LocateEntry]
	// InactiveFilesCache holds the files of non-live sessions (dead_pid/pre_boot), located once.
	// Key: locateCacheKey; entries that leave the registry are pruned.
	InactiveFilesCache *Cache[SessionFiles]
}

// NewDeps is Deps over the real system, with empty caches.
func NewDeps() Deps {
	return Deps{
		FS:                 OSFS{},
		IsPidAlive:         IsPidAlive,
		Now:                nowMs,
		UptimeMs:           SystemUptimeMs,
		LocateCache:        NewCache[LocateEntry](),
		InactiveFilesCache: NewCache[SessionFiles](),
	}
}

// LocateKind tells what a LocateEntry remembers.
type LocateKind string

const (
	// LocateFound: Project is the projects/ folder where the transcript is.
	LocateFound LocateKind = "found"
	// LocateMissing: no transcript at the last scan; no new scan until Until (epoch ms).
	// Project is where only the session folder was found, if it was.
	LocateMissing LocateKind = "missing"
)

// LocateEntry is a locate cache entry.
type LocateEntry struct {
	Kind    LocateKind
	Project string
	Until   int64
}

// Activity is the status of sessions/<pid>.json. Claude Code writes busy (turn running), shell
// (turn over, a background shell still running), waiting (blocked on the user) and idle.
// Anything else is unknown.
type Activity string

const (
	ActivityBusy    Activity = "busy"
	ActivityShell   Activity = "shell"
	ActivityWaiting Activity = "waiting"
	ActivityIdle    Activity = "idle"
	ActivityUnknown Activity = "unknown"
)

// DeadReason is why a record is not alive.
type DeadReason string

const (
	DeadPid     DeadReason = "dead_pid"
	DeadPreBoot DeadReason = "pre_boot"
)

// LiveSessionRecord is a valid sessions/<pid>.json. Displayable strings are sanitized; empty
// strings and zero times are "absent".
type LiveSessionRecord struct {
	Profile   config.ProfileDir
	PID       int
	SessionID string
	Cwd       string
	Kind      string
	Version   string
	Name      string
	Activity  Activity
	// RawStatus is the registry status as given (sanitized, short); only with ActivityUnknown.
	RawStatus string
	// UpdatedAt and StartedAt are epoch ms.
	UpdatedAt int64
	StartedAt int64
	// ProcessStartedAt is the start of the process at PID as the operating system reports it
	// (epoch ms), for providers without a registry record; 0 when unknown.
	ProcessStartedAt int64
	Alive            bool
	// DeadReason is set only when Alive is false.
	DeadReason DeadReason
}

// IssueReason is why a registry file was ignored.
type IssueReason string

const (
	IssueTooLarge         IssueReason = "too_large"
	IssueReadError        IssueReason = "read_error"
	IssueInvalidJSON      IssueReason = "invalid_json"
	IssueNotObject        IssueReason = "not_object"
	IssueMissingFields    IssueReason = "missing_fields"
	IssueInvalidPID       IssueReason = "invalid_pid"
	IssuePIDMismatch      IssueReason = "pid_mismatch"
	IssueInvalidSessionID IssueReason = "invalid_session_id"
	// IssueDatabase: a provider database (File) could not be read; the store error says why.
	IssueDatabase IssueReason = "database"
)

// RegistryIssue is a problem with a registry file. It never includes file content.
type RegistryIssue struct {
	// File is the name (already validated as <integer>.json).
	File   string
	Reason IssueReason
}

// Diagnostics describe what the registry scan skipped.
type Diagnostics struct {
	// SessionsDirMissing: sessions/ absent or unreadable (old Claude Code or unused profile).
	SessionsDirMissing bool
	// SkippedEntries are entries that are not <integer>.json (e.g. <pid>.<hash>.key).
	SkippedEntries int
	Issues         []RegistryIssue
}

// RegistryResult is the content of a profile registry.
type RegistryResult struct {
	Records     []LiveSessionRecord
	Diagnostics Diagnostics
}

// SessionFiles are validated paths (real path inside <claudeDir>/projects); "" is absent.
type SessionFiles struct {
	TranscriptPath string
	SessionDir     string
	SubagentsDir   string
	// CustomTitle is from custom-title.json, sanitized.
	CustomTitle string
}

// SubagentFiles are the files of one sub-agent.
type SubagentFiles struct {
	AgentID        string
	TranscriptPath string
	MetaPath       string
}

// DiscoveredSession is a registry record with its files.
type DiscoveredSession struct {
	LiveSessionRecord
	// Key is "<profile label>:<sessionId>".
	Key   string
	Files SessionFiles
}

// InactiveReason is why a session is not in Live.
type InactiveReason string

const (
	InactiveDeadPid InactiveReason = "dead_pid"
	InactivePreBoot InactiveReason = "pre_boot"
	// InactiveDuplicate: the pid is alive but the same sessionId is already live with a more
	// recent record.
	InactiveDuplicate InactiveReason = "duplicate"
)

// InactiveSession is a session outside Live, with the reason.
type InactiveSession struct {
	DiscoveredSession
	Reason InactiveReason
}

// ProfileDiscovery is the result for one profile.
type ProfileDiscovery struct {
	Profile config.ProfileDir
	// Live has one session per sessionId, with an alive pid.
	Live []DiscoveredSession
	// Dead is everything else; duplicates have Alive true.
	Dead        []InactiveSession
	Diagnostics Diagnostics
}

// Result is one scan of all profiles.
type Result struct {
	// ScannedAt is the epoch ms when the scan started.
	ScannedAt int64
	Profiles  []ProfileDiscovery
}
