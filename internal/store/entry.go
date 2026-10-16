package store

import (
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/domain/reduce"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

type agentEntry struct {
	agentID        string
	transcriptPath string
	metaPath       string
	tailer         Tailer
	// metaLoaded: the meta.json was already read once.
	metaLoaded bool
	// metaSig is the "size:mtimeMs" of the meta.json at the last read: a scan re-reads it when it
	// changed (agents not final yet), without depending on a watcher event.
	metaSig string
}

type sessionEntry struct {
	key       string
	provider  domain.ProviderID
	profile   config.ProfileDir
	sessionID string
	// firstSeenAt is when the store first saw the session (order fallback without startedAt).
	firstSeenAt int64
	discovered  discovery.DiscoveredSession
	// discoveredVer changes with discovered (view cache key).
	discoveredVer uint64
	state         domain.SessionState
	// stateVer changes with every new state (view cache key).
	stateVer uint64
	// rebuilding counts the nested rebuilds in progress.
	rebuilding     int
	transcriptPath string
	subagentsDir   string
	// subagentNames caches name to validated path in subagents/ (see ListSubagents).
	subagentNames map[string]string
	main          Tailer
	agents        map[string]*agentEntry
	// agentOrder is the insertion order of agents.
	agentOrder   []string
	watcher      watcher.Watcher
	watchKey     string
	watchSet     bool
	liveness     Liveness
	staleSince   int64
	needsRebuild bool
	removed      bool
	view         *cachedView
}

type cachedView struct {
	stateVer      uint64
	discoveredVer uint64
	liveness      string
	learned       string
	v             *SessionView
}

type fileKind int

const (
	fileMain fileKind = iota
	fileAgent
	fileMeta
)

type fileRef struct {
	entry   *sessionEntry
	kind    fileKind
	agentID string
}

// retiredActivity is the activity of a removed session, kept until it leaves the window.
type retiredActivity struct {
	label     string
	histogram domain.ActivityHistogram
}

func newEntry(s discovery.DiscoveredSession, now int64, provider domain.ProviderID) *sessionEntry {
	return &sessionEntry{
		key:           s.Key,
		provider:      provider,
		profile:       s.Profile,
		sessionID:     s.SessionID,
		firstSeenAt:   now,
		discovered:    s,
		state:         reduce.NewSessionState(s.SessionID),
		subagentNames: map[string]string{},
		agents:        map[string]*agentEntry{},
		liveness:      Alive,
		needsRebuild:  true,
	}
}

func (e *sessionEntry) setState(s domain.SessionState) {
	e.state = s
	e.stateVer++
}

func (e *sessionEntry) addAgent(a *agentEntry) {
	e.agents[a.agentID] = a
	e.agentOrder = append(e.agentOrder, a.agentID)
}
