package store

import (
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

const (
	pid1 = 4_100_001
	pid2 = 4_100_002
	pid3 = 4_100_003
)

var contextAt = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC).UnixMilli()

// mainAssistant is an assistant line of a main session with used tokens of context.
func mainAssistant(sessionID string, n int, model string, used int) string {
	return line(obj{"type": "assistant", "timestamp": ts(n), "sessionId": sessionID, "message": obj{
		"id": "msg_" + sessionID + "_" + strconv.Itoa(n), "model": model,
		"content": arr{obj{"type": "text", "text": "SECRET"}},
		"usage":   obj{"input_tokens": used, "output_tokens": 1},
	}})
}

// addSession makes sessionID live at pid (synthetic; isPidAlive decides) with a transcript.
func addSession(t *testing.T, tr tree, sessionID string, pid int, content string) string {
	t.Helper()
	now := nowMs()
	writeFile(t, tr.registry(pid), jsonOf(obj{"pid": pid, "sessionId": sessionID, "status": "idle", "startedAt": now, "updatedAt": now}))
	p := filepath.Join(tr.project, sessionID+".jsonl")
	writeFile(t, p, content)
	return p
}

func contextStore(t *testing.T, tr tree, dead *sync32, mod func(*Options)) *testStore {
	deps := discovery.NewDeps()
	deps.IsPidAlive = func(pid int) bool { return !dead.has(pid) }
	return newTestStore(t, tr, func(o *Options) {
		o.Watch, o.DiscoveryIntervalMs, o.EmitThrottleMs = false, 60_000, 0
		if mod != nil {
			mod(o)
		}
	}, Deps{Discovery: deps})
}

func bySid(t *testing.T, s *Snapshot, sessionID string) *SessionView {
	t.Helper()
	for _, v := range s.Sessions {
		if v.SessionID == sessionID {
			return v
		}
	}
	t.Fatalf("session %s missing", sessionID)
	return nil
}

func hasSid(s *Snapshot, sessionID string) bool {
	return slices.ContainsFunc(s.Sessions, func(v *SessionView) bool { return v.SessionID == sessionID })
}

func window(size int, source domain.ContextWindowSource) domain.ContextWindow {
	return domain.ContextWindow{Size: size, Source: source}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

// writeContext writes <profile>/agtop/context.json atomically, like the hook.
func writeContext(t *testing.T, tr tree, sessions obj) {
	t.Helper()
	writeAtomic(t, filepath.Join(tr.profile.Dir, "agtop", "context.json"),
		jsonOf(obj{"version": 1, "updatedAt": nowMs(), "sessions": sessions}))
}

func entry(model string, size int, at int64) obj { return obj{"model": model, "size": size, "at": at} }

func TestLearnedContextWindow(t *testing.T) {
	type expect struct {
		sid     string
		window  domain.ContextWindow
		percent float64 // < 0: not checked
	}
	type session struct {
		sid   string
		pid   int
		model string
		used  int
	}
	tests := []struct {
		name     string
		sessions []session
		context  obj
		want     []expect
	}{
		{
			"two sessions of the same model: one passes 200k and both use 1M inferred",
			[]session{{"s-small", pid1, opus, 59_000}, {"s-big", pid2, opus, 279_000}, {"s-other", pid3, "claude-sonnet-4-5", 59_000}},
			nil,
			[]expect{
				{"s-small", window(1_000_000, domain.SourceInferred), 5.9},
				{"s-big", window(1_000_000, domain.SourceInferred), 27.9},
				// A different model is not affected.
				{"s-other", window(200_000, domain.SourceTable), 29.5},
			},
		},
		{
			"with the [1m] suffix the source stays suffix",
			[]session{{"s-1m", pid1, opus + "[1m]", 59_000}, {"s-big", pid2, opus, 279_000}},
			nil,
			[]expect{{"s-1m", window(1_000_000, domain.SourceSuffix), -1}, {"s-big", window(1_000_000, domain.SourceInferred), -1}},
		},
		{
			"reported window: the session uses its own; another of the same model inherits it (model)",
			[]session{{"s-a", pid1, opus, 59_000}, {"s-b", pid2, opus + "[1m]", 59_000}, {"s-c", pid3, opus, 59_000}},
			obj{"s-a": entry(opus, 500_000, contextAt), "s-b": entry(opus+"[1m]", 300_000, contextAt+1)},
			[]expect{
				{"s-a", window(500_000, domain.SourceReported), 11.8},
				{"s-b", window(300_000, domain.SourceReported), -1},
				// Without its own entry: the one of the same full id (not the [1m] variant).
				{"s-c", window(500_000, domain.SourceModel), -1},
			},
		},
		{
			"the window of an id does not apply to its [1m] variant",
			[]session{{"s-a", pid1, "claude-sonnet-4-5", 59_000}, {"s-b", pid2, "claude-sonnet-4-5[1m]", 350_000}},
			obj{"s-a": entry("claude-sonnet-4-5", 200_000, contextAt)},
			[]expect{{"s-b", window(1_000_000, domain.SourceSuffix), -1}},
		},
		{
			"nor the window of the [1m] variant to the plain id",
			[]session{{"s-a", pid1, "claude-sonnet-4-5[1m]", 59_000}, {"s-b", pid2, "claude-sonnet-4-5", 59_000}},
			obj{"s-a": entry("claude-sonnet-4-5[1m]", 1_000_000, contextAt)},
			[]expect{{"s-b", window(200_000, domain.SourceTable), -1}},
		},
		{
			"an entry of another model is ignored; a reported window teaches nothing",
			[]session{{"s-a", pid1, opus, 279_000}, {"s-b", pid2, opus, 59_000}, {"s-s", pid3, "claude-sonnet-4-5", 59_000}},
			obj{"s-s": entry(opus, 200_000, contextAt), "s-a": entry(opus, 400_000, contextAt+1)},
			[]expect{
				{"s-a", window(400_000, domain.SourceReported), -1},
				{"s-b", window(400_000, domain.SourceModel), -1},
				{"s-s", window(200_000, domain.SourceTable), -1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := makeTree(t)
			for _, s := range tt.sessions {
				addSession(t, tr, s.sid, s.pid, mainAssistant(s.sid, 1, s.model, s.used))
			}
			if tt.context != nil {
				writeContext(t, tr, tt.context)
			}
			s := contextStore(t, tr, &sync32{}, nil)
			s.Start()
			snap := s.Snapshot()
			for _, e := range tt.want {
				v := bySid(t, snap, e.sid)
				if v.ContextWindow != e.window || (e.percent >= 0 && !near(v.ContextPercent, e.percent)) {
					t.Errorf("%s: window %+v percent %v, want %+v %v", e.sid, v.ContextWindow, v.ContextPercent, e.window, e.percent)
				}
			}
		})
	}
}

func TestLearnedContextWindowAgents(t *testing.T) {
	t.Run("agents of the same model use the learned window (and teach too)", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
		writeAgentFiles(t, tr)
		// Another haiku session above 200k teaches 1M for haiku.
		addSession(t, tr, "s-haiku", pid1, mainAssistant("s-haiku", 1, haiku, 300_000))
		s := contextStore(t, tr, &sync32{}, nil)
		s.Start()
		main := bySid(t, s.Snapshot(), sid)
		if c := main.Agents[agentID].Context; c.ContextWindow != window(1_000_000, domain.SourceInferred) {
			t.Errorf("agent = %+v", c)
		}
		// The model of the session itself (opus-4-5) is not affected.
		if main.ContextWindow != window(200_000, domain.SourceTable) {
			t.Errorf("session = %+v", main.ContextWindow)
		}
	})

	t.Run("an agent above 200k teaches the window to sessions of the same model", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
		writeAgentFiles(t, tr)
		appendFile(t, filepath.Join(tr.subagents, "agent-"+agentID+".jsonl"), line(obj{
			"type": "assistant", "timestamp": ts(9), "isSidechain": true, "agentId": agentID,
			"message": obj{"id": "sm_big", "model": haiku, "content": arr{obj{"type": "text", "text": "SECRET"}},
				"usage": obj{"input_tokens": 250_000, "output_tokens": 1}},
		}))
		addSession(t, tr, "s-haiku", pid1, mainAssistant("s-haiku", 1, haiku, 10_000))
		s := contextStore(t, tr, &sync32{}, nil)
		s.Start()
		v := bySid(t, s.Snapshot(), "s-haiku")
		if v.ContextWindow != window(1_000_000, domain.SourceInferred) || !near(v.ContextPercent, 1) {
			t.Errorf("view = %+v %v", v.ContextWindow, v.ContextPercent)
		}
	})

	t.Run("agents inherit the window reported for their model (source model)", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFile(t, tr.transcript, assistantAgentLaunch(40_000)+asyncLaunchedResult())
		writeAgentFiles(t, tr)
		addSession(t, tr, "s-haiku", pid1, mainAssistant("s-haiku", 1, haiku, 10_000))
		writeContext(t, tr, obj{"s-haiku": entry(haiku, 400_000, contextAt)})
		s := contextStore(t, tr, &sync32{}, nil)
		s.Start()
		if c := bySid(t, s.Snapshot(), sid).Agents[agentID].Context; c.ContextWindow != window(400_000, domain.SourceModel) {
			t.Errorf("agent = %+v", c)
		}
	})
}

func TestLearnedContextWindowOverTime(t *testing.T) {
	t.Run("a stale session also teaches while it exists", func(t *testing.T) {
		tr := makeTree(t)
		addSession(t, tr, "s-big", pid2, mainAssistant("s-big", 1, opus, 279_000))
		dead := &sync32{}
		s := contextStore(t, tr, dead, func(o *Options) { o.StaleMs = 60_000 })
		s.Start()
		dead.add(pid2)
		s.Refresh()
		if bySid(t, s.Snapshot(), "s-big").Liveness != Stale {
			t.Fatal("not stale")
		}
		// A new session of the same model, seen only after the other turned stale.
		addSession(t, tr, "s-late", pid1, mainAssistant("s-late", 1, opus, 59_000))
		s.Refresh()
		after := s.waitFor(t, func(x *Snapshot) bool { return hasSid(x, "s-late") }, 0)
		if bySid(t, after, "s-big").Liveness != Stale || bySid(t, after, "s-late").ContextWindow != window(1_000_000, domain.SourceInferred) {
			t.Error("stale session did not teach")
		}
	})

	t.Run("new learning renews only the views of the same model", func(t *testing.T) {
		tr := makeTree(t)
		addSession(t, tr, "s-sonnet", pid1, mainAssistant("s-sonnet", 1, "claude-sonnet-4-5", 59_000))
		growing := addSession(t, tr, "s-opus", pid2, mainAssistant("s-opus", 1, opus, 59_000))
		addSession(t, tr, "s-opus-2", pid3, mainAssistant("s-opus-2", 1, opus, 40_000))
		s := contextStore(t, tr, &sync32{}, nil)
		s.Start()
		before := s.Snapshot()
		if bySid(t, before, "s-opus-2").ContextWindow != window(200_000, domain.SourceTable) {
			t.Fatal("unexpected window before")
		}
		appendFile(t, growing, mainAssistant("s-opus", 2, opus, 279_000))
		s.Refresh()
		after := s.waitFor(t, func(x *Snapshot) bool { return bySid(t, x, "s-opus").ContextWindow.Source == domain.SourceInferred }, 0)
		if after == before {
			t.Fatal("same snapshot")
		}
		if v := bySid(t, after, "s-opus-2"); v.ContextWindow != window(1_000_000, domain.SourceInferred) || !near(v.ContextPercent, 4) {
			t.Errorf("s-opus-2 = %+v %v", v.ContextWindow, v.ContextPercent)
		}
		// Different model: same view (identity kept).
		if bySid(t, after, "s-sonnet") != bySid(t, before, "s-sonnet") {
			t.Error("view of another model renewed")
		}
	})

	t.Run("a changed file renews the views; a removed one goes back to the table", func(t *testing.T) {
		tr := makeTree(t)
		addSession(t, tr, "s-a", pid1, mainAssistant("s-a", 1, opus, 59_000))
		addSession(t, tr, "s-b", pid2, mainAssistant("s-b", 1, "claude-sonnet-4-5", 59_000))
		s := contextStore(t, tr, &sync32{}, func(o *Options) { o.DiscoveryIntervalMs = 1_000 })
		s.Start()
		before := s.Snapshot()
		if bySid(t, before, "s-a").ContextWindow.Source != domain.SourceTable {
			t.Fatal("unexpected source before")
		}
		writeContext(t, tr, obj{"s-a": entry(opus, 700_000, contextAt)})
		after := s.waitFor(t, func(x *Snapshot) bool { return bySid(t, x, "s-a").ContextWindow.Source == domain.SourceReported }, 500)
		if bySid(t, after, "s-a").ContextWindow.Size != 700_000 || bySid(t, after, "s-b") != bySid(t, before, "s-b") {
			t.Error("unexpected views after the change")
		}
		removeFile(t, filepath.Join(tr.profile.Dir, "agtop", "context.json"))
		gone := s.waitFor(t, func(x *Snapshot) bool { return bySid(t, x, "s-a").ContextWindow.Source == domain.SourceTable }, 500)
		if bySid(t, gone, "s-a").ContextWindow.Size != 200_000 {
			t.Error("window not back to the table")
		}
	})

	t.Run("invalid context.json: one short error, no content, nothing reported", func(t *testing.T) {
		tr := makeTree(t)
		addSession(t, tr, "s-a", pid1, mainAssistant("s-a", 1, opus, 59_000))
		file := filepath.Join(tr.profile.Dir, "agtop", "context.json")
		mkdirAll(t, filepath.Dir(file))
		writeFile(t, file, jsonOf(obj{"version": 1, "updatedAt": contextAt, "cwd": "SECRET", "sessions": obj{}}))
		s := contextStore(t, tr, &sync32{}, nil)
		s.Start()
		s.Refresh()
		snap := s.Snapshot()
		if bySid(t, snap, "s-a").ContextWindow.Source != domain.SourceTable {
			t.Error("reported window from an invalid file")
		}
		var errs []string
		for _, e := range snap.Errors {
			if strings.Contains(e, "context") {
				errs = append(errs, e)
			}
		}
		if want := []string{"context " + file + ": INVALID"}; !slices.Equal(errs, want) {
			t.Errorf("errors = %q", errs)
		}
		if strings.Contains(jsonOf(snap), "SECRET") {
			t.Error("snapshot leaks file content")
		}
	})

	t.Run("the agtop/ watcher re-reads context.json without waiting for a scan", func(t *testing.T) {
		tr := makeTree(t)
		addSession(t, tr, "s-a", pid1, mainAssistant("s-a", 1, opus, 59_000))
		writeContext(t, tr, obj{})
		w := &fakeWatchers{}
		deps := discovery.NewDeps()
		deps.IsPidAlive = func(int) bool { return true }
		s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.EmitThrottleMs = 3_600_000, 0 },
			Deps{Discovery: deps, NewWatcher: w.factory})
		s.Start()
		if bySid(t, s.Snapshot(), "s-a").ContextWindow.Source != domain.SourceTable {
			t.Fatal("unexpected source before")
		}
		writeContext(t, tr, obj{"s-a": entry(opus, 900_000, contextAt)})
		w.get(t, watcher.KindUsage).emit(watcher.Change, filepath.Join(tr.profile.Dir, "agtop", "context.json"))
		snap := s.waitFor(t, func(x *Snapshot) bool { return bySid(t, x, "s-a").ContextWindow.Source == domain.SourceReported }, 0)
		if bySid(t, snap, "s-a").ContextWindow.Size != 900_000 {
			t.Error("wrong reported size")
		}
	})
}
