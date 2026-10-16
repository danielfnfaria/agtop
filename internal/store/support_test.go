package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

// Shared helpers of the store tests: temporary trees (never a real profile), synthetic
// transcripts, fake watchers/tailers/clock and waiting on snapshots.

const (
	sid     = "sess-0001"
	agentID = "a1b2c3"
	toolUse = "toolu_agent_1"
)

// ---------------------------------------------------------------------------
// Fake clock
// ---------------------------------------------------------------------------

type fakeTimer struct {
	id int
	at int64
	f  func()
}

// fakeClock only moves on Advance, which runs the due timers in order, each one with Now at
// its due time.
type fakeClock struct {
	mu     sync.Mutex
	now    int64
	nextID int
	timers []*fakeTimer
}

func newFakeClock(start int64) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(ms int64, f func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	t := &fakeTimer{id: c.nextID, at: c.now + max(0, ms), f: f}
	c.timers = append(c.timers, t)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.timers = slices.DeleteFunc(c.timers, func(x *fakeTimer) bool { return x == t })
	}
}

func (c *fakeClock) takeDue(until int64) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	best := -1
	for i, t := range c.timers {
		if t.at > until {
			continue
		}
		if best < 0 || t.at < c.timers[best].at || (t.at == c.timers[best].at && t.id < c.timers[best].id) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	t := c.timers[best]
	c.timers = slices.Delete(c.timers, best, best+1)
	c.now = t.at
	return t
}

func (c *fakeClock) Advance(ms int64) {
	c.mu.Lock()
	target := c.now + ms
	c.mu.Unlock()
	for t := c.takeDue(target); t != nil; t = c.takeDue(target) {
		t.f()
	}
	c.mu.Lock()
	c.now = target
	c.mu.Unlock()
}

func (c *fakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// ---------------------------------------------------------------------------
// Fake watchers and tracked tailers
// ---------------------------------------------------------------------------

type fakeWatcher struct {
	target watcher.Target
	events chan watcher.Event
	errs   chan error
	ready  chan struct{}
	mu     sync.Mutex
	closed bool
}

func (w *fakeWatcher) Events() <-chan watcher.Event { return w.events }
func (w *fakeWatcher) Errors() <-chan error         { return w.errs }
func (w *fakeWatcher) Ready() <-chan struct{}       { return w.ready }
func (w *fakeWatcher) Rewatch() error               { return nil }
func (w *fakeWatcher) WatchedPathCount() int        { return 0 }

func (w *fakeWatcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		close(w.events)
		close(w.errs)
	}
	return nil
}

func (w *fakeWatcher) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *fakeWatcher) emit(op watcher.Op, p string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.events <- watcher.Event{Op: op, Path: p}
	}
}

type fakeWatchers struct {
	mu  sync.Mutex
	all []*fakeWatcher
}

func (f *fakeWatchers) factory(t watcher.Target) (watcher.Watcher, error) {
	ready := make(chan struct{})
	close(ready)
	w := &fakeWatcher{target: t, events: make(chan watcher.Event, 64), errs: make(chan error, 1), ready: ready}
	f.mu.Lock()
	f.all = append(f.all, w)
	f.mu.Unlock()
	return w, nil
}

// get is the most recent open watcher of kind (the session watcher is replaced when its paths
// change).
func (f *fakeWatchers) get(t *testing.T, kind watcher.Kind) *fakeWatcher {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.all) - 1; i >= 0; i-- {
		if w := f.all[i]; w.target.Kind == kind && !w.isClosed() {
			return w
		}
	}
	t.Fatalf("watcher %d not created", kind)
	return nil
}

func (f *fakeWatchers) list() []*fakeWatcher {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.all)
}

func (f *fakeWatchers) allClosed() bool {
	for _, w := range f.list() {
		if !w.isClosed() {
			return false
		}
	}
	return true
}

type trackedTailer struct {
	inner  *tailer.Tailer
	owner  *tailerCounter
	closed atomic.Bool
	read   func() (tailer.Result, error)
}

func (t *trackedTailer) Read() (tailer.Result, error) {
	if t.read != nil {
		return t.read()
	}
	return t.inner.Read()
}

func (t *trackedTailer) Close() error {
	err := t.inner.Close()
	if !t.closed.Swap(true) {
		t.owner.closed.Add(1)
	}
	return err
}

type tailerCounter struct {
	created atomic.Int64
	closed  atomic.Int64
	// wrap, when set, replaces the read of each new tailer.
	wrap func(path string, inner *tailer.Tailer) func() (tailer.Result, error)
}

func (c *tailerCounter) open() int64 { return c.created.Load() - c.closed.Load() }

func (c *tailerCounter) factory(p string) (Tailer, error) {
	inner, err := tailer.New(p)
	if err != nil {
		return nil, err
	}
	c.created.Add(1)
	t := &trackedTailer{inner: inner, owner: c}
	if c.wrap != nil {
		t.read = c.wrap(p, inner)
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Temporary tree and synthetic transcripts
// ---------------------------------------------------------------------------

type tree struct {
	profile    config.ProfileDir
	project    string
	transcript string
	sessionDir string
	subagents  string
}

func (tr tree) registry(pid int) string {
	return filepath.Join(tr.profile.Dir, "sessions", strconv.Itoa(pid)+".json")
}

func makeTree(t *testing.T) tree {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".claude")
	project := filepath.Join(dir, "projects", "x")
	mkdirAll(t, project)
	mkdirAll(t, filepath.Join(dir, "sessions"))
	sessionDir := filepath.Join(project, sid)
	return tree{
		profile:    config.ProfileDir{Label: "test", Dir: dir, Source: config.SourceConfig},
		project:    project,
		transcript: filepath.Join(project, sid+".jsonl"),
		sessionDir: sessionDir,
		subagents:  filepath.Join(sessionDir, "subagents"),
	}
}

func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, p, content string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func removeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func nowMs() int64 { return time.Now().UnixMilli() }

func writeRegistryPID(t *testing.T, tr tree, extra map[string]any, pid int) {
	t.Helper()
	now := nowMs()
	reg := map[string]any{
		"pid":       pid,
		"sessionId": sid,
		"cwd":       "/home/dev/projects/demo-app",
		"kind":      "interactive",
		"status":    "busy",
		"name":      "nome-do-registro",
		"startedAt": now,
		"updatedAt": now,
	}
	for k, v := range extra {
		reg[k] = v
	}
	writeFile(t, tr.registry(pid), jsonOf(reg))
}

func writeRegistry(t *testing.T, tr tree, extra map[string]any) {
	t.Helper()
	writeRegistryPID(t, tr, extra, os.Getpid())
}

func ts(s int) string {
	return time.Date(2026, 1, 1, 10, 0, s, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")
}

func tsMs(s int) int64 { return time.Date(2026, 1, 1, 10, 0, s, 0, time.UTC).UnixMilli() }

func line(obj map[string]any) string { return jsonOf(obj) + "\n" }

type obj = map[string]any
type arr = []any

func assistantAgentLaunch(cacheRead int) string {
	return line(obj{
		"type": "assistant", "timestamp": ts(1), "sessionId": sid, "cwd": "/home/dev/projects/demo-app",
		"message": obj{
			"id": "msg_1", "model": "claude-opus-4-5",
			"content": arr{obj{"type": "tool_use", "id": toolUse, "name": "Agent", "input": obj{
				"subagent_type": "executor", "description": "Tarefa sintética", "prompt": "SECRET_PROMPT",
				"run_in_background": true,
			}}},
			"usage": obj{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": cacheRead,
				"cache_creation_input_tokens": 9_990},
		},
	})
}

func asyncLaunchedResult() string {
	return line(obj{
		"type": "user", "timestamp": ts(2), "sessionId": sid,
		"message":       obj{"content": arr{obj{"type": "tool_result", "tool_use_id": toolUse, "content": "SECRET"}}},
		"toolUseResult": obj{"status": "async_launched", "agentId": agentID, "description": "Tarefa sintética"},
	})
}

func agentPrompt() string {
	return line(obj{"type": "user", "timestamp": ts(3), "isSidechain": true, "agentId": agentID,
		"message": obj{"content": "SECRET_SUB_PROMPT"}})
}

func agentAssistant(n int, toolName string) string {
	return line(obj{
		"type": "assistant", "timestamp": ts(3 + n), "isSidechain": true, "agentId": agentID,
		"message": obj{
			"id": "sm_" + strconv.Itoa(n), "model": "claude-haiku-4-5",
			"content": arr{obj{"type": "tool_use", "id": "toolu_s_" + strconv.Itoa(n), "name": toolName, "input": obj{}}},
			"usage":   obj{"input_tokens": 1, "output_tokens": 1, "cache_read_input_tokens": 0},
		},
	})
}

func peerHandback(s int) string {
	return line(obj{"type": "user", "timestamp": ts(s), "sessionId": sid,
		"message": obj{"content": "SECRET_REPORT"},
		"origin":  obj{"kind": "peer", "senderTaskId": agentID, "handback": true}})
}

func aiTitle(title string) string { return line(obj{"type": "ai-title", "aiTitle": title}) }

type agentFiles struct{ meta, jsonl string }

func writeAgentFiles(t *testing.T, tr tree) agentFiles {
	t.Helper()
	mkdirAll(t, tr.subagents)
	f := agentFiles{
		meta:  filepath.Join(tr.subagents, "agent-"+agentID+".meta.json"),
		jsonl: filepath.Join(tr.subagents, "agent-"+agentID+".jsonl"),
	}
	writeFile(t, f.meta, jsonOf(obj{"agentType": "executor", "description": "Tarefa sintética", "toolUseId": toolUse}))
	writeFile(t, f.jsonl, agentPrompt()+agentAssistant(1, "Read"))
	return f
}

func writeManyAgents(t *testing.T, tr tree, n int) {
	t.Helper()
	mkdirAll(t, tr.subagents)
	for i := range n {
		writeFile(t, filepath.Join(tr.subagents, "agent-x"+strconv.Itoa(i)+".jsonl"), agentPrompt())
	}
}

// ---------------------------------------------------------------------------
// Stores under test
// ---------------------------------------------------------------------------

type testStore struct {
	*Store
	clock *fakeClock
	// step is how far waitFor moves the clock per poll (default: the emit throttle).
	step int64
}

// newTestStore is a store over the tree with a fake clock (unless deps has a clock): throttle,
// staleness and periodic scans only move with Advance (or waitFor). It is stopped at cleanup.
func newTestStore(t *testing.T, tr tree, mod func(*Options), deps Deps) *testStore {
	t.Helper()
	opts := DefaultOptions([]config.ProfileDir{tr.profile})
	if mod != nil {
		mod(&opts)
	}
	if deps.Discovery.FS == nil {
		deps.Discovery = discovery.NewDeps()
	}
	st := &testStore{step: opts.EmitThrottleMs}
	if deps.Clock == nil {
		st.clock = newFakeClock(nowMs())
		deps.Clock = st.clock
	} else if c, ok := deps.Clock.(*fakeClock); ok {
		st.clock = c
	}
	if st.step == 0 {
		st.step = 1
	}
	st.Store = New(opts, deps)
	t.Cleanup(func() {
		st.Stop()
		<-st.Done()
	})
	return st
}

// waitFor waits until the snapshot satisfies pred. With a fake clock it polls, moving the clock
// step ms per poll so that the pending emission (and, with a short interval, the next scan)
// happens. stepMs 0 is the store default.
func (s *testStore) waitFor(t *testing.T, pred func(*Snapshot) bool, stepMs int64) *Snapshot {
	t.Helper()
	if stepMs == 0 {
		stepMs = s.step
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		s.sync()
		if snap := s.Snapshot(); pred(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout; last snapshot: %s", summary(s.Snapshot()))
		}
		if s.clock != nil {
			s.clock.Advance(stepMs)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// advance moves the fake clock and waits for what it triggered.
func (s *testStore) advance(ms int64) {
	s.clock.Advance(ms)
	s.sync()
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func summary(s *Snapshot) string {
	type sv struct {
		Key      string
		Liveness Liveness
		Agents   map[string]string
	}
	var out []sv
	for _, v := range s.Sessions {
		agents := map[string]string{}
		for k, a := range v.Agents {
			agents[k] = string(a.Status)
		}
		out = append(out, sv{v.Key, v.Liveness, agents})
	}
	return jsonOf(out)
}

func only(t *testing.T, s *Snapshot) *SessionView {
	t.Helper()
	if len(s.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(s.Sessions))
	}
	return s.Sessions[0]
}

func agentStatus(s *Snapshot) string {
	if len(s.Sessions) != 1 {
		return ""
	}
	if a := s.Sessions[0].Agents[agentID]; a != nil {
		return string(a.Status)
	}
	return ""
}

// gate is released by hand.
type gate struct{ ch chan struct{} }

func newGate() *gate     { return &gate{make(chan struct{})} }
func (g *gate) wait()    { <-g.ch }
func (g *gate) release() { close(g.ch) }

var errBoom = errors.New("boom")
