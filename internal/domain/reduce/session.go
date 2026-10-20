package reduce

import (
	"maps"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// Session reducer (the session's own transcript).

var failedNotificationStatuses = map[string]bool{
	"failed": true, "failure": true, "error": true, "killed": true, "cancelled": true,
	"canceled": true, "stopped": true, "timeout": true, "timed_out": true,
}

// NewSessionState returns an empty session.
func NewSessionState(sessionID string) domain.SessionState {
	return domain.SessionState{
		SessionID:         sessionID,
		ActivityState:     emptyActivity(),
		Agents:            map[string]domain.AgentState{},
		ActivityHistogram: domain.ActivityHistogram{},
	}
}

func launchAgentsFromAssistant(session domain.SessionState, ev domain.AssistantMessageEvent) domain.SessionState {
	next := session
	for _, toolUse := range ev.ToolUses {
		launch := toolUse.AgentLaunch
		if launch == nil {
			continue
		}
		key, found := FindAgentKey(next.Agents, "", toolUse.ID)
		agent := NewAgentState()
		agent.ToolUseID = toolUse.ID
		agent.Type = launch.Type
		agent.Description = launch.Description
		agent.IsBackground = launch.RunInBackground
		agent.Status = domain.StatusLaunched
		agent.Model = launch.Model
		agent.StartedAt = ev.Timestamp
		if found {
			agent = mergeAgents(agent, next.Agents[key])
		} else {
			key = ""
		}
		next = putAgent(next, agent, key)
	}
	return next
}

func applyToolResultToAgents(session domain.SessionState, ev domain.ToolResultEvent) domain.SessionState {
	launch := ev.AgentLaunch
	// Prefer the entry created by the tool_use; putAgent merges it with the transcript's, if any.
	key, found := FindAgentKey(session.Agents, "", ev.ToolUseID)
	if !found && launch != nil {
		key, found = FindAgentKey(session.Agents, launch.AgentID, "")
	}
	if !found {
		key = ""
	}

	if launch != nil {
		// Launch confirmed: the agentId is known now. In real data every Agent (foreground too)
		// returns async_launched; isBackground comes only from run_in_background.
		var agent domain.AgentState
		if found {
			agent = session.Agents[key]
		} else {
			agent = NewAgentState()
			agent.ToolUseID = ev.ToolUseID
			agent.Status = domain.StatusLaunched
			agent.StartedAt = ev.Timestamp
		}
		if launch.ResolvedModel != "" {
			agent.Model = launch.ResolvedModel
		}
		agent.AgentID = launch.AgentID
		if agent.ToolUseID == "" {
			agent.ToolUseID = ev.ToolUseID
		}
		if agent.Description == "" {
			agent.Description = launch.Description
		}
		if resumed, ok := resumeAgent(session, agent, ev.ToolUseID); ok {
			return resumed
		}
		return putAgent(session, agent, key)
	}

	if ev.ResumedAgentID != "" {
		return resumeFinishedAgent(session, ev)
	}
	if !found {
		return session
	}
	// The result of an agent without async_launched (robustness): the agent ended.
	existing := session.Agents[key]
	var agent domain.AgentState
	if ev.IsError {
		agent = finishAgent(existing, domain.StatusFailed, ev.Timestamp, "error")
	} else {
		agent = finishAgent(existing, domain.StatusCompleted, ev.Timestamp, "")
	}
	if ev.ResultAgentID != "" && agent.AgentID == "" {
		agent.AgentID = ev.ResultAgentID
	}
	return putAgent(session, agent, key)
}

// resumeFinishedAgent handles a tool (e.g. SendMessage) that resumed a finished agent
// (toolUseResult.resumedAgentId): it runs again in the same transcript until its next
// handback. A resume older than the agent's end (agent transcript read first) changes nothing.
func resumeFinishedAgent(session domain.SessionState, ev domain.ToolResultEvent) domain.SessionState {
	if ev.ResumedAgentID == "" || ev.IsError {
		return session
	}
	key, found := FindAgentKey(session.Agents, ev.ResumedAgentID, "")
	if !found {
		return session
	}
	existing := session.Agents[key]
	if !isResumeAfterFinish(existing, ev.Timestamp, true) {
		return session
	}
	return putAgent(session, reopenAgent(existing, domain.StatusRunning), key)
}

func notificationStatus(status string) domain.AgentStatus {
	if status == "completed" || status == "success" {
		return domain.StatusCompleted
	}
	if failedNotificationStatuses[status] {
		return domain.StatusFailed
	}
	return domain.StatusUnknown
}

func applyTaskNotification(session domain.SessionState, ev domain.TaskNotificationEvent) domain.SessionState {
	key, found := FindAgentKey(session.Agents, ev.TaskID, ev.ToolUseID)
	// Notifications of tasks that are not agents (e.g. a background Bash) are ignored.
	if !found {
		return session
	}
	existing := session.Agents[key]
	// Without <status> the notification does not say how the agent ended: nothing changes.
	if domain.IsFinal(existing.Status) || ev.Status == "" {
		return session
	}
	finished := finishAgent(existing, notificationStatus(ev.Status), ev.Timestamp, ev.Status)
	if existing.AgentID == "" && ev.TaskID != "" {
		finished.AgentID = ev.TaskID
	}
	return putAgent(session, finished, key)
}

// applyAgentHandback handles a sub-agent's result reaching the parent (origin.kind peer with
// handback).
func applyAgentHandback(session domain.SessionState, ev domain.AgentHandbackEvent) domain.SessionState {
	if !ev.Handback {
		return session
	}
	key, found := FindAgentKey(session.Agents, ev.AgentID, "")
	if !found {
		return session
	}
	existing := session.Agents[key]
	received := maxDefined(existing.HandbackReceivedAt, ev.Timestamp)
	withReceived := existing
	withReceived.HandbackReceivedAt = received
	if domain.IsFinal(existing.Status) {
		if received == existing.HandbackReceivedAt {
			return session
		}
		return putAgent(session, withReceived, key)
	}
	// A handback from an earlier run of an agent that is active again (agent transcript read
	// first and already past the resume) does not end the current run.
	if domain.IsActive(existing.Status) && ev.Timestamp != 0 && existing.LastActivityAt != 0 &&
		existing.LastActivityAt > ev.Timestamp {
		return putAgent(session, withReceived, key)
	}
	// An older handback (agent transcript read first, then resumed and handed back again) does
	// not move finishedAt back.
	finishedAt := maxDefined(existing.FinishedAt, ev.Timestamp)
	return putAgent(session, finishAgent(withReceived, domain.StatusCompleted, finishedAt, ""), key)
}

// applyAgentsKilled handles system agents_killed: every agent still running ends as failed.
func applyAgentsKilled(session domain.SessionState, ts int64) domain.SessionState {
	var agents map[string]domain.AgentState
	for key, agent := range session.Agents {
		// handed_back already returned its result: it is not demoted to failed.
		if domain.IsSettled(agent.Status) {
			continue
		}
		// Launched after the kill (agent transcript read before the session's): not affected.
		if ts != 0 && agent.StartedAt != 0 && agent.StartedAt > ts {
			continue
		}
		if agents == nil {
			agents = maps.Clone(session.Agents)
		}
		agents[key] = finishAgent(agent, domain.StatusFailed, ts, "killed")
	}
	if agents != nil {
		session.Agents = agents
	}
	return session
}

// applyMessageModel: the model attachment is authoritative (it has the [1m] suffix and the
// marketing name); message.model only applies without one or on a real model change (a
// different base id).
func applyMessageModel(session domain.SessionState, model string) domain.SessionState {
	if current := session.Model; current != nil && domain.ModelIDBase(current.ID) == domain.ModelIDBase(model) {
		return session
	}
	session.Model = &domain.ModelInfo{ID: model}
	return session
}

// Session applies an event of the session's own transcript.
func Session(state domain.SessionState, event domain.TranscriptEvent) domain.SessionState {
	s := state
	touch(&s.ActivityState, event.EventTimestamp())
	switch ev := event.(type) {
	case domain.EnvelopeInfoEvent:
		if ev.Cwd != "" {
			s.Cwd = ev.Cwd
			if name, ok := domain.LastPathSegment(ev.Cwd); ok {
				s.ProjectName = name
			}
		}
		if ev.GitBranch != "" {
			s.GitBranch = ev.GitBranch
		}
		if ev.Version != "" {
			s.Version = ev.Version
		}
		return s
	case domain.AssistantMessageEvent:
		seen := s.ActivityState
		next := s
		applyUsage(&next.ActivityState, ev)
		recordActivity(&next, ev, &seen)
		markToolUsesSeen(&next.ActivityState, ev)
		if ev.Model != "" && !isSynthetic(ev) {
			next = applyMessageModel(next, ev.Model)
		}
		if ev.Effort != "" {
			next.Effort = ev.Effort
		}
		addPendingTools(&next.ActivityState, ev, nil)
		addEventPullRequests(&next.ActivityState, ev.PullRequests)
		return launchAgentsFromAssistant(next, ev)
	case domain.ToolResultEvent:
		prs := eventPullRequests(&s.ActivityState, ev)
		removePendingTool(&s.ActivityState, ev.ToolUseID)
		addEventPullRequests(&s.ActivityState, prs)
		return applyToolResultToAgents(s, ev)
	case domain.TaskNotificationEvent:
		return applyTaskNotification(s, ev)
	case domain.AgentHandbackEvent:
		return applyAgentHandback(s, ev)
	case domain.AgentsKilledEvent:
		return applyAgentsKilled(s, ev.Timestamp)
	case domain.UserPromptEvent:
		if !ev.IsMeta && ev.Timestamp != 0 {
			s.LastUserPromptAt = ev.Timestamp
		}
		return s
	case domain.TurnEndEvent:
		// End of the turn: nothing should stay pending (e.g. a tool the user interrupted).
		if len(s.PendingTools) > 0 {
			s.PendingTools = []domain.PendingEntry{}
		}
		if ev.PendingBackgroundAgentCount != nil {
			n := *ev.PendingBackgroundAgentCount
			s.PendingBackgroundAgents = &n
		}
		return s
	case domain.ModelAttachmentEvent:
		s.Model = &domain.ModelInfo{ID: ev.ModelID, Name: ev.Name, ContextWindow: ev.ContextWindow}
		return s
	case domain.AITitleEvent:
		s.AITitle = ev.Title
		if s.CustomTitle == "" {
			s.Title = ev.Title
			s.TitleSource = domain.TitleAI
		}
		return s
	case domain.CustomTitleEvent:
		s.CustomTitle = ev.Title
		s.Title = ev.Title
		s.TitleSource = domain.TitleCustom
		return s
	case domain.IgnoredEvent:
		s.Ignored++
		return s
	case domain.ParseErrorEvent:
		s.ParseErrors++
		return s
	}
	return s
}

// SessionEvents applies events in order.
func SessionEvents(state domain.SessionState, events []domain.TranscriptEvent) domain.SessionState {
	for _, event := range events {
		state = Session(state, event)
	}
	return state
}
