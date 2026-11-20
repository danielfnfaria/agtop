package ui

import (
	"slices"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// agentStart adapts an agent view to the start order of the domain.
type agentStart struct{ *store.AgentView }

func (a agentStart) GetStartedAt() int64 { return a.StartedAt }

type keyedAgent struct {
	key   string
	agent *store.AgentView
	// depth is how far the agent is from its root of the forest: 0 at the first level.
	depth int
}

// agentNode is an agent of the forest with the agents it launched, in display order.
type agentNode struct {
	keyedAgent
	children []agentNode
}

// sortedAgents is the agents by start (unknown last), ties by key.
func sortedAgents(agents map[string]*store.AgentView) []keyedAgent {
	list := make([]domain.KeyedAgent[agentStart], 0, len(agents))
	for k, a := range agents {
		list = append(list, domain.KeyedAgent[agentStart]{Key: k, Agent: agentStart{a}})
	}
	slices.SortFunc(list, domain.CompareByStart[agentStart])
	out := make([]keyedAgent, len(list))
	for i, a := range list {
		out[i] = keyedAgent{key: a.Key, agent: a.Agent.AgentView}
	}
	return out
}

// agentParents maps each agent to the key of its parent, for the agents that are shown: an
// agent with no parentAgentId, whose parent is not in the list (not seen, keyed by its launch
// toolUseId, or no longer shown because it finished), that points at itself or that is the
// smallest key of a cycle gets no entry, so it is a root.
func agentParents(agents []keyedAgent) map[string]string {
	index := make(map[string]string, len(agents))
	for _, a := range agents {
		if a.agent.AgentID == "" {
			continue
		}
		if _, dup := index[a.agent.AgentID]; !dup {
			index[a.agent.AgentID] = a.key
		}
	}
	parents := make(map[string]string, len(agents))
	for _, a := range agents {
		if a.agent.ParentAgentID == "" {
			continue
		}
		if parent, ok := index[a.agent.ParentAgentID]; ok && parent != a.key {
			parents[a.key] = parent
		}
	}
	// A corrupt meta or a recycled key can make a cycle, and a cycle has no root: the agent with
	// the smallest key on it loses its parent, a choice that depends only on who is on the cycle,
	// never on the order the agents come in. Only that edge falls, so an agent that merely hangs
	// from a cycle keeps its parent. Keys already known to lead to a root are not walked again.
	rooted := make(map[string]bool, len(parents))
	for _, a := range agents {
		var path []string
		onPath := make(map[string]bool)
		for key := a.key; !rooted[key]; {
			if onPath[key] {
				// The walk is back on its own path: from key on, path is the cycle.
				delete(parents, slices.Min(path[slices.Index(path, key):]))
				break
			}
			onPath[key] = true
			path = append(path, key)
			parent, ok := parents[key]
			if !ok {
				break
			}
			key = parent
		}
		// Whatever the walk crossed now leads to a root: the cycle it may have found is broken.
		for _, key := range path {
			rooted[key] = true
		}
	}
	return parents
}

// agentForest arranges the agents, given in display order, as the forest of who launched whom:
// the roots in that same order, each node's children too. It is pure: it only reads the views.
func agentForest(agents []keyedAgent) []agentNode {
	parents := agentParents(agents)
	children := make(map[string][]keyedAgent, len(agents))
	var roots []keyedAgent
	for _, a := range agents {
		if parent, ok := parents[a.key]; ok {
			children[parent] = append(children[parent], a)
			continue
		}
		roots = append(roots, a)
	}
	var build func(a keyedAgent, depth int) agentNode
	build = func(a keyedAgent, depth int) agentNode {
		a.depth = depth
		node := agentNode{keyedAgent: a}
		for _, child := range children[a.key] {
			node.children = append(node.children, build(child, depth+1))
		}
		return node
	}
	out := make([]agentNode, len(roots))
	for i, root := range roots {
		out[i] = build(root, 0)
	}
	return out
}

// flatForest is the forest in depth-first order (a parent right before its children): the one
// list the tree, the detail and the navigation share.
func flatForest(nodes []agentNode) []keyedAgent {
	var out []keyedAgent
	var walk func([]agentNode)
	walk = func(nodes []agentNode) {
		for _, node := range nodes {
			out = append(out, node.keyedAgent)
			walk(node.children)
		}
	}
	walk(nodes)
	return out
}

// treeAgents is the forest as the layout takes it.
func treeAgents(nodes []agentNode) []model.TreeAgent {
	out := make([]model.TreeAgent, len(nodes))
	for i, node := range nodes {
		out[i] = model.TreeAgent{Key: node.key, Children: treeAgents(node.children)}
	}
	return out
}

// orderedAgents is every agent of the session in forest order.
func orderedAgents(agents map[string]*store.AgentView) []keyedAgent {
	return flatForest(agentForest(sortedAgents(agents)))
}

// activeForest is the forest of the active agents only: a child whose parent is not active
// anymore is a root of its own.
func activeForest(agents map[string]*store.AgentView) []agentNode {
	var active []keyedAgent
	for _, a := range sortedAgents(agents) {
		if domain.IsActive(a.agent.Status) {
			active = append(active, a)
		}
	}
	return agentForest(active)
}

func agentCounts(agents map[string]*store.AgentView) (active, total int) {
	for _, a := range agents {
		if domain.IsActive(a.Status) {
			active++
		}
	}
	return active, len(agents)
}

// treeSel is the sessions the tree shows, with no layout yet: the status bar is measured from
// them, and only then are the rows left for the body known, which the layout needs to shrink the
// agent boxes (see model.NewTreeLayout).
type treeSel struct {
	multiProfile bool
	// filter is the profile filter in effect ("" = all; a label that no longer exists is ignored).
	filter string
	// labels are the profiles the filter may cycle through (nil with a single profile).
	labels   []string
	sessions []*store.SessionView
}

// selectSessions is the sessions of the snapshot that the profile filter of the navigation
// leaves, in the order of the snapshot.
func selectSessions(snap *store.Snapshot, profiles []config.ProfileDir, nav model.NavState) treeSel {
	var all []*store.SessionView
	if snap != nil {
		all = snap.Sessions
	}
	sel := treeSel{multiProfile: len(profiles) > 1, sessions: all}
	if sel.multiProfile {
		if snap != nil {
			for _, p := range snap.Profiles {
				sel.labels = append(sel.labels, p.Label)
			}
		}
		if len(sel.labels) == 0 {
			for _, p := range profiles {
				sel.labels = append(sel.labels, p.Label)
			}
		}
	}
	if nav.ProfileFilter == "" || !slices.Contains(sel.labels, nav.ProfileFilter) {
		return sel
	}
	sel.filter, sel.sessions = nav.ProfileFilter, nil
	for _, s := range all {
		if s.ProfileLabel == sel.filter {
			sel.sessions = append(sel.sessions, s)
		}
	}
	return sel
}

func (sel treeSel) find(key string) *store.SessionView {
	if key == "" {
		return nil
	}
	for _, s := range sel.sessions {
		if s.Key == key {
			return s
		}
	}
	return nil
}

// treeModel is what the tree, the detail and the navigation derive from the selected sessions and
// the navigation state (only its open session is read).
type treeModel struct {
	treeSel
	layout model.TreeLayout
	ctx    model.NavContext
}

// buildTreeModel lays the selected sessions out in columns columns and bodyRows rows
// (model.UnlimitedRows when the height is not known; see model.NewTreeLayout).
func buildTreeModel(sel treeSel, nav model.NavState, columns, bodyRows int, line style.Lines) treeModel {
	keys := make([]string, len(sel.sessions))
	trees := make([]model.TreeSession, len(sel.sessions))
	for i, s := range sel.sessions {
		keys[i] = s.Key
		trees[i] = model.TreeSession{Key: s.Key, Agents: treeAgents(activeForest(s.Agents))}
	}
	layout := model.NewTreeLayout(trees, columns, bodyRows, model.SessionBoxHeight(sel.multiProfile), line)
	var agentKeys []string
	if open := sel.find(nav.DetailKey); open != nil {
		for _, a := range orderedAgents(open.Agents) {
			agentKeys = append(agentKeys, a.key)
		}
	}
	return treeModel{
		treeSel: sel,
		layout:  layout,
		ctx: model.NavContext{Keys: keys, Grid: layout.NavGrid(), AgentKeys: agentKeys,
			ProfileLabels: sel.labels},
	}
}
