package domain

// TokenTotals are the tokens of an API response (or the sum of several).
type TokenTotals struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheCreate int `json:"cacheCreate"`
}

type ContextCounter struct {
	// Used is input + cacheCreate + cacheRead of the assistant's last response.
	Used int `json:"used"`
	// LastMessageID is the message.id of the response that set Used.
	LastMessageID string `json:"lastMessageId,omitempty"`
}

type PendingTool struct {
	Name string `json:"name"`
	// Target is the (sanitized) file path, only for Read/Edit/Write/NotebookEdit.
	Target string `json:"target,omitempty"`
	// CommandCategory is only for Bash (the command itself is never kept).
	CommandCategory CommandCategory `json:"commandCategory,omitempty"`
	// UsesGh is only for Bash: some segment of the command runs `gh`.
	UsesGh bool `json:"usesGh,omitempty"`
	// StartedAt is epoch ms.
	StartedAt int64 `json:"startedAt,omitempty"`
}

// PendingEntry is a tool_use without a tool_result, by tool_use.id. Entries are kept in
// insertion order: the last one is the most recent.
type PendingEntry struct {
	ID string `json:"id"`
	PendingTool
}

type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type AgentStatus string

const (
	StatusLaunched   AgentStatus = "launched"
	StatusRunning    AgentStatus = "running"
	StatusHandedBack AgentStatus = "handed_back"
	StatusCompleted  AgentStatus = "completed"
	StatusFailed     AgentStatus = "failed"
	StatusUnknown    AgentStatus = "unknown"
)

// ActivityState holds the activity fields shared by a session and an agent.
type ActivityState struct {
	Context ContextCounter `json:"context"`
	// Totals is the sum deduplicated by message.id.
	Totals TokenTotals `json:"totals"`
	// UsageByMessageID is the usage already counted per message.id.
	UsageByMessageID map[string]TokenTotals `json:"usageByMessageId"`
	// PendingTools are the tool_use without tool_result, in insertion order.
	PendingTools []PendingEntry `json:"pendingTools"`
	// SeenToolUseIDs are the most recent tool_use.id counted in the activity histogram (up to
	// MaxSeenToolUses), oldest first.
	SeenToolUseIDs []string `json:"seenToolUseIds"`
	// LastActivityAt is the epoch ms of the latest line with a timestamp.
	LastActivityAt int64 `json:"lastActivityAt,omitempty"`
	// PullRequests are the PR links seen in assistant text and in `gh` and sub-agent results,
	// oldest first.
	PullRequests []PullRequestRef `json:"pullRequests"`
}

type AgentState struct {
	ActivityState
	AgentID      string      `json:"agentId,omitempty"`
	ToolUseID    string      `json:"toolUseId,omitempty"`
	Type         string      `json:"type"`
	Description  string      `json:"description"`
	Model        string      `json:"model,omitempty"`
	IsBackground bool        `json:"isBackground"`
	Status       AgentStatus `json:"status"`
	// FailureReason is why the agent failed, when known: stopped, killed, error, or the
	// notification status.
	FailureReason string `json:"failureReason,omitempty"`
	SpawnDepth    *int   `json:"spawnDepth,omitempty"`
	StartedAt     int64  `json:"startedAt,omitempty"`
	FinishedAt    int64  `json:"finishedAt,omitempty"`
	// HandbackReceivedAt is the epoch ms of the latest peer handback the parent session received
	// from this agent.
	HandbackReceivedAt int64 `json:"handbackReceivedAt,omitempty"`
}

// GetStartedAt implements AgentStart.
func (a AgentState) GetStartedAt() int64 { return a.StartedAt }

type TitleSource string

const (
	TitleCustom TitleSource = "custom"
	TitleAI     TitleSource = "ai"
)

type SessionState struct {
	ActivityState
	SessionID   string      `json:"sessionId"`
	Cwd         string      `json:"cwd,omitempty"`
	ProjectName string      `json:"projectName,omitempty"`
	GitBranch   string      `json:"gitBranch,omitempty"`
	Title       string      `json:"title,omitempty"`
	TitleSource TitleSource `json:"titleSource,omitempty"`
	AITitle     string      `json:"aiTitle,omitempty"`
	CustomTitle string      `json:"customTitle,omitempty"`
	Version     string      `json:"version,omitempty"`
	Model       *ModelInfo  `json:"model,omitempty"`
	Effort      string      `json:"effort,omitempty"`
	// LastUserPromptAt is epoch ms.
	LastUserPromptAt int64 `json:"lastUserPromptAt,omitempty"`
	// PendingBackgroundAgents is the pendingBackgroundAgentCount of the last turn_duration.
	PendingBackgroundAgents *int `json:"pendingBackgroundAgents,omitempty"`
	// Agents by agentId when known, else by the launch toolUseId.
	Agents            map[string]AgentState `json:"agents"`
	ActivityHistogram ActivityHistogram     `json:"activityHistogram"`
	ParseErrors       int                   `json:"parseErrors"`
	Ignored           int                   `json:"ignored"`
}

// TranscriptEvent is a normalized transcript line: one of the *Event types below.
type TranscriptEvent interface {
	EventKind() string
	EventTimestamp() int64
}

// EventBase is the part shared by every event.
type EventBase struct {
	// Timestamp is the epoch ms of the line envelope, if any.
	Timestamp int64 `json:"timestamp,omitempty"`
}

// EventTimestamp returns the epoch ms of the line envelope, 0 if absent.
func (b EventBase) EventTimestamp() int64 { return b.Timestamp }

// AgentLaunchRequest is a sub-agent launch seen in the Agent/Task tool_use.
type AgentLaunchRequest struct {
	Type            string `json:"type"`
	Description     string `json:"description"`
	Model           string `json:"model,omitempty"`
	RunInBackground bool   `json:"runInBackground"`
}

type ToolUseInfo struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Target          string              `json:"target,omitempty"`
	CommandCategory CommandCategory     `json:"commandCategory,omitempty"`
	UsesGh          bool                `json:"usesGh,omitempty"`
	AgentLaunch     *AgentLaunchRequest `json:"agentLaunch,omitempty"`
}

type EnvelopeInfoEvent struct {
	EventBase
	SessionID   string `json:"sessionId,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	GitBranch   string `json:"gitBranch,omitempty"`
	Version     string `json:"version,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
	IsSidechain *bool  `json:"isSidechain,omitempty"`
}

type AssistantMessageEvent struct {
	EventBase
	MessageID      string           `json:"messageId,omitempty"`
	Model          string           `json:"model,omitempty"`
	RequestedModel string           `json:"requestedModel,omitempty"`
	Effort         string           `json:"effort,omitempty"`
	StopReason     string           `json:"stopReason,omitempty"`
	Usage          *TokenTotals     `json:"usage,omitempty"`
	ToolUses       []ToolUseInfo    `json:"toolUses"`
	PullRequests   []PullRequestRef `json:"pullRequests,omitempty"`
}

// AgentLaunchResult confirms an async launch (toolUseResult.status == "async_launched").
type AgentLaunchResult struct {
	AgentID       string `json:"agentId"`
	Description   string `json:"description,omitempty"`
	ResolvedModel string `json:"resolvedModel,omitempty"`
}

type ToolResultEvent struct {
	EventBase
	ToolUseID   string             `json:"toolUseId"`
	IsError     bool               `json:"isError"`
	AgentLaunch *AgentLaunchResult `json:"agentLaunch,omitempty"`
	// ResultAgentID is the toolUseResult.agentId of an agent result that is not async_launched.
	ResultAgentID string `json:"resultAgentId,omitempty"`
	// PullRequests are the validated PR links in the result content; the reducers keep them
	// only when the tool may report a PR (ToolResultPullRequests).
	PullRequests []PullRequestRef `json:"pullRequests,omitempty"`
	// ResumedAgentID is the toolUseResult.resumedAgentId: the tool resumed this finished agent.
	ResumedAgentID string `json:"resumedAgentId,omitempty"`
}

// AgentHandbackEvent is a peer message (origin.kind == "peer"). With Handback it is a
// sub-agent's result reaching the parent, the agent's real completion.
type AgentHandbackEvent struct {
	EventBase
	// AgentID is origin.senderTaskId, else origin.from.
	AgentID  string `json:"agentId"`
	Handback bool   `json:"handback"`
}

// AgentsKilledEvent is a system line with subtype "agents_killed".
type AgentsKilledEvent struct{ EventBase }

type TaskNotificationEvent struct {
	EventBase
	TaskID    string `json:"taskId,omitempty"`
	ToolUseID string `json:"toolUseId,omitempty"`
	// Status is lowercase (completed, failed, killed...).
	Status string `json:"status,omitempty"`
}

type UserPromptEvent struct {
	EventBase
	IsMeta bool `json:"isMeta"`
}

type TurnEndEvent struct {
	EventBase
	DurationMs                  *int64 `json:"durationMs,omitempty"`
	PendingBackgroundAgentCount *int   `json:"pendingBackgroundAgentCount,omitempty"`
}

type ModelAttachmentEvent struct {
	EventBase
	ModelID string `json:"modelId"`
	Name    string `json:"name,omitempty"`
}

type AITitleEvent struct {
	EventBase
	Title string `json:"title"`
}

// CustomTitleEvent is a user-given title; it wins over the AI title.
type CustomTitleEvent struct {
	EventBase
	Title string `json:"title"`
}

type IgnoredEvent struct {
	EventBase
	// LineType is the line's type (sanitized and short) or a reason.
	LineType string `json:"lineType"`
}

type ParseErrorReason string

const (
	ParseInvalidJSON ParseErrorReason = "invalid_json"
	ParseNotObject   ParseErrorReason = "not_object"
)

type ParseErrorEvent struct {
	EventBase
	Reason ParseErrorReason `json:"reason"`
}

func (EnvelopeInfoEvent) EventKind() string     { return "envelope" }
func (AssistantMessageEvent) EventKind() string { return "assistant" }
func (ToolResultEvent) EventKind() string       { return "tool_result" }
func (TaskNotificationEvent) EventKind() string { return "task_notification" }
func (AgentHandbackEvent) EventKind() string    { return "agent_handback" }
func (AgentsKilledEvent) EventKind() string     { return "agents_killed" }
func (UserPromptEvent) EventKind() string       { return "user_prompt" }
func (TurnEndEvent) EventKind() string          { return "turn_end" }
func (ModelAttachmentEvent) EventKind() string  { return "model_attachment" }
func (AITitleEvent) EventKind() string          { return "ai_title" }
func (CustomTitleEvent) EventKind() string      { return "custom_title" }
func (IgnoredEvent) EventKind() string          { return "ignored" }
func (ParseErrorEvent) EventKind() string       { return "parse_error" }

// AgentMeta is the validated content of subagents/agent-<id>.meta.json.
type AgentMeta struct {
	AgentType   string `json:"agentType,omitempty"`
	Description string `json:"description,omitempty"`
	ToolUseID   string `json:"toolUseId,omitempty"`
	SpawnDepth  *int   `json:"spawnDepth,omitempty"`
	Model       string `json:"model,omitempty"`
	// StoppedByUser is only true when stoppedByUser === true in the meta.json.
	StoppedByUser bool `json:"stoppedByUser,omitempty"`
}
