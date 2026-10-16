package store

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

// The store over an injected source: a fake provider (test-provider, only here) with its own
// discovery and line format.

const testProvider domain.ProviderID = "test-provider"

type fakeSource struct {
	transcript   string
	registryRoot string
	live         atomic.Bool
	mu           sync.Mutex
	forgotten    []string
	registryCall []string
}

func newFakeSource(t *testing.T, tr tree, registryRoot string) *fakeSource {
	p := filepath.Join(tr.project, "fake-session.log")
	writeFile(t, p, "TITLE Fake provider session\nnoise\n")
	s := &fakeSource{transcript: p, registryRoot: registryRoot}
	s.live.Store(true)
	return s
}

func (s *fakeSource) Provider() domain.ProviderID { return testProvider }

func (s *fakeSource) Discover(profiles []config.ProfileDir) (discovery.Result, error) {
	var out discovery.Result
	for _, profile := range profiles {
		pd := discovery.ProfileDiscovery{Profile: profile}
		if s.live.Load() {
			pd.Live = []discovery.DiscoveredSession{{
				LiveSessionRecord: discovery.LiveSessionRecord{Profile: profile, PID: 4321, SessionID: "fake-1",
					Activity: discovery.ActivityBusy, Alive: true},
				Key:   profile.Label + ":fake-1",
				Files: discovery.SessionFiles{TranscriptPath: s.transcript},
			}}
		}
		out.Profiles = append(out.Profiles, pd)
	}
	return out, nil
}

func (s *fakeSource) RegistryRoot(profile config.ProfileDir) (string, bool) {
	s.mu.Lock()
	s.registryCall = append(s.registryCall, profile.Label)
	s.mu.Unlock()
	return s.registryRoot, s.registryRoot != ""
}

func (s *fakeSource) ForgetSession(profile config.ProfileDir, sessionID string) {
	s.mu.Lock()
	s.forgotten = append(s.forgotten, profile.Label+":"+sessionID)
	s.mu.Unlock()
}

func (s *fakeSource) ListAgents(string, map[string]string) []discovery.SubagentFiles { return nil }

func (s *fakeSource) ReadAgentMeta(string) discovery.AgentMetaResult {
	return discovery.AgentMetaResult{Code: "UNUSED"}
}

func (s *fakeSource) FileSignature(string) (string, bool) { return "", false }

func (s *fakeSource) ParseLine(l string) []domain.TranscriptEvent {
	if title, ok := strings.CutPrefix(l, "TITLE "); ok {
		return []domain.TranscriptEvent{domain.AITitleEvent{Title: title}}
	}
	return nil
}

func TestInjectedSource(t *testing.T) {
	t.Run("marks sessions and profiles with the source provider and parses with the source", func(t *testing.T) {
		tr := makeTree(t)
		src := newFakeSource(t, tr, "")
		s := newTestStore(t, tr, func(o *Options) { o.Watch, o.EmitThrottleMs = false, 10 }, Deps{Source: src})
		s.Start()
		snap := s.waitFor(t, func(x *Snapshot) bool {
			return len(x.Sessions) == 1 && x.Sessions[0].DisplayTitle == "Fake provider session"
		}, 0)
		v := only(t, snap)
		if v.Key != "test:fake-1" || v.Provider != testProvider || v.ProfileLabel != "test" || v.PID != 4321 ||
			v.Activity != discovery.ActivityBusy {
			t.Errorf("view = %+v", v)
		}
		if len(snap.Profiles) != 1 || snap.Profiles[0].Label != "test" || snap.Profiles[0].Provider != testProvider ||
			snap.Profiles[0].LiveCount != 1 {
			t.Errorf("profiles = %+v", snap.Profiles)
		}
	})

	t.Run("watches the registry root the source gives and tells it when a session leaves", func(t *testing.T) {
		tr := makeTree(t)
		root := filepath.Join(tr.profile.Dir, "sessions")
		src := newFakeSource(t, tr, root)
		w := &fakeWatchers{}
		s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = 60_000, 0, 10 },
			Deps{Source: src, NewWatcher: w.factory})
		s.Start()
		if !slices.Equal(src.registryCall, []string{"test"}) {
			t.Errorf("registry calls = %v", src.registryCall)
		}
		if got := w.get(t, watcher.KindSessions).target; got != (watcher.Target{Kind: watcher.KindSessions, Root: root}) {
			t.Errorf("target = %+v", got)
		}
		snap := s.Snapshot()
		if only(t, snap).Provider != testProvider || len(snap.Errors) != 0 {
			t.Errorf("provider %s errors %q", only(t, snap).Provider, snap.Errors)
		}
		// Gone from the source: stale on one scan, removed (and forgotten) on the next.
		src.live.Store(false)
		s.Refresh()
		if only(t, s.Snapshot()).Liveness != Stale {
			t.Fatal("not stale")
		}
		s.Refresh()
		if len(s.Snapshot().Sessions) != 0 {
			t.Fatal("not removed")
		}
		src.mu.Lock()
		defer src.mu.Unlock()
		if !slices.Equal(src.forgotten, []string{"test:fake-1"}) {
			t.Errorf("forgotten = %v", src.forgotten)
		}
	})
}
