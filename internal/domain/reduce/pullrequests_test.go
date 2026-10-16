package reduce

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

const prISO = "2026-01-01T10:00:00.000Z"

func prURL(n int, repo string) string {
	return fmt.Sprintf("https://github.com/acme/%s/pull/%d", repo, n)
}

func pr(n int, repo string) domain.PullRequestRef {
	return domain.PullRequestRef{Owner: "acme", Repo: repo, Number: n, URL: prURL(n, repo)}
}

func line(t *testing.T, v map[string]any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assistantText(t *testing.T, id, text string, extra map[string]any) string {
	m := map[string]any{"type": "assistant", "timestamp": prISO,
		"message": obj{"id": id, "model": "claude-test-1", "content": []any{obj{"type": "text", "text": text}}}}
	for k, v := range extra {
		m[k] = v
	}
	return line(t, m)
}

// toolUseLine is an assistant line with one tool_use (input stays in the normalizer).
func toolUseLine(t *testing.T, messageID, id, name string, input, extra map[string]any) string {
	m := map[string]any{"type": "assistant", "timestamp": prISO,
		"message": obj{"id": messageID, "content": []any{obj{"type": "tool_use", "id": id, "name": name, "input": input}}}}
	for k, v := range extra {
		m[k] = v
	}
	return line(t, m)
}

func toolResultLine(t *testing.T, toolUseID string, content any) string {
	return line(t, obj{"type": "user", "timestamp": prISO,
		"message": obj{"role": "user", "content": []any{obj{"type": "tool_result", "tool_use_id": toolUseID, "content": content}}}})
}

func prNumbers(prs []domain.PullRequestRef) []int {
	out := []int{}
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}

func sessionOf(lines ...string) domain.SessionState {
	return ReduceSessionEvents(NewSessionState("s"), normalize(lines...))
}

func TestReducerPullRequests(t *testing.T) {
	t.Run("session collects from assistant text and gh results, deduplicated, in order", func(t *testing.T) {
		s := sessionOf(
			assistantText(t, "m1", "opened "+prURL(1, "app"), nil),
			toolUseLine(t, "m2", "t1", "Bash", obj{"command": "gh pr list"}, nil),
			toolResultLine(t, "t1", prURL(2, "app")+" "+prURL(1, "app")),
			assistantText(t, "m3", "merged "+prURL(3, "app"), nil),
		)
		expectEqual(t, prNumbers(s.PullRequests), []int{1, 2, 3})
	})
	t.Run("a Read result with 8 links leaves the list as it was", func(t *testing.T) {
		before := sessionOf(assistantText(t, "m1", "opened "+prURL(1, "app"), nil))
		var links []string
		for i := range 8 {
			links = append(links, prURL(100+i, "app"))
		}
		s := ReduceSessionEvents(before, normalize(
			toolUseLine(t, "m2", "t1", "Read", obj{"file_path": "/repo/CHANGELOG.md"}, nil),
			toolResultLine(t, "t1", strings.Join(links, "\n")),
		))
		expectEqual(t, prNumbers(s.PullRequests), []int{1})
	})
	t.Run("a Bash without gh, an unknown tool_use_id and other tools do not add", func(t *testing.T) {
		s := sessionOf(
			toolUseLine(t, "m1", "t1", "Bash", obj{"command": "grep -r pull/ . # " + prURL(1, "app")}, nil),
			toolResultLine(t, "t1", prURL(2, "app")),
			toolResultLine(t, "t-unknown", prURL(3, "app")),
			toolUseLine(t, "m2", "t2", "WebFetch", obj{"url": prURL(4, "app")}, nil),
			toolResultLine(t, "t2", prURL(5, "app")),
		)
		expectEqual(t, s.PullRequests, []domain.PullRequestRef{})
	})
	t.Run("gh pr create adds the created PR", func(t *testing.T) {
		s := sessionOf(
			toolUseLine(t, "m1", "t1", "Bash", obj{"command": "git push -u origin x && gh pr create --fill"}, nil),
			toolResultLine(t, "t1", prURL(42, "app")+"\n"),
		)
		expectEqual(t, s.PullRequests, []domain.PullRequestRef{pr(42, "app")})
	})
	t.Run("the result of an Agent/Task tool adds (the report names the PRs)", func(t *testing.T) {
		s := sessionOf(
			toolUseLine(t, "m1", "t1", "Agent", obj{"subagent_type": "executor", "description": "x"}, nil),
			toolResultLine(t, "t1", "done: "+prURL(7, "app")),
			toolUseLine(t, "m2", "t2", "Task", obj{"subagent_type": "executor", "description": "y"}, nil),
			toolResultLine(t, "t2", "done: "+prURL(8, "app")),
		)
		expectEqual(t, prNumbers(s.PullRequests), []int{7, 8})
	})
	t.Run("limit: keeps the most recent MaxPullRequests", func(t *testing.T) {
		var lines []string
		for i := range 12 {
			lines = append(lines, assistantText(t, fmt.Sprintf("m%d", i), "pr "+prURL(i+1, "app"), nil))
		}
		expectEqual(t, prNumbers(sessionOf(lines...).PullRequests), []int{5, 6, 7, 8, 9, 10, 11, 12})
	})
	t.Run("PRs of an agent transcript stay on the agent and go up to the session", func(t *testing.T) {
		base := sessionOf(assistantText(t, "m1", "session "+prURL(1, "app"), nil))
		agentExtra := obj{"agentId": "ag-1"}
		agentEvents := normalize(
			assistantText(t, "am1", "agent "+prURL(20, "lib"), agentExtra),
			toolUseLine(t, "am2", "at1", "Bash", obj{"command": "gh pr create --fill"}, agentExtra),
			toolResultLine(t, "at1", "gh output "+prURL(21, "lib")),
			toolUseLine(t, "am3", "at2", "Read", obj{"file_path": "/x/CHANGELOG.md"}, agentExtra),
			toolResultLine(t, "at2", "changelog "+prURL(22, "lib")),
		)
		s := ReduceSessionAgentEvents(base, "ag-1", agentEvents)
		expectEqual(t, s.PullRequests, []domain.PullRequestRef{pr(1, "app"), pr(20, "lib"), pr(21, "lib")})
		expectEqual(t, agentOf(t, s, "ag-1").PullRequests, []domain.PullRequestRef{pr(20, "lib"), pr(21, "lib")})
	})
	t.Run("no PR in the event: the list keeps its identity", func(t *testing.T) {
		s := sessionOf(assistantText(t, "m1", "x "+prURL(1, "app"), nil))
		next := ReduceSession(s, domain.UserPromptEvent{})
		if !sameSlice(next.PullRequests, s.PullRequests) {
			t.Error("pull request list copied")
		}
	})
	t.Run("merging two views of an agent keeps the PRs of both", func(t *testing.T) {
		launch := NewAgentState()
		launch.ToolUseID = "t"
		launch.PullRequests = []domain.PullRequestRef{pr(1, "app")}
		other := NewAgentState()
		other.AgentID = "a"
		other.PullRequests = []domain.PullRequestRef{pr(2, "app"), pr(1, "app")}
		expectEqual(t, prNumbers(mergeAgents(launch, other).PullRequests), []int{1, 2})
	})
}
