package task

import (
	"fmt"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/trust"
)

type Status string

const (
	StatusDraft       Status = "draft"
	StatusQueued      Status = "queued"
	StatusPlanning    Status = "planning"
	StatusRunning     Status = "running"
	StatusWaitingUser Status = "waiting_user"
	StatusLost        Status = "lost"
	StatusRecovered   Status = "recovered"
	StatusStale       Status = "stale"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
)

const (
	DefaultWaitExpiry            = 24 * time.Hour
	DefaultScheduleTriggerExpiry = 1 * time.Hour
)

type Origin string

const (
	OriginForeground Origin = "foreground"
	OriginBackground Origin = "background"
	OriginSchedule   Origin = "schedule"
	OriginHook       Origin = "hook"
	OriginSubagent   Origin = "subagent"
)

type AutomationKind string

const (
	AutomationKindBackground AutomationKind = "background"
	AutomationKindSchedule   AutomationKind = "schedule"
	AutomationKindHook       AutomationKind = "hook"
	AutomationKindSubagent   AutomationKind = "subagent"
)

type AutomationRisk string

const (
	AutomationRiskSafe      AutomationRisk = "safe"
	AutomationRiskDangerous AutomationRisk = "dangerous"
)

type AutomationMetadata struct {
	Kind    AutomationKind `json:"kind,omitempty"`
	Risk    AutomationRisk `json:"risk,omitempty"`
	Source  string         `json:"source,omitempty"`
	Trigger string         `json:"trigger,omitempty"`
}

func NormalizeAutomationMetadata(request CreateRequest) (Origin, AutomationMetadata, error) {
	origin := request.Origin
	automation := request.Automation
	automation.Source = strings.TrimSpace(automation.Source)
	automation.Trigger = strings.TrimSpace(automation.Trigger)

	if origin == "" {
		switch automation.Kind {
		case "":
			origin = OriginForeground
		case AutomationKindBackground:
			origin = OriginBackground
		case AutomationKindSchedule:
			origin = OriginSchedule
		case AutomationKindHook:
			origin = OriginHook
		case AutomationKindSubagent:
			origin = OriginSubagent
		default:
			return "", AutomationMetadata{}, fmt.Errorf("unknown automation kind %q", automation.Kind)
		}
	}
	switch origin {
	case OriginForeground, OriginBackground, OriginSchedule, OriginHook, OriginSubagent:
	default:
		return "", AutomationMetadata{}, fmt.Errorf("unknown task origin %q", origin)
	}

	if automation.Kind == "" {
		switch origin {
		case OriginBackground:
			automation.Kind = AutomationKindBackground
		case OriginSchedule:
			automation.Kind = AutomationKindSchedule
		case OriginHook:
			automation.Kind = AutomationKindHook
		case OriginSubagent:
			automation.Kind = AutomationKindSubagent
		}
	}
	expectedKind := AutomationKind("")
	switch origin {
	case OriginBackground:
		expectedKind = AutomationKindBackground
	case OriginSchedule:
		expectedKind = AutomationKindSchedule
	case OriginHook:
		expectedKind = AutomationKindHook
	case OriginSubagent:
		expectedKind = AutomationKindSubagent
	}
	if automation.Kind != "" {
		switch automation.Kind {
		case AutomationKindBackground, AutomationKindSchedule, AutomationKindHook, AutomationKindSubagent:
		default:
			return "", AutomationMetadata{}, fmt.Errorf("unknown automation kind %q", automation.Kind)
		}
		if automation.Kind != expectedKind {
			return "", AutomationMetadata{}, fmt.Errorf("automation kind %q does not match origin %q", automation.Kind, origin)
		}
	}

	if automation.Risk == "" {
		switch origin {
		case OriginBackground:
			automation.Risk = AutomationRiskSafe
		case OriginSchedule, OriginHook, OriginSubagent:
			automation.Risk = AutomationRiskDangerous
		}
	}
	if automation.Risk != "" {
		switch automation.Risk {
		case AutomationRiskSafe, AutomationRiskDangerous:
		default:
			return "", AutomationMetadata{}, fmt.Errorf("unknown automation risk %q", automation.Risk)
		}
	}
	return origin, automation, nil
}

func AutomationRequiresApproval(origin Origin, automation AutomationMetadata) bool {
	switch origin {
	case OriginSchedule, OriginHook, OriginSubagent:
		return automation.Risk != AutomationRiskSafe
	default:
		return false
	}
}

type EventType string

const (
	EventTaskCreated        EventType = "task.created"
	EventTaskQueued         EventType = "task.queued"
	EventPlanning           EventType = "task.planning"
	EventPlanReady          EventType = "task.plan_ready"
	EventReplanning         EventType = "task.replanning"
	EventToolCall           EventType = "tool.call"
	EventToolResult         EventType = "tool.result"
	EventTodoUpdated        EventType = "todo.updated"
	EventTranscriptEntry    EventType = "transcript.entry"
	EventArtifactReference  EventType = "artifact.reference"
	EventCompactBoundary    EventType = "compact.boundary"
	EventSummary            EventType = "task.summary"
	EventDiff               EventType = "task.diff"
	EventSandboxRun         EventType = "sandbox.run"
	EventSandboxWorkspace   EventType = "sandbox.workspace"
	EventPatchApply         EventType = "task.patch_applied"
	EventPermissionRequest  EventType = "permission.requested"
	EventPermissionApproved EventType = "permission.approved"
	EventPermissionDenied   EventType = "permission.denied"
	EventHookRun            EventType = "hook.run"
	EventScheduleTriggered  EventType = "schedule.triggered"
	EventSubagentStarted    EventType = "subagent.started"
	EventSubagentCompleted  EventType = "subagent.completed"
	EventUserInputRequest   EventType = "user_input.requested"
	EventUserInputReceived  EventType = "user_input.received"
	EventError              EventType = "task.error"
	EventCompleted          EventType = "task.completed"
	EventCancelled          EventType = "task.cancelled"
)

type Task struct {
	ID                       string             `json:"id"`
	SessionID                string             `json:"session_id,omitempty"`
	Title                    string             `json:"title"`
	UserInput                string             `json:"user_input"`
	Natural                  bool               `json:"natural"`
	Status                   Status             `json:"status"`
	Workspace                string             `json:"workspace"`
	Origin                   Origin             `json:"origin"`
	Automation               AutomationMetadata `json:"automation"`
	ApprovalGranted          bool               `json:"approval_granted,omitempty"`
	ParentTaskID             string             `json:"parent_task_id,omitempty"`
	Scope                    TaskScope          `json:"scope,omitempty"`
	InheritedScopeFromParent bool               `json:"inherited_scope_from_parent,omitempty"`
	ApprovalGrants           []string           `json:"approval_grants,omitempty"`
	ModelConfig              *ModelConfig       `json:"model_config,omitempty"`
	CreatedAt                time.Time          `json:"created_at"`
	UpdatedAt                time.Time          `json:"updated_at"`
	CompletedAt              *time.Time         `json:"completed_at,omitempty"`
}

type ModelConfig struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Source   string `json:"source,omitempty"`
}

type Event struct {
	Seq       int64     `json:"seq"`
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      EventType `json:"type"`
	Payload   string    `json:"payload_json"`
	CreatedAt time.Time `json:"created_at"`
}

type Todo struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id,omitempty"`
	SourceTaskID string    `json:"source_task_id,omitempty"`
	Content      string    `json:"content"`
	Status       string    `json:"status"`
	Priority     string    `json:"priority"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TodoWriteItem struct {
	ID           string `json:"id,omitempty"`
	SourceTaskID string `json:"source_task_id,omitempty"`
	Content      string `json:"content"`
	Status       string `json:"status,omitempty"`
	Priority     string `json:"priority,omitempty"`
}

type TodoWriteRequest struct {
	SessionID    string          `json:"session_id,omitempty"`
	SourceTaskID string          `json:"source_task_id,omitempty"`
	Todos        []TodoWriteItem `json:"todos"`
}

type TodoReadRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

type CreateRequest struct {
	Workspace         string             `json:"workspace"`
	Prompt            string             `json:"prompt"`
	SessionID         string             `json:"session_id,omitempty"`
	ThreadID          *string            `json:"thread_id,omitempty"`
	Natural           bool               `json:"natural"`
	RunAsync          bool               `json:"run_async"`
	Queue             bool               `json:"queue,omitempty"`
	Origin            Origin             `json:"origin,omitempty"`
	Automation        AutomationMetadata `json:"automation,omitempty"`
	Schedule          ScheduleMetadata   `json:"schedule,omitempty"`
	ParentTaskID      string             `json:"parent_task_id,omitempty"`
	Scope             TaskScope          `json:"scope,omitempty"`
	ApprovalGrants    []string           `json:"approval_grants,omitempty"`
	AutoApproveParent bool               `json:"auto_approve_parent,omitempty"`
	ModelConfig       *ModelConfig       `json:"model_config,omitempty"`
}

type ScheduleCatchUpPolicy string

const (
	ScheduleCatchUpDefaultMax = 3

	ScheduleCatchUpPolicySkip    ScheduleCatchUpPolicy = "skip"
	ScheduleCatchUpPolicyRunOnce ScheduleCatchUpPolicy = "run_once"
	ScheduleCatchUpPolicyLimited ScheduleCatchUpPolicy = "limited"
)

type ScheduleMetadata struct {
	ID             string                `json:"id,omitempty"`
	CatchUpPolicy  ScheduleCatchUpPolicy `json:"catch_up_policy,omitempty"`
	MissedRuns     int                   `json:"missed_runs,omitempty"`
	MaxCatchUpRuns int                   `json:"max_catch_up_runs,omitempty"`
	CatchUpRuns    int                   `json:"catch_up_runs,omitempty"`
}

func NormalizeScheduleMetadata(origin Origin, metadata ScheduleMetadata) (ScheduleMetadata, error) {
	metadata.ID = strings.TrimSpace(metadata.ID)
	if metadata.MissedRuns < 0 {
		return ScheduleMetadata{}, fmt.Errorf("schedule missed_runs cannot be negative")
	}
	if metadata.MaxCatchUpRuns < 0 {
		return ScheduleMetadata{}, fmt.Errorf("schedule max_catch_up_runs cannot be negative")
	}
	if metadata.CatchUpRuns < 0 {
		return ScheduleMetadata{}, fmt.Errorf("schedule catch_up_runs cannot be negative")
	}
	hasMetadata := metadata.ID != "" || metadata.CatchUpPolicy != "" || metadata.MissedRuns != 0 || metadata.MaxCatchUpRuns != 0 || metadata.CatchUpRuns != 0
	if !hasMetadata {
		return ScheduleMetadata{}, nil
	}
	if origin != OriginSchedule {
		return ScheduleMetadata{}, fmt.Errorf("schedule metadata is only valid for schedule origin")
	}
	if metadata.ID == "" {
		return ScheduleMetadata{}, fmt.Errorf("schedule id is required")
	}
	if metadata.CatchUpPolicy == "" {
		metadata.CatchUpPolicy = ScheduleCatchUpPolicyRunOnce
	}
	switch metadata.CatchUpPolicy {
	case ScheduleCatchUpPolicySkip:
		metadata.CatchUpRuns = 0
	case ScheduleCatchUpPolicyRunOnce:
		if metadata.MissedRuns > 0 {
			metadata.CatchUpRuns = 1
		}
	case ScheduleCatchUpPolicyLimited:
		limit := metadata.MaxCatchUpRuns
		if limit <= 0 || limit > ScheduleCatchUpDefaultMax {
			limit = ScheduleCatchUpDefaultMax
		}
		metadata.MaxCatchUpRuns = limit
		if metadata.MissedRuns < limit {
			metadata.CatchUpRuns = metadata.MissedRuns
		} else {
			metadata.CatchUpRuns = limit
		}
	default:
		return ScheduleMetadata{}, fmt.Errorf("unknown schedule catch_up_policy %q", metadata.CatchUpPolicy)
	}
	if metadata.CatchUpRuns > ScheduleCatchUpDefaultMax {
		metadata.CatchUpRuns = ScheduleCatchUpDefaultMax
	}
	return metadata, nil
}

type TaskScope struct {
	Paths           []string `json:"paths,omitempty"`
	NetworkHosts    []string `json:"network_hosts,omitempty"`
	MCPServers      []string `json:"mcp_servers,omitempty"`
	MCPTools        []string `json:"mcp_tools,omitempty"`
	ApprovalActions []string `json:"approval_actions,omitempty"`
}

type CreateResponse struct {
	Task Task `json:"task"`
}

type Session struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Workspace  string    `json:"workspace"`
	LastTaskID string    `json:"last_task_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	TaskID    string    `json:"task_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TimelineItem struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	TaskID       string    `json:"task_id,omitempty"`
	Kind         string    `json:"kind"`
	Role         string    `json:"role,omitempty"`
	Type         string    `json:"type,omitempty"`
	Title        string    `json:"title,omitempty"`
	Content      string    `json:"content,omitempty"`
	Tool         string    `json:"tool,omitempty"`
	ToolCallID   string    `json:"tool_call_id,omitempty"`
	ToolResultID string    `json:"tool_result_id,omitempty"`
	Input        string    `json:"input,omitempty"`
	Output       string    `json:"output,omitempty"`
	Target       string    `json:"target,omitempty"`
	Status       string    `json:"status,omitempty"`
	Diff         string    `json:"diff,omitempty"`
	Risk         string    `json:"risk,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	Profile      string    `json:"profile,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type CreateSessionRequest struct {
	Workspace string `json:"workspace"`
	Title     string `json:"title,omitempty"`
}

type CreateSessionResponse struct {
	Session Session `json:"session"`
}

type Workbench struct {
	Workspace         string            `json:"workspace,omitempty"`
	Sessions          []Session         `json:"sessions"`
	Threads           []ThreadWorkbench `json:"threads"`
	ActiveTasks       []Task            `json:"active_tasks"`
	QueuedTasks       []Task            `json:"queued_tasks"`
	RecentTasks       []Task            `json:"recent_tasks"`
	PendingApprovals  []PendingApproval `json:"pending_approvals"`
	PendingUserInputs []PendingInput    `json:"pending_user_inputs"`
}

type ThreadWorkbench struct {
	ID                  string             `json:"id"`
	Title               string             `json:"title"`
	Workspace           string             `json:"workspace"`
	LastTaskID          string             `json:"last_task_id,omitempty"`
	ModelConfig         *ThreadModelConfig `json:"model_config,omitempty"`
	TranscriptSessionID string             `json:"transcript_session_id"`
	ContextSessionID    string             `json:"context_session_id"`
	Lifecycle           string             `json:"lifecycle"`
	ActiveTasks         []Task             `json:"active_tasks"`
	QueuedTasks         []Task             `json:"queued_tasks"`
	RecentTasks         []Task             `json:"recent_tasks"`
	PendingApprovals    []PendingApproval  `json:"pending_approvals"`
	PendingUserInputs   []PendingInput     `json:"pending_user_inputs"`
}

type ThreadModelConfig struct {
	ThreadID              string `json:"thread_id"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	BaseURL               string `json:"base_url,omitempty"`
	Profile               string `json:"profile,omitempty"`
	InheritedFromThreadID string `json:"inherited_from_thread_id,omitempty"`
}

type RestartRecoveryCounts struct {
	Queued      int
	WaitingUser int
	Lost        int
}

type PendingApproval struct {
	Task    Task         `json:"task"`
	Request EventPayload `json:"request"`
	Item    ApprovalItem `json:"item"`
}

type PendingInput struct {
	Task    Task         `json:"task"`
	Request EventPayload `json:"request"`
}

type ApprovalItem struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"task_id"`
	ToolCallID     string     `json:"tool_call_id,omitempty"`
	ToolName       string     `json:"tool_name"`
	ArgsPreview    string     `json:"args_preview,omitempty"`
	Risk           string     `json:"risk,omitempty"`
	CommandPreview string     `json:"command_preview,omitempty"`
	DiffPreview    string     `json:"diff_preview,omitempty"`
	Reason         string     `json:"reason,omitempty"`
	Status         string     `json:"status"`
	Decision       string     `json:"decision,omitempty"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type EventPayload struct {
	ID              string `json:"id,omitempty"`
	Message         string `json:"message,omitempty"`
	Action          string `json:"action,omitempty"`
	Target          string `json:"target,omitempty"`
	Path            string `json:"path,omitempty"`
	Tool            string `json:"tool,omitempty"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	ToolResultID    string `json:"tool_result_id,omitempty"`
	Input           string `json:"input,omitempty"`
	Output          string `json:"output,omitempty"`
	Status          string `json:"status,omitempty"`
	Steps           string `json:"steps,omitempty"`
	Diff            string `json:"diff,omitempty"`
	Risk            string `json:"risk,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Origin          string `json:"origin,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Source          string `json:"source,omitempty"`
	Trigger         string `json:"trigger,omitempty"`
	MissedRuns      int    `json:"missed_runs,omitempty"`
	CatchUpPolicy   string `json:"catch_up_policy,omitempty"`
	CatchUpRuns     int    `json:"catch_up_runs,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	StaleAt         string `json:"stale_at,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	Trust           string `json:"trust,omitempty"`
	ContentSource   string `json:"content_source,omitempty"`
	ParentTaskID    string `json:"parent_task_id,omitempty"`
	SourceTaskID    string `json:"source_task_id,omitempty"`
	SourceStartID   string `json:"source_start_id,omitempty"`
	SourceEndID     string `json:"source_end_id,omitempty"`
	SourceItemCount int    `json:"source_item_count,omitempty"`
	Priority        string `json:"priority,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Profile         string `json:"profile,omitempty"`
	TokenEstimate   int    `json:"token_estimate,omitempty"`
	TokenBudget     int    `json:"token_budget,omitempty"`
	LatencyMS       int64  `json:"latency_ms,omitempty"`
	RetryCount      int    `json:"retry_count,omitempty"`
	StopReason      string `json:"stop_reason,omitempty"`
	ReplanReason    string `json:"replan_reason,omitempty"`
}

func ExpiresAtAfter(duration time.Duration) string {
	return formatTime(time.Now().UTC().Add(duration))
}

func NormalizeEventPayload(eventType EventType, payload EventPayload) EventPayload {
	payload.ContentSource = inferredContentSource(eventType, payload.ContentSource)
	payload.Trust = normalizedTrust(payload.Trust, payload.ContentSource)
	return payload
}

func inferredContentSource(eventType EventType, source string) string {
	source = trust.NormalizeSource(source)
	if source != "" {
		return source
	}
	switch eventType {
	case EventToolResult:
		return trust.SourceToolOutput
	case EventHookRun:
		return trust.SourceHookOutput
	case EventArtifactReference:
		return trust.SourceArtifact
	case EventTranscriptEntry:
		return trust.SourceTranscript
	default:
		return ""
	}
}

func normalizedTrust(level string, source string) string {
	sourceLevel := trust.LevelForSource(source)
	if sourceLevel != "" {
		return sourceLevel
	}
	switch trust.NormalizeLevel(level) {
	case trust.LevelTrusted:
		return trust.LevelTrusted
	case trust.LevelUntrusted:
		return trust.LevelUntrusted
	default:
		return ""
	}
}

type ContextRequest struct {
	ItemLimit   int `json:"item_limit,omitempty"`
	TokenBudget int `json:"token_budget,omitempty"`
}

type CompactMode string

const (
	CompactModeManual CompactMode = "manual"
	CompactModeAuto   CompactMode = "auto"
)

type CompactRequest struct {
	Mode        CompactMode `json:"mode,omitempty"`
	ItemLimit   int         `json:"item_limit,omitempty"`
	TokenBudget int         `json:"token_budget,omitempty"`
	Reason      string      `json:"reason,omitempty"`
}

type CompactResult struct {
	Session               Session                 `json:"session"`
	Mode                  CompactMode             `json:"mode"`
	Compacted             bool                    `json:"compacted"`
	SkippedReason         string                  `json:"skipped_reason,omitempty"`
	Reason                string                  `json:"reason,omitempty"`
	TokenBudget           int                     `json:"token_budget"`
	BeforeEstimatedTokens int                     `json:"before_estimated_tokens"`
	AfterEstimatedTokens  int                     `json:"after_estimated_tokens"`
	TranscriptItems       int                     `json:"transcript_items"`
	Boundary              *ContextCompactBoundary `json:"boundary,omitempty"`
	GeneratedAt           time.Time               `json:"generated_at"`
}

type ContextEnvelope struct {
	Session           Session                  `json:"session"`
	Budget            ContextBudget            `json:"budget"`
	Transcript        []TimelineItem           `json:"transcript"`
	Todos             []Todo                   `json:"todos"`
	Memories          []ContextMemory          `json:"memories"`
	Summaries         []ContextSummary         `json:"summaries"`
	ArtifactRefs      []ContextArtifactRef     `json:"artifact_refs"`
	CompactBoundaries []ContextCompactBoundary `json:"compact_boundaries"`
	Pack              ContextPack              `json:"pack"`
	Diagnostics       []ContextDiagnostic      `json:"diagnostics"`
	GeneratedAt       time.Time                `json:"generated_at"`
}

type ArtifactPageRequest struct {
	URI      string `json:"uri,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}

type ArtifactPage struct {
	URI        string   `json:"uri"`
	Page       int      `json:"page"`
	PageSize   int      `json:"page_size"`
	TotalLines int      `json:"total_lines"`
	TotalPages int      `json:"total_pages"`
	HasPrev    bool     `json:"has_prev"`
	HasNext    bool     `json:"has_next"`
	Lines      []string `json:"lines"`
}

type ContextBudget struct {
	MaxTokens       int                   `json:"max_tokens"`
	EstimatedTokens int                   `json:"estimated_tokens"`
	ItemLimit       int                   `json:"item_limit"`
	Truncated       bool                  `json:"truncated"`
	Buckets         []ContextBudgetBucket `json:"buckets"`
}

type ContextBudgetBucket struct {
	Name            string `json:"name"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Items           int    `json:"items"`
}

type ContextSummary struct {
	TaskID    string    `json:"task_id,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type ContextMemory struct {
	ID         string     `json:"id"`
	Text       string     `json:"text"`
	Kind       string     `json:"kind"`
	Source     string     `json:"source,omitempty"`
	Workspace  string     `json:"workspace,omitempty"`
	Importance int        `json:"importance"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type ContextPack struct {
	Sources []ContextPackSource `json:"sources"`
}

type ContextPackSource struct {
	Name            string `json:"name"`
	Selected        int    `json:"selected"`
	Available       int    `json:"available"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Truncated       bool   `json:"truncated"`
}

type ContextDiagnostic struct {
	Source          string    `json:"source"`
	ItemID          string    `json:"item_id,omitempty"`
	ItemKind        string    `json:"item_kind,omitempty"`
	Reason          string    `json:"reason"`
	Summary         string    `json:"summary,omitempty"`
	EstimatedTokens int       `json:"estimated_tokens"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

type ContextArtifactRef struct {
	TaskID    string    `json:"task_id,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Path      string    `json:"path,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ContextCompactBoundary struct {
	TaskID          string    `json:"task_id,omitempty"`
	Summary         string    `json:"summary"`
	TokenBudget     int       `json:"token_budget,omitempty"`
	TokenEstimate   int       `json:"token_estimate,omitempty"`
	SourceStartID   string    `json:"source_start_id,omitempty"`
	SourceEndID     string    `json:"source_end_id,omitempty"`
	SourceItemCount int       `json:"source_item_count,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type InputRequest struct {
	Message string `json:"message"`
}

type InputResponse struct {
	Task Task `json:"task"`
}
