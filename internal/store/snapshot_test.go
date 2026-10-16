package store

import (
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/domain/reduce"
)

var (
	profile1 = config.ProfileDir{Label: "p1", Dir: "/fake/p1/.claude", Source: config.SourceConfig}
	profile2 = config.ProfileDir{Label: "p2", Dir: "/fake/p2/.claude", Source: config.SourceConfig}
	testNow  = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
)

func discovered(sessionID string, profile config.ProfileDir, mod func(*discovery.DiscoveredSession)) discovery.DiscoveredSession {
	d := discovery.DiscoveredSession{
		LiveSessionRecord: discovery.LiveSessionRecord{
			Profile: profile, PID: 100, SessionID: sessionID, Activity: discovery.ActivityBusy, Alive: true,
		},
		Key: profile.Label + ":" + sessionID,
	}
	if mod != nil {
		mod(&d)
	}
	return d
}

func testEntry(sessionID string, firstSeenAt int64, profile config.ProfileDir, mod func(*discovery.DiscoveredSession)) *sessionEntry {
	if firstSeenAt == 0 {
		firstSeenAt = testNow
	}
	return newEntry(discovered(sessionID, profile, mod), firstSeenAt, domain.ClaudeCode)
}

func withState(e *sessionEntry, patch func(*domain.SessionState)) *sessionEntry {
	s := e.state
	patch(&s)
	e.setState(s)
	return e
}

func TestDiscoveredChanged(t *testing.T) {
	a := discovered("s", profile1, nil)
	tests := []struct {
		name string
		mod  func(*discovery.DiscoveredSession)
		want bool
	}{
		{"updatedAt is not used", func(d *discovery.DiscoveredSession) { d.UpdatedAt = 5 }, false},
		{"pid", func(d *discovery.DiscoveredSession) { d.PID = 7 }, true},
		{"name", func(d *discovery.DiscoveredSession) { d.Name = "n" }, true},
		{"custom title", func(d *discovery.DiscoveredSession) { d.Files.CustomTitle = "t" }, true},
		{"transcript", func(d *discovery.DiscoveredSession) { d.Files.TranscriptPath = "/x.jsonl" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := a
			tt.mod(&b)
			if got := discoveredChanged(a, b); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareEntries(t *testing.T) {
	started := func(ms int64) func(*discovery.DiscoveredSession) {
		return func(d *discovery.DiscoveredSession) { d.StartedAt = ms }
	}
	late := testEntry("a", 0, profile1, started(300))
	early := testEntry("b", 0, profile1, started(100))
	seen := testEntry("c", 200, profile1, nil)
	tieA := testEntry("d", 0, profile1, started(400))
	tieB := testEntry("e", 0, profile1, started(400))
	sorted := []*sessionEntry{tieB, late, seen, tieA, early}
	slices.SortFunc(sorted, compareEntries)
	var ids []string
	for _, e := range sorted {
		ids = append(ids, e.sessionID)
	}
	if want := []string{"b", "c", "a", "d", "e"}; !slices.Equal(ids, want) {
		t.Errorf("got %v, want %v", ids, want)
	}
}

func TestProfileStatuses(t *testing.T) {
	t.Run("one status per profile, in order, with zeros before any discovery", func(t *testing.T) {
		got := profileStatuses([]config.ProfileDir{profile1, profile2}, domain.ClaudeCode, nil, nil)
		if len(got) != 2 || got[0].Label != "p1" || got[0].LiveCount != 0 || got[1].Label != "p2" || got[1].LiveCount != 0 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("counts live sessions and issues by reason", func(t *testing.T) {
		result := &discovery.Result{ScannedAt: testNow, Profiles: []discovery.ProfileDiscovery{{
			Profile: profile1,
			Live:    []discovery.DiscoveredSession{discovered("s1", profile1, nil), discovered("s2", profile1, nil)},
			Diagnostics: discovery.Diagnostics{SkippedEntries: 3, Issues: []discovery.RegistryIssue{
				{File: "1.json", Reason: discovery.IssueInvalidJSON},
				{File: "2.json", Reason: discovery.IssueInvalidJSON},
				{File: "3.json", Reason: discovery.IssueTooLarge},
			}},
		}}}
		got := profileStatuses([]config.ProfileDir{profile1, profile2}, domain.ClaudeCode, result, nil)
		p1, p2 := got[0], got[1]
		wantIssues := map[discovery.IssueReason]int{discovery.IssueInvalidJSON: 2, discovery.IssueTooLarge: 1}
		if p1.LiveCount != 2 || p1.SkippedEntries != 3 || !maps.Equal(p1.Issues, wantIssues) {
			t.Errorf("p1 = %+v", p1)
		}
		if p2.LiveCount != 0 || p2.SessionsDirMissing || len(p2.Issues) != 0 {
			t.Errorf("p2 = %+v", p2)
		}
	})
}

func histogramOf(out []domain.ProfileHistogram, label string) domain.ActivityHistogram {
	for _, p := range out {
		if p.Label == label {
			return p.Histogram
		}
	}
	return nil
}

func TestActivityByProfile(t *testing.T) {
	bucket := domain.ActivityBucket(testNow)
	t.Run("merges present and retired sessions per profile and prunes the window", func(t *testing.T) {
		e1 := withState(testEntry("s1", 0, profile1, nil), func(s *domain.SessionState) {
			s.ActivityHistogram = domain.ActivityHistogram{bucket: 2}
		})
		e2 := withState(testEntry("s2", 0, profile2, nil), func(s *domain.SessionState) {
			s.ActivityHistogram = domain.ActivityHistogram{bucket: 1}
		})
		old := activityWindowStart(testNow) - domain.ActivityBucketMs
		retired := map[string]retiredActivity{
			"p1:gone": {label: "p1", histogram: domain.ActivityHistogram{bucket: 3}},
			"p1:old":  {label: "p1", histogram: domain.ActivityHistogram{old: 9}},
		}
		out := activityByProfile([]config.ProfileDir{profile1, profile2}, []*sessionEntry{e1, e2}, retired, testNow)
		if len(out) != 2 || out[0].Label != "p1" || out[1].Label != "p2" {
			t.Fatalf("labels: %+v", out)
		}
		if got := histogramOf(out, "p1"); !maps.Equal(got, domain.ActivityHistogram{bucket: 5}) {
			t.Errorf("p1 = %v", got)
		}
		if got := histogramOf(out, "p2"); !maps.Equal(got, domain.ActivityHistogram{bucket: 1}) {
			t.Errorf("p2 = %v", got)
		}
		// A retired entry entirely out of the window is dropped from the map.
		if keys := slices.Collect(maps.Keys(retired)); !slices.Equal(keys, []string{"p1:gone"}) {
			t.Errorf("retired = %v", keys)
		}
	})
}

func TestProjector(t *testing.T) {
	t.Run("projects the PR links and the activity of each agent", func(t *testing.T) {
		p := projector{newContextLearner(nil)}
		prs := []domain.PullRequestRef{{Owner: "acme", Repo: "app", Number: 7, URL: "https://github.com/acme/app/pull/7"}}
		running := reduce.NewAgentState()
		running.Status = domain.StatusRunning
		running.PendingTools = []domain.PendingEntry{{ID: "t1", PendingTool: domain.PendingTool{Name: "Bash", CommandCategory: domain.CmdBuild}}}
		launched := reduce.NewAgentState()
		launched.Status = domain.StatusLaunched
		done := reduce.NewAgentState()
		done.Status = domain.StatusCompleted
		e := withState(testEntry("s-prs", 0, profile1, nil), func(s *domain.SessionState) {
			s.PullRequests = prs
			s.Agents = map[string]domain.AgentState{"a1": running, "a2": launched, "a3": done}
		})
		v := p.project(e)
		if !reflect.DeepEqual(v.PRs, prs) {
			t.Errorf("prs = %v", v.PRs)
		}
		tests := []struct {
			key  string
			want *domain.AgentActivity
		}{
			{"a1", &domain.AgentActivity{Kind: domain.KindBuilding}},
			{"a2", &domain.AgentActivity{Kind: domain.KindStarting}},
			{"a3", nil},
		}
		for _, tt := range tests {
			if got := v.Agents[tt.key].Activity; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s activity = %v, want %v", tt.key, got, tt.want)
			}
		}
		if got := p.project(testEntry("s-none", 0, profile1, nil)).PRs; got == nil || len(got) != 0 {
			t.Errorf("prs without links = %v", got)
		}
	})

	t.Run("display title fallbacks", func(t *testing.T) {
		p := projector{newContextLearner(nil)}
		withCwd := func(name string) func(*discovery.DiscoveredSession) {
			return func(d *discovery.DiscoveredSession) { d.Cwd, d.Name = "/home/dev/demo-app", name }
		}
		tests := []struct {
			name string
			e    *sessionEntry
			want string
		}{
			{"registry name", testEntry("sess-x", 0, profile1, withCwd("reg-name")), "reg-name"},
			{"ai title", withState(testEntry("sess-y", 0, profile1, withCwd("")), func(s *domain.SessionState) { s.AITitle = "AI title" }), "AI title"},
			{"last cwd segment", testEntry("sess-z", 0, profile1, withCwd("")), "demo-app"},
			{"session id", testEntry("sess-w", 0, profile1, nil), "sess-w"},
		}
		for _, tt := range tests {
			if got := p.project(tt.e).DisplayTitle; got != tt.want {
				t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
			}
		}
	})

	t.Run("reuses the view while inputs are the same; liveness change makes a new one", func(t *testing.T) {
		p := projector{newContextLearner(nil)}
		e := testEntry("s1", 0, profile1, nil)
		v1 := p.project(e)
		if p.project(e) != v1 {
			t.Error("view not reused")
		}
		e.liveness, e.staleSince = Stale, testNow
		v2 := p.project(e)
		if v2 == v1 || v2.Liveness != Stale || v2.StaleSince != testNow {
			t.Errorf("v2 = %+v", v2)
		}
	})

	t.Run("learning a model renews the agent views", func(t *testing.T) {
		l := newContextLearner(nil)
		p := projector{l}
		agent := reduce.NewAgentState()
		agent.Model = "claude-opus-4-5"
		agent.Context.Used = 10
		e := withState(testEntry("s1", 0, profile1, nil), func(s *domain.SessionState) {
			s.Agents = map[string]domain.AgentState{"a1": agent}
		})
		a1 := p.project(e).Agents["a1"]
		withState(e, func(s *domain.SessionState) { s.LastActivityAt = testNow })
		if got := p.project(e).Agents["a1"]; !reflect.DeepEqual(got, a1) {
			t.Errorf("unchanged agent view differs: %+v", got)
		}
		big := e.state
		big.Model = &domain.ModelInfo{ID: "claude-opus-4-5"}
		big.Context.Used = 300_000
		l.learnFrom(big, "")
		if got := p.project(e).Agents["a1"].Context.Size; got != domain.ExtendedContextWindow {
			t.Errorf("agent window = %d", got)
		}
	})
}

func TestProjectorReportedWindows(t *testing.T) {
	reportsWith := func(size int) domain.ContextReports {
		return domain.NewContextReports(domain.ContextFile{Version: 1, UpdatedAt: 1,
			Sessions: map[string]domain.ContextEntry{"s1": {Model: "claude-opus-4-5", Size: size, At: 1}}})
	}
	reports := reportsWith(400_000)
	l := newContextLearner(func(string) domain.ContextReports { return reports })
	p := projector{l}
	agent := reduce.NewAgentState()
	agent.Model = "claude-opus-4-5"
	agent.Context.Used = 10
	e := withState(testEntry("s1", 0, profile1, nil), func(s *domain.SessionState) {
		s.Model = &domain.ModelInfo{ID: "claude-opus-4-5"}
		s.Agents = map[string]domain.AgentState{"a1": agent}
	})
	v1 := p.project(e)
	if v1.ContextWindow != (domain.ContextWindow{Size: 400_000, Source: domain.SourceReported}) {
		t.Errorf("window = %+v", v1.ContextWindow)
	}
	if c := v1.Agents["a1"].Context; c.Size != 400_000 || c.Source != domain.SourceModel {
		t.Errorf("agent = %+v", c)
	}
	if p.project(e) != v1 {
		t.Error("view not reused")
	}
	// Same values in a new object (the file was re-read): same view.
	reports = reportsWith(400_000)
	if p.project(e) != v1 {
		t.Error("view renewed without a change")
	}
	reports = reportsWith(600_000)
	v2 := p.project(e)
	if v2 == v1 || v2.ContextWindow != (domain.ContextWindow{Size: 600_000, Source: domain.SourceReported}) ||
		v2.Agents["a1"].Context.Size != 600_000 {
		t.Errorf("v2 = %+v", v2)
	}
}

func TestBuildSnapshot(t *testing.T) {
	t.Run("EmptySnapshot is empty", func(t *testing.T) {
		if len(EmptySnapshot.Sessions) != 0 || EmptySnapshot.ScannedAt != 0 {
			t.Errorf("%+v", EmptySnapshot)
		}
	})
	t.Run("sorts sessions, copies errors and learns before projecting", func(t *testing.T) {
		l := newContextLearner(nil)
		opus := func(used int) func(*domain.SessionState) {
			return func(s *domain.SessionState) {
				s.Model = &domain.ModelInfo{ID: "claude-opus-4-5"}
				s.Context.Used = used
			}
		}
		big := withState(testEntry("big", 0, profile1, func(d *discovery.DiscoveredSession) { d.StartedAt = 2 }), opus(300_000))
		small := withState(testEntry("small", 0, profile1, func(d *discovery.DiscoveredSession) { d.StartedAt = 1 }), opus(100_000))
		errs := []string{"read /x: ENOENT"}
		snap := buildSnapshot(snapshotInput{
			profiles: []config.ProfileDir{profile1}, provider: domain.ClaudeCode,
			entries: []*sessionEntry{big, small}, errors: errs, retired: map[string]retiredActivity{},
			now: testNow, learner: l,
		})
		if snap.Sessions[0].SessionID != "small" || snap.Sessions[1].SessionID != "big" {
			t.Errorf("order: %s, %s", snap.Sessions[0].SessionID, snap.Sessions[1].SessionID)
		}
		// small was projected before big but already sees the window big taught.
		if w := snap.Sessions[0].ContextWindow; w != (domain.ContextWindow{Size: domain.ExtendedContextWindow, Source: domain.SourceInferred}) {
			t.Errorf("window = %+v", w)
		}
		if !slices.Equal(snap.Errors, errs) || &snap.Errors[0] == &errs[0] {
			t.Error("errors not copied")
		}
		if len(snap.ActivityByProfile) != 1 || snap.ActivityByProfile[0].Label != "p1" {
			t.Errorf("activity = %+v", snap.ActivityByProfile)
		}
	})
}
