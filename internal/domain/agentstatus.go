package domain

import "sort"

// IsFinal reports completed or failed: the agent is over and nothing changes its status.
func IsFinal(s AgentStatus) bool {
	return s == StatusCompleted || s == StatusFailed
}

// IsSettled reports final or handed_back (almost final: the agent already returned its result).
// Late activity does not demote it to running.
func IsSettled(s AgentStatus) bool {
	return IsFinal(s) || s == StatusHandedBack
}

// IsActive reports launched or running: the agent is still working.
func IsActive(s AgentStatus) bool {
	return s == StatusLaunched || s == StatusRunning
}

// AgentStart is what the agent order needs. A zero start means unknown.
type AgentStart interface {
	GetStartedAt() int64
}

// KeyedAgent is an agent with the key it is stored under.
type KeyedAgent[A AgentStart] struct {
	Key   string
	Agent A
}

// CompareByStart orders agents by start (unknown start last), ties broken by the key. It
// returns a negative, zero or positive number like cmp.Compare.
func CompareByStart[A AgentStart](a, b KeyedAgent[A]) int {
	sa, sb := a.Agent.GetStartedAt(), b.Agent.GetStartedAt()
	if sa == 0 {
		sa = maxInt64
	}
	if sb == 0 {
		sb = maxInt64
	}
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	case a.Key < b.Key:
		return -1
	case a.Key > b.Key:
		return 1
	}
	return 0
}

const maxInt64 = int64(^uint64(0) >> 1)

// SortedAgents returns the agents in CompareByStart order.
func SortedAgents[A AgentStart](agents map[string]A) []KeyedAgent[A] {
	out := make([]KeyedAgent[A], 0, len(agents))
	for k, a := range agents {
		out = append(out, KeyedAgent[A]{k, a})
	}
	sort.Slice(out, func(i, j int) bool { return CompareByStart(out[i], out[j]) < 0 })
	return out
}

// AgentCounts returns how many agents are active, out of the total.
func AgentCounts(agents map[string]AgentState) (active, total int) {
	for _, a := range agents {
		if IsActive(a.Status) {
			active++
		}
	}
	return active, len(agents)
}
