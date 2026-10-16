package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

func noWatch(interval int64) func(*Options) {
	return func(o *Options) {
		o.Watch = false
		o.DiscoveryIntervalMs = interval
	}
}

func TestScansWithoutChangesDoNotEmit(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	writeAgentFiles(t, tr)
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.EmitThrottleMs = 60_000, 0 },
		Deps{NewWatcher: w.factory})
	s.Start()
	first := s.Snapshot()
	if got := agentStatus(first); got != "running" {
		t.Fatalf("agent = %q", got)
	}
	emits := s.emits.Load()
	for range 5 {
		s.Refresh()
	}
	s.advance(20) // runs any pending emission
	if s.Snapshot() != first || s.emits.Load() != emits {
		t.Error("scans without changes emitted a new snapshot")
	}
}

func TestViewHasRegistryStartedAt(t *testing.T) {
	tr := makeTree(t)
	// After the boot of the machine, else the record counts as pre_boot.
	startedAt := nowMs() - 5_000
	writeRegistry(t, tr, obj{"startedAt": startedAt})
	writeFile(t, tr.transcript, "")
	s := newTestStore(t, tr, noWatch(60_000), Deps{})
	s.Start()
	if got := only(t, s.Snapshot()).StartedAt; got != startedAt {
		t.Errorf("startedAt = %d, want %d", got, startedAt)
	}
}

func TestContextAboveTableWindowIsInferred(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	// Opus 4.5 without [1m] using 215k: the table window of 200k cannot be right.
	writeFile(t, tr.transcript, assistantAgentLaunch(205_000))
	s := newTestStore(t, tr, noWatch(60_000), Deps{})
	s.Start()
	v := only(t, s.Snapshot())
	if v.ContextUsed != 215_000 || v.ContextWindow != (domain.ContextWindow{Size: 1_000_000, Source: domain.SourceInferred}) ||
		v.ContextPercent < 21.49 || v.ContextPercent > 21.51 {
		t.Errorf("view = used %d window %+v percent %v", v.ContextUsed, v.ContextWindow, v.ContextPercent)
	}
}

func TestAgentContext(t *testing.T) {
	tests := []struct {
		name  string
		model string
		used  int
		want  AgentContext
	}{
		{"above the window is promoted", "claude-sonnet-4-5", 300_000,
			AgentContext{300_000, domain.ContextUsage{ContextWindow: domain.ContextWindow{Size: 1_000_000, Source: domain.SourceInferred}, Percent: 30}}},
		{"unknown model uses the default window", "", 0,
			AgentContext{0, domain.ContextUsage{ContextWindow: domain.ContextWindow{Size: 200_000, Source: domain.SourceDefault}, Percent: 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentContext(tt.model, tt.used, nil, 0); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAgentWithoutModelUsesDefaultWindow(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	s := newTestStore(t, tr, noWatch(60_000), Deps{})
	s.Start()
	a := only(t, s.Snapshot()).Agents[agentID]
	want := AgentContext{0, domain.ContextUsage{ContextWindow: domain.ContextWindow{Size: 200_000, Source: domain.SourceDefault}}}
	if a == nil || a.Status != domain.StatusLaunched || a.Context != want {
		t.Errorf("agent = %+v", a)
	}
}

func TestStartNeverFails(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, aiTitle("ok"))
	failing := func(watcher.Target) (watcher.Watcher, error) {
		return nil, &os.SyscallError{Syscall: "SECRET", Err: syscall.EMFILE}
	}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 60_000 }, Deps{NewWatcher: failing})
	s.Start()
	snap := s.Snapshot()
	if got := only(t, snap).DisplayTitle; got != "ok" {
		t.Errorf("title = %q", got)
	}
	want := []string{
		"watcher " + filepath.Join(tr.profile.Dir, "sessions") + ": EMFILE",
		"watcher session test:" + sid + ": EMFILE",
	}
	if !slices.Equal(snap.Errors, want) {
		t.Errorf("errors = %q", snap.Errors)
	}
}

func TestStopDuringInitialLoad(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	writeManyAgents(t, tr, 30)
	reads := newGate()
	held := make(chan struct{}, 1)
	tailers := &tailerCounter{wrap: func(_ string, inner *tailer.Tailer) func() (tailer.Result, error) {
		return func() (tailer.Result, error) {
			select {
			case held <- struct{}{}:
			default:
			}
			reads.wait()
			return inner.Read()
		}
	}}
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 60_000 },
		Deps{NewWatcher: w.factory, NewTailer: tailers.factory})
	started := make(chan struct{})
	go func() { s.Start(); close(started) }()
	<-held // the load is stuck on the first read (main transcript)
	// Stop returns while the read is still held: it does not wait for the load.
	s.Stop()
	created := tailers.created.Load()
	if tailers.open() != 0 {
		t.Errorf("open tailers after stop: %d", tailers.open())
	}
	reads.release()
	<-started
	<-s.Done()
	if tailers.created.Load() != created {
		t.Error("tailers created after stop")
	}
	if !w.allClosed() {
		t.Error("watchers left open")
	}
}

// heldFS holds ReadDir until released.
type heldFS struct {
	discovery.FS
	gate  *gate
	calls chan struct{}
}

func (f heldFS) ReadDir(p string) ([]string, error) {
	select {
	case f.calls <- struct{}{}:
	default:
	}
	f.gate.wait()
	return f.FS.ReadDir(p)
}

func TestStopDuringSlowScan(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	deps := discovery.NewDeps()
	scan := newGate()
	calls := make(chan struct{}, 1)
	deps.FS = heldFS{FS: deps.FS, gate: scan, calls: calls}
	tailers := &tailerCounter{}
	s := newTestStore(t, tr, noWatch(60_000), Deps{Discovery: deps, NewTailer: tailers.factory})
	started := make(chan struct{})
	go func() { s.Start(); close(started) }()
	<-calls
	// Stop returns while the scan is still held.
	s.Stop()
	scan.release()
	<-started
	<-s.Done()
	if tailers.created.Load() != 0 {
		t.Errorf("tailers created: %d", tailers.created.Load())
	}
}

func TestSessionRemovedAndRecreated(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, aiTitle("a"))
	writeAgentFiles(t, tr)
	w := &fakeWatchers{}
	tailers := &tailerCounter{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = 60_000, 30, 0 },
		Deps{NewWatcher: w.factory, NewTailer: tailers.factory})
	s.Start()
	if tailers.open() != 2 {
		t.Fatalf("open tailers = %d", tailers.open())
	}
	oldWatcher := w.get(t, watcher.KindSession)

	removeFile(t, tr.registry(os.Getpid()))
	s.Refresh()
	if only(t, s.Snapshot()).Liveness != Stale {
		t.Fatal("not stale")
	}
	s.advance(30) // staleMs
	s.Refresh()
	if len(s.Snapshot().Sessions) != 0 {
		t.Fatal("not removed")
	}
	if tailers.open() != 0 || !oldWatcher.isClosed() {
		t.Error("tailers or watcher left open")
	}

	writeRegistry(t, tr, nil)
	appendFile(t, tr.transcript, aiTitle("b"))
	s.Refresh()
	v := only(t, s.Snapshot())
	if v.Liveness != Alive || v.DisplayTitle != "b" || len(v.Agents) != 1 {
		t.Errorf("view = %s %q %d", v.Liveness, v.DisplayTitle, len(v.Agents))
	}
	if tailers.open() != 2 || w.get(t, watcher.KindSession) == oldWatcher {
		t.Error("tailers or watcher not renewed")
	}
}

func TestScanOnlyFindsNewAgentAndRereadsMeta(t *testing.T) {
	t.Run("new agent shows up through the scan alone", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
		s := newTestStore(t, tr, func(o *Options) { o.Watch, o.DiscoveryIntervalMs, o.EmitThrottleMs = false, 20, 10 }, Deps{})
		s.Start()
		if got := agentStatus(s.Snapshot()); got != "launched" {
			t.Fatalf("agent = %q", got)
		}
		writeAgentFiles(t, tr)
		s.waitFor(t, func(x *Snapshot) bool { return agentStatus(x) == "running" }, 20)
	})
	t.Run("rewritten meta.json is re-read by the scan", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
		f := writeAgentFiles(t, tr)
		s := newTestStore(t, tr, func(o *Options) { o.Watch, o.DiscoveryIntervalMs, o.EmitThrottleMs = false, 20, 10 }, Deps{})
		s.Start()
		if got := agentStatus(s.Snapshot()); got != "running" {
			t.Fatalf("agent = %q", got)
		}
		writeFile(t, f.meta, jsonOf(obj{"agentType": "executor", "toolUseId": toolUse, "stoppedByUser": true}))
		snap := s.waitFor(t, func(x *Snapshot) bool { return agentStatus(x) == "failed" }, 20)
		if got := only(t, snap).Agents[agentID].FailureReason; got != "stopped" {
			t.Errorf("failureReason = %q", got)
		}
	})
}

func TestScanOnlyLifecycle(t *testing.T) {
	t.Run("a session that leaves the registry turns stale and is removed after staleMs", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, aiTitle("t"))
		tailers := &tailerCounter{}
		s := newTestStore(t, tr, func(o *Options) {
			o.Watch, o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = false, 20, 150, 10
		}, Deps{NewTailer: tailers.factory})
		s.Start()
		if only(t, s.Snapshot()).Liveness != Alive {
			t.Fatal("not alive")
		}
		// New lines arrive through the periodic scan.
		appendFile(t, tr.transcript, aiTitle("t2"))
		s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && x.Sessions[0].DisplayTitle == "t2" }, 20)

		removedAt := s.clock.Now()
		removeFile(t, tr.registry(os.Getpid()))
		stale := s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && x.Sessions[0].Liveness == Stale }, 20)
		staleSince := only(t, stale).StaleSince
		if staleSince < removedAt {
			t.Errorf("staleSince %d before removal %d", staleSince, removedAt)
		}
		s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 0 }, 20)
		if s.clock.Now()-staleSince < 150 {
			t.Error("removed before staleMs")
		}
		if tailers.open() != 0 {
			t.Errorf("open tailers = %d", tailers.open())
		}
	})
	t.Run("a dead pid counts as leaving the live set", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, "")
		var alive atomic.Bool
		alive.Store(true)
		deps := discovery.NewDeps()
		deps.IsPidAlive = func(int) bool { return alive.Load() }
		s := newTestStore(t, tr, func(o *Options) {
			o.Watch, o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = false, 20, 60_000, 10
		}, Deps{Discovery: deps})
		s.Start()
		alive.Store(false)
		s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && x.Sessions[0].Liveness == Stale }, 20)
	})
}

func TestErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"tailer error", &tailer.Error{Path: "/x", Code: "EACCES"}, "EACCES"},
		{"too large", discovery.ErrFileTooLarge, "TOO_LARGE"},
		{"errno", &os.PathError{Op: "open", Path: "/SECRET", Err: syscall.ENOENT}, "ENOENT"},
		{"anything else", errors.New("SECRET boom"), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorCode(tt.err)
			if got != tt.want || strings.Contains(got, "SECRET") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPanicStopsTheStoreWithErr(t *testing.T) {
	tr := makeTree(t)
	var log strings.Builder
	s := New(DefaultOptions([]config.ProfileDir{tr.profile}), Deps{
		NewWatcher: func(watcher.Target) (watcher.Watcher, error) { panic("boom") },
		Log:        &log,
	})
	s.Start()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the store did not stop after a panic")
	}
	var pe *PanicError
	if !errors.As(s.Err(), &pe) || pe.Value != "boom" || !strings.Contains(string(pe.Stack), "goroutine") {
		t.Fatalf("Err = %v", s.Err())
	}
	if !strings.Contains(log.String(), "panic: boom") {
		t.Errorf("log = %q", log.String())
	}
	s.Refresh() // no actor left: returns at once
}
