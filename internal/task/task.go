package task

import "time"

type Status string

const (
	StatusDraft       Status = "draft"
	StatusPlanning    Status = "planning"
	StatusRunning     Status = "running"
	StatusWaitingUser Status = "waiting_user"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
)

type EventType string

const (
	EventTaskCreated      EventType = "task.created"
	EventPlanning         EventType = "task.planning"
	EventPlanReady        EventType = "task.plan_ready"
	EventToolCall         EventType = "tool.call"
	EventToolResult       EventType = "tool.result"
	EventSummary          EventType = "task.summary"
	EventDiff             EventType = "task.diff"
	EventSandboxRun       EventType = "sandbox.run"
	EventSandboxWorkspace EventType = "sandbox.workspace"
	EventPatchApply       EventType = "task.patch_applied"
	EventError            EventType = "task.error"
	EventCompleted        EventType = "task.completed"
	EventCancelled        EventType = "task.cancelled"
)

type Task struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id,omitempty"`
	Title       string     `json:"title"`
	UserInput   string     `json:"user_input"`
	Natural     bool       `json:"natural"`
	Status      Status     `json:"status"`
	Workspace   string     `json:"workspace"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Event struct {
	Seq       int64     `json:"seq"`
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      EventType `json:"type"`
	Payload   string    `json:"payload_json"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateRequest struct {
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id,omitempty"`
	Natural   bool   `json:"natural"`
	RunAsync  bool   `json:"run_async"`
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

type CreateSessionRequest struct {
	Workspace string `json:"workspace"`
	Title     string `json:"title,omitempty"`
}

type CreateSessionResponse struct {
	Session Session `json:"session"`
}

type EventPayload struct {
	Message string `json:"message,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Input   string `json:"input,omitempty"`
	Output  string `json:"output,omitempty"`
	Status  string `json:"status,omitempty"`
	Steps   string `json:"steps,omitempty"`
	Diff    string `json:"diff,omitempty"`
}
