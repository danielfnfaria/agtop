// Package opencode is opencode (https://opencode.ai) as a source.Source: its sessions live in
// the sqlite database <dir>/opencode.db, which the source opens read-only (one lazy connection
// per profile; it never writes the database or its WAL and never checkpoints, though SQLite may
// create the shared-memory -shm file, as any WAL reader does) and queries for metadata only.
//
// The opencode TUI is one process showing one session, in its project directory, and there is
// no registry to say which: each live opencode process is mapped to the most recently touched
// root session (no parent, not archived) whose directory is the process cwd and whose last touch
// is not before the process start. Newest process first, each takes the newest session not
// claimed yet. A permission request is not persisted, so "waiting" is never shown.
//
// References handed to the store are opaque strings: "opencode:<sessionID>" is a transcript,
// "opencode:<sessionID>/agents" the agents of a session and "opencode:<sessionID>/meta" the
// metadata of an agent.
package opencode

import (
	"cmp"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/proc"
	"github.com/danielfnfaria/agtop/internal/source"
)

const (
	// ProcessName is the executable (and process name) of the opencode TUI.
	ProcessName = "opencode"
	// CodeProcesses: the processes could not be listed.
	CodeProcesses = "PROC_LIST"

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
	// ListProcesses lists the live opencode processes (default: proc.List over the system).
	ListProcesses func() ([]proc.Process, error)
}

// Source is the opencode source. It is safe for concurrent use and must be closed (Close)
// to release the connections.
type Source struct {
	now           func() int64
	listProcesses func() ([]proc.Process, error)
	models        *catalog

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
	list := opts.ListProcesses
	if list == nil {
		list = func() ([]proc.Process, error) { return proc.List(ProcessName) }
	}
	return &Source{
		now:           now,
		listProcesses: list,
		models:        newCatalog(ModelsPath(env, home)),
		dbs:           map[string]*profileDB{},
		refs:          map[string]refInfo{},
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

// Discover lists the live root sessions of each profile: those of the running opencode
// processes. Without a process nothing is live and the database is not queried. A profile whose
// database cannot be read gets an issue (file opencode.db) and no sessions; the first such
// failure is also returned with the (partial) result.
func (s *Source) Discover(profiles []config.ProfileDir) (discovery.Result, error) {
	now := s.now()
	result := discovery.Result{ScannedAt: now}
	procs, err := s.listProcesses()
	if err != nil {
		for _, profile := range profiles {
			result.Profiles = append(result.Profiles, discovery.ProfileDiscovery{Profile: profile})
		}
		return result, codedErr(CodeProcesses, err)
	}
	byCwd := processesByCwd(procs)
	var firstErr error
	for _, profile := range profiles {
		pd := discovery.ProfileDiscovery{Profile: profile}
		var roots []discovery.DiscoveredSession
		if len(byCwd) > 0 {
			roots, err = s.discoverProfile(profile, byCwd)
		}
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

// processesByCwd groups the processes by their resolved cwd, each group newest first; processes
// without a readable cwd are dropped.
func processesByCwd(procs []proc.Process) map[string][]proc.Process {
	out := map[string][]proc.Process{}
	for _, p := range procs {
		if p.Cwd == "" || p.PID <= 0 {
			continue
		}
		dir := resolveDir(p.Cwd)
		out[dir] = append(out[dir], p)
	}
	for dir, group := range out {
		slices.SortStableFunc(group, func(a, b proc.Process) int { return b.Start.Compare(a.Start) })
		out[dir] = group
	}
	return out
}

// resolveDir is the canonical form of a directory for the cwd comparison: cleaned, symlinks
// resolved when it exists.
func resolveDir(dir string) string {
	dir = filepath.Clean(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// candidate is a root session of a directory that has a process, with its activity.
type candidate struct {
	rootRow
	activityRow
}

// discoverProfile is the live sessions of a profile: one per running process, the most recently
// touched root session of the process cwd that was touched since the process started.
func (s *Source) discoverProfile(profile config.ProfileDir, byCwd map[string][]proc.Process) ([]discovery.DiscoveredSession, error) {
	db, err := s.dbFor(profile)
	if err != nil {
		return nil, err
	}
	conn, err := db.conn()
	if err != nil {
		return nil, err
	}
	roots, err := selectRoots(conn)
	if err != nil {
		return nil, err
	}
	resolved := map[string]string{}
	byDir := map[string][]candidate{}
	for _, r := range roots {
		if r.ID == "" || r.Directory == "" {
			continue
		}
		dir, ok := resolved[r.Directory]
		if !ok {
			dir = resolveDir(r.Directory)
			resolved[r.Directory] = dir
		}
		if len(byCwd[dir]) == 0 {
			continue
		}
		activity, err := selectActivity(conn, r)
		if err != nil {
			return nil, err
		}
		byDir[dir] = append(byDir[dir], candidate{r, activity})
	}
	var live []discovery.DiscoveredSession
	for _, dir := range slices.Sorted(maps.Keys(byDir)) {
		sessions := byDir[dir]
		slices.SortStableFunc(sessions, func(a, b candidate) int { return cmp.Compare(b.LastActivity, a.LastActivity) })
		// Processes newest first: each takes the newest session left that it may have touched.
		for _, p := range byCwd[dir] {
			var start int64
			if !p.Start.IsZero() {
				start = p.Start.UnixMilli()
			}
			i := slices.IndexFunc(sessions, func(c candidate) bool { return c.LastActivity >= start })
			if i < 0 {
				continue
			}
			c := sessions[i]
			sessions = slices.Delete(sessions, i, i+1)
			live = append(live, s.discovered(profile, db, c, p, start))
		}
	}
	return live, nil
}

// discovered is the session c as shown for the process p that owns it.
func (s *Source) discovered(profile config.ProfileDir, db *profileDB, c candidate, p proc.Process, start int64) discovery.DiscoveredSession {
	state := discovery.ActivityIdle
	if c.InProgress {
		state = discovery.ActivityBusy
	}
	id := domain.Sanitize(c.ID, idMax)
	s.remember(c.ID, db, "")
	return discovery.DiscoveredSession{
		LiveSessionRecord: discovery.LiveSessionRecord{
			Profile:          profile,
			PID:              p.PID,
			SessionID:        id,
			Cwd:              domain.Sanitize(c.Directory, pathMax),
			Version:          domain.Sanitize(c.Version, nameMax),
			Name:             domain.Sanitize(c.Title, textMax),
			Activity:         state,
			UpdatedAt:        c.LastActivity,
			StartedAt:        c.TimeCreated,
			ProcessStartedAt: start,
			Alive:            true,
		},
		Key:   discovery.SessionKey(profile, id),
		Files: discovery.SessionFiles{TranscriptPath: transcriptRef(c.ID), SubagentsDir: agentsRef(c.ID)},
	}
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
