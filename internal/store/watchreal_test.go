package store

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/fdcount"
)

// waitNudging waits for pred touching file every second without an answer: the watcher may
// drop events under load; in production the periodic scan covers that, here the scan is off on
// purpose.
func waitNudging(t *testing.T, s *testStore, file string, pred func(*Snapshot) bool) *Snapshot {
	t.Helper()
	for attempt := 0; ; attempt++ {
		if waitReal(s, pred, time.Second) {
			return s.Snapshot()
		}
		if attempt >= 7 {
			t.Fatalf("timeout; last snapshot: %s", summary(s.Snapshot()))
		}
		now := time.Now()
		_ = os.Chtimes(file, now, now)
	}
}

func realStore(t *testing.T, tr tree, deps Deps) *testStore {
	deps.Clock = realClock{}
	return newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.EmitThrottleMs = 60_000, 10 }, deps)
}

func TestRealWatcherDrivesState(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	s := realStore(t, tr, Deps{})
	s.Start()
	if len(only(t, s.Snapshot()).Agents) != 0 {
		t.Fatal("agents at start")
	}

	appendFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	waitNudging(t, s, tr.transcript, statusIs("launched"))

	// subagents/ appears after the scan: only the scan finds it (here, by hand); from then on
	// the new session watcher covers it.
	f := writeAgentFiles(t, tr)
	s.Refresh()
	if !waitReal(s, statusIs("running"), 8*time.Second) {
		t.Fatal("agent not running")
	}
	if s.WatchedPathCount() == 0 {
		t.Error("no watched path")
	}

	appendFile(t, f.jsonl, agentAssistant(2, "SubagentHandback"))
	waitNudging(t, s, f.jsonl, statusIs("handed_back"))

	appendFile(t, tr.transcript, peerHandback(30))
	done := waitNudging(t, s, tr.transcript, statusIs("completed"))
	if got := only(t, done).Agents[agentID].FinishedAt; got != tsMs(30) {
		t.Errorf("finishedAt = %d", got)
	}

	// A new registry in sessions/ -> immediate scan.
	other := "sess-0002"
	writeFile(t, filepath.Join(tr.project, other+".jsonl"), "")
	now := nowMs()
	registry := tr.registry(os.Getppid())
	writeFile(t, registry, jsonOf(obj{"pid": os.Getppid(), "sessionId": other, "startedAt": now, "updatedAt": now}))
	waitNudging(t, s, registry, func(x *Snapshot) bool { return len(x.Sessions) == 2 })
}

func TestRealWatcherPathCountFollowsLiveSessions(t *testing.T) {
	tr := makeTree(t)
	deadPid := func(i int) int { return 4_000_000 + i }
	count := func(dead int) int {
		for i := range dead {
			id := "dead-" + strconv.Itoa(i)
			writeFile(t, filepath.Join(tr.project, id+".jsonl"), aiTitle(id))
			sub := filepath.Join(tr.project, id, "subagents")
			mkdirAll(t, sub)
			writeFile(t, filepath.Join(sub, "agent-z.jsonl"), agentPrompt())
			writeFile(t, tr.registry(deadPid(i)), jsonOf(obj{"pid": deadPid(i), "sessionId": id, "startedAt": 1, "updatedAt": 1}))
		}
		deps := discovery.NewDeps()
		deps.IsPidAlive = func(pid int) bool { return pid == os.Getpid() }
		s := realStore(t, tr, Deps{Discovery: deps})
		s.Start()
		var ids []string
		for _, v := range s.Snapshot().Sessions {
			ids = append(ids, v.SessionID)
		}
		if !slices.Equal(ids, []string{sid}) {
			t.Fatalf("sessions = %v", ids)
		}
		n := s.WatchedPathCount()
		s.Stop()
		<-s.Done()
		return n
	}
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	writeAgentFiles(t, tr)
	withoutDead := count(0)
	withDead := count(50)
	// sessions/ (1) + transcript (1) + subagents/ (1): the watcher reports the paths it added,
	// not the files kqueue opens inside a directory.
	if withoutDead != 3 || withDead != withoutDead {
		t.Errorf("watched paths: %d without dead sessions, %d with", withoutDead, withDead)
	}
}

func TestRealWatcherDescriptorsDoNotGrowWithDeadRegistries(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	writeAgentFiles(t, tr)
	deps := discovery.NewDeps()
	deps.IsPidAlive = func(pid int) bool { return pid == os.Getpid() }
	cost := func() int {
		before, err := fdcount.Open()
		if err != nil {
			t.Skipf("cannot count descriptors: %v", err)
		}
		s := realStore(t, tr, Deps{Discovery: deps})
		s.Start()
		if !waitReal(s, statusIs("running"), 8*time.Second) {
			t.Fatalf("agent not running: %s", summary(s.Snapshot()))
		}
		after, _ := fdcount.Open()
		s.Stop()
		<-s.Done()
		return after - before
	}
	without := cost()
	for i := range 300 {
		pid := 4_000_000 + i
		writeFile(t, tr.registry(pid), jsonOf(obj{"pid": pid, "sessionId": "dead-" + strconv.Itoa(i), "startedAt": 1, "updatedAt": 1}))
	}
	with := cost()
	if with > without+2 {
		t.Errorf("descriptors: %d without dead registries, %d with 300", without, with)
	}
}

func TestRealWatcherResumesAfterTranscriptUnlinkAndRecreate(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	s := realStore(t, tr, Deps{})
	s.Start()
	if agentStatus(s.Snapshot()) != "launched" {
		t.Fatalf("start: %s", summary(s.Snapshot()))
	}

	// The unlink event resets the session; the scan has not run, so the transcript path stays.
	removeFile(t, tr.transcript)
	if !waitReal(s, func(x *Snapshot) bool { return len(x.Sessions) == 1 && len(x.Sessions[0].Agents) == 0 }, 5*time.Second) {
		t.Fatalf("not reset: %s", summary(s.Snapshot()))
	}
	writeFile(t, tr.transcript, "")
	s.Refresh() // the next scan renews the watch

	appendFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
	if !waitReal(s, statusIs("launched"), 5*time.Second) {
		t.Fatalf("no event after the transcript came back: %s", summary(s.Snapshot()))
	}
	for _, e := range s.Snapshot().Errors {
		if strings.HasPrefix(e, "watcher session") {
			t.Errorf("unexpected error %q", e)
		}
	}
}

func TestCallsBeforeStartReturn(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	s := realStore(t, tr, Deps{})
	done := make(chan int)
	go func() {
		s.Refresh()
		s.sync()
		done <- s.WatchedPathCount()
	}()
	select {
	case n := <-done:
		if n != 0 {
			t.Errorf("WatchedPathCount before Start = %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a call before Start blocked")
	}
	s.Start()
	if len(s.Snapshot().Sessions) != 1 || s.WatchedPathCount() == 0 {
		t.Errorf("after Start: %s, %d paths", summary(s.Snapshot()), s.WatchedPathCount())
	}
}

// A watched directory that is deleted and created again drops its watch; the scan must renew
// the global watchers, or the registry stops being followed.
func TestRealRegistryWatcherSurvivesSessionsDirReplacement(t *testing.T) {
	tr := makeTree(t)
	writeRegistry(t, tr, nil)
	writeFile(t, tr.transcript, "")
	s := realStore(t, tr, Deps{})
	s.Start()

	sessions := filepath.Dir(tr.registry(os.Getpid()))
	if err := os.RemoveAll(sessions); err != nil {
		t.Fatal(err)
	}
	mkdirAll(t, sessions)
	s.Refresh() // the scan renews the watcher of the recreated directory

	other := "sess-0002"
	writeFile(t, filepath.Join(tr.project, other+".jsonl"), "")
	now := nowMs()
	registry := tr.registry(os.Getppid())
	writeFile(t, registry, jsonOf(obj{"pid": os.Getppid(), "sessionId": other, "startedAt": now, "updatedAt": now}))
	waitNudging(t, s, registry, func(x *Snapshot) bool { return len(x.Sessions) == 2 })
}

func TestRealUsageWatcherSurvivesDirReplacement(t *testing.T) {
	tr := makeTree(t)
	writeUsage(t, tr, nowMs(), 10, nowMs()+3_600_000)
	s := realStore(t, tr, Deps{})
	s.Start()
	if got := fiveHourPercent(s.Snapshot()); got != 10 {
		t.Fatalf("percent = %v", got)
	}

	if err := os.RemoveAll(filepath.Dir(usagePath(tr))); err != nil {
		t.Fatal(err)
	}
	mkdirAll(t, filepath.Dir(usagePath(tr)))
	s.Refresh()

	for attempt := 0; ; attempt++ {
		writeUsage(t, tr, nowMs(), float64(40+attempt), nowMs()+3_600_000)
		if waitReal(s, func(x *Snapshot) bool { return fiveHourPercent(x) >= 40 }, time.Second) {
			break
		}
		if attempt >= 7 {
			t.Fatal("usage.json not followed after agtop/ was replaced")
		}
	}
}
