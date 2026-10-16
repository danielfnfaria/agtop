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

func agentCounts(agents map[string]*store.AgentView) (active, total int) {
	for _, a := range agents {
		if domain.IsActive(a.Status) {
			active++
		}
	}
	return active, len(agents)
}

// treeModel is what the tree, the detail and the navigation derive from the snapshot and the
// navigation state (only its profile filter and open session are read).
type treeModel struct {
	multiProfile bool
	// filter is the profile filter in effect ("" = all; a label that no longer exists is ignored).
	filter   string
	sessions []*store.SessionView
	layout   model.TreeLayout
	ctx      model.NavContext
}

func buildTreeModel(snap *store.Snapshot, profiles []config.ProfileDir, nav model.NavState, columns int, line style.Lines) treeModel {
	var all []*store.SessionView
	if snap != nil {
		all = snap.Sessions
	}
	multi := len(profiles) > 1
	var labels []string
	if multi {
		if snap != nil {
			for _, p := range snap.Profiles {
				labels = append(labels, p.Label)
			}
		}
		if len(labels) == 0 {
			for _, p := range profiles {
				labels = append(labels, p.Label)
			}
		}
	}
	filter := ""
	if nav.ProfileFilter != "" && slices.Contains(labels, nav.ProfileFilter) {
		filter = nav.ProfileFilter
	}
	sessions := all
	if filter != "" {
		sessions = nil
		for _, s := range all {
			if s.ProfileLabel == filter {
				sessions = append(sessions, s)
			}
		}
	}
	keys := make([]string, len(sessions))
	trees := make([]model.TreeSession, len(sessions))
	var open *store.SessionView
	for i, s := range sessions {
		keys[i] = s.Key
		var active []string
		for _, a := range sortedAgents(s.Agents) {
			if domain.IsActive(a.agent.Status) {
				active = append(active, a.key)
			}
		}
		trees[i] = model.TreeSession{Key: s.Key, AgentKeys: active}
		if nav.DetailKey != "" && s.Key == nav.DetailKey {
			open = s
		}
	}
	layout := model.NewTreeLayout(trees, columns, model.SessionBoxHeight(multi), line)
	var agentKeys []string
	if open != nil {
		for _, a := range sortedAgents(open.Agents) {
			agentKeys = append(agentKeys, a.key)
		}
	}
	return treeModel{
		multiProfile: multi,
		filter:       filter,
		sessions:     sessions,
		layout:       layout,
		ctx:          model.NavContext{Keys: keys, Grid: layout.NavGrid(), AgentKeys: agentKeys, ProfileLabels: labels},
	}
}

func (t treeModel) find(key string) *store.SessionView {
	if key == "" {
		return nil
	}
	for _, s := range t.sessions {
		if s.Key == key {
			return s
		}
	}
	return nil
}
