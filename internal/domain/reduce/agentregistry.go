package reduce

import (
	"maps"
	"math"
	"slices"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// Agents inside a session: lookup, merge of two views of the same agent, and status changes.

// NewAgentState returns an agent with no activity, of unknown type and status.
func NewAgentState() domain.AgentState {
	return domain.AgentState{
		ActivityState: emptyActivity(),
		Type:          "unknown",
		Status:        domain.StatusUnknown,
	}
}

var statusRank = map[domain.AgentStatus]int{
	domain.StatusUnknown:    0,
	domain.StatusLaunched:   1,
	domain.StatusRunning:    2,
	domain.StatusHandedBack: 3,
	domain.StatusCompleted:  4,
	domain.StatusFailed:     4,
}

func agentKey(a domain.AgentState) string {
	if a.AgentID != "" {
		return a.AgentID
	}
	return a.ToolUseID
}

// FindAgentKey finds the key of an agent by its agentId or by the toolUseId of its launch (an
// empty id is not searched).
//
// The fallback scan goes in key order: the TypeScript version scans in insertion order, which a
// Go map does not keep. The two only differ when two distinct entries match, which the reducers
// never produce (they always store an agent under its canonical key).
func FindAgentKey(agents map[string]domain.AgentState, agentID, toolUseID string) (string, bool) {
	if agentID != "" {
		if _, ok := agents[agentID]; ok {
			return agentID, true
		}
	}
	if toolUseID != "" {
		if direct, ok := agents[toolUseID]; ok && direct.ToolUseID == toolUseID {
			return toolUseID, true
		}
	}
	for _, key := range slices.Sorted(maps.Keys(agents)) {
		a := agents[key]
		if agentID != "" && a.AgentID == agentID {
			return key, true
		}
		if toolUseID != "" && a.ToolUseID == toolUseID {
			return key, true
		}
	}
	return "", false
}

// mergeAgents joins two views of the same agent: launch (from the session transcript) and other
// (from the sub-agent transcript or the meta.json). The activity comes from other when it has
// any.
func mergeAgents(launch, other domain.AgentState) domain.AgentState {
	hasActivity := len(other.UsageByMessageID) > 0 || len(other.SeenToolUseIDs) > 0 || other.LastActivityAt != 0
	activity := launch.ActivityState
	if hasActivity {
		activity = other.ActivityState
	}
	activity.LastActivityAt = maxDefined(launch.LastActivityAt, other.LastActivityAt)
	activity.PullRequests = domain.AddPullRequests(launch.PullRequests, other.PullRequests, domain.MaxPullRequests)
	// status, finishedAt and failureReason always come from the same side.
	side := statusSide(launch, other)
	merged := domain.AgentState{
		ActivityState:      activity,
		AgentID:            launch.AgentID,
		ToolUseID:          launch.ToolUseID,
		Type:               launch.Type,
		Description:        launch.Description,
		Model:              other.Model,
		IsBackground:       launch.IsBackground || other.IsBackground,
		Status:             side.Status,
		FailureReason:      side.FailureReason,
		SpawnDepth:         other.SpawnDepth,
		StartedAt:          minDefined(launch.StartedAt, other.StartedAt),
		FinishedAt:         side.FinishedAt,
		HandbackReceivedAt: maxDefined(launch.HandbackReceivedAt, other.HandbackReceivedAt),
	}
	if merged.AgentID == "" {
		merged.AgentID = other.AgentID
	}
	if merged.ToolUseID == "" {
		merged.ToolUseID = other.ToolUseID
	}
	if merged.Type == "unknown" {
		merged.Type = other.Type
	}
	if merged.Description == "" {
		merged.Description = other.Description
	}
	if merged.Model == "" {
		merged.Model = launch.Model
	}
	if merged.SpawnDepth == nil {
		merged.SpawnDepth = launch.SpawnDepth
	}
	return merged
}

func finishedOrMin(a domain.AgentState) int64 {
	if a.FinishedAt == 0 {
		return math.MinInt64
	}
	return a.FinishedAt
}

// statusSide returns the side that sets the status: the higher rank; between two final ones,
// the most recent finishedAt; a tie goes to a.
func statusSide(a, b domain.AgentState) domain.AgentState {
	if domain.IsFinal(a.Status) && domain.IsFinal(b.Status) {
		if finishedOrMin(b) > finishedOrMin(a) {
			return b
		}
		return a
	}
	if statusRank[b.Status] > statusRank[a.Status] {
		return b
	}
	return a
}

func maxDefined(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	return max(a, b)
}

func minDefined(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	return min(a, b)
}

// putAgent stores agent in the session under its canonical key (agentId, else toolUseId),
// removing oldKey ("" for none) and merging with an entry already under the new key.
func putAgent(session domain.SessionState, agent domain.AgentState, oldKey string) domain.SessionState {
	key := agentKey(agent)
	if key == "" {
		return session
	}
	agents := maps.Clone(session.Agents)
	if agents == nil {
		agents = map[string]domain.AgentState{}
	}
	if oldKey != "" && oldKey != key {
		delete(agents, oldKey)
	}
	if existing, ok := agents[key]; ok && oldKey != key {
		agent = mergeAgents(agent, existing)
	}
	agents[key] = agent
	session.Agents = agents
	return session
}

// replaceAgent stores agent under its canonical key without merging: it removes removeKeys and
// the previous entry.
func replaceAgent(session domain.SessionState, agent domain.AgentState, removeKeys ...string) domain.SessionState {
	key := agentKey(agent)
	if key == "" {
		return session
	}
	agents := maps.Clone(session.Agents)
	if agents == nil {
		agents = map[string]domain.AgentState{}
	}
	for _, k := range removeKeys {
		delete(agents, k)
	}
	agents[key] = agent
	session.Agents = agents
	return session
}

// finishAgent ends a non-final agent with status; ts 0 keeps the previous finishedAt. The
// failure reason is kept only for failed.
func finishAgent(agent domain.AgentState, status domain.AgentStatus, ts int64, failureReason string) domain.AgentState {
	if domain.IsFinal(agent.Status) {
		return agent
	}
	agent.Status = status
	if ts != 0 {
		agent.FinishedAt = ts
	}
	if status == domain.StatusFailed {
		agent.FailureReason = failureReason
	} else {
		agent.FailureReason = ""
	}
	return agent
}

// isResumeAfterFinish reports whether activity at ts comes after the agent settled, so it is a
// resume and not a late line of the run that already ended. failed only reopens through an
// explicit signal, never from transcript lines alone (e.g. an interruption line after a kill).
// A final status needs both times to compare; handed_back without them reopens (the agent
// transcript is read in order, so a new prompt there is always later).
func isResumeAfterFinish(agent domain.AgentState, ts int64, explicit bool) bool {
	if !domain.IsSettled(agent.Status) {
		return false
	}
	if agent.Status == domain.StatusFailed && !explicit {
		return false
	}
	if agent.FinishedAt == 0 || ts == 0 {
		return agent.Status == domain.StatusHandedBack || explicit
	}
	return ts > agent.FinishedAt
}

// reopenAgent reopens a finished (resumed) agent: it clears the end and the failure reason.
func reopenAgent(agent domain.AgentState, status domain.AgentStatus) domain.AgentState {
	agent.Status = status
	agent.FinishedAt = 0
	agent.FailureReason = ""
	return agent
}

// resumeAgent handles a resumed agent: a new tool_use launched an agentId that already has a
// settled entry from another launch. It reopens it as launched with the new tool_use, keeping
// the accumulated activity (the agent transcript goes on in the same file). False if it is not
// the case.
func resumeAgent(session domain.SessionState, launched domain.AgentState, toolUseID string) (domain.SessionState, bool) {
	agentID := launched.AgentID
	if agentID == "" {
		return session, false
	}
	priorKey, ok := FindAgentKey(session.Agents, agentID, "")
	if !ok {
		return session, false
	}
	prior := session.Agents[priorKey]
	if prior.ToolUseID == "" || prior.ToolUseID == toolUseID || !domain.IsSettled(prior.Status) {
		return session, false
	}
	reopened := reopenAgent(prior, domain.StatusLaunched)
	reopened.AgentID = agentID
	reopened.ToolUseID = toolUseID
	if launched.Type != "unknown" {
		reopened.Type = launched.Type
	}
	if launched.Description != "" {
		reopened.Description = launched.Description
	}
	reopened.IsBackground = launched.IsBackground
	if launched.Model != "" {
		reopened.Model = launched.Model
	}
	if launched.StartedAt != 0 {
		reopened.StartedAt = launched.StartedAt
	}
	removeKeys := []string{priorKey}
	if launchKey, ok := FindAgentKey(session.Agents, "", toolUseID); ok {
		removeKeys = append(removeKeys, launchKey)
	}
	return replaceAgent(session, reopened, removeKeys...), true
}
