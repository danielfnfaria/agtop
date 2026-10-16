package reduce

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/transcript"
)

type obj = map[string]any

type events = []domain.TranscriptEvent

func at(ms int64) domain.EventBase { return domain.EventBase{Timestamp: ms} }

func agentToolUse(id, description string, background bool) domain.ToolUseInfo {
	return domain.ToolUseInfo{ID: id, Name: "Agent", AgentLaunch: &domain.AgentLaunchRequest{
		Type: "x", Description: description, RunInBackground: background}}
}

func launchResult(toolUseID, agentID string, ts int64) domain.ToolResultEvent {
	return domain.ToolResultEvent{EventBase: at(ts), ToolUseID: toolUseID, AgentLaunch: &domain.AgentLaunchResult{AgentID: agentID}}
}

func sortedKeys(agents map[string]domain.AgentState) []string {
	return slices.Sorted(maps.Keys(agents))
}

func TestReduceSessionFixture(t *testing.T) {
	s := mainSession(t)

	t.Run("envelope metadata, title, model and effort", func(t *testing.T) {
		expectMatch(t, s, obj{
			"sessionId": "sess-0001", "cwd": "/home/dev/projects/demo-app", "projectName": "demo-app",
			"gitBranch": "main", "version": "2.1.300", "title": "My custom title", "titleSource": "custom",
			"aiTitle": "Demo title refactor", "customTitle": "My custom title",
			"model": obj{"id": "claude-test-1", "name": "Claude Test 1"}, "effort": "high", "pendingBackgroundAgents": 2,
		})
	})
	t.Run("ignored and invalid line counters", func(t *testing.T) {
		expectEqual(t, s.Ignored, 3)
		expectEqual(t, s.ParseErrors, 1)
	})
	t.Run("totals deduplicated by message.id", func(t *testing.T) {
		// msg_01 is in 3 lines and msg_05 in 2; each counts once.
		expectEqual(t, s.Totals, domain.TokenTotals{Input: 37, Output: 91, CacheRead: 10300, CacheCreate: 168})
		expectEqual(t, len(s.UsageByMessageID), 5)
	})
	t.Run("context = input + cache of the last response", func(t *testing.T) {
		expectEqual(t, s.Context, domain.ContextCounter{Used: 3015, LastMessageID: "msg_05"})
	})
	t.Run("activity and last human prompt (isMeta does not count)", func(t *testing.T) {
		expectEqual(t, s.LastActivityAt, t0+61000)
		expectEqual(t, s.LastUserPromptAt, t0+60000)
	})
	t.Run("pendingTools: Bash with only the category and Read with file_path", func(t *testing.T) {
		expectEqual(t, s.PendingTools, []domain.PendingEntry{
			{ID: "toolu_bash_pending", PendingTool: domain.PendingTool{Name: "Bash", CommandCategory: domain.CmdOther, StartedAt: t0 + 61000}},
			{ID: "toolu_read_pending", PendingTool: domain.PendingTool{Name: "Read", Target: "/home/dev/projects/demo-app/src/index.ts", StartedAt: t0 + 61000}},
		})
	})
	t.Run("serialized state has no prompt, command, output or content", func(t *testing.T) {
		json := serialized(t, s)
		for _, secret := range []string{"SECRET_BASH_CMD", "curl", "SECRET"} {
			if strings.Contains(json, secret) {
				t.Errorf("%s leaked", secret)
			}
		}
	})
	t.Run("async agent: launched, rekeyed by agentId and completed by the notification", func(t *testing.T) {
		if _, ok := s.Agents["toolu_agent_bg"]; ok {
			t.Error("still keyed by toolUseId")
		}
		expectMatch(t, agentOf(t, s, "a-bg-001"), obj{
			"agentId": "a-bg-001", "toolUseId": "toolu_agent_bg", "type": "explorer", "description": "Scan repo",
			"model": "claude-haiku-test", "isBackground": true, "status": "completed",
			"startedAt": t0 + 2000, "finishedAt": t0 + 40000,
		})
	})
	t.Run("foreground agent: async_launched does not make it background; completed by the peer handback", func(t *testing.T) {
		if _, ok := s.Agents["toolu_agent_fg"]; ok {
			t.Error("still keyed by toolUseId")
		}
		expectMatch(t, agentOf(t, s, "a-fg-002"), obj{
			"toolUseId": "toolu_agent_fg", "type": "reviewer", "description": "Review diff", "isBackground": false,
			"status": "completed", "startedAt": t0 + 4000, "finishedAt": t0 + 44000,
		})
	})
	t.Run("peer message without handback does not change the agent", func(t *testing.T) {
		expectEqual(t, agentOf(t, s, "a-bg-001").FinishedAt, t0+40000)
	})
	t.Run("async agent failed by the notification", func(t *testing.T) {
		expectMatch(t, agentOf(t, s, "a-fail-003"), obj{
			"type": "builder", "model": "claude-test-1", "status": "failed", "finishedAt": t0 + 41000,
		})
	})
	t.Run("notification of a task that is not an agent is ignored", func(t *testing.T) {
		expectEqual(t, sortedKeys(s.Agents), []string{"a-bg-001", "a-fail-003", "a-fg-002"})
	})
	t.Run("does not mutate the input state", func(t *testing.T) {
		initial := NewSessionState("x")
		snapshot := serialized(t, initial)
		ReduceSessionEvents(initial, fixtureEvents(t, "session-main"))
		expectEqual(t, serialized(t, initial), snapshot)
		// Nor an intermediate state: reducing more events leaves it as it was.
		half := ReduceSessionEvents(NewSessionState("x"), fixtureEvents(t, "session-main")[:20])
		snapshot = serialized(t, half)
		ReduceSessionEvents(half, fixtureEvents(t, "session-main")[20:])
		expectEqual(t, serialized(t, half), snapshot)
	})
}

func TestReduceSessionCases(t *testing.T) {
	base := NewSessionState("s")

	t.Run("intermediate state: launched before the tool_result", func(t *testing.T) {
		s := ReduceSessionEvents(base, normalize(fixtureLines(t, "session-main")[:8]...))
		expectMatch(t, agentOf(t, s, "toolu_agent_bg"), obj{
			"toolUseId": "toolu_agent_bg", "status": "launched", "model": "haiku", "isBackground": true,
		})
		if pendingIndex(&s.ActivityState, "toolu_agent_bg") < 0 {
			t.Error("agent tool not pending")
		}
	})
	t.Run("agent tool_result with is_error (no async_launched) fails it", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AssistantMessageEvent{EventBase: at(1), ToolUses: []domain.ToolUseInfo{agentToolUse("t1", "d", false)}},
			domain.ToolResultEvent{EventBase: at(2), ToolUseID: "t1", IsError: true},
		})
		expectMatch(t, agentOf(t, s, "t1"), obj{"status": "failed", "finishedAt": 2, "failureReason": "error"})
	})
	t.Run("unknown notification status is unknown; a final status is not overwritten", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			launchResult("t9", "a9", 0),
			domain.TaskNotificationEvent{TaskID: "a9", Status: "weird"},
		})
		expectEqual(t, agentOf(t, s, "a9").Status, domain.StatusUnknown)
		s = ReduceSessionEvents(base, events{
			launchResult("t9", "a9", 0),
			domain.TaskNotificationEvent{TaskID: "a9", Status: "killed"},
			domain.TaskNotificationEvent{TaskID: "a9", Status: "completed"},
		})
		expectEqual(t, agentOf(t, s, "a9").Status, domain.StatusFailed)
	})
	t.Run("queued_command notification (TaskStop) ends the agent; a duplicate is idempotent", func(t *testing.T) {
		notify := func(status, ts string) events {
			return transcript.NormalizeRecord(map[string]any{
				"type": "attachment", "timestamp": ts,
				"attachment": map[string]any{
					"type": "queued_command", "origin": map[string]any{"kind": "task-notification"},
					"prompt": fmt.Sprintf("<task-notification><task-id>a9</task-id><status>%s</status></task-notification>", status),
				},
			})
		}
		launch := launchResult("t9", "a9", 0)
		once := ReduceSessionEvents(base, append(events{launch}, notify("killed", "2026-01-01T10:00:05.000Z")...))
		expectEqual(t, agentOf(t, once, "a9").Status, domain.StatusFailed)
		twice := events{launch}
		twice = append(twice, notify("killed", "2026-01-01T10:00:05.000Z")...)
		twice = append(twice, notify("killed", "2026-01-01T10:00:06.000Z")...)
		twice = append(twice, domain.TaskNotificationEvent{TaskID: "a9", Status: "killed"})
		expectEqual(t, agentOf(t, ReduceSessionEvents(base, twice), "a9"), agentOf(t, once, "a9"))
		done := ReduceSessionEvents(base, append(events{launch}, notify("completed", "2026-01-01T10:00:05.000Z")...))
		expectEqual(t, agentOf(t, done, "a9").Status, domain.StatusCompleted)
	})
	t.Run("notification without <status> does not change the agent", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			launchResult("t9", "a9", 0),
			domain.TaskNotificationEvent{EventBase: at(5), TaskID: "a9"},
		})
		expectEqual(t, agentOf(t, s, "a9").Status, domain.StatusLaunched)
		expectEqual(t, agentOf(t, s, "a9").FinishedAt, 0)
		expectEqual(t, s.LastActivityAt, 5)
	})
	t.Run("isBackground comes only from run_in_background", func(t *testing.T) {
		launch := func(id string, background bool) domain.AssistantMessageEvent {
			return domain.AssistantMessageEvent{EventBase: at(1), ToolUses: []domain.ToolUseInfo{agentToolUse(id, "d", background)}}
		}
		s := ReduceSessionEvents(base, events{
			launch("t1", false),
			launchResult("t1", "a1", 0),
			domain.TaskNotificationEvent{TaskID: "a1", Status: "completed"},
			launch("t2", true),
			launchResult("t2", "a2", 0),
		})
		expectMatch(t, agentOf(t, s, "a1"), obj{"isBackground": false, "status": "completed"})
		expectEqual(t, agentOf(t, s, "a2").IsBackground, true)
	})
	t.Run("peer handback completes the agent and does not reopen a final one", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			launchResult("t1", "a1", 0),
			domain.AgentHandbackEvent{EventBase: at(5), AgentID: "a1"},
			domain.AgentHandbackEvent{EventBase: at(10), AgentID: "a1", Handback: true},
			domain.AgentHandbackEvent{EventBase: at(20), AgentID: "a1", Handback: true},
			domain.AgentHandbackEvent{EventBase: at(21), AgentID: "nobody", Handback: true},
		})
		expectMatch(t, agentOf(t, s, "a1"), obj{"status": "completed", "finishedAt": 10})
		expectEqual(t, len(s.Agents), 1)
		expectEqual(t, s.LastUserPromptAt, 0)
	})
	t.Run("resumed agent (new tool_use, same agentId) is reopened", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AssistantMessageEvent{EventBase: at(1), ToolUses: []domain.ToolUseInfo{agentToolUse("t1", "first", false)}},
			launchResult("t1", "a1", 0),
			domain.AgentHandbackEvent{EventBase: at(10), AgentID: "a1", Handback: true},
		})
		expectMatch(t, agentOf(t, s, "a1"), obj{"status": "completed", "finishedAt": 10})
		s = ReduceSessionEvents(s, events{
			domain.AssistantMessageEvent{EventBase: at(20), ToolUses: []domain.ToolUseInfo{agentToolUse("t2", "second", true)}},
			launchResult("t2", "a1", 21),
		})
		expectEqual(t, sortedKeys(s.Agents), []string{"a1"})
		reopened := agentOf(t, s, "a1")
		expectMatch(t, reopened, obj{"status": "launched", "toolUseId": "t2", "startedAt": 20, "description": "second", "isBackground": true})
		expectEqual(t, reopened.FinishedAt, 0)
		s = ReduceSession(s, domain.AgentHandbackEvent{EventBase: at(30), AgentID: "a1", Handback: true})
		expectMatch(t, agentOf(t, s, "a1"), obj{"status": "completed", "finishedAt": 30})
	})
	t.Run("merge: status and finishedAt from the same side; between finals the most recent wins", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AssistantMessageEvent{EventBase: at(1), ToolUses: []domain.ToolUseInfo{agentToolUse("t1", "d", true)}},
			// Launch entry (key t1): failed at 10.
			domain.TaskNotificationEvent{EventBase: at(10), ToolUseID: "t1", Status: "failed"},
		})
		// Agent transcript entry (key a1): completed at 30.
		s = ReduceSessionAgentEvents(s, "a1", events{domain.UserPromptEvent{EventBase: at(2)}})
		s = ReduceSession(s, domain.AgentHandbackEvent{EventBase: at(30), AgentID: "a1", Handback: true})
		expectMatch(t, agentOf(t, s, "t1"), obj{"status": "failed", "finishedAt": 10})
		expectMatch(t, agentOf(t, s, "a1"), obj{"status": "completed", "finishedAt": 30})
		s = ReduceSession(s, launchResult("t1", "a1", 0))
		expectEqual(t, sortedKeys(s.Agents), []string{"a1"})
		merged := agentOf(t, s, "a1")
		expectMatch(t, merged, obj{"status": "completed", "finishedAt": 30, "toolUseId": "t1"})
		expectEqual(t, merged.FailureReason, "")
	})
	t.Run("model: the attachment is authoritative; message.model only on a real change or without one", func(t *testing.T) {
		msg := func(model string) domain.AssistantMessageEvent {
			return domain.AssistantMessageEvent{Model: model, ToolUses: []domain.ToolUseInfo{}}
		}
		s := ReduceSession(base, msg("claude-opus-5"))
		expectEqual(t, s.Model, &domain.ModelInfo{ID: "claude-opus-5"})
		s = ReduceSessionEvents(s, events{
			domain.ModelAttachmentEvent{ModelID: "claude-opus-5[1m]", Name: "Claude Opus 5"},
			msg("claude-opus-5"),
		})
		expectEqual(t, s.Model, &domain.ModelInfo{ID: "claude-opus-5[1m]", Name: "Claude Opus 5"})
		s = ReduceSession(s, msg("claude-sonnet-5"))
		expectEqual(t, s.Model, &domain.ModelInfo{ID: "claude-sonnet-5"})
	})
	t.Run("custom title wins over ai-title", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AITitleEvent{Title: "ai 1"},
			domain.CustomTitleEvent{Title: "mine"},
			domain.AITitleEvent{Title: "ai 2"},
		})
		expectMatch(t, s, obj{"title": "mine", "titleSource": "custom", "customTitle": "mine", "aiTitle": "ai 2"})
		expectMatch(t, ReduceSession(base, domain.AITitleEvent{Title: "ai"}), obj{"title": "ai", "titleSource": "ai"})
	})
	t.Run("agents_killed: non-final become failed (killed); handed_back and final stay", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			launchResult("t1", "a1", 0),
			launchResult("t2", "a2", 0),
			launchResult("t3", "a3", 0),
			domain.AgentHandbackEvent{EventBase: at(5), AgentID: "a2", Handback: true},
		})
		s = ReduceSessionAgentEvents(s, "a3", events{
			domain.AssistantMessageEvent{EventBase: at(6), ToolUses: []domain.ToolUseInfo{{ID: "h", Name: HandbackToolName}}},
		})
		s = ReduceSession(s, domain.AgentsKilledEvent{EventBase: at(9)})
		expectMatch(t, agentOf(t, s, "a1"), obj{"status": "failed", "failureReason": "killed", "finishedAt": 9})
		expectMatch(t, agentOf(t, s, "a2"), obj{"status": "completed", "finishedAt": 5})
		expectMatch(t, agentOf(t, s, "a3"), obj{"status": "handed_back", "finishedAt": 6})
	})
	t.Run("turn_end clears the pending tools", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{{ID: "b", Name: "Bash"}}},
			domain.TurnEndEvent{},
		})
		expectEqual(t, s.PendingTools, []domain.PendingEntry{})
	})
	t.Run("<synthetic> message changes neither model nor context", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AssistantMessageEvent{MessageID: "m1", Model: "claude-test-1",
				Usage: &domain.TokenTotals{Input: 1, Output: 1, CacheRead: 10}, ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{Model: domain.SyntheticModel, Usage: &domain.TokenTotals{}, ToolUses: []domain.ToolUseInfo{}},
		})
		expectEqual(t, s.Model, &domain.ModelInfo{ID: "claude-test-1"})
		expectEqual(t, s.Context, domain.ContextCounter{Used: 11, LastMessageID: "m1"})
	})
	t.Run("a different usage for the same message.id replaces the previous one", func(t *testing.T) {
		s := ReduceSessionEvents(base, events{
			domain.AssistantMessageEvent{MessageID: "m1", Usage: &domain.TokenTotals{Input: 1, Output: 1}, ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{MessageID: "m1", Usage: &domain.TokenTotals{Input: 1, Output: 9}, ToolUses: []domain.ToolUseInfo{}},
		})
		expectEqual(t, s.Totals, domain.TokenTotals{Input: 1, Output: 9})
	})
	t.Run("ignored event counts and keeps the rest", func(t *testing.T) {
		want := base
		want.Ignored = 1
		expectEqual(t, ReduceSession(base, domain.IgnoredEvent{LineType: "x"}), want)
	})
}

func reduceAgentEvents(state domain.AgentState, evs events) domain.AgentState {
	for _, e := range evs {
		state = ReduceAgent(state, e)
	}
	return state
}

func TestReduceAgentFixture(t *testing.T) {
	sub := fixtureEvents(t, "subagent-handback")

	t.Run("alone: running -> handed_back, totals, context and model", func(t *testing.T) {
		a := reduceAgentEvents(NewAgentState(), sub)
		expectMatch(t, a, obj{
			"agentId": "a-bg-001", "status": "handed_back", "model": "claude-haiku-test",
			"startedAt": t0 + 5000, "lastActivityAt": t0 + 21000,
			"totals":  obj{"input": 6, "output": 36, "cacheRead": 1750, "cacheCreate": 65},
			"context": obj{"used": 656, "lastMessageId": "sm_03"},
		})
		expectEqual(t, len(a.PendingTools), 0)
	})
	t.Run("running in the middle of the transcript, with a pending Read", func(t *testing.T) {
		a := reduceAgentEvents(NewAgentState(), sub[:6])
		expectEqual(t, a.Status, domain.StatusRunning)
		expectEqual(t, a.PendingTools, []domain.PendingEntry{{ID: a.PendingTools[0].ID, PendingTool: domain.PendingTool{
			Name: "Read", Target: "/home/dev/projects/demo-app/README", StartedAt: t0 + 6000}}})
	})
	t.Run("session -> agent order: the notification's completed wins", func(t *testing.T) {
		s := ReduceSessionAgentEvents(mainSession(t), "a-bg-001", sub)
		expectMatch(t, agentOf(t, s, "a-bg-001"), obj{
			"status": "completed", "type": "explorer",
			"totals": obj{"input": 6, "output": 36, "cacheRead": 1750, "cacheCreate": 65},
		})
		expectEqual(t, len(s.Agents), 3)
		if strings.Contains(serialized(t, s), "SECRET") {
			t.Error("SECRET leaked")
		}
	})
	t.Run("agent -> session order: entries merged into one", func(t *testing.T) {
		s := ReduceSessionAgentEvents(NewSessionState("sess-0001"), "a-bg-001", sub)
		expectEqual(t, agentOf(t, s, "a-bg-001").Status, domain.StatusHandedBack)
		// Only the launch (up to the async_launched tool_result).
		s = ReduceSessionEvents(s, normalize(fixtureLines(t, "session-main")[:9]...))
		expectEqual(t, len(s.Agents), 1)
		expectMatch(t, agentOf(t, s, "a-bg-001"), obj{
			"toolUseId": "toolu_agent_bg", "type": "explorer", "description": "Scan repo",
			"status": "handed_back", "model": "claude-haiku-test", "context": obj{"used": 656},
		})
	})
	t.Run("the SubagentHandback tool_result does not reopen; finishedAt = handback", func(t *testing.T) {
		a := reduceAgentEvents(NewAgentState(), sub)
		expectEqual(t, a.Status, domain.StatusHandedBack)
		expectEqual(t, a.FinishedAt, t0+20000)
	})
	t.Run("Read + SubagentHandback in one message: the late Read result does not demote", func(t *testing.T) {
		a := reduceAgentEvents(NewAgentState(), events{
			domain.UserPromptEvent{EventBase: at(1)},
			domain.AssistantMessageEvent{EventBase: at(10), ToolUses: []domain.ToolUseInfo{
				{ID: "r1", Name: "Read", Target: "/x"}, {ID: "h1", Name: HandbackToolName}}},
			domain.ToolResultEvent{EventBase: at(11), ToolUseID: "r1"},
			domain.ToolResultEvent{EventBase: at(12), ToolUseID: "h1"},
		})
		expectMatch(t, a, obj{"status": "handed_back", "finishedAt": 10, "lastActivityAt": 12})
		expectEqual(t, len(a.PendingTools), 0)
		// Resumed agent: a new message to it reopens it.
		resumed := ReduceAgent(a, domain.UserPromptEvent{EventBase: at(20), IsMeta: true})
		expectEqual(t, resumed.Status, domain.StatusRunning)
		expectEqual(t, resumed.FinishedAt, 0)
	})
}

func TestApplyAgentMeta(t *testing.T) {
	t.Run("creates the agent from the meta.json", func(t *testing.T) {
		s := ApplyAgentMeta(NewSessionState("s"), "a1", domain.AgentMeta{
			AgentType: "explorer", Description: "Scan", ToolUseID: "toolu_1", SpawnDepth: ptr(1)})
		expectMatch(t, agentOf(t, s, "a1"), obj{
			"agentId": "a1", "toolUseId": "toolu_1", "type": "explorer", "description": "Scan",
			"spawnDepth": 1, "status": "launched",
		})
	})
	t.Run("model as fallback and stoppedByUser -> failed (stopped)", func(t *testing.T) {
		s := ApplyAgentMeta(NewSessionState("s"), "a1", domain.AgentMeta{Model: "claude-test-1", StoppedByUser: true})
		expectMatch(t, agentOf(t, s, "a1"), obj{"model": "claude-test-1", "status": "failed", "failureReason": "stopped"})
		withModel := ApplyAgentMeta(
			ReduceSession(NewSessionState("s"), domain.ToolResultEvent{ToolUseID: "t1",
				AgentLaunch: &domain.AgentLaunchResult{AgentID: "a1", ResolvedModel: "claude-resolved"}}),
			"a1", domain.AgentMeta{Model: "claude-test-1"})
		expectMatch(t, agentOf(t, withModel, "a1"), obj{"model": "claude-resolved", "status": "launched"})
	})
	t.Run("rekeys the launched agent (found by toolUseId) without overwriting the tool_use data", func(t *testing.T) {
		launched := ReduceSession(NewSessionState("s"), domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{
			{ID: "toolu_1", Name: "Agent", AgentLaunch: &domain.AgentLaunchRequest{Type: "reviewer", Description: "Review"}}}})
		s := ApplyAgentMeta(launched, "a1", domain.AgentMeta{AgentType: "other", Description: "Other", ToolUseID: "toolu_1", SpawnDepth: ptr(2)})
		expectEqual(t, sortedKeys(s.Agents), []string{"a1"})
		expectMatch(t, agentOf(t, s, "a1"), obj{"type": "reviewer", "description": "Review", "spawnDepth": 2, "status": "launched"})
		key, ok := FindAgentKey(s.Agents, "", "toolu_1")
		expectEqual(t, key, "a1")
		expectEqual(t, ok, true)
	})
}

func TestSessionActivityHistogram(t *testing.T) {
	const b = domain.ActivityBucketMs
	type hist = domain.ActivityHistogram
	usage := &domain.TokenTotals{Input: 1, Output: 1}

	t.Run("starts empty", func(t *testing.T) {
		expectEqual(t, len(NewSessionState("s").ActivityHistogram), 0)
	})
	t.Run("sums per bucket: 1 per response (distinct message.id) + 1 per tool_use", func(t *testing.T) {
		// 10:00:02-10:00:13: msg_01 (3 lines, 1 Agent), msg_02, msg_03 and msg_04 (1 tool each) = 8;
		// 10:01:01: msg_05 with Bash + Read (3) and the second line of the same response (0).
		expectEqual(t, histogram(mainSession(t)), hist{t0: 8, t0 + 2*b: 3})
	})
	t.Run("assistant lines of the sub-agent count in the session histogram", func(t *testing.T) {
		sub := fixtureEvents(t, "subagent-handback")
		s := ReduceSessionAgentEvents(mainSession(t), "a-bg-001", sub)
		// Sub-agent: sm_01 (2 lines, 1 Read), sm_02 (Grep) and sm_03 (handback) = 6.
		expectEqual(t, histogram(s), hist{t0: 14, t0 + 2*b: 3})
		// Even without the session having seen the launch.
		expectEqual(t, histogram(ReduceSessionAgentEvents(NewSessionState("s"), "a-bg-001", sub)), hist{t0: 6})
	})
	t.Run("counts even when the agent does not change (e.g. already final)", func(t *testing.T) {
		s := ReduceSessionAgentEvents(NewSessionState("s"), "a1", events{
			domain.AssistantMessageEvent{EventBase: at(t0), ToolUses: []domain.ToolUseInfo{{ID: "h", Name: HandbackToolName}}},
		})
		s = ApplyAgentMeta(s, "a1", domain.AgentMeta{StoppedByUser: true})
		before := agentOf(t, s, "a1")
		s = ReduceSessionAgentEvents(s, "a1", events{domain.AssistantMessageEvent{EventBase: at(t0), ToolUses: []domain.ToolUseInfo{}}})
		// Without message.id each line is a new response: (1 + 1) + 1.
		expectEqual(t, histogram(s), hist{t0: 3})
		expectEqual(t, agentOf(t, s, "a1").Status, before.Status)
	})
	t.Run("synthetic, no timestamp and user/tool_result lines do not count", func(t *testing.T) {
		s := ReduceSessionEvents(NewSessionState("s"), events{
			domain.AssistantMessageEvent{EventBase: at(t0), Model: domain.SyntheticModel, ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{{ID: "t1", Name: "Bash"}}},
			domain.UserPromptEvent{EventBase: at(t0)},
			domain.ToolResultEvent{EventBase: at(t0), ToolUseID: "t1"},
		})
		expectEqual(t, len(s.ActivityHistogram), 0)
		viaAgent := ReduceSessionAgentEvents(NewSessionState("s"), "a1", events{
			domain.AssistantMessageEvent{EventBase: at(t0), Model: domain.SyntheticModel, ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{}},
		})
		expectEqual(t, len(viaAgent.ActivityHistogram), 0)
	})
	t.Run("two tool_use in one line weigh 2, plus 1 for the response", func(t *testing.T) {
		s := ReduceSession(NewSessionState("s"), domain.AssistantMessageEvent{EventBase: at(t0 + 1000),
			ToolUses: []domain.ToolUseInfo{{ID: "a", Name: "Read"}, {ID: "b", Name: "Grep"}}})
		expectEqual(t, histogram(s), hist{t0: 3})
	})
	t.Run("the same response in several lines counts once, deduplicated in its own state", func(t *testing.T) {
		lines := events{
			domain.AssistantMessageEvent{EventBase: at(t0), MessageID: "m1", Usage: usage, ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{EventBase: at(t0), MessageID: "m1", Usage: usage, ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{EventBase: at(t0), MessageID: "m1", Usage: usage, ToolUses: []domain.ToolUseInfo{{ID: "t", Name: "Bash"}}},
		}
		s := ReduceSessionEvents(NewSessionState("s"), lines)
		expectEqual(t, histogram(s), hist{t0: 2})
		// The agent has its own tracking: the same id seen in the session counts again in it.
		expectEqual(t, histogram(ReduceSessionAgentEvents(s, "a1", lines)), hist{t0: 4})
	})
	t.Run("a response with message.id and no usage counts once, totals untouched", func(t *testing.T) {
		lines := events{
			domain.AssistantMessageEvent{EventBase: at(t0), MessageID: "m1", ToolUses: []domain.ToolUseInfo{}},
			domain.AssistantMessageEvent{EventBase: at(t0), MessageID: "m1", ToolUses: []domain.ToolUseInfo{}},
		}
		s := ReduceSessionEvents(NewSessionState("s"), lines)
		expectEqual(t, histogram(s), hist{t0: 1})
		expectEqual(t, s.Totals, domain.TokenTotals{})
		expectEqual(t, s.Context, domain.ContextCounter{})
		// The usage that arrives later for the same id enters the totals (once) and does not
		// count again.
		later := ReduceSession(s, domain.AssistantMessageEvent{EventBase: at(t0), MessageID: "m1",
			Usage: &domain.TokenTotals{Input: 2, Output: 3}, ToolUses: []domain.ToolUseInfo{}})
		expectEqual(t, histogram(later), hist{t0: 1})
		expectEqual(t, later.Totals, domain.TokenTotals{Input: 2, Output: 3})
		// Same in the agent.
		viaAgent := ReduceSessionAgentEvents(NewSessionState("s"), "a1", lines)
		expectEqual(t, histogram(viaAgent), hist{t0: 1})
		expectEqual(t, agentOf(t, viaAgent, "a1").Totals, s.Totals)
	})
	t.Run("the same line with tool_use delivered twice does not count again", func(t *testing.T) {
		line := domain.AssistantMessageEvent{EventBase: at(t0), ToolUses: []domain.ToolUseInfo{{ID: "t1", Name: "Read"}, {ID: "t2", Name: "Grep"}}}
		withID := line
		withID.MessageID = "m1"
		withID.Usage = usage
		// Session: 1 for the response + 2 tool_use; the repetition adds nothing.
		s := ReduceSessionEvents(NewSessionState("s"), events{withID, withID})
		expectEqual(t, histogram(s), hist{t0: 3})
		// Without message.id the response counts again, but the tool_use already seen do not.
		expectEqual(t, histogram(ReduceSessionEvents(NewSessionState("s"), events{line, line})), hist{t0: 4})
		// Agent: deduplicated in the agent's own state.
		viaAgent := ReduceSessionAgentEvents(NewSessionState("s"), "a1", events{withID, withID})
		expectEqual(t, histogram(viaAgent), hist{t0: 3})
		expectEqual(t, agentOf(t, viaAgent, "a1").SeenToolUseIDs, []string{"t1", "t2"})
		// The session and the agent track apart: the agent line counts the first time.
		expectEqual(t, histogram(ReduceSessionAgentEvents(s, "a1", events{withID})), hist{t0: 6})
	})
	t.Run("seenToolUseIds keeps only the most recent (MaxSeenToolUses)", func(t *testing.T) {
		var many events
		for i := range domain.MaxSeenToolUses + 2 {
			many = append(many, domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{{ID: fmt.Sprintf("t%d", i), Name: "Read"}}})
		}
		s := ReduceSessionEvents(NewSessionState("s"), many)
		expectEqual(t, len(s.SeenToolUseIDs), domain.MaxSeenToolUses)
		expectEqual(t, slices.Contains(s.SeenToolUseIDs, "t0"), false)
		expectEqual(t, slices.Contains(s.SeenToolUseIDs, "t1"), false)
		expectEqual(t, slices.Contains(s.SeenToolUseIDs, fmt.Sprintf("t%d", domain.MaxSeenToolUses+1)), true)
	})
}

func TestHugeTokenCountsSaturate(t *testing.T) {
	line := func() []domain.TranscriptEvent {
		return transcript.NormalizeRecord(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []any{},
				"usage":   map[string]any{"input_tokens": 1e300, "output_tokens": 1e300},
			},
		})
	}
	var evs []domain.TranscriptEvent
	evs = append(evs, line()...)
	evs = append(evs, line()...)
	s := ReduceSessionEvents(NewSessionState("s"), evs)
	for _, v := range []int{s.Totals.Input, s.Totals.Output, s.Context.Used} {
		if v < 0 || v > domain.MaxSafeInt {
			t.Errorf("total %d out of [0, 2^53-1]", v)
		}
	}
	if s.Totals.Input != domain.MaxSafeInt {
		t.Errorf("Input = %d, want capped at %d", s.Totals.Input, domain.MaxSafeInt)
	}
}
