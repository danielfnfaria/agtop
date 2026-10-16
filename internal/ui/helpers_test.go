package ui

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/danielfnfaria/agtop/internal/closer"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const now int64 = 1_700_000_000_000

var profiles = []config.ProfileDir{
	{Label: "claude", Dir: "/tmp/a/.claude", Source: "default"},
	{Label: "claude-x", Dir: "/tmp/b/.claude-x", Source: "config"},
}

func profileStatus(p config.ProfileDir) store.ProfileStatus {
	return store.ProfileStatus{Label: p.Label, Provider: domain.ClaudeCode, Dir: p.Dir, Source: p.Source,
		Issues: map[discovery.IssueReason]int{}}
}

func tools(list ...domain.PendingTool) []domain.PendingEntry {
	out := make([]domain.PendingEntry, len(list))
	for i, t := range list {
		out[i] = domain.PendingEntry{ID: fmt.Sprintf("toolu_%d", i), PendingTool: t}
	}
	return out
}

// agent is a test agent; the activity and the context follow the store's rules.
func agent(f func(a *store.AgentView)) *store.AgentView {
	a := &store.AgentView{Type: "general-purpose", Description: "faz algo", Status: domain.StatusRunning}
	if f != nil {
		f(a)
	}
	a.Activity = domain.CurrentAgentActivity(a.Status, a.PendingTools)
	a.Context = store.AgentContext{Used: a.Context.Used,
		ContextUsage: domain.ComputeContextUsage(a.Model, a.Context.Used, nil, domain.ReportedWindows{})}
	return a
}

func session(key string, f func(s *store.SessionView)) *store.SessionView {
	s := &store.SessionView{
		Key: key, SessionID: key, Provider: domain.ClaudeCode, ProfileLabel: "claude", PID: 4242,
		Activity: discovery.ActivityIdle, Liveness: store.Alive,
		ContextWindow: domain.ContextWindow{Size: 200_000, Source: domain.SourceTable},
		DisplayTitle:  key, Agents: map[string]*store.AgentView{}, LastActivityAt: now - 5_000,
	}
	if f != nil {
		f(s)
	}
	return s
}

func snapshot(sessions []*store.SessionView, profs []config.ProfileDir) *store.Snapshot {
	snap := &store.Snapshot{Sessions: sessions, ScannedAt: now, Errors: []string{}}
	for _, p := range profs {
		snap.Profiles = append(snap.Profiles, profileStatus(p))
		snap.ActivityByProfile = append(snap.ActivityByProfile,
			domain.ProfileHistogram{Label: p.Label, Histogram: domain.ActivityHistogram{}})
	}
	return snap
}

func fiveSessions() []*store.SessionView {
	return []*store.SessionView{
		session("claude:s1", func(s *store.SessionView) {
			s.DisplayTitle, s.Activity = "Alpha task", discovery.ActivityBusy
			s.ProjectName, s.Cwd, s.GitBranch = "proj-alpha", "/home/u/proj-alpha", "main"
			s.Model = &domain.ModelInfo{ID: "claude-opus-4-7", Name: "Opus 4.7"}
			s.ContextUsed, s.ContextPercent = 90_000, 45
			s.PendingTools = tools(domain.PendingTool{Name: "Read", Target: "/home/u/proj-alpha/src/foo.ts"})
			s.Agents = map[string]*store.AgentView{
				"ag1": agent(func(a *store.AgentView) { a.StartedAt = now - 10_000 }),
				"ag2": agent(func(a *store.AgentView) { a.Status, a.StartedAt = domain.StatusCompleted, now-20_000 }),
			}
		}),
		session("claude-x:s2", func(s *store.SessionView) {
			s.ProfileLabel, s.DisplayTitle, s.Kind, s.ProjectName = "claude-x", "Bravo task", "sdk-cli", "proj-bravo"
			s.Model = &domain.ModelInfo{ID: "mystery-model"}
			s.ContextWindow = domain.ContextWindow{Size: 200_000, Source: domain.SourceDefault}
			s.ContextUsed, s.ContextPercent, s.LastActivityAt = 50_000, 25, now-125_000
		}),
		session("claude:s3", func(s *store.SessionView) {
			s.DisplayTitle, s.Liveness, s.StaleSince = "Charlie task", store.Stale, now-12_000
		}),
		session("claude:s4", func(s *store.SessionView) { s.DisplayTitle, s.Kind = "Delta task", "interactive" }),
		session("claude:s5", func(s *store.SessionView) { s.DisplayTitle = "Echo task" }),
	}
}

func sessionWithAgents() *store.SessionView {
	return session("claude:det", func(s *store.SessionView) {
		s.DisplayTitle, s.Cwd, s.ProjectName, s.GitBranch = "Detail task", "/home/u/projects/detail-proj", "detail-proj", "feat/x"
		s.Version, s.PID, s.Effort, s.Activity = "2.1.0", 777, "high", discovery.ActivityBusy
		s.Model = &domain.ModelInfo{ID: "claude-opus-4-7", Name: "Opus 4.7"}
		s.ContextUsed, s.ContextPercent = 120_000, 60
		s.Totals = domain.TokenTotals{Input: 1_500, Output: 2_500, CacheRead: 300_000, CacheCreate: 40_000}
		s.PendingTools = tools(domain.PendingTool{Name: "Bash"}, domain.PendingTool{Name: "Edit", Target: "/home/u/projects/x/y.ts"})
		s.Agents = map[string]*store.AgentView{
			"a-run": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Model, a.StartedAt = "Explore", "explora o repo", "claude-sonnet-4-5", now-65_000
				a.Context.Used = 20_000
				a.PendingTools = tools(domain.PendingTool{Name: "Grep"}, domain.PendingTool{Name: "Read", Target: "/deep/path/agent-file.ts"})
			}),
			"a-done": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Status = "reviewer", "revisa", domain.StatusCompleted
				a.StartedAt, a.FinishedAt = now-120_000, now-100_000
			}),
			"a-fail": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Status, a.FailureReason = "executor", "executa", domain.StatusFailed, "killed"
				a.StartedAt, a.FinishedAt = now-200_000, now-150_000
			}),
			"a-unk": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Status = "mystery", "sem dados", domain.StatusUnknown
			}),
			"a-launch": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Status, a.IsBackground = "bg-worker", "trabalha no fundo", domain.StatusLaunched, true
				a.StartedAt = now - 30_000
			}),
			"a-hand": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Status = "planner", "planeja", domain.StatusHandedBack
				a.StartedAt, a.FinishedAt = now-90_000, now-80_000
			}),
		}
	})
}

// fakeStore is an in-memory Source; set swaps the snapshot and signals.
type fakeStore struct {
	mu      sync.Mutex
	snap    *store.Snapshot
	updates chan struct{}
}

func newFakeStore(s *store.Snapshot) *fakeStore {
	return &fakeStore{snap: s, updates: make(chan struct{}, 1)}
}

func (f *fakeStore) Snapshot() *store.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeStore) Updates() <-chan struct{} { return f.updates }

func (f *fakeStore) set(s *store.Snapshot) {
	f.mu.Lock()
	f.snap = s
	f.mu.Unlock()
	select {
	case f.updates <- struct{}{}:
	default:
	}
}

func testOptions(st Source, profs []config.ProfileDir) Options {
	return Options{
		Profiles: profs, Appearance: style.DefaultAppearance(), Store: st,
		Now: func() int64 { return now }, Location: time.UTC,
	}
}

// fakeCloser records the cards it closes and returns a canned error. It never signals.
type fakeCloser struct {
	called []closer.Card
	err    error
}

func (f *fakeCloser) Close(card closer.Card) error {
	f.called = append(f.called, card)
	return f.err
}

// key builds a key press as the terminal sends it.
func key(name string) tea.KeyPressMsg {
	switch name {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+l":
		return tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}
	case "ctrl+z":
		return tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}
	}
	r := []rune(name)[0]
	return tea.KeyPressMsg{Code: r, Text: name}
}

// press feeds keys to the app and returns the commands of the last one.
func press(a *App, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = a.Update(key(k))
	}
	return cmd
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m|\x1b\]8;[^\x07\x1b]*\x07`)

func stripANSI(s string) string { return sgr.ReplaceAllString(s, "") }

// screen is the frame without styles and trailing spaces.
func screen(a *App) string {
	lines := strings.Split(stripANSI(a.Render()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func mustContain(t *testing.T, frame string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(frame, p) {
			t.Errorf("frame lacks %q:\n%s", p, frame)
		}
	}
}

func mustNotContain(t *testing.T, frame string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(frame, p) {
			t.Errorf("frame has %q:\n%s", p, frame)
		}
	}
}
