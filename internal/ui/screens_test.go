package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/danielfnfaria/agtop/internal/config"
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
	// The time of the last activity rides on the title bar of the session box, the duration of
	// the agent on its own box.
	mustContain(t, screen(a), "idle 0s")
	tm += 7_000
	a.Update(clockMsg{})
	mustContain(t, screen(a), "idle 7s", "7s │")
	tm += 120_000
	a.Update(clockMsg{})
	mustContain(t, screen(a), "idle 2m", "2m07s")
}

func TestTopBarHidesBeforeTheTreeLosesABox(t *testing.T) {
	// The session box takes four rows and shrinks down to two (three with the label of the
	// profile), so the top bar fits every terminal the viewer draws at all: it gives way only
	// when the bar itself is tall, one row per profile watched, and not before the smallest box
	// with its scroll indicators runs out of rows.
	a := newApp(fiveStore(), 100, 20)
	mustContain(t, screen(a), "5h ")
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 15})
	mustContain(t, screen(a), "5h ", "Alpha task")

	var many []config.ProfileDir
	for i := range 6 {
		many = append(many, config.ProfileDir{Label: "p" + strconv.Itoa(i), Dir: "/tmp/p", Source: "config"})
	}
	snap := snapshot(fiveSessions(), many)
	tall := newApp(testOptions(newFakeStore(snap), many), 100, 20)
	mustContain(t, screen(tall), "5h ")
	// One row over it the bar stays: the boxes have a step of their own to drop (see
	// model.SessionStep), so the tree still shows a box of its own with the indicators.
	tall.Update(tea.WindowSizeMsg{Width: 100, Height: 15})
	mustContain(t, screen(tall), "5h ", "Alpha t")

	tall.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	f := screen(tall)
	mustNotContain(t, f, "5h ")
	mustContain(t, f, "Alpha task")
	if n := len(strings.Split(f, "\n")); n != 13 {
		t.Errorf("%d lines at 100x14", n)
	}
}
