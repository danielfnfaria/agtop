// Package reduce holds the pure reducers: reduce(state, event) -> state. They never mutate their
// input (maps and slices are copied when changed) and do no I/O.
package reduce

import (
	"maps"
	"slices"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// Shared core of the session and agent reducers: token usage, activity and pending tools. The
// helpers below change *st in place (st is always a copy owned by the caller) and report
// whether they changed it.

func emptyActivity() domain.ActivityState {
	return domain.ActivityState{
		UsageByMessageID: map[string]domain.TokenTotals{},
		PendingTools:     []domain.PendingEntry{},
		SeenToolUseIDs:   []string{},
		PullRequests:     []domain.PullRequestRef{},
	}
}

// sameSlice reports whether b is a itself (same backing array and length).
func sameSlice[T any](a, b []T) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// addEventPullRequests adds the PR links of an event.
func addEventPullRequests(st *domain.ActivityState, prs []domain.PullRequestRef) bool {
	next := domain.AddPullRequests(st.PullRequests, prs, domain.MaxPullRequests)
	if sameSlice(next, st.PullRequests) {
		return false
	}
	st.PullRequests = next
	return true
}

func pendingIndex(st *domain.ActivityState, id string) int {
	return slices.IndexFunc(st.PendingTools, func(e domain.PendingEntry) bool { return e.ID == id })
}

// eventPullRequests returns the PR links of an event that st keeps: always those of assistant
// text; those of a tool_result only when the pending tool it answers (in st, before it is
// removed) may report PRs. st may be nil.
func eventPullRequests(st *domain.ActivityState, event domain.TranscriptEvent) []domain.PullRequestRef {
	switch ev := event.(type) {
	case domain.AssistantMessageEvent:
		return ev.PullRequests
	case domain.ToolResultEvent:
		var tool *domain.PendingTool
		if st != nil {
			if i := pendingIndex(st, ev.ToolUseID); i >= 0 {
				tool = &st.PendingTools[i].PendingTool
			}
		}
		return domain.ToolResultPullRequests(tool, ev.PullRequests)
	}
	return nil
}

func touch(st *domain.ActivityState, ts int64) bool {
	if ts == 0 || (st.LastActivityAt != 0 && st.LastActivityAt >= ts) {
		return false
	}
	st.LastActivityAt = ts
	return true
}

func addTotals(a, b domain.TokenTotals, sign int) domain.TokenTotals {
	return domain.TokenTotals{
		Input:       domain.SatAdd(a.Input, sign*b.Input),
		Output:      domain.SatAdd(a.Output, sign*b.Output),
		CacheRead:   domain.SatAdd(a.CacheRead, sign*b.CacheRead),
		CacheCreate: domain.SatAdd(a.CacheCreate, sign*b.CacheCreate),
	}
}

func isSynthetic(ev domain.AssistantMessageEvent) bool {
	return ev.Model == domain.SyntheticModel
}

// applyUsage updates context and totals. One API response spans several lines with the same
// message.id: the totals keep the usage per id and replace (not add) the previous value. A line
// with a message.id and no usage records the id with zero usage (totals untouched), so the
// response counts once in the activity.
func applyUsage(st *domain.ActivityState, ev domain.AssistantMessageEvent) bool {
	if isSynthetic(ev) {
		return false
	}
	if ev.Usage == nil {
		if ev.MessageID == "" {
			return false
		}
		if _, ok := st.UsageByMessageID[ev.MessageID]; ok {
			return false
		}
		usage := maps.Clone(st.UsageByMessageID)
		if usage == nil {
			usage = map[string]domain.TokenTotals{}
		}
		usage[ev.MessageID] = domain.TokenTotals{}
		st.UsageByMessageID = usage
		return true
	}
	u := *ev.Usage
	st.Context = domain.ContextCounter{Used: domain.SatAdd(domain.SatAdd(u.Input, u.CacheCreate), u.CacheRead), LastMessageID: ev.MessageID}
	if ev.MessageID == "" {
		st.Totals = addTotals(st.Totals, u, 1)
		return true
	}
	previous, seen := st.UsageByMessageID[ev.MessageID]
	if seen && previous == u {
		return true
	}
	totals := st.Totals
	if seen {
		totals = addTotals(totals, previous, -1)
	}
	usage := maps.Clone(st.UsageByMessageID)
	if usage == nil {
		usage = map[string]domain.TokenTotals{}
	}
	usage[ev.MessageID] = u
	st.UsageByMessageID = usage
	st.Totals = addTotals(totals, u, 1)
	return true
}

// isNewResponse reports whether the line opens a response st has not seen yet (no message.id
// counts as new). Call before applyUsage. st may be nil.
func isNewResponse(st *domain.ActivityState, ev domain.AssistantMessageEvent) bool {
	if ev.MessageID == "" || st == nil {
		return true
	}
	_, seen := st.UsageByMessageID[ev.MessageID]
	return !seen
}

// newToolUseCount returns how many tool_use of the line st has not counted yet (an id repeated
// in the line counts once). st may be nil.
func newToolUseCount(st *domain.ActivityState, ev domain.AssistantMessageEvent) int {
	ids := map[string]bool{}
	for _, t := range ev.ToolUses {
		ids[t.ID] = true
	}
	if st == nil {
		return len(ids)
	}
	n := 0
	for id := range ids {
		if !slices.Contains(st.SeenToolUseIDs, id) {
			n++
		}
	}
	return n
}

// markToolUsesSeen remembers the tool_use ids of the line (the MaxSeenToolUses most recent).
// Call after recordActivity, on the same state used as seen.
func markToolUsesSeen(st *domain.ActivityState, ev domain.AssistantMessageEvent) bool {
	all := slices.Clone(st.SeenToolUseIDs)
	fresh := false
	for _, t := range ev.ToolUses {
		if !slices.Contains(st.SeenToolUseIDs, t.ID) {
			all = append(all, t.ID)
			fresh = true
		}
	}
	if !fresh {
		return false
	}
	tail := all[max(0, len(all)-domain.MaxSeenToolUses):]
	seen := make([]string, 0, len(tail))
	for _, id := range tail {
		if !slices.Contains(seen, id) {
			seen = append(seen, id)
		}
	}
	st.SeenToolUseIDs = seen
	return true
}

// recordActivity adds an assistant line to the session histogram (no timestamp or synthetic
// does not count). seen is the state (session or agent) that deduplicates the response by
// message.id and the tool_use by id; it may be nil.
func recordActivity(session *domain.SessionState, event domain.TranscriptEvent, seen *domain.ActivityState) bool {
	ev, ok := event.(domain.AssistantMessageEvent)
	if !ok || ev.Timestamp == 0 {
		return false
	}
	weight := domain.ActivityWeight(ev, isNewResponse(seen, ev), newToolUseCount(seen, ev))
	if weight <= 0 {
		return false
	}
	session.ActivityHistogram = domain.AddActivity(session.ActivityHistogram, ev.Timestamp, weight)
	return true
}

func addPendingTools(st *domain.ActivityState, ev domain.AssistantMessageEvent, skip func(name string) bool) bool {
	var pending []domain.PendingEntry
	for _, t := range ev.ToolUses {
		if (skip != nil && skip(t.Name)) || pendingIndex(st, t.ID) >= 0 {
			continue
		}
		if pending == nil {
			pending = slices.Clone(st.PendingTools)
		}
		entry := domain.PendingEntry{ID: t.ID, PendingTool: domain.PendingTool{
			Name:            t.Name,
			Target:          t.Target,
			CommandCategory: t.CommandCategory,
			UsesGh:          t.UsesGh,
			StartedAt:       ev.Timestamp,
		}}
		// An id repeated in the line keeps its first position with the last value.
		if i := slices.IndexFunc(pending, func(e domain.PendingEntry) bool { return e.ID == t.ID }); i >= 0 {
			pending[i] = entry
		} else {
			pending = append(pending, entry)
		}
	}
	if pending == nil {
		return false
	}
	st.PendingTools = pending
	return true
}

func removePendingTool(st *domain.ActivityState, id string) bool {
	i := pendingIndex(st, id)
	if i < 0 {
		return false
	}
	st.PendingTools = slices.Delete(slices.Clone(st.PendingTools), i, i+1)
	return true
}
