package store

import (
	"maps"
	"os"
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
	"github.com/danielfnfaria/agtop/internal/source"
)

// The store over an injected source: a fake provider (test-provider, only here) with its own
// discovery and line format.

const (
	testProvider  domain.ProviderID = "test-provider"
	otherProvider domain.ProviderID = "other-provider"
)

type fakeSource struct {
	provider     domain.ProviderID
	transcript   string
	registryRoot string
	live         atomic.Bool
	mu           sync.Mutex
	forgotten    []string
	registryCall []string
}

func newFakeSource(t *testing.T, provider domain.ProviderID, dir string, registryRoot string) *fakeSource {
	p := filepath.Join(dir, "fake-session.log")
	writeFile(t, p, "TITLE Fake provider session\nnoise\n")
	s := &fakeSource{provider: provider, transcript: p, registryRoot: registryRoot}
	s.live.Store(true)
	return s
}

func (s *fakeSource) Provider() domain.ProviderID { return s.provider }

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

func (s *fakeSource) RegistryWatch(profile config.ProfileDir) (watcher.Target, bool) {
	s.mu.Lock()
	s.registryCall = append(s.registryCall, profile.Label)
	s.mu.Unlock()
	return watcher.Target{Kind: watcher.KindSessions, Root: s.registryRoot}, s.registryRoot != ""
}

func (s *fakeSource) SessionWatch(string, string) (watcher.Target, bool) {
	return watcher.Target{}, false
}

func (s *fakeSource) ForgetSession(profile config.ProfileDir, sessionID string) {
	s.mu.Lock()
	s.forgotten = append(s.forgotten, profile.Label+":"+sessionID)
	s.mu.Unlock()
}

func (s *fakeSource) OpenTranscript(ref string) (source.TranscriptReader, error) {
	return &fakeReader{path: ref}, nil
}

func (s *fakeSource) ListAgents(string, map[string]string) []discovery.SubagentFiles { return nil }

func (s *fakeSource) ReadAgentMeta(string) discovery.AgentMetaResult {
	return discovery.AgentMetaResult{Code: "UNUSED"}
}

func (s *fakeSource) FileSignature(string) (string, bool) { return "", false }

// fakeReader reads its file once and turns the TITLE lines into events.
type fakeReader struct {
	path string
	done bool
}

func (r *fakeReader) Read() (source.TranscriptRead, error) {
	if r.done {
		return source.TranscriptRead{}, nil
	}
	r.done = true
	data, err := os.ReadFile(r.path)
	if err != nil {
		return source.TranscriptRead{}, err
	}
	var out source.TranscriptRead
	for _, l := range strings.Split(string(data), "\n") {
		if title, ok := strings.CutPrefix(l, "TITLE "); ok {
			out.Events = append(out.Events, domain.AITitleEvent{Title: title})
		}
	}
	return out, nil
}

func (r *fakeReader) Close() error { return nil }

// withProviders makes the profiles of the test tree belong to the given providers, in order
// (the first one is the profile of the tree; the others are copies labeled after the provider).
func withProviders(tr tree, providers ...domain.ProviderID) func(*Options) {
	return func(o *Options) {
		o.Profiles = nil
		for i, p := range providers {
			profile := tr.profile
			profile.Provider = p
			if i > 0 {
				profile.Label = string(p)
			}
			o.Profiles = append(o.Profiles, profile)
		}
	}
}

func TestInjectedSource(t *testing.T) {
	t.Run("marks sessions and profiles with the source provider and parses with the source", func(t *testing.T) {
		tr := makeTree(t)
		src := newFakeSource(t, testProvider, tr.project, "")
		s := newTestStore(t, tr, func(o *Options) {
			withProviders(tr, testProvider)(o)
			o.Watch, o.EmitThrottleMs = false, 10
		}, Deps{Sources: []source.Source{src}})
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
		src := newFakeSource(t, testProvider, tr.project, root)
		w := &fakeWatchers{}
		s := newTestStore(t, tr, func(o *Options) {
			withProviders(tr, testProvider)(o)
			o.DiscoveryIntervalMs, o.StaleMs, o.EmitThrottleMs = 60_000, 0, 10
		}, Deps{Sources: []source.Source{src}, NewWatcher: w.factory})
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

func TestTwoSources(t *testing.T) {
	tr := makeTree(t)
	first := newFakeSource(t, testProvider, tr.project, "")
	otherDir := filepath.Join(tr.project, "other")
	mkdirAll(t, otherDir)
	second := newFakeSource(t, otherProvider, otherDir, "")
	s := newTestStore(t, tr, func(o *Options) {
		withProviders(tr, testProvider, otherProvider)(o)
		o.Watch, o.EmitThrottleMs = false, 10
	}, Deps{Sources: []source.Source{first, second}})
	s.Start()
	snap := s.waitFor(t, func(x *Snapshot) bool {
		return len(x.Sessions) == 2 && x.Sessions[0].DisplayTitle != "" && x.Sessions[1].DisplayTitle != ""
	}, 0)
	got := map[string]domain.ProviderID{}
	for _, v := range snap.Sessions {
		got[v.Key] = v.Provider
	}
	want := map[string]domain.ProviderID{"test:fake-1": testProvider, "other-provider:fake-1": otherProvider}
	if !maps.Equal(got, want) {
		t.Errorf("session providers = %v", got)
	}
	gotProfiles := map[string]domain.ProviderID{}
	for _, p := range snap.Profiles {
		gotProfiles[p.Label] = p.Provider
	}
	wantProfiles := map[string]domain.ProviderID{"test": testProvider, "other-provider": otherProvider}
	if !maps.Equal(gotProfiles, wantProfiles) || len(snap.Errors) != 0 {
		t.Errorf("profiles = %v errors %q", gotProfiles, snap.Errors)
	}
}
