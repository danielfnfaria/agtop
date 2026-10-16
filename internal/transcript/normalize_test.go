package transcript

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// t0 is 2026-01-01T10:00:00.000Z.
const t0 int64 = 1767261600000

func only[E domain.TranscriptEvent](events []domain.TranscriptEvent) []E {
	out := []E{}
	for _, e := range events {
		if v, ok := e.(E); ok {
			out = append(out, v)
		}
	}
	return out
}

func kinds(events []domain.TranscriptEvent) []string {
	out := []string{}
	for _, e := range events {
		out = append(out, e.EventKind())
	}
	return out
}

func toolUsesOf(events []domain.TranscriptEvent) []domain.ToolUseInfo {
	var out []domain.ToolUseInfo
	for _, a := range only[domain.AssistantMessageEvent](events) {
		out = append(out, a.ToolUses...)
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func expect[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func normalizeJSON(t *testing.T, v any) []domain.TranscriptEvent {
	t.Helper()
	return NormalizeLine(mustJSON(t, v))
}

func ts(ms int64) domain.EventBase { return domain.EventBase{Timestamp: ms} }

type obj = map[string]any

func TestNormalizeFixture(t *testing.T) {
	lines := fixtureLines(t, "session-main")
	all := fixtureEvents(t, "session-main")

	t.Run("every line of the fixture, one parse_error", func(t *testing.T) {
		expect(t, only[domain.ParseErrorEvent](all), []domain.ParseErrorEvent{{Reason: domain.ParseInvalidJSON}})
	})
	t.Run("unknown and unused types are ignored", func(t *testing.T) {
		var types []string
		for _, e := range only[domain.IgnoredEvent](all) {
			types = append(types, e.LineType)
		}
		expect(t, types, []string{"permission-mode", "attachment:date", "some-future-type"})
	})
	t.Run("envelope comes with user/assistant/system/attachment lines", func(t *testing.T) {
		expect(t, only[domain.EnvelopeInfoEvent](all)[0], domain.EnvelopeInfoEvent{
			EventBase: ts(t0), SessionID: "sess-0001", Cwd: "/home/dev/projects/demo-app",
			GitBranch: "main", Version: "2.1.300", IsSidechain: new(bool),
		})
	})
	t.Run("model attachment", func(t *testing.T) {
		expect(t, only[domain.ModelAttachmentEvent](all), []domain.ModelAttachmentEvent{
			{EventBase: ts(t0), ModelID: "claude-test-1", Name: "Claude Test 1"},
		})
	})
	t.Run("ai-title is sanitized", func(t *testing.T) {
		expect(t, only[domain.AITitleEvent](all), []domain.AITitleEvent{{Title: "Demo title refactor"}})
	})
	t.Run("custom-title is sanitized", func(t *testing.T) {
		expect(t, only[domain.CustomTitleEvent](all), []domain.CustomTitleEvent{{Title: "My custom title"}})
	})
	t.Run("Agent tool_use becomes agentLaunch", func(t *testing.T) {
		var launches []domain.ToolUseInfo
		for _, tu := range toolUsesOf(all) {
			if tu.AgentLaunch != nil {
				launches = append(launches, tu)
			}
		}
		expect(t, launches, []domain.ToolUseInfo{
			{ID: "toolu_agent_bg", Name: "Agent", AgentLaunch: &domain.AgentLaunchRequest{
				Type: "explorer", Description: "Scan repo", Model: "haiku", RunInBackground: true}},
			{ID: "toolu_agent_fg", Name: "Agent", AgentLaunch: &domain.AgentLaunchRequest{
				Type: "reviewer", Description: "Review diff"}},
			{ID: "toolu_agent_fail", Name: "Agent", AgentLaunch: &domain.AgentLaunchRequest{
				Type: "builder", Description: "Build it", RunInBackground: true}},
		})
	})
	t.Run("assistant has id, model, effort and usage", func(t *testing.T) {
		first := only[domain.AssistantMessageEvent](all)[0]
		expect(t, first, domain.AssistantMessageEvent{
			EventBase: first.EventBase, MessageID: "msg_01", Model: "claude-test-1",
			RequestedModel: "claude-test-1", Effort: "high", StopReason: first.StopReason,
			Usage:    &domain.TokenTotals{Input: 10, Output: 5, CacheRead: 1000, CacheCreate: 100},
			ToolUses: []domain.ToolUseInfo{},
		})
	})
	t.Run("Read has a target; Bash keeps only the command category", func(t *testing.T) {
		byID := map[string]domain.ToolUseInfo{}
		for _, tu := range toolUsesOf(all) {
			byID[tu.ID] = tu
		}
		expect(t, byID["toolu_read_pending"], domain.ToolUseInfo{
			ID: "toolu_read_pending", Name: "Read", Target: "/home/dev/projects/demo-app/src/index.ts"})
		expect(t, byID["toolu_bash_pending"], domain.ToolUseInfo{
			ID: "toolu_bash_pending", Name: "Bash", CommandCategory: domain.CmdOther})
	})

	results := map[string]domain.ToolResultEvent{}
	for _, r := range only[domain.ToolResultEvent](all) {
		results[r.ToolUseID] = r
	}
	t.Run("async_launched tool_result has agentLaunch", func(t *testing.T) {
		expect(t, results["toolu_agent_bg"], domain.ToolResultEvent{
			EventBase: ts(t0 + 3000), ToolUseID: "toolu_agent_bg",
			AgentLaunch: &domain.AgentLaunchResult{AgentID: "a-bg-001", Description: "Scan repo", ResolvedModel: "claude-haiku-test"},
		})
	})
	t.Run("a foreground agent also returns async_launched", func(t *testing.T) {
		expect(t, results["toolu_agent_fg"], domain.ToolResultEvent{
			EventBase: ts(t0 + 5000), ToolUseID: "toolu_agent_fg",
			AgentLaunch: &domain.AgentLaunchResult{AgentID: "a-fg-002", Description: "Review diff", ResolvedModel: "claude-test-1"},
		})
	})
	t.Run("a string toolUseResult does not break", func(t *testing.T) {
		expect(t, results["toolu_bash_done"], domain.ToolResultEvent{EventBase: ts(t0 + 14000), ToolUseID: "toolu_bash_done"})
	})
	t.Run("peer message becomes agent_handback with only agentId and handback", func(t *testing.T) {
		expect(t, only[domain.AgentHandbackEvent](all), []domain.AgentHandbackEvent{
			{EventBase: ts(t0 + 44000), AgentID: "a-fg-002", Handback: true},
			{EventBase: ts(t0 + 45000), AgentID: "a-bg-001"},
		})
	})
	t.Run("task-notification completed, failed and of a task without tool-use-id", func(t *testing.T) {
		expect(t, only[domain.TaskNotificationEvent](all), []domain.TaskNotificationEvent{
			{EventBase: ts(t0 + 40000), TaskID: "a-bg-001", ToolUseID: "toolu_agent_bg", Status: "completed"},
			{EventBase: ts(t0 + 41000), TaskID: "a-fail-003", ToolUseID: "toolu_agent_fail", Status: "failed"},
			{EventBase: ts(t0 + 42000), TaskID: "b-shell-9", Status: "completed"},
		})
	})
	t.Run("prompts: human (string and text array) and meta", func(t *testing.T) {
		var metas []bool
		for _, p := range only[domain.UserPromptEvent](all) {
			metas = append(metas, p.IsMeta)
		}
		expect(t, metas, []bool{false, true, false})
	})
	t.Run("turn_duration becomes turn_end", func(t *testing.T) {
		expect(t, only[domain.TurnEndEvent](all), []domain.TurnEndEvent{
			{EventBase: ts(t0 + 15000), DurationMs: new(int64(14000)), PendingBackgroundAgentCount: new(2)},
		})
	})
	t.Run("no event carries prompt, command, output or thinking text", func(t *testing.T) {
		if strings.Contains(mustJSON(t, all), "SECRET") {
			t.Error("SECRET leaked")
		}
	})
	t.Run("kinds of the assistant line with a tool_use", func(t *testing.T) {
		expect(t, kinds(NormalizeLine(lines[7])), []string{"envelope", "assistant"})
	})
	t.Run("agent transcript: envelope with agentId and SubagentHandback", func(t *testing.T) {
		sub := fixtureEvents(t, "subagent-handback")
		for _, e := range only[domain.EnvelopeInfoEvent](sub) {
			if e.AgentID != "a-bg-001" || e.IsSidechain == nil || !*e.IsSidechain {
				t.Errorf("unexpected envelope %+v", e)
			}
		}
		var names []string
		for _, tu := range toolUsesOf(sub) {
			names = append(names, tu.Name)
		}
		expect(t, names, []string{"Read", "Grep", "SubagentHandback"})
	})
}

func TestNormalizeRecord(t *testing.T) {
	notification := "<task-notification>\n<task-id>a-kill-1</task-id>\n<tool-use-id>toolu_k</tool-use-id>\n" +
		"<status>killed</status>\n<summary>SECRET</summary>\n</task-notification>"
	cases := []struct {
		name string
		in   any
		want []domain.TranscriptEvent
	}{
		{"blank line gives no event", "", []domain.TranscriptEvent{}},
		{"JSON that is not an object", 42.0, []domain.TranscriptEvent{domain.ParseErrorEvent{Reason: domain.ParseNotObject}}},
		{"array is not an object", []any{"x"}, []domain.TranscriptEvent{domain.ParseErrorEvent{Reason: domain.ParseNotObject}}},
		{"custom-title of the wrong type", obj{"type": "custom-title", "customTitle": 3},
			[]domain.TranscriptEvent{domain.IgnoredEvent{LineType: "custom-title"}}},
		{"legacy Task name is also an agent launch", obj{"type": "assistant", "message": obj{
			"content": []any{obj{"type": "tool_use", "id": "t1", "name": "Task", "input": obj{"prompt": "SECRET"}}}}},
			[]domain.TranscriptEvent{domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{{ID: "t1", Name: "Task",
				AgentLaunch: &domain.AgentLaunchRequest{Type: "general-purpose"}}}}}},
		{"agent result without async_launched has resultAgentId", obj{"type": "user",
			"message":       obj{"content": []any{obj{"type": "tool_result", "tool_use_id": "t1", "is_error": true}}},
			"toolUseResult": obj{"status": "completed", "agentId": "a1", "content": "SECRET"}},
			[]domain.TranscriptEvent{domain.ToolResultEvent{ToolUseID: "t1", IsError: true, ResultAgentID: "a1"}}},
		{"peer without senderTaskId uses from", obj{"type": "user", "isMeta": true, "message": obj{"content": "SECRET"},
			"origin": obj{"kind": "peer", "from": "a2", "handback": true, "body": "SECRET"}},
			[]domain.TranscriptEvent{domain.AgentHandbackEvent{AgentID: "a2", Handback: true}}},
		{"peer without senderTaskId nor from is ignored", obj{"type": "user", "message": obj{"content": "SECRET"},
			"origin": obj{"kind": "peer", "handback": true, "handbackReportText": "SECRET"}},
			[]domain.TranscriptEvent{domain.IgnoredEvent{LineType: "user:peer"}}},
		{"task-notification tolerates spaces and upper case", obj{"type": "user",
			"message": obj{"content": "  <task-notification><TASK-ID> x1 </TASK-ID><status>Killed</status>"}},
			[]domain.TranscriptEvent{domain.TaskNotificationEvent{TaskID: "x1", Status: "killed"}}},
		{"queued_command attachment with a task-notification", obj{"type": "attachment", "timestamp": "2026-01-01T10:00:00.000Z",
			"attachment": obj{"type": "queued_command", "origin": obj{"kind": "task-notification"}, "prompt": notification}},
			[]domain.TranscriptEvent{domain.TaskNotificationEvent{EventBase: ts(t0), TaskID: "a-kill-1", ToolUseID: "toolu_k", Status: "killed"}}},
		{"task-notification ignores tags inside <summary>", obj{"type": "user", "message": obj{"content": "<task-notification><task-id>x1</task-id>" +
			"<summary>text <status>completed</status> <tool-use-id>evil</tool-use-id></summary></task-notification>"}},
			[]domain.TranscriptEvent{domain.TaskNotificationEvent{TaskID: "x1"}}},
		{"task-notification ignores tags inside <result>", obj{"type": "user", "message": obj{
			"content": "<task-notification><result><task-id>evil</task-id><status>failed</status></result>"}},
			[]domain.TranscriptEvent{domain.TaskNotificationEvent{}}},
		{"system agents_killed", obj{"type": "system", "subtype": "agents_killed", "timestamp": "2026-01-01T10:00:00.000Z"},
			[]domain.TranscriptEvent{domain.AgentsKilledEvent{EventBase: ts(t0)}}},
		{"fields of an unexpected type are tolerated", obj{"type": "assistant", "timestamp": "not a date", "cwd": 42,
			"message": obj{"id": 7, "content": []any{nil, "x", obj{"type": "tool_use", "id": "a"}, obj{"type": "tool_use", "name": "Bash"}},
				"usage": obj{"input_tokens": "many", "output_tokens": 3}}},
			[]domain.TranscriptEvent{domain.AssistantMessageEvent{ToolUses: []domain.ToolUseInfo{}, Usage: &domain.TokenTotals{Output: 3}}}},
		{"user without message", obj{"type": "user"}, []domain.TranscriptEvent{domain.IgnoredEvent{LineType: "user"}}},
		{"unused system subtype", obj{"type": "system", "subtype": "away_summary"},
			[]domain.TranscriptEvent{domain.IgnoredEvent{LineType: "system:away_summary"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []domain.TranscriptEvent
			if s, ok := c.in.(string); ok {
				got = NormalizeLine(s)
			} else {
				got = NormalizeRecord(c.in)
			}
			expect(t, got, c.want)
			if strings.Contains(mustJSON(t, got), "SECRET") {
				t.Error("SECRET leaked")
			}
		})
	}
}

func TestQueuedCommandNotNotification(t *testing.T) {
	for _, attachment := range []obj{
		{"type": "queued_command", "prompt": "fix the bug"},
		{"type": "queued_command", "origin": obj{"kind": "task-notification"}},
		{"type": "queued_command", "prompt": 42},
	} {
		expect(t, kinds(NormalizeRecord(obj{"type": "attachment", "attachment": attachment})), []string{"ignored"})
	}
}

func TestBashUsesGh(t *testing.T) {
	toolUseOf := func(command string) domain.ToolUseInfo {
		events := normalizeJSON(t, obj{"type": "assistant", "message": obj{"id": "m",
			"content": []any{obj{"type": "tool_use", "id": "t", "name": "Bash", "input": obj{"command": command}}}}})
		return only[domain.AssistantMessageEvent](events)[0].ToolUses[0]
	}
	gh := toolUseOf("cd SECRET_DIR && gh pr create --title SECRET_TITLE")
	if !gh.UsesGh {
		t.Error("usesGh not set")
	}
	if strings.Contains(mustJSON(t, gh), "SECRET") {
		t.Error("SECRET leaked")
	}
	if toolUseOf("git push").UsesGh {
		t.Error("usesGh set without gh")
	}
}

// From the toolActivity tests: normalization keeps only the command category.
func TestNormalizeKeepsOnlyCommandCategory(t *testing.T) {
	events := normalizeJSON(t, obj{"type": "assistant", "timestamp": "2026-01-01T10:00:00.000Z",
		"message": obj{"id": "msg_1", "model": "claude-test-1", "content": []any{obj{
			"type": "tool_use", "id": "toolu_1", "name": "Bash",
			"input": obj{"command": "API_KEY=SECRET_TOKEN npm test -- --grep SECRET_ARG", "description": "SECRET_DESC"}}}}})
	json := mustJSON(t, events)
	if strings.Contains(json, "SECRET") || strings.Contains(json, "npm") {
		t.Errorf("command leaked: %s", json)
	}
	expect(t, only[domain.AssistantMessageEvent](events)[0].ToolUses, []domain.ToolUseInfo{
		{ID: "toolu_1", Name: "Bash", CommandCategory: domain.CmdTests},
	})
}

func TestParseAgentMeta(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   domain.AgentMeta
		wantOK bool
	}{
		{"only the known fields", obj{"agentType": "explorer", "description": "Scan\nrepo", "toolUseId": "toolu_x",
			"spawnDepth": 1, "model": "claude-test-1", "stoppedByUser": true, "requestShape": obj{"anything": true}},
			domain.AgentMeta{AgentType: "explorer", Description: "Scan repo", ToolUseID: "toolu_x", SpawnDepth: new(1),
				Model: "claude-test-1", StoppedByUser: true}, true},
		{"wrong types", obj{"agentType": 3, "spawnDepth": "1"}, domain.AgentMeta{}, true},
		{"wrong model, stoppedByUser false", obj{"model": 3, "stoppedByUser": false}, domain.AgentMeta{}, true},
		{"stoppedByUser string", obj{"stoppedByUser": "true"}, domain.AgentMeta{}, true},
		{"null", nil, domain.AgentMeta{}, false},
		{"string", "x", domain.AgentMeta{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseAgentMeta(c.in)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			expect(t, got, c.want)
		})
	}
}

func TestNormalizePullRequests(t *testing.T) {
	url := func(n string) string { return "https://github.com/acme/app/pull/" + n }
	pr := func(n int, s string) domain.PullRequestRef {
		return domain.PullRequestRef{Owner: "acme", Repo: "app", Number: n, URL: url(s)}
	}
	toolResult := func(content any) []domain.TranscriptEvent {
		return normalizeJSON(t, obj{"type": "user", "timestamp": "2026-01-01T10:00:00.000Z",
			"message": obj{"role": "user", "content": []any{obj{"type": "tool_result", "tool_use_id": "t1", "content": content}}}})
	}
	prsOf := func(events []domain.TranscriptEvent) []domain.PullRequestRef {
		var out []domain.PullRequestRef
		for _, r := range only[domain.ToolResultEvent](events) {
			out = append(out, r.PullRequests...)
		}
		return out
	}

	t.Run("assistant text blocks: only the refs leave", func(t *testing.T) {
		events := normalizeJSON(t, obj{"type": "assistant", "timestamp": "2026-01-01T10:00:00.000Z",
			"message": obj{"id": "m1", "model": "claude-test-1", "content": []any{
				obj{"type": "text", "text": "SECRET_TEXT done: " + url("201") + " and " + url("198") + " " + url("201")}}}})
		if strings.Contains(mustJSON(t, events), "SECRET") {
			t.Error("SECRET leaked")
		}
		expect(t, only[domain.AssistantMessageEvent](events)[0].PullRequests, []domain.PullRequestRef{pr(201, "201"), pr(198, "198")})
	})
	t.Run("tool_result content as a string and as text blocks", func(t *testing.T) {
		fromString := toolResult("SECRET_OUT " + url("5") + "\n")
		fromBlocks := toolResult([]any{
			obj{"type": "text", "text": "created " + url("6")},
			obj{"type": "image", "source": url("7")},
			obj{"type": "text", "text": "SECRET_OUT " + url("8")},
		})
		expect(t, prsOf(fromString), []domain.PullRequestRef{pr(5, "5")})
		expect(t, prsOf(fromBlocks), []domain.PullRequestRef{pr(6, "6"), pr(8, "8")})
		if strings.Contains(mustJSON(t, [][]domain.TranscriptEvent{fromString, fromBlocks}), "SECRET") {
			t.Error("SECRET leaked")
		}
	})
	t.Run("user prompts and thinking blocks are not scanned", func(t *testing.T) {
		prompt := normalizeJSON(t, obj{"type": "user", "timestamp": "2026-01-01T10:00:00.000Z",
			"message": obj{"role": "user", "content": "please review " + url("9")}})
		thinking := normalizeJSON(t, obj{"type": "assistant", "timestamp": "2026-01-01T10:00:00.000Z",
			"message": obj{"id": "m2", "content": []any{obj{"type": "thinking", "thinking": url("10")}}}})
		if strings.Contains(mustJSON(t, [][]domain.TranscriptEvent{prompt, thinking}), "pull") {
			t.Error("PR link scanned")
		}
	})
}
