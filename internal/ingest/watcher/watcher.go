// Package watcher tells when the files agtop reads may have changed. Only what belongs to live
// sessions is watched, so the descriptors follow the live sessions, not the history of
// projects/. On macOS directories go through a kqueue watch of their own (see dirWatch).
package watcher

import (
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// SettleDelay is the wait after a watch is set up before it is considered ready: the first
// milliseconds still lose events on some platforms.
const SettleDelay = 100 * time.Millisecond

// Op is what happened to a path.
type Op int

const (
	// Add means the path appeared.
	Add Op = iota
	// Change means the path was written to or touched.
	Change
	// Unlink means the path was removed or renamed away.
	Unlink
)

// Event is a change of a watched path.
type Event struct {
	Op   Op
	Path string
}

// Kind selects what a Target watches.
type Kind int

const (
	// KindSessions watches <dir>/sessions (a single watch on the directory).
	KindSessions Kind = iota
	// KindUsage watches <dir>/agtop (same single watch as KindSessions).
	KindUsage
	// KindSession watches the transcript of a live session and its subagents/ directory.
	KindSession
	// KindFiles watches a few named files directly inside Root (Names); their changes are
	// debounced (FilesDebounce) into one Change event on Root.
	KindFiles
)

// FilesDebounce is the quiet time after the last change of a KindFiles target before it reports.
const FilesDebounce = 250 * time.Millisecond

// Target is what to watch.
//
//   - KindSessions, KindUsage: Root is the directory; only *.json names matter and every one of
//     them is reported as Change (the consumer only needs to know it must scan again).
//   - KindSession: TranscriptPath (the file itself) and SubagentsDir (agent-*.jsonl and
//     agent-*.meta.json directly inside it). Canonical paths, as discovery returns them. A
//     session without transcript or subagents/ has nothing to watch there: the periodic scan
//     finds the new file and the consumer swaps the session watcher.
//   - KindFiles: Root is the directory and Names the file names inside it (see FilesTarget);
//     any change of one of them is reported, debounced, as one Change on Root.
type Target struct {
	Kind           Kind
	Root           string
	TranscriptPath string
	SubagentsDir   string
	// Names are the file names of a KindFiles target, newline separated (Target stays
	// comparable).
	Names string
}

// FilesTarget is the KindFiles target over names inside root.
func FilesTarget(root string, names ...string) Target {
	return Target{Kind: KindFiles, Root: root, Names: strings.Join(names, "\n")}
}

// watchesName reports whether name is one of the Names of a KindFiles target.
func (t Target) watchesName(name string) bool {
	if t.Kind != KindFiles || name == "" {
		return false
	}
	return slices.Contains(strings.Split(t.Names, "\n"), name)
}

// Watcher reports the changes of one Target.
type Watcher interface {
	// Events delivers the changes; it is closed after Close.
	Events() <-chan Event
	// Errors delivers watch errors; it is closed after Close.
	Errors() <-chan error
	// Ready is closed once the watch is set up and has settled (or the watcher is closed).
	Ready() <-chan struct{}
	// Rewatch re-adds the watched paths; call it after a tailer reset, as replacing or
	// recreating a file drops the watch on it.
	Rewatch() error
	// Close stops watching; it is idempotent.
	Close() error
	// WatchedPathCount is how many paths are watched right now (debugging and tests).
	WatchedPathCount() int
}

// Factory creates the Watcher of a Target; it fails if the directory to watch does not exist.
type Factory func(Target) (Watcher, error)

var subagentFileRE = regexp.MustCompile(`^agent-.+(?:\.jsonl|\.meta\.json)$`)

// EntryKind is the type of an entry when the caller knows it.
type EntryKind int

const (
	// KindUnknown means the caller does not know the type.
	KindUnknown EntryKind = iota
	// KindFile is a regular file.
	KindFile
	// KindDir is a directory.
	KindDir
)

// IgnoreInSession reports whether p must be ignored by the watcher of a session: only the
// transcript itself, the subagents/ directory itself and the agent-*.{jsonl,meta.json}
// directly inside it pass.
func IgnoreInSession(t Target, p string, kind EntryKind) bool {
	if p == t.TranscriptPath {
		return kind == KindDir
	}
	dir := t.SubagentsDir
	if dir == "" {
		return true
	}
	if p == dir {
		return kind == KindFile
	}
	return filepath.Dir(p) != dir || kind == KindDir || !subagentFileRE.MatchString(filepath.Base(p))
}

// IsRegistryFileName reports whether a name in sessions/ matters: *.json (<pid>.<hash>.key and
// the like do not).
func IsRegistryFileName(name string) bool {
	return strings.HasSuffix(name, ".json") && !strings.ContainsAny(name, `/\`)
}

// dirWatch watches one directory and reports its entries like fsnotify does; its channels are
// never closed.
type dirWatch interface {
	Events() <-chan fsnotify.Event
	Errors() <-chan error
	Rewatch() error
	Close() error
	Count() int
}

type fsWatcher struct {
	target Target
	fsw    *fsnotify.Watcher
	dw     dirWatch
	events chan Event
	errs   chan error
	ready  chan struct{}
	done   chan struct{}

	closeOnce sync.Once
	readyOnce sync.Once
	mu        sync.Mutex
	closed    bool
	timer     *time.Timer
}

// New is the Factory over fsnotify.
func New(t Target) (Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &fsWatcher{
		target: t,
		fsw:    fsw,
		events: make(chan Event),
		errs:   make(chan error),
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
	}
	if err := w.add(); err != nil {
		_ = fsw.Close()
		if w.dw != nil {
			_ = w.dw.Close()
		}
		return nil, err
	}
	w.mu.Lock()
	w.timer = time.AfterFunc(SettleDelay, w.markReady)
	w.mu.Unlock()
	go w.run()
	return w, nil
}

var _ Factory = New

// filePath is the single file watched (the transcript), dirPath the directory.
func (w *fsWatcher) filePath() string {
	if w.target.Kind == KindSession {
		return w.target.TranscriptPath
	}
	return ""
}

func (w *fsWatcher) dirPath() string {
	if w.target.Kind == KindSession {
		return w.target.SubagentsDir
	}
	return w.target.Root
}

// watchesFile tells which entries of the directory also need a watch of their own, so that
// appends are seen; registry and usage files are replaced, which the directory watch sees.
func (w *fsWatcher) watchesFile(name string) bool {
	if w.target.Kind == KindFiles {
		return w.target.watchesName(name)
	}
	return w.target.Kind == KindSession && subagentFileRE.MatchString(name)
}

func (w *fsWatcher) add() error {
	if p := w.filePath(); p != "" {
		if err := w.fsw.Add(p); err != nil {
			return err
		}
	}
	p := w.dirPath()
	if p == "" {
		return nil
	}
	if !nativeDirWatch {
		return w.fsw.Add(p)
	}
	dw, err := newDirWatch(p, w.watchesFile)
	if err != nil {
		return err
	}
	w.dw = dw
	return nil
}

func (w *fsWatcher) markReady() { w.readyOnce.Do(func() { close(w.ready) }) }

func (w *fsWatcher) run() {
	defer close(w.events)
	defer close(w.errs)
	var dwEvents <-chan fsnotify.Event
	var dwErrs <-chan error
	if w.dw != nil {
		dwEvents, dwErrs = w.dw.Events(), w.dw.Errors()
	}
	// KindFiles: the changes are held until FilesDebounce passes without a new one.
	var debounce *time.Timer
	var due <-chan time.Time
	var held Event
	deliver := func(ev Event) bool {
		if w.target.Kind != KindFiles {
			return w.send(ev)
		}
		held = ev
		if debounce == nil {
			debounce = time.NewTimer(FilesDebounce)
			due = debounce.C
		} else {
			debounce.Reset(FilesDebounce)
		}
		return true
	}
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-w.done:
			return
		case <-due:
			due = nil
			debounce = nil
			if !w.send(held) {
				return
			}
		case ev := <-dwEvents:
			if out, ok := w.translate(ev); ok && !deliver(out) {
				return
			}
		case err := <-dwErrs:
			if !w.sendErr(err) {
				return
			}
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if out, ok := w.translate(ev); ok && !deliver(out) {
				return
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if !w.sendErr(err) {
				return
			}
		}
	}
}

func (w *fsWatcher) sendErr(err error) bool {
	select {
	case w.errs <- err:
		return true
	case <-w.done:
		return false
	}
}

func (w *fsWatcher) send(ev Event) bool {
	select {
	case w.events <- ev:
		return true
	case <-w.done:
		return false
	}
}

func (w *fsWatcher) translate(ev fsnotify.Event) (Event, bool) {
	if w.target.Kind == KindFiles {
		if !w.target.watchesName(filepath.Base(ev.Name)) {
			return Event{}, false
		}
		return Event{Op: Change, Path: w.target.Root}, true
	}
	if w.target.Kind != KindSession {
		if !IsRegistryFileName(filepath.Base(ev.Name)) {
			return Event{}, false
		}
		return Event{Op: Change, Path: ev.Name}, true
	}
	if IgnoreInSession(w.target, ev.Name, KindUnknown) {
		return Event{}, false
	}
	switch {
	case ev.Has(fsnotify.Create):
		return Event{Op: Add, Path: ev.Name}, true
	case ev.Has(fsnotify.Remove), ev.Has(fsnotify.Rename):
		return Event{Op: Unlink, Path: ev.Name}, true
	}
	return Event{Op: Change, Path: ev.Name}, true
}

func (w *fsWatcher) Events() <-chan Event   { return w.events }
func (w *fsWatcher) Errors() <-chan error   { return w.errs }
func (w *fsWatcher) Ready() <-chan struct{} { return w.ready }

// Rewatch tries every path and returns the first failure (fs.ErrNotExist while a file is gone).
func (w *fsWatcher) Rewatch() error {
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return nil
	}
	var errs []error
	if p := w.filePath(); p != "" {
		_ = w.fsw.Remove(p) // already gone when the file was replaced
		errs = append(errs, w.fsw.Add(p))
	}
	if p := w.dirPath(); p != "" {
		if w.dw != nil {
			errs = append(errs, w.dw.Rewatch())
		} else {
			_ = w.fsw.Remove(p)
			errs = append(errs, w.fsw.Add(p))
		}
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *fsWatcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
		w.markReady()
		close(w.done)
		err = w.fsw.Close()
		if w.dw != nil {
			err = errors.Join(err, w.dw.Close())
		}
	})
	return err
}

func (w *fsWatcher) WatchedPathCount() int {
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return 0
	}
	n := len(w.fsw.WatchList())
	if w.dw != nil {
		n += w.dw.Count()
	}
	return n
}
