package reduce

import (
	"github.com/danielfnfaria/agtop/internal/domain"
)

// Agent reducer (the sub-agent transcript and its meta.json).

// HandbackToolName is the tool a sub-agent returns its result with, ending its run.
const HandbackToolName = "SubagentHandback"

func isHandback(name string) bool { return name == HandbackToolName }

// markRunning moves launched/unknown to running; it never demotes handed_back or a final status.
func markRunning(a domain.AgentState, changed bool) (domain.AgentState, bool) {
	if a.Status == domain.StatusRunning || domain.IsSettled(a.Status) {
		return a, changed
	}
	a.Status = domain.StatusRunning
	return a, true
}

// handedBackBeforeKill reports an agent that handed back before a system agents_killed marked it
// failed (session transcript read first): the handback wins, as in the other read order.
func handedBackBeforeKill(a domain.AgentState, ts int64) bool {
	return a.Status == domain.StatusFailed && a.FailureReason == "killed" &&
		a.FinishedAt != 0 && ts != 0 && ts <= a.FinishedAt
}

// Agent applies an event of the agent's own transcript.
func Agent(state domain.AgentState, event domain.TranscriptEvent) domain.AgentState {
	a, _ := reduceAgent(state, event)
	return a
}

// reduceAgent also reports whether the agent changed (where the TypeScript reducer returns a new
// object rather than its input).
func reduceAgent(state domain.AgentState, event domain.TranscriptEvent) (domain.AgentState, bool) {
	a := state
	ts := event.EventTimestamp()
	changed := touch(&a.ActivityState, ts)
	if _, ignored := event.(domain.IgnoredEvent); a.StartedAt == 0 && ts != 0 && !ignored {
		a.StartedAt = ts
		changed = true
	}
	switch ev := event.(type) {
	case domain.EnvelopeInfoEvent:
		if ev.AgentID != "" && a.AgentID == "" {
			a.AgentID = ev.AgentID
			changed = true
		}
		return a, changed
	case domain.AssistantMessageEvent:
		if applyUsage(&a.ActivityState, ev) {
			changed = true
		}
		if markToolUsesSeen(&a.ActivityState, ev) {
			changed = true
		}
		if addEventPullRequests(&a.ActivityState, ev.PullRequests) {
			changed = true
		}
		if ev.Model != "" && !isSynthetic(ev) && a.Model != ev.Model {
			a.Model = ev.Model
			changed = true
		}
		if addPendingTools(&a.ActivityState, ev, isHandback) {
			changed = true
		}
		handedBack := false
		for _, t := range ev.ToolUses {
			if isHandback(t.Name) {
				handedBack = true
				break
			}
		}
		if !handedBack {
			return markRunning(a, changed)
		}
		if domain.IsFinal(a.Status) && !handedBackBeforeKill(a, ts) {
			return a, changed
		}
		// The parent already received this result (session transcript read first): it is over.
		received := a.HandbackReceivedAt != 0 && ts != 0 && a.HandbackReceivedAt >= ts
		a.Status = domain.StatusHandedBack
		if received {
			a.Status = domain.StatusCompleted
		}
		if ts != 0 {
			a.FinishedAt = ts
		}
		a.FailureReason = ""
		return a, true
	case domain.ToolResultEvent:
		// The pending tool tells whether the result may carry PRs: read before it is removed.
		if addEventPullRequests(&a.ActivityState, eventPullRequests(&a.ActivityState, ev)) {
			changed = true
		}
		// The result of something not pending (e.g. the SubagentHandback itself) or arriving
		// after the handback (another tool of the same message) does not reopen the agent.
		if !removePendingTool(&a.ActivityState, ev.ToolUseID) {
			return a, changed
		}
		return markRunning(a, true)
	case domain.UserPromptEvent:
		// A new message to the agent after it settled (also meta, like the coordinator's resume
		// through SendMessage) reopens it; older prompts (session transcript read first) do not.
		if isResumeAfterFinish(a, ts, false) {
			return reopenAgent(a, domain.StatusRunning), true
		}
		return markRunning(a, changed)
	}
	return a, changed
}

// SessionAgent applies an event of the transcript agent-<agentID>.jsonl to that agent of
// the session.
func SessionAgent(session domain.SessionState, agentID string, event domain.TranscriptEvent) domain.SessionState {
	key, found := FindAgentKey(session.Agents, agentID, "")
	var existing domain.AgentState
	var seen *domain.ActivityState
	if found {
		existing = session.Agents[key]
		seen = &existing.ActivityState
	}
	// The agent's activity counts in the session histogram even when the agent does not change;
	// its PRs go up to the session (filtered by the agent's pending tool, as in the agent).
	s := session
	recordActivity(&s, event, seen)
	addEventPullRequests(&s.ActivityState, eventPullRequests(seen, event))
	if !found {
		existing = NewAgentState()
		existing.AgentID = agentID
	}
	agent, changed := reduceAgent(existing, event)
	if found && !changed {
		return s
	}
	if agent.AgentID == "" {
		agent.AgentID = agentID
	}
	if !found {
		key = ""
	}
	return putAgent(s, agent, key)
}

// SessionAgentEvents applies the events of an agent transcript in order.
func SessionAgentEvents(session domain.SessionState, agentID string, events []domain.TranscriptEvent) domain.SessionState {
	for _, event := range events {
		session = SessionAgent(session, agentID, event)
	}
	return session
}

// ApplyAgentMeta applies the content of agent-<agentID>.meta.json (validated by
// transcript.ParseAgentMeta).
func ApplyAgentMeta(session domain.SessionState, agentID string, meta domain.AgentMeta) domain.SessionState {
	key, found := FindAgentKey(session.Agents, agentID, meta.ToolUseID)
	var agent domain.AgentState
	if found {
		agent = session.Agents[key]
	} else {
		key = ""
		agent = NewAgentState()
		agent.AgentID = agentID
		agent.Status = domain.StatusLaunched
	}
	agent.AgentID = agentID
	if agent.Type == "unknown" && meta.AgentType != "" {
		agent.Type = meta.AgentType
	}
	if agent.Description == "" && meta.Description != "" {
		agent.Description = meta.Description
	}
	if agent.ToolUseID == "" {
		agent.ToolUseID = meta.ToolUseID
	}
	if agent.Model == "" {
		agent.Model = meta.Model
	}
	if meta.SpawnDepth != nil {
		depth := *meta.SpawnDepth
		agent.SpawnDepth = &depth
	}
	// The meta.json has no time: finishedAt only if there is one already.
	if meta.StoppedByUser {
		agent = finishAgent(agent, domain.StatusFailed, 0, "stopped")
	}
	return putAgent(session, agent, key)
}
