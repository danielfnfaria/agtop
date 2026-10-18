package store

import (
	"slices"
	"strconv"
	"strings"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// contextLearner keeps the window learned per model (id without suffix), in memory: any session
// (live or stale) or agent whose use passes the table/default window teaches 1M to every one of
// the same model. It only grows while the store runs.
type contextLearner struct {
	learned domain.LearnedContextWindows
	// reportsOf gives the windows Claude Code reported for a profile label.
	reportsOf func(label string) domain.ContextReports
}

func newContextLearner(reportsOf func(label string) domain.ContextReports) *contextLearner {
	if reportsOf == nil {
		reportsOf = func(string) domain.ContextReports { return domain.NoReports }
	}
	return &contextLearner{learned: domain.LearnedContextWindows{}, reportsOf: reportsOf}
}

func (l *contextLearner) reportsFor(label string) domain.ContextReports {
	if label == "" {
		return domain.NoReports
	}
	return l.reportsOf(label)
}

func modelID(s domain.SessionState) string {
	if s.Model == nil {
		return ""
	}
	return s.Model.ID
}

// sessionWindows are the reported windows of the session: the ones of the context file and,
// when the provider itself told the window of the session's model (ModelInfo.ContextWindow),
// that one as the session's own.
func sessionWindows(s domain.SessionState, reports domain.ContextReports) domain.ReportedWindows {
	id := modelID(s)
	w := domain.ReportedWindows{Session: reports.ForSession(s.SessionID, id), Model: reports.ForModel(id)}
	if s.Model != nil && s.Model.ContextWindow > 0 {
		w.Session = s.Model.ContextWindow
	}
	return w
}

// agentWindow is the reported window of an agent's model: from the context file, else the
// session's provider-reported window when the agent runs the same model.
func agentWindow(s domain.SessionState, agent domain.AgentState, reports domain.ContextReports) int {
	if size := reports.ForModel(agent.Model); size != 0 {
		return size
	}
	if s.Model != nil && s.Model.ContextWindow > 0 && agent.Model != "" && agent.Model == s.Model.ID {
		return s.Model.ContextWindow
	}
	return 0
}

func (l *contextLearner) learn(model string, used int, reported domain.ReportedWindows) {
	base, size, ok := domain.LearnContextWindow(model, used, reported)
	if ok && l.learned[base] < size {
		l.learned[base] = size
	}
}

// learnFrom feeds the use of the session and its agents (of profile label). A reported window
// (of the session or the model) teaches nothing.
func (l *contextLearner) learnFrom(s domain.SessionState, label string) {
	reports := l.reportsFor(label)
	l.learn(modelID(s), s.Context.Used, sessionWindows(s, reports))
	for _, agent := range s.Agents {
		l.learn(agent.Model, agent.Context.Used, domain.ReportedWindows{Model: agentWindow(s, agent, reports)})
	}
}

// learnedSignature covers the learned and reported windows that touch the models of the session
// and its agents: the view is rebuilt only when one of them changes.
func (l *contextLearner) learnedSignature(s domain.SessionState, label string) string {
	reports := l.reportsFor(label)
	ids := map[string]bool{}
	if id := modelID(s); id != "" {
		ids[id] = true
	}
	for _, agent := range s.Agents {
		if agent.Model != "" {
			ids[agent.Model] = true
		}
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	slices.Sort(sorted)
	var parts []string
	for _, id := range sorted {
		if size, ok := l.learned[domain.ModelIDBase(id)]; ok {
			parts = append(parts, id+"="+strconv.Itoa(size))
		}
		if model := reports.ForModel(id); model != 0 {
			parts = append(parts, id+":model="+strconv.Itoa(model))
		}
	}
	if own := sessionWindows(s, reports).Session; own != 0 {
		parts = append(parts, "session="+strconv.Itoa(own))
	}
	return strings.Join(parts, ",")
}

// learnedFor is the learned window of the base of model, if any.
func (l *contextLearner) learnedFor(model string) (int, bool) {
	if model == "" {
		return 0, false
	}
	size, ok := l.learned[domain.ModelIDBase(model)]
	return size, ok
}
