package store

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

func statusIs(want string) func(*Snapshot) bool {
	return func(s *Snapshot) bool { return agentStatus(s) == want }
}

func TestFakeWatcherFullFlow(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	w := &fakeWatchers{}
	tailers := &tailerCounter{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = 60_000, 100, 10 },
		Deps{NewWatcher: w.factory, NewTailer: tailers.factory})
	s.Start()
	snap := s.Snapshot()
	v := only(t, snap)
	if v.Key != "test:"+sid || v.Provider != domain.ClaudeCode || v.ProfileLabel != "test" || v.PID != os.Getpid() ||
		v.Kind != "interactive" || v.Activity != discovery.ActivityBusy || v.Liveness != Alive ||
		v.DisplayTitle != "nome-do-registro" || v.ContextWindow != (domain.ContextWindow{Size: 200_000, Source: domain.SourceDefault}) {
		t.Errorf("view = %+v", v)
	}
	wantProfiles := []ProfileStatus{{Label: "test", Provider: domain.ClaudeCode, Dir: tr.profile.Dir, Source: config.SourceConfig,
		LiveCount: 1, Issues: map[discovery.IssueReason]int{}}}
	if !reflect.DeepEqual(snap.Profiles, wantProfiles) {
		t.Errorf("profiles = %+v", snap.Profiles)
	}
	var kinds []watcher.Kind
	for _, x := range w.list() {
		kinds = append(kinds, x.target.Kind)
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []watcher.Kind{watcher.KindSessions, watcher.KindSession}) {
		t.Errorf("watchers = %v", kinds)
	}
	if got := w.get(t, watcher.KindSession).target; got != (watcher.Target{Kind: watcher.KindSession, TranscriptPath: tr.transcript}) {
		t.Errorf("session target = %+v", got)
	}

	// Agent launched in the main transcript.
	appendFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	w.get(t, watcher.KindSession).emit(watcher.Change, tr.transcript)
	v = only(t, s.waitFor(t, statusIs("launched"), 0))
	if a := v.Agents[agentID]; a.Type != "executor" || !a.IsBackground {
		t.Errorf("agent = %+v", a)
	}
	if v.Model.ID != "claude-opus-4-5" || v.ContextWindow != (domain.ContextWindow{Size: 200_000, Source: domain.SourceTable}) ||
		v.ContextUsed != 50_000 || v.ContextPercent != 25 || v.DisplayTitle != "nome-do-registro" {
		t.Errorf("view = %+v", v)
	}

	// subagents/ did not exist yet: nothing watches it; the scan finds it and swaps the watcher.
	f := writeAgentFiles(t, tr)
	first := w.get(t, watcher.KindSession)
	s.Refresh()
	snap = s.waitFor(t, statusIs("running"), 0)
	if len(only(t, snap).Agents) != 1 || !first.isClosed() {
		t.Error("agent or watcher not renewed")
	}
	if got := w.get(t, watcher.KindSession).target; got != (watcher.Target{Kind: watcher.KindSession, TranscriptPath: tr.transcript, SubagentsDir: tr.subagents}) {
		t.Errorf("session target = %+v", got)
	}

	// SubagentHandback in the agent transcript.
	appendFile(t, f.jsonl, agentAssistant(2, "SubagentHandback"))
	w.get(t, watcher.KindSession).emit(watcher.Change, f.jsonl)
	s.waitFor(t, statusIs("handed_back"), 0)

	// Handback reaching the parent: completed with finishedAt.
	appendFile(t, tr.transcript, peerHandback(30))
	w.get(t, watcher.KindSession).emit(watcher.Change, tr.transcript)
	snap = s.waitFor(t, statusIs("completed"), 0)
	if got := only(t, snap).Agents[agentID].FinishedAt; got != tsMs(30) {
		t.Errorf("finishedAt = %d", got)
	}

	// Truncation: state rebuilt from scratch with the new content and the agents re-applied.
	createdBefore := tailers.created.Load()
	writeFile(t, tr.transcript, aiTitle("Título novo"))
	w.get(t, watcher.KindSession).emit(watcher.Change, tr.transcript)
	snap = s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && x.Sessions[0].DisplayTitle == "Título novo" }, 0)
	v = only(t, snap)
	if v.ContextUsed != 0 || v.Model != nil {
		t.Errorf("old usage kept: %d %v", v.ContextUsed, v.Model)
	}
	// The agent comes back only through its meta.json and transcript (without the handback).
	if a := v.Agents[agentID]; a.Status != domain.StatusHandedBack || a.Type != "executor" {
		t.Errorf("agent = %+v", a)
	}
	if tailers.created.Load() <= createdBefore || tailers.open() != 2 {
		t.Errorf("tailers created %d, open %d", tailers.created.Load(), tailers.open())
	}

	// Registry removed -> stale -> removed after staleMs, with tailers closed.
	removeFile(t, tr.registry(os.Getpid()))
	w.get(t, watcher.KindSessions).emit(watcher.Unlink, tr.registry(os.Getpid()))
	snap = s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && x.Sessions[0].Liveness == Stale }, 0)
	if only(t, snap).StaleSince == 0 || snap.Profiles[0].LiveCount != 0 {
		t.Error("stale session without staleSince or still live")
	}
	s.advance(100) // staleMs
	s.Refresh()
	if len(s.Snapshot().Sessions) != 0 {
		t.Error("session not removed")
	}
	s.Stop()
	<-s.Done()
	if tailers.open() != 0 || !w.allClosed() {
		t.Error("tailers or watchers left open")
	}
}

func TestResumedAgentIsActiveAgain(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	f := writeAgentFiles(t, tr)
	appendFile(t, f.jsonl, agentAssistant(2, "SubagentHandback"))
	appendFile(t, tr.transcript, peerHandback(30))
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = 60_000, 60_000, 10 },
		Deps{NewWatcher: w.factory})
	s.Start()
	if got := agentStatus(s.Snapshot()); got != "completed" {
		t.Fatalf("agent = %q", got)
	}
	session := func() *fakeWatcher { return w.get(t, watcher.KindSession) }

	// The parent resumes the finished agent; the agent gets the message in its own transcript.
	appendFile(t, tr.transcript, line(obj{
		"type": "user", "timestamp": ts(41), "sessionId": sid,
		"message":       obj{"content": arr{obj{"type": "tool_result", "tool_use_id": "toolu_send", "content": "x"}}},
		"toolUseResult": obj{"success": true, "message": "SECRET", "resumedAgentId": agentID},
	}))
	session().emit(watcher.Change, tr.transcript)
	snap := s.waitFor(t, statusIs("running"), 0)
	if only(t, snap).Agents[agentID].FinishedAt != 0 {
		t.Error("finishedAt kept")
	}
	appendFile(t, f.jsonl, line(obj{
		"type": "user", "timestamp": ts(41), "isSidechain": true, "agentId": agentID, "isMeta": true,
		"origin": obj{"kind": "coordinator"}, "message": obj{"content": "SECRET_FOLLOWUP"},
	})+agentAssistant(40, "Read"))
	session().emit(watcher.Change, f.jsonl)
	snap = s.waitFor(t, func(x *Snapshot) bool {
		if len(x.Sessions) != 1 || x.Sessions[0].Agents[agentID] == nil {
			return false
		}
		return slices.ContainsFunc(x.Sessions[0].Agents[agentID].PendingTools, func(p domain.PendingEntry) bool { return p.ID == "toolu_s_40" })
	}, 0)
	if got := agentStatus(snap); got != "running" {
		t.Errorf("agent = %q", got)
	}

	// Next handback ends it again.
	appendFile(t, f.jsonl, agentAssistant(45, "SubagentHandback"))
	session().emit(watcher.Change, f.jsonl)
	s.waitFor(t, statusIs("handed_back"), 0)
	appendFile(t, tr.transcript, peerHandback(50))
	session().emit(watcher.Change, tr.transcript)
	snap = s.waitFor(t, statusIs("completed"), 0)
	if got := only(t, snap).Agents[agentID].FinishedAt; got != tsMs(50) {
		t.Errorf("finishedAt = %d", got)
	}
}

func TestSnapshotHasNoTranscriptContent(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult()+peerHandback(30))
	writeAgentFiles(t, tr)
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 60_000 }, Deps{NewWatcher: w.factory})
	s.Start()
	snap := s.Snapshot()
	if strings.Contains(jsonOf(snap), "SECRET") {
		t.Error("snapshot leaks transcript content")
	}
	if got := agentStatus(snap); got != "completed" {
		t.Errorf("agent = %q", got)
	}
}

func TestThrottle(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	w := &fakeWatchers{}
	var linesRead atomic.Int64
	tailers := &tailerCounter{wrap: func(_ string, inner *tailer.Tailer) func() (tailer.Result, error) {
		return func() (tailer.Result, error) {
			res, err := inner.Read()
			linesRead.Add(int64(len(res.Lines)))
			return res, err
		}
	}}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.EmitThrottleMs = 60_000, 100 },
		Deps{NewWatcher: w.factory, NewTailer: tailers.factory})
	s.Start()
	start := s.emits.Load()
	emissions := func() int64 { return s.emits.Load() - start }
	assistant := func(i int) string {
		return line(obj{"type": "assistant", "timestamp": ts(i + 1), "message": obj{
			"id": "m_" + strconv.Itoa(i), "model": "claude-sonnet-5[1m]", "content": arr{},
			"usage": obj{"input_tokens": 0, "output_tokens": 1},
		}})
	}
	const n = 30
	for i := range n {
		appendFile(t, tr.transcript, assistant(i))
		w.get(t, watcher.KindSession).emit(watcher.Change, tr.transcript)
	}
	eventually(t, func() bool { s.sync(); return linesRead.Load() == n })

	// Clock frozen: the whole burst is held by the throttle window that started at Start.
	if emissions() != 0 {
		t.Fatalf("emissions = %d", emissions())
	}
	s.advance(99)
	if emissions() != 0 {
		t.Fatalf("emissions after 99 ms = %d", emissions())
	}
	s.advance(1)
	if emissions() != 1 {
		t.Fatalf("emissions after 100 ms = %d", emissions())
	}
	v := only(t, s.Snapshot())
	if v.Totals.Output != n || v.ContextWindow != (domain.ContextWindow{Size: 1_000_000, Source: domain.SourceSuffix}) {
		t.Errorf("view = %+v %+v", v.Totals, v.ContextWindow)
	}

	// A change right after an emission waits for the next window.
	appendFile(t, tr.transcript, assistant(n))
	w.get(t, watcher.KindSession).emit(watcher.Change, tr.transcript)
	eventually(t, func() bool { s.sync(); return linesRead.Load() == n+1 })
	s.advance(99)
	if emissions() != 1 {
		t.Fatalf("emissions = %d", emissions())
	}
	s.advance(1)
	if emissions() != 2 || only(t, s.Snapshot()).Totals.Output != n+1 {
		t.Errorf("emissions = %d", emissions())
	}
}

func TestSnapshotKeepsIdentityBetweenEmissions(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.EmitThrottleMs = 60_000, 5 }, Deps{NewWatcher: w.factory})
	s.Start()
	a := s.Snapshot()
	if s.Snapshot() != a {
		t.Fatal("snapshot changed without an emission")
	}
	// Drain the notification of the first emission.
	select {
	case <-s.Updates():
	default:
	}
	appendFile(t, tr.transcript, aiTitle("x"))
	w.get(t, watcher.KindSession).emit(watcher.Change, tr.transcript)
	s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && x.Sessions[0].DisplayTitle == "x" }, 0)
	if s.Snapshot() == a {
		t.Error("same snapshot after an emission")
	}
	select {
	case <-s.Updates():
	default:
		t.Error("no update notification")
	}
}

func TestInvalidMetaBecomesShortError(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000))
	mkdirAll(t, tr.subagents)
	meta := filepath.Join(tr.subagents, "agent-"+agentID+".meta.json")
	writeFile(t, meta, `{"agentType": "SECRET`)
	writeFile(t, filepath.Join(tr.subagents, "agent-"+agentID+".jsonl"), agentPrompt())
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 60_000 }, Deps{NewWatcher: w.factory})
	s.Start()
	snap := s.Snapshot()
	if want := []string{"meta " + meta + ": INVALID_JSON"}; !slices.Equal(snap.Errors, want) {
		t.Errorf("errors = %q", snap.Errors)
	}
	if got := agentStatus(snap); got != "running" {
		t.Errorf("agent = %q", got)
	}
	// Rewritten valid: the watcher event re-applies the meta.
	writeFile(t, meta, jsonOf(obj{"agentType": "revisor", "toolUseId": toolUse}))
	w.get(t, watcher.KindSession).emit(watcher.Change, meta)
	s.waitFor(t, func(x *Snapshot) bool {
		return len(x.Sessions) == 1 && x.Sessions[0].Agents[agentID] != nil && x.Sessions[0].Agents[agentID].Type == "revisor"
	}, 0)
}

func TestDuplicateRegistriesShowOnce(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, obj{"updatedAt": nowMs() + 1})
	writeRegistryPID(t, tr, obj{"updatedAt": nowMs() - 1000, "name": "antigo"}, os.Getppid())
	writeFile(t, tr.transcript, "")
	w := &fakeWatchers{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 60_000 }, Deps{NewWatcher: w.factory})
	s.Start()
	v := only(t, s.Snapshot())
	if v.PID != os.Getpid() || v.Liveness != Alive {
		t.Errorf("view = pid %d %s", v.PID, v.Liveness)
	}
}

func TestStableOrder(t *testing.T) {
	tr := makeTree(t)
	base := nowMs() - 60_000
	var dead sync32
	reg := func(sessionID string, pid int, status string, startedAt int64) {
		r := obj{"pid": pid, "sessionId": sessionID, "status": status, "updatedAt": nowMs()}
		if startedAt != 0 {
			r["startedAt"] = startedAt
		}
		writeFile(t, tr.registry(pid), jsonOf(r))
	}
	transcript := func(sessionID string) string { return filepath.Join(tr.project, sessionID+".jsonl") }
	// Keys in the opposite order of the start, so the key alone would give another order.
	sessions := []struct {
		id      string
		pid     int
		started int64
	}{{"s-c", 4_200_001, base}, {"s-b", 4_200_002, base + 1_000}, {"s-a", 4_200_003, base + 2_000}}
	for _, x := range sessions {
		reg(x.id, x.pid, "idle", x.started)
		writeFile(t, transcript(x.id), aiTitle(x.id))
	}
	deps := discovery.NewDeps()
	deps.IsPidAlive = func(pid int) bool { return !dead.has(pid) }
	// The sessions started up to a minute ago: a freshly booted machine would mark them pre_boot.
	deps.UptimeMs = func() int64 { return 24 * 60 * 60 * 1_000 }
	s := newTestStore(t, tr, func(o *Options) {
		o.Watch, o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = false, 600_000, 1_000, 0
	}, Deps{Discovery: deps})
	order := func() []string {
		var ids []string
		for _, v := range s.Snapshot().Sessions {
			ids = append(ids, v.SessionID)
		}
		return ids
	}
	expectOrder := func(want ...string) {
		t.Helper()
		if got := order(); !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	}
	s.Start()
	s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 3 }, 0)
	expectOrder("s-c", "s-b", "s-a")

	// The last one turns busy and gets the most recent activity: nothing moves.
	reg("s-a", 4_200_003, "busy", base+2_000)
	appendFile(t, transcript("s-a"), line(obj{"type": "ai-title", "aiTitle": "late", "timestamp": ts(59)}))
	reg("s-c", 4_200_001, "busy", base)
	s.Refresh()
	snap := s.waitFor(t, func(x *Snapshot) bool {
		return slices.ContainsFunc(x.Sessions, func(v *SessionView) bool { return v.DisplayTitle == "late" })
	}, 0)
	var acts []string
	for _, v := range snap.Sessions {
		acts = append(acts, v.SessionID+"="+string(v.Activity))
	}
	if want := []string{"s-c=busy", "s-b=idle", "s-a=busy"}; !slices.Equal(acts, want) {
		t.Errorf("activities = %v", acts)
	}

	// A new session goes to the end; one without startedAt is placed by when the store first
	// saw it (now), also after the ones already shown.
	reg("s-0", 4_200_004, "idle", base+3_000)
	writeFile(t, transcript("s-0"), "")
	s.Refresh()
	expectOrder("s-c", "s-b", "s-a", "s-0")
	reg("s-00", 4_200_005, "busy", 0)
	writeFile(t, transcript("s-00"), "")
	s.Refresh()
	expectOrder("s-c", "s-b", "s-a", "s-0", "s-00")

	// A session that leaves (stale, then removed) does not reorder the others.
	dead.add(4_200_002)
	s.Refresh()
	expectOrder("s-c", "s-b", "s-a", "s-0", "s-00")
	s.advance(1_000) // staleMs
	s.Refresh()
	expectOrder("s-c", "s-a", "s-0", "s-00")
}

func TestStopIsIdempotentAndSilences(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	w := &fakeWatchers{}
	tailers := &tailerCounter{}
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 20 }, Deps{NewWatcher: w.factory, NewTailer: tailers.factory})
	s.Start()
	emits := s.emits.Load()
	sessionWatcher := w.get(t, watcher.KindSession)
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	s.Stop()
	<-done
	s.Stop()
	<-s.Done()
	if tailers.open() != 0 || !w.allClosed() {
		t.Error("tailers or watchers left open")
	}
	// No timer survives stop (neither the periodic scan nor an emission).
	if n := s.clock.Pending(); n != 0 {
		t.Errorf("pending timers = %d", n)
	}
	appendFile(t, tr.transcript, aiTitle("depois"))
	sessionWatcher.emit(watcher.Change, tr.transcript)
	s.clock.Advance(60_000)
	if s.clock.Pending() != 0 || s.emits.Load() != emits {
		t.Error("store still active after stop")
	}
}

// sync32 is a set of pids safe for concurrent use.
type sync32 struct{ m atomic.Pointer[map[int]bool] }

func (d *sync32) add(pid int) {
	next := map[int]bool{pid: true}
	if cur := d.m.Load(); cur != nil {
		for k := range *cur {
			next[k] = true
		}
	}
	d.m.Store(&next)
}

func (d *sync32) has(pid int) bool {
	cur := d.m.Load()
	return cur != nil && (*cur)[pid]
}
