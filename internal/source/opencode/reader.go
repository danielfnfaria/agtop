package opencode

import (
	"cmp"
	"context"
	"database/sql"
	"slices"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/source"
)

// reader is the transcript of one session as domain events. Rows are read by time_updated:
// each Read takes the messages and tool parts updated since the last one (rows are updated in
// place, so a row comes back on every change) and turns them into the same events a JSONL
// transcript yields. A row is never emitted twice for the same time_updated.
type reader struct {
	src       *Source
	db        *profileDB
	sessionID string

	// started: the envelope and model were emitted; hadRows: some row was read (a session gone
	// after that is a reset, before it is simply empty).
	started bool
	hadRows bool
	// modelKey is the provider/model of the last attachment.
	modelKey string
	messages cursor
	parts    cursor
}

// lookbackMs is how far behind the cursor each read looks again: opencode stamps time_updated
// before the write commits, so a row can land with a time below rows already read.
const lookbackMs = 2_000

// cursor is the position in one table: at is the latest time_updated read, and seen holds, by
// id, the time_updated of every row read in the lookback window (older entries are pruned), so
// a row comes back only when it changed.
type cursor struct {
	at   int64
	seen map[string]int64
}

// since is the lower bound of the next query.
func (c *cursor) since() int64 { return max(0, c.at-lookbackMs) }

func (c *cursor) skip(id string, updated int64) bool {
	v, ok := c.seen[id]
	return ok && v == updated
}

func (c *cursor) advance(id string, updated int64) {
	if c.seen == nil {
		c.seen = map[string]int64{}
	}
	c.seen[id] = updated
	if updated <= c.at {
		return
	}
	c.at = updated
	for k, v := range c.seen {
		if v < c.since() {
			delete(c.seen, k)
		}
	}
}

func newReader(src *Source, db *profileDB, sessionID string) *reader {
	return &reader{src: src, db: db, sessionID: sessionID}
}

func (r *reader) reset() {
	r.started, r.hadRows, r.modelKey = false, false, ""
	r.messages, r.parts = cursor{}, cursor{}
}

// item is a row with its order key: by time_updated, messages before parts, then by id.
type item struct {
	updated int64
	kind    int
	id      string
	events  []domain.TranscriptEvent
}

func (r *reader) Read(ctx context.Context) (source.TranscriptRead, error) {
	conn, err := r.db.conn(ctx)
	if err != nil {
		return source.TranscriptRead{}, err
	}
	session, found, err := selectSession(ctx, conn, r.sessionID)
	if err != nil {
		return source.TranscriptRead{}, err
	}
	if !found {
		had := r.hadRows || r.started
		r.reset()
		return source.TranscriptRead{Reset: had}, nil
	}
	var out source.TranscriptRead
	out.Events = append(out.Events, r.headEvents(session)...)

	var items []item
	msgs, err := r.readMessages(ctx, conn)
	if err != nil {
		return source.TranscriptRead{}, err
	}
	items = append(items, msgs...)
	parts, err := r.readParts(ctx, conn)
	if err != nil {
		return source.TranscriptRead{}, err
	}
	items = append(items, parts...)
	slices.SortStableFunc(items, func(a, b item) int {
		if c := cmp.Compare(a.updated, b.updated); c != 0 {
			return c
		}
		if c := cmp.Compare(a.kind, b.kind); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	for _, it := range items {
		out.Events = append(out.Events, it.events...)
	}
	if len(items) > 0 {
		r.hadRows = true
	}
	return out, nil
}

func (r *reader) Close() error { return nil }

// headEvents are the envelope (first read) and the model attachment (first read and on a
// model change).
func (r *reader) headEvents(s sessionRow) []domain.TranscriptEvent {
	var out []domain.TranscriptEvent
	if !r.started {
		r.started = true
		out = append(out, domain.EnvelopeInfoEvent{
			EventBase: domain.EventBase{Timestamp: s.TimeCreated},
			SessionID: domain.Sanitize(s.ID, idMax),
			Cwd:       domain.Sanitize(s.Directory, pathMax),
			Version:   domain.Sanitize(s.Version, nameMax),
		})
	}
	if s.ModelID == "" {
		return out
	}
	key := s.ProviderID + "/" + s.ModelID
	if key == r.modelKey {
		return out
	}
	r.modelKey = key
	ev := domain.ModelAttachmentEvent{
		EventBase: domain.EventBase{Timestamp: s.TimeUpdated},
		ModelID:   domain.Sanitize(s.ModelID, nameMax),
	}
	if entry, ok := r.src.models.lookup(s.ProviderID, s.ModelID); ok {
		ev.Name, ev.ContextWindow = entry.Name, entry.Context
	}
	return append(out, ev)
}

func (r *reader) readMessages(ctx context.Context, conn *sql.DB) ([]item, error) {
	var out []item
	since, afterID := r.messages.since(), ""
	for range maxPages {
		rows, err := selectMessages(ctx, conn, r.sessionID, since, afterID)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			since, afterID = m.TimeUpdated, m.ID
			if r.messages.skip(m.ID, m.TimeUpdated) {
				continue
			}
			r.messages.advance(m.ID, m.TimeUpdated)
			if events := messageEvents(m); len(events) > 0 {
				out = append(out, item{updated: m.TimeUpdated, kind: 0, id: m.ID, events: events})
			}
		}
		if len(rows) < pageSize {
			break
		}
	}
	return out, nil
}

func (r *reader) readParts(ctx context.Context, conn *sql.DB) ([]item, error) {
	var out []item
	since, afterID := r.parts.since(), ""
	for range maxPages {
		rows, err := selectToolParts(ctx, conn, r.sessionID, since, afterID)
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			since, afterID = p.TimeUpdated, p.ID
			if r.parts.skip(p.ID, p.TimeUpdated) {
				continue
			}
			r.parts.advance(p.ID, p.TimeUpdated)
			if events := partEvents(p); len(events) > 0 {
				out = append(out, item{updated: p.TimeUpdated, kind: 1, id: p.ID, events: events})
			}
		}
		if len(rows) < pageSize {
			break
		}
	}
	return out, nil
}

// messageEvents maps a message row: a user prompt, or an assistant message with its usage once
// completed and the end of the turn when it did not stop for tool calls.
func messageEvents(m messageRow) []domain.TranscriptEvent {
	ts := firstNonZero(m.Created, m.TimeCreated)
	switch m.Role {
	case "user":
		return []domain.TranscriptEvent{domain.UserPromptEvent{EventBase: domain.EventBase{Timestamp: ts}}}
	case "assistant":
	default:
		return nil
	}
	ev := domain.AssistantMessageEvent{
		EventBase:  domain.EventBase{Timestamp: ts},
		MessageID:  domain.Sanitize(m.ID, idMax),
		Model:      domain.Sanitize(m.ModelID, nameMax),
		StopReason: domain.Sanitize(m.Finish, nameMax),
		ToolUses:   []domain.ToolUseInfo{},
	}
	done := m.Completed != 0 || m.HasError
	if done {
		ev.Usage = &domain.TokenTotals{
			Input:       clampInt(m.Input),
			Output:      clampInt(m.Output + m.Reasoning),
			CacheRead:   clampInt(m.CacheRead),
			CacheCreate: clampInt(m.CacheWrite),
		}
	}
	events := []domain.TranscriptEvent{ev}
	if done && m.Finish != "tool-calls" {
		events = append(events, domain.TurnEndEvent{EventBase: domain.EventBase{Timestamp: firstNonZero(m.Completed, m.TimeUpdated)}})
	}
	return events
}

// toolNames maps opencode's tool names to the names the activity classifier knows.
var toolNames = map[string]string{
	"bash": "Bash", "read": "Read", "edit": "Edit", "write": "Write", "multiedit": "MultiEdit",
	"glob": "Glob", "grep": "Grep", "webfetch": "WebFetch", "websearch": "WebSearch", "task": "Task",
	// apply_patch edits files: shown as an edit (the patch itself is never read).
	"apply_patch": "Edit",
}

const taskTool = "task"

// partEvents maps a tool part: the tool use (as an assistant line of its message) always; a
// launch confirmation while a task with a child session runs; the result (and, for a task, the
// notification) once completed or failed. The input never leaves: only a path, the category of
// a bash command and the description of a task.
func partEvents(p partRow) []domain.TranscriptEvent {
	callID := p.CallID
	if callID == "" {
		callID = p.ID
	}
	if callID == "" || p.Tool == "" {
		return nil
	}
	name := p.Tool
	if canonical, ok := toolNames[p.Tool]; ok {
		name = canonical
	}
	info := domain.ToolUseInfo{ID: domain.Sanitize(callID, idMax), Name: domain.Sanitize(name, nameMax)}
	if target := firstNonEmpty(p.FilePath, p.Path); target != "" {
		info.Target = domain.Sanitize(target, pathMax)
	}
	child := domain.Sanitize(p.ChildSessionID, idMax)
	switch p.Tool {
	case "bash":
		info.CommandCategory = domain.ClassifyCommand(p.Command)
		info.UsesGh = domain.UsesGh(p.Command)
	case taskTool:
		agentType := p.SubagentType
		if agentType == "" {
			agentType = "general"
		}
		info.AgentLaunch = &domain.AgentLaunchRequest{
			Type:        domain.Sanitize(agentType, nameMax),
			Description: domain.Sanitize(p.Description, textMax),
		}
	}
	start := firstNonZero(p.Start, p.TimeCreated)
	events := []domain.TranscriptEvent{domain.AssistantMessageEvent{
		EventBase: domain.EventBase{Timestamp: start},
		MessageID: domain.Sanitize(p.MessageID, idMax),
		ToolUses:  []domain.ToolUseInfo{info},
	}}
	switch p.Status {
	case "pending", "running":
		if p.Tool == taskTool && child != "" {
			events = append(events, domain.ToolResultEvent{
				EventBase:   domain.EventBase{Timestamp: firstNonZero(p.Start, p.TimeUpdated)},
				ToolUseID:   info.ID,
				AgentLaunch: &domain.AgentLaunchResult{AgentID: child, Description: info.AgentLaunch.Description},
			})
		}
	case "completed", "error":
		end := firstNonZero(p.End, p.TimeUpdated)
		result := domain.ToolResultEvent{
			EventBase: domain.EventBase{Timestamp: end},
			ToolUseID: info.ID,
			IsError:   p.Status == "error",
		}
		if p.Tool == taskTool {
			result.ResultAgentID = child
		}
		events = append(events, result)
		if p.Tool == taskTool {
			events = append(events, domain.TaskNotificationEvent{
				EventBase: domain.EventBase{Timestamp: end},
				TaskID:    child,
				ToolUseID: info.ID,
				Status:    p.Status,
			})
		}
	}
	return events
}

func firstNonZero(a, b int64) int64 {
	if a != 0 {
		return a
	}
	return b
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// clampInt narrows a row count to int, never negative.
func clampInt(v int64) int {
	if v < 0 {
		return 0
	}
	if v > int64(maxInt) {
		return maxInt
	}
	return int(v)
}

const maxInt = int(^uint(0) >> 1)
