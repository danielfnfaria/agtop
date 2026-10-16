package store

import (
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/source"
	"github.com/danielfnfaria/agtop/internal/source/claudecode"
)

// DefaultDeps are the collaborators over the real system.
func DefaultDeps() Deps {
	return Deps{
		Discovery:  discovery.NewDeps(),
		NewTailer:  func(p string) (Tailer, error) { return tailer.New(p) },
		NewWatcher: watcher.New,
		Clock:      realClock{},
	}
}

// Store keeps the live sessions of the profiles up to date. Its methods are safe for
// concurrent use.
type Store struct {
	opts    Options
	deps    Deps
	sources []source.Source

	inbox   chan func()
	stopCh  chan struct{}
	done    chan struct{}
	updates chan struct{}
	snap    atomic.Pointer[Snapshot]
	stopped atomic.Bool
	started atomic.Bool
	// emits counts the emissions (tests).
	emits atomic.Int64

	startOnce sync.Once
	stopOnce  sync.Once
	// failure is the panic that stopped the store, if any.
	failure atomic.Pointer[PanicError]

	// mu guards closers: everything open (tailers, watchers) that Stop must close.
	mu      sync.Mutex
	closers map[io.Closer]struct{}

	// Owned by the actor goroutine.
	sessions       map[string]*sessionEntry
	fileIndex      map[string]fileRef
	subagentsIndex map[string]*sessionEntry
	retired        map[string]retiredActivity
	globals        []watcher.Watcher
	learner        *contextLearner
	usage          *usageTracker
	lastDiscovery  *discovery.Result
	profilesSig    []ProfileStatus
	scanTok        *struct{}
	scanStop       func()
	scanQueued     bool
	errors         []string
	dirty          bool
	lastEmitAt     int64
	emitted        bool
	emitTok        *struct{}
	emitStop       func()
}

// New creates a store; nothing runs until Start.
func New(opts Options, deps Deps) *Store {
	d := DefaultDeps()
	if deps.Discovery.FS != nil {
		d.Discovery = deps.Discovery
	}
	if deps.NewTailer != nil {
		d.NewTailer = deps.NewTailer
	}
	if deps.NewWatcher != nil {
		d.NewWatcher = deps.NewWatcher
	}
	if deps.Clock != nil {
		d.Clock = deps.Clock
	}
	d.Log = deps.Log
	if d.Log == nil {
		d.Log = io.Discard
	}
	d.Sources = deps.Sources
	if len(d.Sources) == 0 {
		d.Sources = []source.Source{claudecode.New(d.Discovery, d.NewTailer)}
	}
	s := &Store{
		opts:           opts,
		deps:           d,
		sources:        d.Sources,
		inbox:          make(chan func(), 1024),
		stopCh:         make(chan struct{}),
		done:           make(chan struct{}),
		updates:        make(chan struct{}, 1),
		closers:        map[io.Closer]struct{}{},
		sessions:       map[string]*sessionEntry{},
		fileIndex:      map[string]fileRef{},
		subagentsIndex: map[string]*sessionEntry{},
		retired:        map[string]retiredActivity{},
	}
	s.snap.Store(EmptySnapshot)
	s.usage = newUsageTracker(s)
	s.learner = newContextLearner(s.usage.reportedWindows)
	return s
}

// sourceOf is the source of provider; nil when the store has none for it. Sessions only come from
// discover(), which groups the profiles by existing sources, so it is never nil for an entry.
func (s *Store) sourceOf(provider domain.ProviderID) source.Source {
	for _, src := range s.sources {
		if src.Provider() == provider {
			return src
		}
	}
	return nil
}

// profileProvider is the provider of a profile (Claude Code when it does not say).
func profileProvider(p config.ProfileDir) domain.ProviderID {
	if p.Provider == "" {
		return domain.ClaudeCode
	}
	return p.Provider
}

// Snapshot is the last emitted snapshot (the same pointer until the next emission).
func (s *Store) Snapshot() *Snapshot { return s.snap.Load() }

// Updates receives a value after each emission (coalesced: read Snapshot for the latest).
func (s *Store) Updates() <-chan struct{} { return s.updates }

// Done is closed once the store stopped and its goroutine exited.
func (s *Store) Done() <-chan struct{} { return s.done }

// PanicError is a panic recovered in a goroutine of the store, with its stack.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v\n%s", e.Value, e.Stack) }

// Err is the panic that stopped the store (a *PanicError), or nil. It is set before Done is
// closed.
func (s *Store) Err() error {
	if e := s.failure.Load(); e != nil {
		return e
	}
	return nil
}

// recoverPanic, deferred in each goroutine of the store, turns a panic into Err and stops the
// store.
func (s *Store) recoverPanic() {
	r := recover()
	if r == nil {
		return
	}
	e := &PanicError{Value: r, Stack: debug.Stack()}
	if s.failure.CompareAndSwap(nil, e) {
		_, _ = fmt.Fprintf(s.deps.Log, "agtop: the store stopped after a panic: %v\n", r)
	}
	s.Stop()
}

// Start opens the watchers, runs the first full scan and emits the first snapshot. Failures go
// to Snapshot().Errors. It returns early if Stop is called meanwhile.
func (s *Store) Start() {
	s.startOnce.Do(func() {
		s.started.Store(true)
		go s.run()
	})
	s.call(s.start)
}

// Refresh scans now, reads what grew and emits right away (no throttle).
func (s *Store) Refresh() {
	s.call(func() {
		s.triggerScan()
		s.flush()
	})
}

// WatchedPathCount is how many paths the watchers watch now (debugging and tests).
func (s *Store) WatchedPathCount() int {
	n := 0
	s.call(func() {
		for _, w := range s.globals {
			n += w.WatchedPathCount()
		}
		for _, e := range s.sessions {
			if e.watcher != nil {
				n += e.watcher.WatchedPathCount()
			}
		}
		n += s.usage.watchedPathCount()
	})
	return n
}

// Stop closes watchers, tailers and timers; it is idempotent. It does not wait for the load in
// progress (only for the read in progress of each tailer): nothing new starts after it. Done is
// closed once the store goroutine is gone.
func (s *Store) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped.Store(true)
		closers := s.closers
		s.closers = nil
		s.mu.Unlock()
		close(s.stopCh)
		for c := range closers {
			_ = c.Close()
		}
		// Never started: there is no goroutine to close done.
		s.startOnce.Do(func() { close(s.done) })
	})
}

func (s *Store) isStopped() bool { return s.stopped.Load() }

// post queues f for the actor; false once stopped.
func (s *Store) post(f func()) bool {
	select {
	case <-s.stopCh:
		return false
	default:
	}
	select {
	case s.inbox <- f:
		return true
	case <-s.stopCh:
		return false
	}
}

// call runs f on the actor and waits for it (or for the stop). Before Start there is no actor:
// it returns at once without running f.
func (s *Store) call(f func()) {
	if !s.started.Load() {
		return
	}
	reply := make(chan struct{})
	if !s.post(func() { defer close(reply); f() }) {
		return
	}
	select {
	case <-reply:
	case <-s.done:
	}
}

// sync waits until every message queued before it was handled (tests).
func (s *Store) sync() { s.call(func() {}) }

func (s *Store) run() {
	defer close(s.done)
	defer s.recoverPanic()
	defer s.shutdown()
	for {
		select {
		case <-s.stopCh:
			return
		case f := <-s.inbox:
			f()
			if s.scanQueued && len(s.inbox) == 0 {
				s.scanQueued = false
				s.triggerScan()
			}
		}
	}
}

// shutdown runs on the actor when it exits: cancels the timers and closes what is still open.
func (s *Store) shutdown() {
	s.cancelScanTimer()
	s.cancelEmit()
	for _, e := range s.sessions {
		s.closeSessionWatcher(e)
		s.closeTailers(e)
	}
	for _, w := range s.globals {
		s.release(w)
	}
	s.usage.close()
}

func (s *Store) start() {
	if s.opts.Watch {
		s.openWatchers()
	}
	s.triggerScan()
	if s.opts.Watch && !s.isStopped() {
		s.watchersReady()
		if !s.isStopped() {
			s.triggerScan()
		}
	}
	s.flush()
}

// track registers c to be closed by Stop; false (and c closed) once stopped.
func (s *Store) track(c io.Closer) bool {
	s.mu.Lock()
	if s.closers == nil {
		s.mu.Unlock()
		_ = c.Close()
		return false
	}
	s.closers[c] = struct{}{}
	s.mu.Unlock()
	return true
}

// release closes c and forgets it.
func (s *Store) release(c io.Closer) {
	s.mu.Lock()
	if s.closers != nil {
		delete(s.closers, c)
	}
	s.mu.Unlock()
	_ = c.Close()
}

// changed marks the snapshot dirty and schedules an emission.
func (s *Store) changed() {
	s.dirty = true
	s.scheduleEmit()
}

// pushError records an error (operation, path and code only), bounded to MaxErrors.
func (s *Store) pushError(msg string) {
	if s.isStopped() {
		return
	}
	s.errors = append(s.errors, msg)
	if n := len(s.errors) - s.opts.MaxErrors; n > 0 {
		s.errors = append([]string(nil), s.errors[n:]...)
	}
	s.changed()
}

func (s *Store) scheduleEmit() {
	if s.isStopped() || s.emitTok != nil {
		return
	}
	var wait int64
	if s.emitted {
		wait = max(0, s.lastEmitAt+s.opts.EmitThrottleMs-s.deps.Clock.Now())
	}
	tok := new(struct{})
	s.emitTok = tok
	s.emitStop = s.deps.Clock.AfterFunc(wait, func() {
		s.post(func() {
			if s.emitTok == tok {
				s.emitTok, s.emitStop = nil, nil
				s.emit()
			}
		})
	})
}

func (s *Store) cancelEmit() {
	if s.emitStop != nil {
		s.emitStop()
	}
	s.emitTok, s.emitStop = nil, nil
}

func (s *Store) flush() {
	s.cancelEmit()
	s.emit()
}

func (s *Store) emit() {
	if !s.dirty || s.isStopped() {
		return
	}
	s.dirty = false
	entries := make([]*sessionEntry, 0, len(s.sessions))
	for _, e := range s.sessions {
		entries = append(entries, e)
	}
	now := s.deps.Clock.Now()
	s.snap.Store(buildSnapshot(snapshotInput{
		profiles: s.opts.Profiles,
		entries:  entries,
		last:     s.lastDiscovery,
		errors:   s.errors,
		retired:  s.retired,
		now:      now,
		usage:    s.usage.views(now),
		learner:  s.learner,
	}))
	s.lastEmitAt = s.deps.Clock.Now()
	s.emitted = true
	s.emits.Add(1)
	select {
	case s.updates <- struct{}{}:
	default:
	}
}
