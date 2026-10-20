package store

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/domain/reduce"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/transcript"
)

var (
	activityT0      = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	fixtureActivity = domain.ActivityHistogram{activityT0: 14, activityT0 + 2*bucketMs: 3}
)

const (
	bucketMs     = domain.ActivityBucketMs
	activityHour = 60 * 60 * 1000
)

func fixtureLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../testdata/fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

func writeFixtures(t *testing.T, tr tree) {
	t.Helper()
	writeFile(t, tr.transcript, strings.Join(fixtureLines(t, "session-main.jsonl"), "\n"))
	mkdirAll(t, tr.subagents)
	writeFile(t, filepath.Join(tr.subagents, "agent-a-bg-001.jsonl"), strings.Join(fixtureLines(t, "subagent-handback.jsonl"), "\n"))
}

// mainFixtureActivity is the activity of the main fixture transcript alone, from the reducer.
func mainFixtureActivity(t *testing.T) domain.ActivityHistogram {
	var events []domain.TranscriptEvent
	for _, l := range fixtureLines(t, "session-main.jsonl") {
		events = append(events, transcript.NormalizeLine(l)...)
	}
	return reduce.SessionEvents(reduce.NewSessionState(sid), events).ActivityHistogram
}

func activityOf(t *testing.T, s *Snapshot, label string) domain.ActivityHistogram {
	t.Helper()
	for _, p := range s.ActivityByProfile {
		if p.Label == label {
			return p.Histogram
		}
	}
	t.Fatalf("profile %s missing", label)
	return nil
}

func expectActivity(t *testing.T, s *Snapshot, want domain.ActivityHistogram) {
	t.Helper()
	if got := activityOf(t, s, "test"); !maps.Equal(got, want) {
		t.Errorf("activity = %v, want %v", got, want)
	}
}

// activityStore is a scan-only store with the clock at start (the fixtures are from 2026-01-01
// and the window is pruned by now).
func activityStore(t *testing.T, tr tree, start int64, mod func(*Options), deps Deps) *testStore {
	deps.Clock = newFakeClock(start)
	return newTestStore(t, tr, func(o *Options) {
		o.Watch, o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = false, 60_000, 1_000, 0
		if mod != nil {
			mod(o)
		}
	}, deps)
}

// retire removes the session from the registry and waits staleMs: its activity goes to retired.
func retire(t *testing.T, tr tree, s *testStore) {
	t.Helper()
	removeFile(t, tr.registry(os.Getpid()))
	s.Refresh()
	s.advance(1_000)
	s.Refresh()
	if len(s.Snapshot().Sessions) != 0 {
		t.Fatal("session not removed")
	}
	expectActivity(t, s.Snapshot(), fixtureActivity)
}

func TestActivityByProfileInStore(t *testing.T) {
	t.Run("one key per profile, in profiles order, even empty", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		emptyDir := filepath.Join(filepath.Dir(tr.profile.Dir), ".claude-vazio")
		mkdirAll(t, filepath.Join(emptyDir, "sessions"))
		other := config.ProfileDir{Label: "vazio", Dir: emptyDir, Source: config.SourceConfig}
		s := activityStore(t, tr, activityT0+4*bucketMs, func(o *Options) {
			o.Profiles = []config.ProfileDir{other, tr.profile}
		}, Deps{})
		if len(s.Snapshot().ActivityByProfile) != 0 {
			t.Fatal("activity before start")
		}
		s.Start()
		snap := s.Snapshot()
		if len(snap.ActivityByProfile) != 2 || snap.ActivityByProfile[0].Label != "vazio" || snap.ActivityByProfile[1].Label != "test" {
			t.Fatalf("profiles = %+v", snap.ActivityByProfile)
		}
		if len(activityOf(t, snap, "vazio")) != 0 {
			t.Error("empty profile has activity")
		}
	})

	tests := []struct {
		name  string
		start int64
		want  domain.ActivityHistogram
	}{
		// Session: 8 in the 1st bucket and 3 in the one of 10:01; sub-agent: 6 in the 1st.
		{"fixture counts: assistant lines of the session and the sub-agent", activityT0 + 4*bucketMs, fixtureActivity},
		// Window that ends 119 buckets after 10:01: only that bucket still fits.
		{"buckets out of the window of now leave the snapshot", activityT0 + 2*bucketMs + 119*bucketMs,
			domain.ActivityHistogram{activityT0 + 2*bucketMs: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := makeTree(t)
			writeRegistry(t, tr, nil)
			writeFixtures(t, tr)
			s := activityStore(t, tr, tt.start, nil, Deps{})
			s.Start()
			expectActivity(t, s.Snapshot(), tt.want)
		})
	}

	t.Run("a removed session keeps its contribution; recreated with the same key it does not double", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		s := activityStore(t, tr, activityT0+4*bucketMs, nil, Deps{})
		s.Start()
		if a := only(t, s.Snapshot()).Agents["a-bg-001"]; a == nil || a.Status != domain.StatusCompleted {
			t.Fatalf("agent = %+v", a)
		}
		expectActivity(t, s.Snapshot(), fixtureActivity)
		retire(t, tr, s)
		// --resume: the same key comes back and the transcript is read from the start.
		writeRegistry(t, tr, nil)
		s.Refresh()
		snap := s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && len(x.Sessions[0].Agents) > 0 }, 0)
		expectActivity(t, snap, fixtureActivity)
	})

	t.Run("recreated session: while its first rebuild runs the removed contribution stays", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		// Holds the read of the sub-agent transcript: the rebuild stops in the middle.
		var holding atomic.Bool
		held := newGate()
		tailers := &tailerCounter{wrap: func(p string, inner *tailer.Tailer) func() (tailer.Result, error) {
			return func() (tailer.Result, error) {
				if holding.Load() && strings.HasPrefix(filepath.Base(p), "agent-") {
					held.wait()
				}
				return inner.Read()
			}
		}}
		s := activityStore(t, tr, activityT0+4*bucketMs, nil, Deps{NewTailer: tailers.factory})
		s.Start()
		retire(t, tr, s)

		holding.Store(true)
		writeRegistry(t, tr, nil)
		refreshed := make(chan struct{})
		go func() { s.Refresh(); close(refreshed) }()
		// The actor is inside the rebuild: the published snapshot is still the previous one.
		time.Sleep(20 * time.Millisecond)
		mid := s.Snapshot()
		if len(mid.Sessions) != 0 {
			t.Errorf("half-built session published: %d sessions", len(mid.Sessions))
		}
		expectActivity(t, mid, fixtureActivity)

		held.release()
		<-refreshed
		done := s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 && len(x.Sessions[0].Agents) > 0 }, 0)
		expectActivity(t, done, fixtureActivity)
	})

	t.Run("recreated without a located transcript keeps the removed contribution", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		s := activityStore(t, tr, activityT0+4*bucketMs, nil, Deps{})
		s.Start()
		retire(t, tr, s)
		// Back in the registry, but the transcript and the session folder are gone.
		removeFile(t, tr.transcript)
		removeFile(t, tr.sessionDir)
		writeRegistry(t, tr, nil)
		s.Refresh()
		snap := s.waitFor(t, func(x *Snapshot) bool { return len(x.Sessions) == 1 }, 0)
		if only(t, snap).LastActivityAt != 0 {
			t.Error("session read a transcript")
		}
		expectActivity(t, snap, fixtureActivity)
	})

	t.Run("a rebuild that fails midway keeps the removed contribution", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		// After the removal, opening the sub-agent tailer fails: the rebuild ends with an error
		// after reading the main transcript.
		var failing atomic.Bool
		base := &tailerCounter{}
		factory := func(p string) (Tailer, error) {
			if failing.Load() && strings.HasPrefix(filepath.Base(p), "agent-") {
				return nil, errBoom
			}
			return base.factory(p)
		}
		s := activityStore(t, tr, activityT0+4*bucketMs, nil, Deps{NewTailer: factory})
		s.Start()
		retire(t, tr, s)

		failing.Store(true)
		writeRegistry(t, tr, nil)
		s.Refresh()
		snap := s.waitFor(t, func(x *Snapshot) bool {
			if len(x.Sessions) != 1 {
				return false
			}
			for _, e := range x.Errors {
				if strings.HasPrefix(e, "session ") {
					return true
				}
			}
			return false
		}, 0)
		// The session only has the main transcript; the removed histogram is still fully added.
		own := mainFixtureActivity(t)
		if len(own) == 0 {
			t.Fatal("main fixture without activity")
		}
		want := maps.Clone(fixtureActivity)
		for k, v := range own {
			want[k] += v
		}
		expectActivity(t, snap, want)
	})

	t.Run("a removed contribution leaves once out of the window", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		s := activityStore(t, tr, activityT0+4*bucketMs, nil, Deps{})
		s.Start()
		retire(t, tr, s)
		// An hour later, another session of the profile forces a new snapshot.
		s.advance(activityHour)
		other := "sess-0002"
		writeFile(t, filepath.Join(tr.project, other+".jsonl"), "")
		now := nowMs()
		writeFile(t, tr.registry(os.Getppid()), jsonOf(obj{"pid": os.Getppid(), "sessionId": other, "startedAt": now, "updatedAt": now}))
		s.Refresh()
		snap := s.Snapshot()
		if len(snap.Sessions) != 1 || snap.Sessions[0].SessionID != other {
			t.Fatalf("sessions = %s", summary(snap))
		}
		expectActivity(t, snap, domain.ActivityHistogram{})
	})

	t.Run("a rebuild (truncation) reads from the start without doubling", func(t *testing.T) {
		tr := makeTree(t)
		writeRegistry(t, tr, nil)
		writeFixtures(t, tr)
		s := activityStore(t, tr, activityT0+4*bucketMs, nil, Deps{})
		s.Start()
		if got := only(t, s.Snapshot()).DisplayTitle; got != "My custom title" {
			t.Fatalf("title = %q", got)
		}
		// Without the last line (custom-title): smaller than the offset, the tailer resets.
		var lines []string
		for _, l := range fixtureLines(t, "session-main.jsonl") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		writeFile(t, tr.transcript, strings.Join(lines[:len(lines)-1], "\n")+"\n")
		s.Refresh()
		snap := s.waitFor(t, func(x *Snapshot) bool {
			return len(x.Sessions) == 1 && x.Sessions[0].DisplayTitle == "Demo title refactor"
		}, 0)
		expectActivity(t, snap, fixtureActivity)
	})
}
