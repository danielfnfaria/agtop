package store

import (
	"path/filepath"

	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

// One watcher of the registry root per profile (any event triggers a scan) and one per live
// session over its transcript and subagents/ (events feed the session reads). The number of
// watched files follows the live sessions and their agents, not the history of projects/.

// forward hands the events and errors of w to the actor until w is closed.
func (s *Store) forward(w watcher.Watcher, onEvent func(watcher.Event), errPrefix string) {
	events, errs := w.Events(), w.Errors()
	go func() {
		defer s.recoverPanic()
		for events != nil || errs != nil {
			select {
			case ev, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if !s.post(func() {
					if !s.isStopped() {
						onEvent(ev)
					}
				}) {
					return
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if !s.post(func() { s.pushError(errPrefix + ": " + errorCode(err)) }) {
					return
				}
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *Store) closeSessionWatcher(e *sessionEntry) {
	w := e.watcher
	e.watcher = nil
	e.watchKey, e.watchSet = "", false
	if w != nil {
		s.release(w)
	}
}

// syncSessionWatcher opens (or replaces, when its paths changed) the watcher of a session over
// [transcriptPath, subagentsDir], or renews it when it lost a path. Without either there is no watcher: the periodic scan finds
// the file when it appears.
func (s *Store) syncSessionWatcher(e *sessionEntry) {
	if !s.opts.Watch || s.isStopped() || e.removed {
		return
	}
	key := e.transcriptPath + "\x00" + e.subagentsDir
	if e.watchSet && e.watchKey == key {
		s.rewatchIfShort(e)
		return
	}
	s.closeSessionWatcher(e)
	e.watchKey, e.watchSet = key, true
	if e.transcriptPath == "" && e.subagentsDir == "" {
		return
	}
	w, err := s.deps.NewWatcher(watcher.Target{
		Kind:           watcher.KindSession,
		TranscriptPath: e.transcriptPath,
		SubagentsDir:   e.subagentsDir,
	})
	if err != nil {
		// Not retried until the paths change; the scan keeps covering the session.
		s.pushError("watcher session " + e.key + ": " + errorCode(err))
		return
	}
	if !s.track(w) {
		return
	}
	e.watcher = w
	s.forward(w, func(ev watcher.Event) { s.onFileEvent(e, ev) }, "watcher session "+e.key)
}

func (s *Store) onFileEvent(owner *sessionEntry, ev watcher.Event) {
	if s.isStopped() || owner.removed {
		return
	}
	if ref, ok := s.fileIndex[ev.Path]; ok && ref.entry == owner {
		e := ref.entry
		switch ref.kind {
		case fileMain:
			s.runTask(e, func() error {
				_, err := s.readMain(e)
				return err
			})
		case fileAgent:
			s.runTask(e, func() error {
				if a := e.agents[ref.agentID]; a != nil {
					_, err := s.readAgent(e, a)
					return err
				}
				return nil
			})
		case fileMeta:
			if ev.Op != watcher.Unlink {
				s.runTask(e, func() error {
					if a := e.agents[ref.agentID]; a != nil {
						s.loadMeta(e, a, "", false)
					}
					return nil
				})
			}
		}
		return
	}
	// A new file in subagents/: list it again.
	if ev.Op != watcher.Unlink && s.subagentsIndex[filepath.Dir(ev.Path)] == owner {
		s.runTask(owner, func() error { return s.syncAgents(owner) })
	}
}

// openWatchers opens one watcher per profile over the source registry root.
func (s *Store) openWatchers() {
	for _, profile := range s.opts.Profiles {
		root, ok := s.source.RegistryRoot(profile)
		if s.isStopped() {
			return
		}
		if !ok {
			continue
		}
		w, err := s.deps.NewWatcher(watcher.Target{Kind: watcher.KindSessions, Root: root})
		if err != nil {
			s.pushError("watcher " + root + ": " + errorCode(err))
			continue
		}
		if !s.track(w) {
			return
		}
		s.globals = append(s.globals, w)
		s.forward(w, func(watcher.Event) { s.requestScan() }, "watcher "+root)
	}
}

// watchersReady waits for the ready of every open watcher (or the stop).
func (s *Store) watchersReady() {
	all := append([]watcher.Watcher{}, s.globals...)
	for _, e := range s.sessions {
		if e.watcher != nil {
			all = append(all, e.watcher)
		}
	}
	all = append(all, s.usage.watchers()...)
	for _, w := range all {
		select {
		case <-w.Ready():
		case <-s.stopCh:
			return
		}
	}
}
