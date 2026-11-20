package ui

import (
	"maps"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
)

// forestAgent is a view with only what the forest reads.
func forestAgent(id, parent string) *store.AgentView {
	return &store.AgentView{AgentID: id, ParentAgentID: parent, Status: domain.StatusRunning}
}

// forestOf lays out the given agents (already in display order) and renders the result as
// "<indent><key>" lines, so the order and the depth are both checked.
func forestOf(agents ...keyedAgent) string {
	var lines []string
	for _, a := range flatForest(agentForest(agents)) {
		lines = append(lines, strings.Repeat(" ", a.depth)+a.key)
	}
	return strings.Join(lines, "\n")
}

func forestInput(pairs ...string) []keyedAgent {
	out := make([]keyedAgent, 0, len(pairs))
	for _, pair := range pairs {
		key, parent, _ := strings.Cut(pair, "<")
		out = append(out, keyedAgent{key: key, agent: forestAgent(key, parent)})
	}
	return out
}

func TestAgentForest(t *testing.T) {
	tests := []struct {
		name  string
		input []keyedAgent
		want  string
	}{
		{"no parents: every agent is a root", forestInput("a", "b"), "a\nb"},
		{"two levels", forestInput("a", "b<a", "c<a"), "a\n b\n c"},
		{"three levels, children right after the parent", forestInput("a", "b<a", "c", "d<b"),
			"a\n b\n  d\nc"},
		{"a parent that is not in the list: the child is a root", forestInput("a", "b<gone"), "a\nb"},
		{"a parent keyed by its toolUseId is not seen", []keyedAgent{
			{key: "toolu_1", agent: forestAgent("", "")},
			{key: "b", agent: forestAgent("b", "a1")},
		}, "toolu_1\nb"},
		{"self reference", forestInput("a<a", "b"), "a\nb"},
		{"cycle a -> b -> a", forestInput("a<b", "b<a"), "a\n b"},
		{"cycle of three", forestInput("a<c", "b<a", "c<b"), "a\n b\n  c"},
		{"siblings keep the display order", forestInput("a", "c<a", "b<a"), "a\n c\n b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forestOf(tt.input...); got != tt.want {
				t.Errorf("forest =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestAgentParentsBreaksOnlyTheCycle pins that only the edge of the smallest key of the cycle
// falls: c merely hangs from the cycle, so it keeps the parent it has, whatever order the agents
// come in.
func TestAgentParentsBreaksOnlyTheCycle(t *testing.T) {
	want := map[string]string{"c": "a", "b": "a"}
	orders := [][]string{
		{"c<a", "a<b", "b<a"},
		{"a<b", "b<a", "c<a"},
		{"b<a", "c<a", "a<b"},
		{"b<a", "a<b", "c<a"},
		{"a<b", "c<a", "b<a"},
		{"c<a", "b<a", "a<b"},
	}
	for _, order := range orders {
		got := agentParents(forestInput(order...))
		if !maps.Equal(got, want) {
			t.Errorf("parents of %v = %v, want %v", order, got, want)
		}
	}
}

func TestAgentForestDropsInactiveParents(t *testing.T) {
	agents := map[string]*store.AgentView{
		"a": agent(func(a *store.AgentView) {
			a.AgentID, a.Status, a.StartedAt = "a", domain.StatusCompleted, now-30_000
		}),
		"b": agent(func(a *store.AgentView) { a.AgentID, a.ParentAgentID, a.StartedAt = "b", "a", now-20_000 }),
		"c": agent(func(a *store.AgentView) { a.AgentID, a.ParentAgentID, a.StartedAt = "c", "b", now-10_000 }),
	}
	// The detail shows every agent, so the whole chain is there.
	var all []string
	for _, a := range orderedAgents(agents) {
		all = append(all, strings.Repeat(" ", a.depth)+a.key)
	}
	if got := strings.Join(all, "\n"); got != "a\n b\n  c" {
		t.Errorf("detail order =\n%s", got)
	}
	// The tree shows only the active ones: b lost its parent and became a root.
	if got := strings.Join(forestKeys(activeForest(agents)), "\n"); got != "b\n c" {
		t.Errorf("tree order =\n%s", got)
	}
}

func forestKeys(nodes []agentNode) []string {
	var out []string
	for _, a := range flatForest(nodes) {
		out = append(out, strings.Repeat(" ", a.depth)+a.key)
	}
	return out
}
