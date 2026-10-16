package store

import (
	"path/filepath"
	"reflect"
	"strings"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

// The files the status line hook writes in <dir>/agtop/, per profile: the plan usage
// (usage.json) and the context windows of the sessions (context.json). The store keeps the last
// valid content of each, re-read on every scan when its size/mtime changed and right away on an
// event of the agtop/ watcher (opened once the directory exists).

// fileState is the last valid content of one file and what is known about its last read.
type fileState[T any] struct {
	value *T
	// sig is the signature behind value ("" after a failed read).
	sig string
	// lastError is the code of the last failed read, so a broken file is reported once.
	lastError string
}

type profileUsage struct {
	profile    config.ProfileDir
	usage      fileState[domain.UsageFile]
	context    fileState[domain.ContextFile]
	reports    domain.ContextReports
	watcher    watcher.Watcher
	watchTried bool
}

type usageTracker struct {
	s      *Store
	states []*profileUsage
}

func newUsageTracker(s *Store) *usageTracker {
	u := &usageTracker{s: s}
	for _, p := range s.opts.Profiles {
		u.states = append(u.states, &profileUsage{profile: p, reports: domain.NoReports})
	}
	return u
}

// readFile re-reads one file of a profile if its signature changed; true when its content
// changed.
func readFile[T any](s *Store, st *profileUsage, file *fileState[T], name string,
	signature func(discovery.FS, string) (string, bool),
	read func(discovery.FS, string) discovery.FileRead[T], same func(a, b T) bool) bool {
	fs := s.deps.Discovery.FS
	dir := st.profile.Dir
	if sig, ok := signature(fs, dir); ok && sig == file.sig {
		return false
	}
	if s.isStopped() {
		return false
	}
	res := read(fs, dir)
	if s.isStopped() {
		return false
	}
	if res.Kind == discovery.ReadOK {
		file.sig = res.Sig
		file.lastError = ""
		if file.value != nil && same(*file.value, res.Value) {
			return false
		}
		v := res.Value
		file.value = &v
		return true
	}
	file.sig = ""
	if res.Kind == discovery.ReadError && res.Code != file.lastError {
		file.lastError = res.Code
		s.pushError(strings.TrimSuffix(name, ".json") + " " +
			filepath.Join(discovery.UsageDir(dir), name) + ": " + res.Code)
	}
	if res.Kind == discovery.ReadMissing {
		file.lastError = ""
	}
	if file.value == nil {
		return false
	}
	file.value = nil
	return true
}

func sameUsage(a, b domain.UsageFile) bool { return reflect.DeepEqual(a, b) }

func (u *usageTracker) refresh(st *profileUsage) {
	s := u.s
	usageChanged := readFile(s, st, &st.usage, domain.UsageFileName,
		discovery.UsageSignature, discovery.ReadUsageFile, sameUsage)
	contextChanged := readFile(s, st, &st.context, domain.ContextFileName,
		discovery.ContextSignature, discovery.ReadContextFile, domain.SameContextWindows)
	if contextChanged {
		if st.context.value == nil {
			st.reports = domain.NoReports
		} else {
			st.reports = domain.NewContextReports(*st.context.value)
		}
	}
	if (usageChanged || contextChanged) && !s.isStopped() {
		s.changed()
	}
}

func (u *usageTracker) syncWatcher(st *profileUsage) {
	s := u.s
	if !s.opts.Watch || st.watchTried || s.isStopped() {
		return
	}
	root, err := s.deps.Discovery.FS.Realpath(discovery.UsageDir(st.profile.Dir))
	if err != nil {
		return // no agtop/ yet: the scans keep looking
	}
	st.watchTried = true
	w, err := s.deps.NewWatcher(watcher.Target{Kind: watcher.KindUsage, Root: root})
	if err != nil {
		s.pushError("watcher " + root + ": " + errorCode(err))
		return
	}
	if !s.track(w) {
		return
	}
	st.watcher = w
	s.forward(w, func(watcher.Event) { u.refresh(st) }, "watcher "+root)
}

// refreshAll re-reads the files of every profile whose signature changed and opens the
// directory watchers still missing.
func (u *usageTracker) refreshAll() {
	for _, st := range u.states {
		u.syncWatcher(st)
		u.refresh(st)
	}
}

// views is the usage of each profile at now (only profiles with a valid file), by label.
func (u *usageTracker) views(now int64) map[string]domain.UsageView {
	out := map[string]domain.UsageView{}
	for _, st := range u.states {
		if st.usage.value != nil {
			out[st.profile.Label] = domain.NewUsageView(*st.usage.value, now)
		}
	}
	return out
}

// reportedWindows are the context windows reported by the sessions of profile label.
func (u *usageTracker) reportedWindows(label string) domain.ContextReports {
	for _, st := range u.states {
		if st.profile.Label == label {
			return st.reports
		}
	}
	return domain.NoReports
}

func (u *usageTracker) watchers() []watcher.Watcher {
	var out []watcher.Watcher
	for _, st := range u.states {
		if st.watcher != nil {
			out = append(out, st.watcher)
		}
	}
	return out
}

func (u *usageTracker) close() {
	for _, st := range u.states {
		if st.watcher != nil {
			u.s.release(st.watcher)
			st.watcher = nil
		}
	}
}

func (u *usageTracker) watchedPathCount() int {
	n := 0
	for _, w := range u.watchers() {
		n += w.WatchedPathCount()
	}
	return n
}
