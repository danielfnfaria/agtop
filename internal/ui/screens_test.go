package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
)

// Ids and keys never reach the screen: only the fields the views pick do.
func TestSecretFieldsNeverOnScreen(t *testing.T) {
	s := session("claude:s", func(s *store.SessionView) {
		s.SessionID, s.DisplayTitle, s.Kind, s.Cwd = "SECRET-session-id", "Titulo ok", "sdk-cli", "/ok/cwd"
		s.PendingTools = []domain.PendingEntry{{ID: "SECRET-id", PendingTool: domain.PendingTool{Name: "Bash"}}}
		s.Agents = map[string]*store.AgentView{
			"SECRET-agent-key": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.Status, a.FailureReason, a.StartedAt = "Explore", "desc ok", domain.StatusFailed, "error", 1
				a.PendingTools = []domain.PendingEntry{{ID: "SECRET-tool-id",
					PendingTool: domain.PendingTool{Name: "Read", Target: "/ok/file.ts"}}}
			}),
			"SECRET-agent-key-2": agent(func(a *store.AgentView) {
				a.Type, a.Description, a.StartedAt = "Runner", "roda ok", 2
				a.PendingTools = []domain.PendingEntry{{ID: "SECRET-run-tool", PendingTool: domain.PendingTool{Name: "Bash"}}}
			}),
		}
	})
	snap := snapshot([]*store.SessionView{s}, profiles)
	snap.Errors = []string{"leitura /ok/x.jsonl: EACCES"}
	pct := 42.0
	for i := range snap.Profiles {
		snap.Profiles[i].Usage = &domain.UsageView{FiveHour: &domain.UsageWindowView{Percent: &pct, ResetsAt: 1},
			UpdatedAt: 1, Stale: true}
	}
	a := newApp(testOptions(newFakeStore(snap), profiles), 120, 40)
	var frames []string
	shot := func(want ...string) {
		t.Helper()
		f := screen(a)
		frames = append(frames, f)
		mustContain(t, f, want...)
	}
	shot("Titulo ok", "42%", "Runner", "roda ok", "1 error")
	press(a, "?")
	shot("leitura /ok/x.jsonl: EACCES")
	press(a, "esc", "down", "enter", "enter")
	shot("Read /ok/file.ts", "failed (error)")
	press(a, "tab")
	shot("Bash")
	press(a, "esc")
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	shot("Titulo ok")
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 40})
	shot("Titulo ok")
	mustNotContain(t, strings.Join(frames, "\n"), "SECRET")
}

func TestAgoAndDurationFollowTheClock(t *testing.T) {
	tm := now
	s := session("claude:c", func(s *store.SessionView) {
		s.DisplayTitle, s.LastActivityAt = "Clock", tm
		s.Agents = map[string]*store.AgentView{"a": agent(func(a *store.AgentView) {
			a.Type, a.StartedAt = "ticker", tm
		})}
	})
	o := testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles)
	o.Now = func() int64 { return tm }
	a := newApp(o, 100, 30)
	mustContain(t, screen(a), "0s ago")
	tm += 7_000
	a.Update(clockMsg{})
	mustContain(t, screen(a), "7s ago", "7s │")
	tm += 120_000
	a.Update(clockMsg{})
	mustContain(t, screen(a), "2m ago", "2m07s")
}

func TestTopBarHidesBeforeTheTreeLosesABox(t *testing.T) {
	a := newApp(fiveStore(), 100, 20)
	mustContain(t, screen(a), "5h ")
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 15})
	f := screen(a)
	mustNotContain(t, f, "5h ")
	mustContain(t, f, "Alpha task")
	if n := len(strings.Split(f, "\n")); n != 14 {
		t.Errorf("%d lines at 100x15", n)
	}
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	mustContain(t, screen(a), "5h ")
}
