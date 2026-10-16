package reduce

import (
	"math"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// Agent finished, resumed by the parent through SendMessage (toolUseResult.resumedAgentId) and
// handed back again, writing to the same agent transcript (format seen in real data).

const (
	resumeT0       int64 = 1767265200000 // 2026-01-01T11:00:00.000Z
	resumedAgentID       = "a-res-001"
)

func resumeAt(seconds float64) int64 { return resumeT0 + int64(seconds*1000) }

var readOrders = []string{"session-first", "agent-first"}

func keepEvent(e domain.TranscriptEvent) domain.TranscriptEvent { return e }

// withoutResumeSignal is the same session without the explicit resume signal (only the agent
// transcript tells).
func withoutResumeSignal(e domain.TranscriptEvent) domain.TranscriptEvent {
	if r, ok := e.(domain.ToolResultEvent); ok && r.ResumedAgentID != "" {
		r.ResumedAgentID = ""
		return r
	}
	return e
}

func eventsUpTo(t *testing.T, name string, upTo int64, strip func(domain.TranscriptEvent) domain.TranscriptEvent) events {
	t.Helper()
	var out events
	for _, e := range fixtureEvents(t, name) {
		if ts := e.EventTimestamp(); ts == 0 || ts <= upTo {
			out = append(out, strip(e))
		}
	}
	return out
}

// loadResume reads both transcripts up to upTo seconds, in the given order.
func loadResume(t *testing.T, order string, upTo float64, strip func(domain.TranscriptEvent) domain.TranscriptEvent) domain.SessionState {
	t.Helper()
	cut := int64(math.MaxInt64)
	if !math.IsInf(upTo, 1) {
		cut = resumeAt(upTo)
	}
	main := eventsUpTo(t, "session-resume", cut, strip)
	sub := eventsUpTo(t, "subagent-resume", cut, keepEvent)
	s := NewSessionState("sess-0002")
	if order == "session-first" {
		s = ReduceSessionEvents(s, main)
		return ReduceSessionAgentEvents(s, resumedAgentID, sub)
	}
	s = ReduceSessionAgentEvents(s, resumedAgentID, sub)
	return ReduceSessionEvents(s, main)
}

func TestAgentResumedAfterFinishing(t *testing.T) {
	all := math.Inf(1)

	t.Run("normalize keeps only the resumed agent id from the SendMessage result", func(t *testing.T) {
		lines := fixtureLines(t, "session-resume")
		var resumed, other domain.ToolResultEvent
		for _, e := range normalize(lines[4]) {
			if r, ok := e.(domain.ToolResultEvent); ok {
				resumed = r
			}
		}
		expectMatch(t, resumed, obj{"toolUseId": "toolu_send_1", "resumedAgentId": resumedAgentID})
		// A SendMessage to a running agent (no resume) carries no id.
		for _, e := range normalize(lines[6]) {
			if r, ok := e.(domain.ToolResultEvent); ok {
				other = r
			}
		}
		expectEqual(t, other.ResumedAgentID, "")
		if strings.Contains(serialized(t, normalize(lines[4])), "SECRET") {
			t.Error("SECRET leaked")
		}
	})

	for _, order := range readOrders {
		t.Run(order, func(t *testing.T) {
			agent := func(s domain.SessionState) domain.AgentState { return agentOf(t, s, resumedAgentID) }
			t.Run("first handback: completed", func(t *testing.T) {
				expectEqual(t, agent(loadResume(t, order, 35, keepEvent)).Status, domain.StatusCompleted)
			})
			t.Run("the closing reply after the handback does not reopen it", func(t *testing.T) {
				// am_3 (end_turn text) comes after both handbacks with a new message.id.
				expectEqual(t, agent(loadResume(t, order, 39, keepEvent)).Status, domain.StatusCompleted)
			})
			t.Run("resumed: running and counted as active, without finishedAt", func(t *testing.T) {
				for _, upTo := range []float64{41.5, 45, 60} {
					s := loadResume(t, order, upTo, keepEvent)
					a := agent(s)
					expectEqual(t, a.Status, domain.StatusRunning)
					expectEqual(t, a.FinishedAt, 0)
					active, total := domain.AgentCounts(s.Agents)
					expectEqual(t, [2]int{active, total}, [2]int{1, 1})
				}
			})
			t.Run("handed back again: completed at the second handback", func(t *testing.T) {
				a := agent(loadResume(t, order, all, keepEvent))
				expectEqual(t, a.Status, domain.StatusCompleted)
				if a.FinishedAt < resumeAt(69) || a.FinishedAt > resumeAt(70) {
					t.Errorf("finishedAt = %d", a.FinishedAt)
				}
				expectEqual(t, a.FailureReason, "")
			})
			t.Run("without the SendMessage signal, the new prompt in the agent transcript reopens it", func(t *testing.T) {
				expectEqual(t, agent(loadResume(t, order, 45, withoutResumeSignal)).Status, domain.StatusRunning)
				expectEqual(t, agent(loadResume(t, order, 60, withoutResumeSignal)).Status, domain.StatusRunning)
				expectEqual(t, agent(loadResume(t, order, all, withoutResumeSignal)).Status, domain.StatusCompleted)
			})
		})
	}

	t.Run("a resume older than the agent end (agent transcript read first) changes nothing", func(t *testing.T) {
		s := loadResume(t, "agent-first", all, keepEvent)
		late := ReduceSession(s, domain.ToolResultEvent{EventBase: at(resumeAt(41)), ToolUseID: "toolu_old", ResumedAgentID: resumedAgentID})
		expectEqual(t, agentOf(t, late, resumedAgentID).Status, domain.StatusCompleted)
	})
}

func TestReopenRules(t *testing.T) {
	done := events{
		domain.UserPromptEvent{EventBase: at(1)},
		domain.AssistantMessageEvent{EventBase: at(10), ToolUses: []domain.ToolUseInfo{{ID: "h1", Name: HandbackToolName}}},
	}

	t.Run("a late tool_result after the handback does not reopen", func(t *testing.T) {
		a := reduceAgentEvents(NewAgentState(), append(append(events{}, done...),
			domain.ToolResultEvent{EventBase: at(11), ToolUseID: "h1"},
			domain.ToolResultEvent{EventBase: at(12), ToolUseID: "other"},
		))
		expectEqual(t, a.Status, domain.StatusHandedBack)
	})
	t.Run("completed reopens on a newer prompt; an older prompt does not", func(t *testing.T) {
		completed := NewAgentState()
		completed.Status = domain.StatusCompleted
		completed.FinishedAt = 20
		expectEqual(t, ReduceAgent(completed, domain.UserPromptEvent{EventBase: at(15), IsMeta: true}).Status, domain.StatusCompleted)
		reopened := ReduceAgent(completed, domain.UserPromptEvent{EventBase: at(25), IsMeta: true})
		expectEqual(t, reopened.Status, domain.StatusRunning)
		expectEqual(t, reopened.FinishedAt, 0)
	})
	t.Run("failed only reopens through the explicit resume signal", func(t *testing.T) {
		s := ReduceSessionEvents(NewSessionState("s"), events{
			launchResult("t1", "a1", 0),
			domain.TaskNotificationEvent{EventBase: at(10), TaskID: "a1", Status: "failed"},
		})
		s = ReduceSessionAgentEvents(s, "a1", events{domain.UserPromptEvent{EventBase: at(11)}})
		expectEqual(t, agentOf(t, s, "a1").Status, domain.StatusFailed)
		s = ReduceSession(s, domain.ToolResultEvent{EventBase: at(12), ToolUseID: "t2", ResumedAgentID: "a1"})
		expectEqual(t, agentOf(t, s, "a1").Status, domain.StatusRunning)
		expectEqual(t, agentOf(t, s, "a1").FailureReason, "")
	})
}

func TestAgentsKilledBothReadOrders(t *testing.T) {
	launch := func(id, tool string, ts int64) events {
		return events{
			domain.AssistantMessageEvent{EventBase: at(ts), ToolUses: []domain.ToolUseInfo{agentToolUse(tool, "d", true)}},
			launchResult(tool, id, ts),
		}
	}
	var main events
	main = append(main, launch("a-old", "t-old", 1)...)
	main = append(main, domain.AgentsKilledEvent{EventBase: at(50)})
	main = append(main, launch("a-new", "t-new", 100)...)
	subs := []struct {
		id     string
		events events
	}{
		// Handed back before the kill (the peer handback never reached the session).
		{"a-old", events{
			domain.UserPromptEvent{EventBase: at(2)},
			domain.AssistantMessageEvent{EventBase: at(40), ToolUses: []domain.ToolUseInfo{{ID: "h", Name: HandbackToolName}}},
		}},
		// Launched after the kill, still running.
		{"a-new", events{
			domain.UserPromptEvent{EventBase: at(101)},
			domain.AssistantMessageEvent{EventBase: at(102), ToolUses: []domain.ToolUseInfo{{ID: "b", Name: "Bash"}}},
		}},
	}
	for _, order := range readOrders {
		t.Run(order, func(t *testing.T) {
			s := NewSessionState("s")
			agents := func() {
				for _, sub := range subs {
					s = ReduceSessionAgentEvents(s, sub.id, sub.events)
				}
			}
			if order == "session-first" {
				s = ReduceSessionEvents(s, main)
				agents()
			} else {
				agents()
				s = ReduceSessionEvents(s, main)
			}
			expectEqual(t, agentOf(t, s, "a-old").Status, domain.StatusHandedBack)
			expectEqual(t, agentOf(t, s, "a-new").Status, domain.StatusRunning)
		})
	}
}
