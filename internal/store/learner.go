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
	id := modelID(s)
	l.learn(id, s.Context.Used, domain.ReportedWindows{
		Session: reports.ForSession(s.SessionID, id),
		Model:   reports.ForModel(id),
	})
	for _, agent := range s.Agents {
		l.learn(agent.Model, agent.Context.Used, domain.ReportedWindows{Model: reports.ForModel(agent.Model)})
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
	if own := reports.ForSession(s.SessionID, modelID(s)); own != 0 {
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
