package store

import (
	"errors"
	"io/fs"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/domain/reduce"
	"github.com/danielfnfaria/agtop/internal/source"
)

// Reading of a session: tailers of the main transcript and of the agents, meta.json, the file
// indexes and the full rebuild.

// runTask runs a session operation unless the store stopped or the session left; a failure
// becomes an error.
func (s *Store) runTask(e *sessionEntry, fn func() error) {
	if s.isStopped() || e.removed {
		return
	}
	if err := fn(); err != nil {
		s.pushError("session " + e.key + ": " + errorCode(err))
	}
}

func (s *Store) unindexFile(p string, e *sessionEntry) {
	if p != "" && s.fileIndex[p].entry == e {
		delete(s.fileIndex, p)
	}
}

func (s *Store) setSubagentsDir(e *sessionEntry, dir string) {
	if e.subagentsDir == dir {
		return
	}
	if e.subagentsDir != "" && s.subagentsIndex[e.subagentsDir] == e {
		delete(s.subagentsIndex, e.subagentsDir)
	}
	e.subagentsDir = dir
	e.subagentNames = map[string]string{}
	if dir != "" {
		s.subagentsIndex[dir] = e
	}
}

// openTranscript is a new reader of the transcript, or nil once the store stopped or the session
// left.
func (s *Store) openTranscript(e *sessionEntry, ref string) (source.TranscriptReader, error) {
	if s.isStopped() || e.removed {
		return nil, nil
	}
	src := s.sourceOf(e.provider)
	if src == nil {
		return nil, nil
	}
	t, err := src.OpenTranscript(ref)
	if err != nil {
		return nil, err
	}
	if !s.track(t) {
		return nil, nil
	}
	return t, nil
}

// closeTailers closes every tailer of the session and drops its files from the index.
func (s *Store) closeTailers(e *sessionEntry) {
	if e.main != nil {
		s.release(e.main)
	}
	s.unindexFile(e.transcriptPath, e)
	e.main = nil
	for _, a := range e.agents {
		if a.tailer != nil {
			s.release(a.tailer)
		}
		s.unindexFile(a.transcriptPath, e)
		s.unindexFile(a.metaPath, e)
		a.tailer = nil
	}
}

// rebuild redoes the session from scratch: new state, new tailers, main -> metas -> agents.
func (s *Store) rebuild(e *sessionEntry) error {
	e.rebuilding++
	completed := false
	defer func() {
		e.rebuilding--
		// A session that came back: the state read again replaces the histogram kept at the
		// removal, but only once the rebuild finished and did read a transcript; otherwise the
		// kept histogram is the only count of the session.
		if e.rebuilding == 0 && completed && e.transcriptPath != "" && !e.removed {
			delete(s.retired, e.key)
		}
	}()
	e.needsRebuild = false
	s.closeTailers(e)
	e.setState(reduce.NewSessionState(e.sessionID))
	e.agents = map[string]*agentEntry{}
	e.agentOrder = nil
	e.subagentNames = map[string]string{}
	files := e.discovered.Files
	e.transcriptPath = files.TranscriptPath
	s.setSubagentsDir(e, files.SubagentsDir)
	s.syncSessionWatcher(e)
	if e.transcriptPath != "" {
		t, err := s.openTranscript(e, e.transcriptPath)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		e.main = t
		s.fileIndex[e.transcriptPath] = fileRef{entry: e, kind: fileMain}
		if _, err := s.readMain(e); err != nil {
			return err
		}
	}
	if err := s.syncAgents(e); err != nil {
		return err
	}
	completed = true
	s.changed()
	return nil
}

// afterReset handles a tailer that saw its file truncated or replaced: the watch is renewed (a
// replaced file drops it) and the whole session is rebuilt.
func (s *Store) afterReset(e *sessionEntry) error {
	s.rewatch(e)
	return s.rebuild(e)
}

// rewatch renews the watch of the session. A file that is gone is not an error: the scans retry
// (rewatchIfShort) until it is back.
func (s *Store) rewatch(e *sessionEntry) {
	if e.watcher == nil {
		return
	}
	if err := e.watcher.Rewatch(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.pushError("watcher session " + e.key + ": " + errorCode(err))
	}
}

// rewatchIfShort renews the watch when it lost a path (an unlinked transcript drops its watch).
func (s *Store) rewatchIfShort(e *sessionEntry) {
	if e.watcher == nil {
		return
	}
	want := 0
	if e.transcriptPath != "" {
		want++
	}
	if e.subagentsDir != "" {
		want++
	}
	if e.watcher.WatchedPathCount() < want {
		s.rewatch(e)
	}
}

// readMain reads what the main transcript grew; false if the session was rebuilt meanwhile
// (the caller must stop).
func (s *Store) readMain(e *sessionEntry) (bool, error) {
	t, file := e.main, e.transcriptPath
	if t == nil || file == "" {
		return true, nil
	}
	res, err := t.Read()
	if err != nil {
		s.pushError("read " + file + ": " + errorCode(err))
		return true, nil
	}
	if e.main != t || s.isStopped() {
		return true, nil
	}
	if res.Reset {
		return false, s.afterReset(e)
	}
	for _, w := range res.Warnings {
		s.pushError(w)
	}
	if len(res.Events) == 0 {
		return true, nil
	}
	e.setState(reduce.ReduceSessionEvents(e.state, res.Events))
	s.changed()
	return true, nil
}

// readAgent reads what an agent transcript grew; false if the session was rebuilt meanwhile.
func (s *Store) readAgent(e *sessionEntry, a *agentEntry) (bool, error) {
	t, file := a.tailer, a.transcriptPath
	if t == nil || file == "" {
		return true, nil
	}
	res, err := t.Read()
	if err != nil {
		s.pushError("read " + file + ": " + errorCode(err))
		return true, nil
	}
	if a.tailer != t || e.agents[a.agentID] != a || s.isStopped() {
		return true, nil
	}
	if res.Reset {
		return false, s.afterReset(e)
	}
	for _, w := range res.Warnings {
		s.pushError(w)
	}
	if len(res.Events) == 0 {
		return true, nil
	}
	e.setState(reduce.ReduceSessionAgentEvents(e.state, a.agentID, res.Events))
	s.changed()
	return true, nil
}

// loadMeta reads the meta.json of an agent; sig is its signature when the caller has it.
func (s *Store) loadMeta(e *sessionEntry, a *agentEntry, sig string, hasSig bool) {
	file := a.metaPath
	if file == "" {
		return
	}
	a.metaLoaded = true
	if !hasSig {
		sig, _ = s.sourceOf(e.provider).FileSignature(file)
	}
	a.metaSig = sig
	res := s.sourceOf(e.provider).ReadAgentMeta(file)
	if !res.OK {
		s.pushError("meta " + file + ": " + res.Code)
		return
	}
	if s.isStopped() || e.agents[a.agentID] != a {
		return
	}
	e.setState(reduce.ApplyAgentMeta(e.state, a.agentID, res.Meta))
	s.changed()
}

func agentIsFinal(e *sessionEntry, a *agentEntry) bool {
	st, ok := e.state.Agents[a.agentID]
	return ok && domain.IsFinal(st.Status)
}

// syncAgents lists subagents/, opens tailers and applies new metas, then reads every agent. The
// meta.json of agents not final yet is re-read when its size:mtimeMs changed (the scan does not
// depend on a watcher event); final agents only go through their tailer, which without growth
// costs a stat.
func (s *Store) syncAgents(e *sessionEntry) error {
	dir := e.subagentsDir
	if dir == "" || s.isStopped() {
		return nil
	}
	files := s.sourceOf(e.provider).ListAgents(dir, e.subagentNames)
	if s.isStopped() || e.removed || e.subagentsDir != dir {
		return nil
	}
	for _, f := range files {
		if s.isStopped() || e.removed {
			return nil
		}
		a := e.agents[f.AgentID]
		if a == nil {
			a = &agentEntry{agentID: f.AgentID}
			e.addAgent(a)
		}
		if f.TranscriptPath != "" && a.transcriptPath != f.TranscriptPath {
			if a.tailer != nil {
				// The agent transcript moved: what was reduced no longer holds.
				return s.rebuild(e)
			}
			t, err := s.openTranscript(e, f.TranscriptPath)
			if err != nil {
				return err
			}
			if t == nil {
				return nil
			}
			a.transcriptPath = f.TranscriptPath
			a.tailer = t
			s.fileIndex[f.TranscriptPath] = fileRef{entry: e, kind: fileAgent, agentID: f.AgentID}
		}
		if f.MetaPath == "" {
			continue
		}
		if a.metaPath != f.MetaPath || !a.metaLoaded {
			s.unindexFile(a.metaPath, e)
			a.metaPath = f.MetaPath
			s.fileIndex[f.MetaPath] = fileRef{entry: e, kind: fileMeta, agentID: f.AgentID}
			s.loadMeta(e, a, "", false)
		} else if !agentIsFinal(e, a) {
			if sig, ok := s.sourceOf(e.provider).FileSignature(f.MetaPath); ok && sig != a.metaSig && e.agents[f.AgentID] == a {
				s.loadMeta(e, a, sig, true)
			}
		}
	}
	for _, id := range append([]string(nil), e.agentOrder...) {
		if s.isStopped() || e.removed {
			return nil
		}
		a := e.agents[id]
		if a == nil {
			continue
		}
		ok, err := s.readAgent(e, a)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}

// syncSession is the scan task of a live session: rebuild if needed, else read what grew and
// look for new agents.
func (s *Store) syncSession(e *sessionEntry) error {
	files := e.discovered.Files
	if e.needsRebuild || files.TranscriptPath != e.transcriptPath {
		return s.rebuild(e)
	}
	s.setSubagentsDir(e, files.SubagentsDir)
	s.syncSessionWatcher(e)
	ok, err := s.readMain(e)
	if err != nil || !ok {
		return err
	}
	return s.syncAgents(e)
}
