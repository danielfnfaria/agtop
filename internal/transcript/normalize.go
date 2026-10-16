package transcript

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/danielfnfaria/agtop/internal/domain"
)

const (
	idMax   = 100
	nameMax = 80
	pathMax = 300
	textMax = 200

	bashToolName           = "Bash"
	taskNotificationPrefix = "<task-notification>"
)

// IsAgentToolName reports whether the tool launches a sub-agent (`Task` in older versions).
func IsAgentToolName(name string) bool {
	return name == "Agent" || name == "Task"
}

// fileToolNames are the tools whose input.file_path becomes the pending tool's target.
var fileToolNames = map[string]bool{"Read": true, "Edit": true, "Write": true, "NotebookEdit": true}

// NormalizeLine converts a raw JSONL line into zero or more events. A blank line gives none;
// invalid JSON gives a parse_error.
func NormalizeLine(line string) []domain.TranscriptEvent {
	value, err := ParseLine(line)
	if errors.Is(err, ErrEmptyLine) {
		return []domain.TranscriptEvent{}
	}
	if err != nil {
		return []domain.TranscriptEvent{domain.ParseErrorEvent{Reason: domain.ParseInvalidJSON}}
	}
	return NormalizeRecord(value)
}

// NormalizeRecord converts an already decoded line into events. It never panics.
func NormalizeRecord(value any) (events []domain.TranscriptEvent) {
	rec, ok := asRecord(value)
	if !ok {
		return []domain.TranscriptEvent{domain.ParseErrorEvent{Reason: domain.ParseNotObject}}
	}
	defer func() {
		// Extra defense: a missed guard must not take the viewer down.
		if recover() != nil {
			events = []domain.TranscriptEvent{domain.IgnoredEvent{LineType: "error"}}
		}
	}()
	return normalizeObject(rec)
}

func base(ts int64) domain.EventBase { return domain.EventBase{Timestamp: ts} }

func normalizeObject(rec record) []domain.TranscriptEvent {
	typ, hasType := getString(rec, "type")
	ts := getTimestamp(rec, "timestamp")
	if !hasType {
		return ignored("(no type)", ts)
	}
	switch typ {
	case "assistant":
		return withEnvelope(rec, ts, normalizeAssistant(rec, ts))
	case "user":
		return withEnvelope(rec, ts, normalizeUser(rec, ts))
	case "system":
		return withEnvelope(rec, ts, normalizeSystem(rec, ts))
	case "attachment":
		return withEnvelope(rec, ts, normalizeAttachment(rec, ts))
	case "ai-title":
		raw, _ := getString(rec, "aiTitle")
		if title := domain.Sanitize(raw, textMax); title != "" {
			return []domain.TranscriptEvent{domain.AITitleEvent{EventBase: base(ts), Title: title}}
		}
		return ignored("ai-title", ts)
	case "custom-title":
		raw, _ := getString(rec, "customTitle")
		if title := domain.Sanitize(raw, textMax); title != "" {
			return []domain.TranscriptEvent{domain.CustomTitleEvent{EventBase: base(ts), Title: title}}
		}
		return ignored("custom-title", ts)
	default:
		return ignored(typ, ts)
	}
}

func ignoredEvent(lineType string, ts int64) domain.IgnoredEvent {
	return domain.IgnoredEvent{EventBase: base(ts), LineType: domain.Sanitize(lineType, 40)}
}

func ignored(lineType string, ts int64) []domain.TranscriptEvent {
	return []domain.TranscriptEvent{ignoredEvent(lineType, ts)}
}

func withEnvelope(rec record, ts int64, events []domain.TranscriptEvent) []domain.TranscriptEvent {
	env := domain.EnvelopeInfoEvent{EventBase: base(ts)}
	has := false
	if v, ok := getNonEmptyString(rec, "sessionId"); ok {
		env.SessionID, has = domain.Sanitize(v, idMax), true
	}
	if v, ok := getNonEmptyString(rec, "cwd"); ok {
		env.Cwd, has = domain.Sanitize(v, pathMax), true
	}
	if v, ok := getNonEmptyString(rec, "gitBranch"); ok {
		env.GitBranch, has = domain.Sanitize(v, nameMax), true
	}
	if v, ok := getNonEmptyString(rec, "version"); ok {
		env.Version, has = domain.Sanitize(v, 40), true
	}
	if v, ok := getNonEmptyString(rec, "agentId"); ok {
		env.AgentID, has = domain.Sanitize(v, idMax), true
	}
	if v, ok := getBoolean(rec, "isSidechain"); ok {
		env.IsSidechain, has = &v, true
	}
	if !has {
		return events
	}
	return append([]domain.TranscriptEvent{env}, events...)
}

// assistant

func normalizeAssistant(rec record, ts int64) []domain.TranscriptEvent {
	message, ok := getRecord(rec, "message")
	if !ok {
		return ignored("assistant", ts)
	}
	event := domain.AssistantMessageEvent{EventBase: base(ts), ToolUses: []domain.ToolUseInfo{}}
	if v, ok := getNonEmptyString(message, "id"); ok {
		event.MessageID = domain.Sanitize(v, idMax)
	}
	if v, ok := getNonEmptyString(message, "model"); ok {
		event.Model = domain.Sanitize(v, nameMax)
	}
	if v, ok := getNonEmptyString(rec, "requestedModel"); ok {
		event.RequestedModel = domain.Sanitize(v, nameMax)
	}
	if v, ok := getNonEmptyString(rec, "effort"); ok {
		event.Effort = domain.Sanitize(v, 20)
	} else if n, ok := getNumber(rec, "effort"); ok {
		event.Effort = domain.Sanitize(jsNumberString(n), 20)
	}
	if v, ok := getNonEmptyString(message, "stop_reason"); ok {
		event.StopReason = domain.Sanitize(v, 40)
	}
	if usage, ok := getRecord(message, "usage"); ok {
		totals := normalizeUsage(usage)
		event.Usage = &totals
	}

	var texts []string
	content, _ := getArray(message, "content")
	for _, item := range content {
		block, ok := asRecord(item)
		if !ok {
			continue
		}
		blockType, _ := getString(block, "type")
		if blockType == "text" {
			if text, ok := getString(block, "text"); ok {
				texts = append(texts, text)
			}
			continue
		}
		if blockType != "tool_use" {
			continue
		}
		if toolUse, ok := normalizeToolUse(block); ok {
			event.ToolUses = append(event.ToolUses, toolUse)
		}
	}
	if prs := pullRequestsIn(texts); len(prs) > 0 {
		event.PullRequests = prs
	}
	return []domain.TranscriptEvent{event}
}

// pullRequestsIn returns the PR links in the texts: only the validated refs leave, never the
// text itself.
func pullRequestsIn(texts []string) []domain.PullRequestRef {
	var out []domain.PullRequestRef
	for _, text := range texts {
		out = domain.AddPullRequests(out, domain.ExtractPullRequests(text, domain.MaxPullRequests), domain.MaxPullRequests)
	}
	return out
}

// toolResultTexts returns the texts of a tool_result content: a string or {type: text} blocks.
func toolResultTexts(block record) []string {
	switch content := block["content"].(type) {
	case string:
		return []string{content}
	case []any:
		var texts []string
		for _, item := range content {
			part, ok := asRecord(item)
			if !ok {
				continue
			}
			if typ, _ := getString(part, "type"); typ != "text" {
				continue
			}
			if text, ok := getString(part, "text"); ok {
				texts = append(texts, text)
			}
		}
		return texts
	}
	return nil
}

func numberOrZero(r record, key string) int {
	n, _ := getNumber(r, key)
	return domain.SafeNonNegInt(n)
}

func normalizeUsage(usage record) domain.TokenTotals {
	return domain.TokenTotals{
		Input:       numberOrZero(usage, "input_tokens"),
		Output:      numberOrZero(usage, "output_tokens"),
		CacheRead:   numberOrZero(usage, "cache_read_input_tokens"),
		CacheCreate: numberOrZero(usage, "cache_creation_input_tokens"),
	}
}

// normalizeToolUse keeps only metadata of a tool_use: the input itself never leaves.
func normalizeToolUse(block record) (domain.ToolUseInfo, bool) {
	id, okID := getNonEmptyString(block, "id")
	name, okName := getNonEmptyString(block, "name")
	if !okID || !okName {
		return domain.ToolUseInfo{}, false
	}
	info := domain.ToolUseInfo{ID: domain.Sanitize(id, idMax), Name: domain.Sanitize(name, nameMax)}
	input, ok := getRecord(block, "input")
	if !ok {
		return info, true
	}
	switch {
	case fileToolNames[name]:
		path, ok := getNonEmptyString(input, "file_path")
		if !ok {
			path, ok = getNonEmptyString(input, "notebook_path")
		}
		if ok {
			info.Target = domain.Sanitize(path, pathMax)
		}
	case name == bashToolName:
		// Only the category leaves: the command itself is never kept.
		if command, ok := getString(input, "command"); ok {
			info.CommandCategory = domain.ClassifyCommand(command)
			info.UsesGh = domain.UsesGh(command)
		}
	case IsAgentToolName(name):
		agentType, ok := getNonEmptyString(input, "subagent_type")
		if !ok {
			agentType = "general-purpose"
		}
		description, _ := getString(input, "description")
		launch := &domain.AgentLaunchRequest{
			Type:            domain.Sanitize(agentType, nameMax),
			Description:     domain.Sanitize(description, textMax),
			RunInBackground: isTrue(input, "run_in_background"),
		}
		if model, ok := getNonEmptyString(input, "model"); ok {
			launch.Model = domain.Sanitize(model, nameMax)
		}
		info.AgentLaunch = launch
	}
	return info, true
}

// user

func hasTaskNotificationPrefix(s string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(s, domain.IsJSSpace), taskNotificationPrefix)
}

func originKind(origin record, ok bool) string {
	if !ok {
		return ""
	}
	kind, _ := getString(origin, "kind")
	return kind
}

func normalizeUser(rec record, ts int64) []domain.TranscriptEvent {
	message, ok := getRecord(rec, "message")
	isMeta := isTrue(rec, "isMeta")
	if !ok {
		return ignored("user", ts)
	}
	origin, hasOrigin := getRecord(rec, "origin")
	kind := originKind(origin, hasOrigin)
	if kind == "peer" {
		return []domain.TranscriptEvent{normalizePeer(origin, ts)}
	}

	prompt := []domain.TranscriptEvent{domain.UserPromptEvent{EventBase: base(ts), IsMeta: isMeta}}
	var content []any
	switch c := message["content"].(type) {
	case string:
		if hasTaskNotificationPrefix(c) || kind == "task-notification" {
			return []domain.TranscriptEvent{parseTaskNotification(c, ts)}
		}
		return prompt
	case []any:
		content = c
	default:
		return ignored("user", ts)
	}

	var toolResults []record
	hasText := false
	for _, item := range content {
		block, ok := asRecord(item)
		if !ok {
			continue
		}
		switch typ, _ := getString(block, "type"); typ {
		case "tool_result":
			toolResults = append(toolResults, block)
		case "text":
			hasText = true
		}
	}
	if len(toolResults) == 0 {
		if hasText {
			return prompt
		}
		return ignored("user", ts)
	}

	// toolUseResult has no tool_use_id: it is only attributable with a single result.
	var rootResult record
	hasRoot := false
	if len(toolResults) == 1 {
		rootResult, hasRoot = asRecord(rec["toolUseResult"])
	}
	var events []domain.TranscriptEvent
	for _, block := range toolResults {
		toolUseID, ok := getNonEmptyString(block, "tool_use_id")
		if !ok {
			continue
		}
		event := domain.ToolResultEvent{
			EventBase: base(ts),
			ToolUseID: domain.Sanitize(toolUseID, idMax),
			IsError:   isTrue(block, "is_error"),
		}
		if prs := pullRequestsIn(toolResultTexts(block)); len(prs) > 0 {
			event.PullRequests = prs
		}
		if hasRoot {
			agentID, hasAgentID := getNonEmptyString(rootResult, "agentId")
			status, _ := getString(rootResult, "status")
			if status == "async_launched" && hasAgentID {
				launch := &domain.AgentLaunchResult{AgentID: domain.Sanitize(agentID, idMax)}
				if description, ok := getString(rootResult, "description"); ok {
					launch.Description = domain.Sanitize(description, textMax)
				}
				if model, ok := getNonEmptyString(rootResult, "resolvedModel"); ok {
					launch.ResolvedModel = domain.Sanitize(model, nameMax)
				}
				event.AgentLaunch = launch
			} else if hasAgentID {
				event.ResultAgentID = domain.Sanitize(agentID, idMax)
			}
			if resumed, ok := getNonEmptyString(rootResult, "resumedAgentId"); ok {
				event.ResumedAgentID = domain.Sanitize(resumed, idMax)
			}
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return ignored("user", ts)
	}
	return events
}

// normalizePeer reads only senderTaskId/from and handback of a message between agents: body,
// name, the handback report and the message content are never touched.
func normalizePeer(origin record, ts int64) domain.TranscriptEvent {
	agentID, ok := getNonEmptyString(origin, "senderTaskId")
	if !ok {
		agentID, ok = getNonEmptyString(origin, "from")
	}
	if !ok {
		return ignoredEvent("user:peer", ts)
	}
	return domain.AgentHandbackEvent{
		EventBase: base(ts),
		AgentID:   domain.Sanitize(agentID, idMax),
		Handback:  isTrue(origin, "handback"),
	}
}

// caseless matches word ignoring the case of its ASCII letters only, like a JavaScript /i
// regular expression without the u flag (which does not fold e.g. the Kelvin sign into k).
func caseless(word string) string {
	var b strings.Builder
	for _, r := range word {
		if unicode.IsLetter(r) && r < unicode.MaxASCII {
			b.WriteString("[" + string(unicode.ToLower(r)) + string(unicode.ToUpper(r)) + "]")
		} else {
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

func tagRE(name string) *regexp.Regexp {
	return regexp.MustCompile("<" + caseless(name) + ">" + domain.JSWS + "*([^<]*?)" + domain.JSWS + "*</" + caseless(name) + ">")
}

var (
	taskIDTag    = tagRE("task-id")
	toolUseIDTag = tagRE("tool-use-id")
	statusTag    = tagRE("status")
	// freeTextStart is where the agent's free text starts: tags after it are not trusted.
	freeTextStart = regexp.MustCompile(`<(?:` + caseless("summary") + `|` + caseless("result") + `)\b`)
)

func tag(head string, re *regexp.Regexp) string {
	if m := re.FindStringSubmatch(head); m != nil {
		return m[1]
	}
	return ""
}

// parseTaskNotification extracts only task-id, tool-use-id and status (the summary/result is
// never kept).
func parseTaskNotification(content string, ts int64) domain.TranscriptEvent {
	head := content
	if loc := freeTextStart.FindStringIndex(content); loc != nil {
		head = content[:loc[0]]
	}
	event := domain.TaskNotificationEvent{EventBase: base(ts)}
	if v := tag(head, taskIDTag); v != "" {
		event.TaskID = domain.Sanitize(v, idMax)
	}
	if v := tag(head, toolUseIDTag); v != "" {
		event.ToolUseID = domain.Sanitize(v, idMax)
	}
	if v := tag(head, statusTag); v != "" {
		event.Status = strings.ToLower(domain.Sanitize(v, 40))
	}
	return event
}

// system / attachment

func normalizeSystem(rec record, ts int64) []domain.TranscriptEvent {
	subtype, ok := getString(rec, "subtype")
	if ok && subtype == "agents_killed" {
		return []domain.TranscriptEvent{domain.AgentsKilledEvent{EventBase: base(ts)}}
	}
	if !ok || subtype != "turn_duration" {
		if !ok {
			subtype = "?"
		}
		return ignored("system:"+subtype, ts)
	}
	event := domain.TurnEndEvent{EventBase: base(ts)}
	if d, ok := getNumber(rec, "durationMs"); ok {
		ms := int64(domain.SafeNonNegInt(d))
		event.DurationMs = &ms
	}
	if n, ok := getNumber(rec, "pendingBackgroundAgentCount"); ok {
		count := domain.SafeNonNegInt(n)
		event.PendingBackgroundAgentCount = &count
	}
	return []domain.TranscriptEvent{event}
}

// normalizeQueuedCommand reads a queued_command attachment of the parent transcript: how
// Claude Code records a task notification delivered between turns (e.g. a sub-agent stopped
// with TaskStop). Anything that is not a task notification is ignored.
func normalizeQueuedCommand(attachment record, ts int64) []domain.TranscriptEvent {
	prompt, hasPrompt := getString(attachment, "prompt")
	origin, hasOrigin := getRecord(attachment, "origin")
	isNotification := originKind(origin, hasOrigin) == "task-notification" ||
		(hasPrompt && hasTaskNotificationPrefix(prompt))
	if !isNotification || !hasPrompt {
		return ignored("attachment:queued_command", ts)
	}
	return []domain.TranscriptEvent{parseTaskNotification(prompt, ts)}
}

func normalizeAttachment(rec record, ts int64) []domain.TranscriptEvent {
	attachment, ok := getRecord(rec, "attachment")
	attType, hasType := "", false
	if ok {
		attType, hasType = getString(attachment, "type")
	}
	if hasType && attType == "queued_command" {
		return normalizeQueuedCommand(attachment, ts)
	}
	if !hasType || attType != "model" {
		if !hasType {
			attType = "?"
		}
		return ignored("attachment:"+attType, ts)
	}
	identity, ok := getRecord(attachment, "identity")
	var modelID string
	if ok {
		modelID, ok = getNonEmptyString(identity, "modelId")
	}
	if !ok {
		return ignored("attachment:model", ts)
	}
	event := domain.ModelAttachmentEvent{EventBase: base(ts), ModelID: domain.Sanitize(modelID, nameMax)}
	if name, ok := getNonEmptyString(identity, "marketingName"); ok {
		event.Name = domain.Sanitize(name, nameMax)
	}
	return []domain.TranscriptEvent{event}
}

// ParseAgentMeta validates the decoded content of agent-<id>.meta.json; false if it is not an
// object.
func ParseAgentMeta(value any) (domain.AgentMeta, bool) {
	rec, ok := asRecord(value)
	if !ok {
		return domain.AgentMeta{}, false
	}
	var meta domain.AgentMeta
	if v, ok := getNonEmptyString(rec, "agentType"); ok {
		meta.AgentType = domain.Sanitize(v, nameMax)
	}
	if v, ok := getString(rec, "description"); ok {
		meta.Description = domain.Sanitize(v, textMax)
	}
	if v, ok := getNonEmptyString(rec, "toolUseId"); ok {
		meta.ToolUseID = domain.Sanitize(v, idMax)
	}
	if n, ok := getNumber(rec, "spawnDepth"); ok {
		depth := domain.SafeNonNegInt(n)
		meta.SpawnDepth = &depth
	}
	if v, ok := getNonEmptyString(rec, "model"); ok {
		meta.Model = domain.Sanitize(v, nameMax)
	}
	meta.StoppedByUser = isTrue(rec, "stoppedByUser")
	return meta, true
}
