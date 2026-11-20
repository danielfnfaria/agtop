package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
)

// This is the tree simulator: a harness, never part of the binary, that renders whole screens of
// made-up agent forests to plain text so the art can be judged by eye. It writes one file per
// scenario and size in the directory of AGTOP_SIM_DIR and is skipped without it, so a plain
// `go test ./...` never touches the disk.
//
//	AGTOP_SIM_DIR=/tmp/sim go test ./internal/ui -run TestSimulation

// simNode is a made-up agent with the agents it launched.
type simNode struct {
	children []simNode
}

// simLevels builds a forest by fan-out per level: levels[0] roots, each with levels[1] agents of
// its own, and so on.
func simLevels(levels ...int) []simNode {
	if len(levels) == 0 {
		return nil
	}
	out := make([]simNode, max(0, levels[0]))
	for i := range out {
		out[i] = simNode{children: simLevels(levels[1:]...)}
	}
	return out
}

type simScenario struct {
	name string
	// forests is the active agents of each session of the scenario, in order.
	forests [][]simNode
	// sizes are the terminals to render, simSizes when empty.
	sizes []simSize
}

// one is a scenario of a single session with the given agents.
func one(roots []simNode) [][]simNode { return [][]simNode{roots} }

func simScenarios() []simScenario {
	return []simScenario{
		{name: "flat-3", forests: one(simLevels(3))},
		{name: "flat-6", forests: one(simLevels(6))},
		{name: "flat-10", forests: one(simLevels(10))},
		{name: "flat-20", forests: one(simLevels(20))},
		{name: "nested-2x3", forests: one(simLevels(2, 3))},
		{name: "nested-3x4", forests: one(simLevels(3, 4))},
		{name: "deep-3", forests: one(simLevels(2, 3, 2))},
		{name: "fan-8", forests: one(simLevels(1, 8))},
		{name: "mixed", forests: one(append(simLevels(1, 5), simLevels(3)...))},
		// dense is the screen that showed the tree had to shrink to the body: two sessions, one
		// of them idle, and ten active agents nested two levels deep in the other.
		{
			name:    "dense",
			forests: [][]simNode{nil, simLevels(3, 3)},
			sizes:   stepSizes,
		},
		// many-sessions is the screen the steps of the session box were made for: five sessions,
		// four of them idle, and one with six agents of its own.
		{
			name:    "many-sessions",
			forests: [][]simNode{nil, nil, simLevels(6), nil, nil},
			sizes:   stepSizes,
		},
	}
}

// simSize is a terminal to render.
type simSize struct{ w, h int }

// stepSizes are the terminals the steps of the boxes are judged on: the heights where the tree
// stops fitting the body.
var stepSizes = []simSize{{90, 30}, {100, 30}, {120, 30}, {120, 40}, {160, 45}}

// simSizes are real terminal sizes, height included: the tree shrinks to the body as well as to
// the width (see model.NewTreeLayout), so a tall screen and a short one are different art.
var simSizes = []simSize{{80, 24}, {100, 30}, {120, 30}, {120, 40}, {160, 40}, {200, 50}}

var (
	simKinds  = []string{"coordinator", "executor", "reviewer", "explorer", "general-purpose", "planner"}
	simDescs  = []string{"splits the work", "writes the code", "reviews the diff", "looks something up", "answers a question", "plans the change"}
	simTools  = []string{"Grep", "Read", "Edit", "Bash", "WebFetch"}
	simModels = []string{"claude-opus-4-7", "claude-sonnet-4-5", "claude-haiku-4-5"}
)

// simSession is one session whose active agents are the given forest.
func simSession(roots []simNode) *store.SessionView {
	s := session("claude:sim", func(s *store.SessionView) {
		s.DisplayTitle, s.Cwd, s.ProjectName, s.GitBranch = "Simulated task", "/home/u/projects/sim", "sim", "main"
		s.Version, s.PID = "2.1.0", 999
		s.Model = &domain.ModelInfo{ID: "claude-opus-4-7", Name: "Opus 4.7"}
		s.ContextUsed, s.ContextPercent = 80_000, 40
		s.Agents = map[string]*store.AgentView{}
	})
	seen := 0
	var add func(nodes []simNode, parentID string, depth int)
	add = func(nodes []simNode, parentID string, depth int) {
		for i, node := range nodes {
			seen++
			id := fmt.Sprintf("ag-%d", seen)
			key := "k-" + strconv.Itoa(seen)
			seq := seen
			s.Agents[key] = agent(func(a *store.AgentView) {
				a.AgentID, a.ParentAgentID = id, parentID
				a.Type = simKinds[(depth+i)%len(simKinds)]
				a.Description = simDescs[seq%len(simDescs)]
				a.Model = simModels[seq%len(simModels)]
				// The start staggers so the display order never depends on the map order.
				a.StartedAt = now - int64(600_000-seq*7_000)
				if seq%4 == 3 {
					a.Status = domain.StatusLaunched
				} else {
					a.PendingTools = tools(domain.PendingTool{Name: simTools[seq%len(simTools)]})
				}
			})
			add(node.children, id, depth+1)
		}
	}
	add(roots, "", 0)
	return s
}

// simSessions is the sessions of a scenario: with more than one they take a key and a title of
// their own, so the view tells them apart.
func simSessions(forests [][]simNode) []*store.SessionView {
	out := make([]*store.SessionView, len(forests))
	for i, roots := range forests {
		s := simSession(roots)
		if len(forests) > 1 {
			s.Key = fmt.Sprintf("%s-%d", s.Key, i+1)
			s.SessionID = s.Key
			s.DisplayTitle = fmt.Sprintf("%s %d", s.DisplayTitle, i+1)
		}
		out[i] = s
	}
	return out
}

func TestSimulation(t *testing.T) {
	dir := os.Getenv("AGTOP_SIM_DIR")
	if dir == "" {
		t.Skip("set AGTOP_SIM_DIR to write the simulated screens")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	single := []config.ProfileDir{profiles[0]}
	for _, sc := range simScenarios() {
		sizes := sc.sizes
		if len(sizes) == 0 {
			sizes = simSizes
		}
		for _, size := range sizes {
			name := fmt.Sprintf("%s-%dx%d", sc.name, size.w, size.h)
			t.Run(name, func(t *testing.T) {
				snap := snapshot(simSessions(sc.forests), single)
				a := newApp(testOptions(newFakeStore(snap), single), size.w, size.h)
				out := screen(a) + "\n"
				if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
