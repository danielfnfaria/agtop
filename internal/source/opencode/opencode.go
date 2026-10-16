// Package opencode is opencode (https://opencode.ai) as a source.Source: its sessions live in
// the sqlite database <dir>/opencode.db, which the source opens read-only (one lazy connection
// per profile; it never writes the database or its WAL and never checkpoints, though SQLite may
// create the shared-memory -shm file, as any WAL reader does) and queries for metadata only.
//
// A root session (no parent, not archived) is live while it had activity in the last
// IdleTimeoutMs, or while an assistant message is still open or a tool still runs. Child
// sessions (parent_id set) are the agents of their parent. There is no process registry:
// sessions have no pid, and a permission request is not persisted, so "waiting" is never shown.
//
// References handed to the store are opaque strings: "opencode:<sessionID>" is a transcript,
// "opencode:<sessionID>/agents" the agents of a session and "opencode:<sessionID>/meta" the
// metadata of an agent.
package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/source"
)

const (
	// IdleTimeoutMs: a session without activity for this long is no longer live.
	IdleTimeoutMs = 10 * 60_000
	// stuckWindowMs: an open assistant message or a running tool older than this (opencode
	// crashed or was killed mid-turn) no longer keeps the session live.
	stuckWindowMs = 2 * 60 * 60_000
	// candidateWindowMs: only sessions updated within it are examined (the liveness query is
	// per session).
	candidateWindowMs = 24 * 60 * 60_000

	refPrefix    = "opencode:"
	agentsSuffix = "/agents"
	metaSuffix   = "/meta"
	// WALName is the write-ahead log next to the database, watched with it.
	WALName = config.OpencodeDBName + "-wal"

	idMax   = 100
	nameMax = 80
	pathMax = 300
	textMax = 200
)

// Options are the collaborators of the source; zero values use the real system.
type Options struct {
	// Env and Homedir locate the model catalog (ModelsPath).
	Env     config.Env
	Homedir string
	// Now is the clock in epoch ms.
	Now func() int64
}

// Source is the opencode source. It is safe for concurrent use and must be closed (Close)
// to release the connections.
type Source struct {
	now    func() int64
	models *catalog

	mu     sync.Mutex
	dbs    map[string]*profileDB // by profile directory
	refs   map[string]refInfo    // session id to its database (and parent, for children)
	closed bool
}

type refInfo struct {
	db     *profileDB
	parent string
}

// New creates the source.
func New(opts Options) *Source {
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	env, home := opts.Env, opts.Homedir
	if env == nil {
		env = config.EnvFromOS()
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &Source{
		now:    now,
		models: newCatalog(ModelsPath(env, home)),
		dbs:    map[string]*profileDB{},
		refs:   map[string]refInfo{},
	}
}

var _ source.Source = (*Source)(nil)

func (*Source) Provider() domain.ProviderID { return domain.Opencode }

// Close closes every connection; the source is unusable afterwards.
func (s *Source) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, db := range s.dbs {
		db.close()
	}
	s.dbs = map[string]*profileDB{}
	s.refs = map[string]refInfo{}
	return nil
}

func (s *Source) dbFor(profile config.ProfileDir) (*profileDB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, codedErr(CodeClosed, nil)
	}
	db := s.dbs[profile.Dir]
	if db == nil {
		db = newProfileDB(profile)
		s.dbs[profile.Dir] = db
	}
	return db, nil
}

func (s *Source) remember(sessionID string, db *profileDB, parent string) {
	s.mu.Lock()
	if !s.closed {
		s.refs[sessionID] = refInfo{db, parent}
	}
	s.mu.Unlock()
}

// dbOf is the database a session id was seen in.
func (s *Source) dbOf(sessionID string) (*profileDB, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.refs[sessionID]
	return r.db, ok
}

// Discover lists the live root sessions of each profile. A profile whose database cannot be
// read gets an issue (file opencode.db) and no sessions; the first such failure is also
// returned with the (partial) result.
func (s *Source) Discover(profiles []config.ProfileDir) (discovery.Result, error) {
	now := s.now()
	result := discovery.Result{ScannedAt: now}
	var firstErr error
	for _, profile := range profiles {
		pd := discovery.ProfileDiscovery{Profile: profile}
		roots, err := s.discoverProfile(profile, now)
		if err != nil {
			pd.Diagnostics.Issues = []discovery.RegistryIssue{{File: config.OpencodeDBName, Reason: discovery.IssueDatabase}}
			if firstErr == nil {
				firstErr = err
			}
		}
		pd.Live = roots
		result.Profiles = append(result.Profiles, pd)
	}
	return result, firstErr
}

func (s *Source) discoverProfile(profile config.ProfileDir, now int64) ([]discovery.DiscoveredSession, error) {
	db, err := s.dbFor(profile)
	if err != nil {
		return nil, err
	}
	conn, err := db.conn()
	if err != nil {
		return nil, err
	}
	roots, err := selectRoots(conn, now-candidateWindowMs, now-stuckWindowMs)
	if err != nil {
		return nil, err
	}
	var live []discovery.DiscoveredSession
	for _, r := range roots {
		if r.ID == "" || !r.InProgress && r.LastActivity < now-IdleTimeoutMs {
			continue
		}
		activity := discovery.ActivityIdle
		if r.InProgress {
			activity = discovery.ActivityBusy
		}
		id := domain.Sanitize(r.ID, idMax)
		s.remember(r.ID, db, "")
		live = append(live, discovery.DiscoveredSession{
			LiveSessionRecord: discovery.LiveSessionRecord{
				Profile:   profile,
				SessionID: id,
				Cwd:       domain.Sanitize(r.Directory, pathMax),
				Version:   domain.Sanitize(r.Version, nameMax),
				Name:      domain.Sanitize(r.Title, textMax),
				Activity:  activity,
				UpdatedAt: r.LastActivity,
				StartedAt: r.TimeCreated,
				Alive:     true,
			},
			Key:   discovery.SessionKey(profile, id),
			Files: discovery.SessionFiles{TranscriptPath: transcriptRef(r.ID), SubagentsDir: agentsRef(r.ID)},
		})
	}
	return live, nil
}

func transcriptRef(sessionID string) string { return refPrefix + sessionID }
func agentsRef(sessionID string) string     { return refPrefix + sessionID + agentsSuffix }
func metaRef(sessionID string) string       { return refPrefix + sessionID + metaSuffix }

// parseRef splits a reference into its session id and suffix ("" for a transcript).
func parseRef(ref string) (sessionID, suffix string, ok bool) {
	rest, ok := strings.CutPrefix(ref, refPrefix)
	if !ok || rest == "" {
		return "", "", false
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		if i == 0 {
			return "", "", false
		}
		return rest[:i], rest[i:], true
	}
	return rest, "", true
}

// RegistryWatch watches the database and its write-ahead log in the profile directory.
func (s *Source) RegistryWatch(profile config.ProfileDir) (watcher.Target, bool) {
	real, err := filepath.EvalSymlinks(profile.Dir)
	if err != nil {
		return watcher.Target{}, false
	}
	return watcher.FilesTarget(real, config.OpencodeDBName, WALName), true
}

// SessionWatch: the database watch of the profile already covers every session.
func (*Source) SessionWatch(string, string) (watcher.Target, bool) { return watcher.Target{}, false }

// ForgetSession drops a session and its children from the reference map.
func (s *Source) ForgetSession(_ config.ProfileDir, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refs, sessionID)
	for id, r := range s.refs {
		if r.parent == sessionID {
			delete(s.refs, id)
		}
	}
}

// OpenTranscript opens the reader of "opencode:<sessionID>".
func (s *Source) OpenTranscript(ref string) (source.TranscriptReader, error) {
	id, suffix, ok := parseRef(ref)
	if !ok || suffix != "" {
		return nil, codedErr(CodeRef, nil)
	}
	db, ok := s.dbOf(id)
	if !ok {
		return nil, codedErr(CodeRef, nil)
	}
	return newReader(s, db, id), nil
}

// ListAgents lists the child sessions of "opencode:<sessionID>/agents".
func (s *Source) ListAgents(agentsDir string, _ map[string]string) []discovery.SubagentFiles {
	id, suffix, ok := parseRef(agentsDir)
	if !ok || suffix != agentsSuffix {
		return nil
	}
	db, ok := s.dbOf(id)
	if !ok {
		return nil
	}
	conn, err := db.conn()
	if err != nil {
		return nil
	}
	children, err := selectChildren(conn, id)
	if err != nil {
		return nil
	}
	out := make([]discovery.SubagentFiles, 0, len(children))
	for _, c := range children {
		if c.ID == "" {
			continue
		}
		s.remember(c.ID, db, id)
		out = append(out, discovery.SubagentFiles{
			AgentID:        domain.Sanitize(c.ID, idMax),
			TranscriptPath: transcriptRef(c.ID),
			MetaPath:       metaRef(c.ID),
		})
	}
	return out
}

// ReadAgentMeta reads "opencode:<sessionID>/meta": the agent type, the description (the title
// without its " (@type subagent)" suffix) and the model of a child session.
func (s *Source) ReadAgentMeta(file string) discovery.AgentMetaResult {
	row, err := s.sessionOf(file, metaSuffix)
	if err != nil {
		return discovery.AgentMetaResult{Code: codeOf(err)}
	}
	return discovery.AgentMetaResult{OK: true, Meta: domain.AgentMeta{
		AgentType:   domain.Sanitize(row.Agent, nameMax),
		Description: domain.Sanitize(agentDescription(row.Title, row.Agent), textMax),
		Model:       domain.Sanitize(row.ModelID, nameMax),
	}}
}

// FileSignature of "opencode:<sessionID>/meta" is the time_updated of the session.
func (s *Source) FileSignature(file string) (string, bool) {
	row, err := s.sessionOf(file, metaSuffix)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(row.TimeUpdated, 10), true
}

// sessionOf reads the session row of a reference with the given suffix.
func (s *Source) sessionOf(ref, suffix string) (sessionRow, error) {
	id, got, ok := parseRef(ref)
	if !ok || got != suffix {
		return sessionRow{}, codedErr(CodeRef, nil)
	}
	db, ok := s.dbOf(id)
	if !ok {
		return sessionRow{}, codedErr(CodeRef, nil)
	}
	conn, err := db.conn()
	if err != nil {
		return sessionRow{}, err
	}
	row, found, err := selectSession(conn, id)
	if err != nil {
		return sessionRow{}, err
	}
	if !found {
		return sessionRow{}, codedErr(CodeRef, errors.New("session gone"))
	}
	return row, nil
}

// agentDescription strips the " (@<agent> subagent)" suffix opencode adds to child titles.
func agentDescription(title, agent string) string {
	if agent != "" {
		title = strings.TrimSuffix(title, " (@"+agent+" subagent)")
	}
	return title
}

func codeOf(err error) string {
	if code := ErrorCode(err); code != "" {
		return code
	}
	return "UNKNOWN"
}
