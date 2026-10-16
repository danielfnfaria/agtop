package store

import (
	"slices"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// agentContext is the context of an agent with the learned windows. Only the window reported for
// the agent's model applies: its own session is the parent's.
func agentContext(model string, used int, learned domain.LearnedContextWindows, reportedModel int) AgentContext {
	return AgentContext{
		Used:         used,
		ContextUsage: domain.ComputeContextUsage(model, used, learned, domain.ReportedWindows{Model: reportedModel}),
	}
}

func agentView(agent domain.AgentState, learned domain.LearnedContextWindows, reportedModel int) *AgentView {
	return &AgentView{
		Type:          agent.Type,
		Description:   agent.Description,
		Model:         agent.Model,
		IsBackground:  agent.IsBackground,
		Status:        agent.Status,
		FailureReason: agent.FailureReason,
		StartedAt:     agent.StartedAt,
		FinishedAt:    agent.FinishedAt,
		Totals:        agent.Totals,
		PendingTools:  slices.Clone(agent.PendingTools),
		Activity:      domain.CurrentAgentActivity(agent.Status, agent.PendingTools),
		Context:       agentContext(agent.Model, agent.Context.Used, learned, reportedModel),
	}
}
