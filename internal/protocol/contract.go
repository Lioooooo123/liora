package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Lioooooo123/liora/internal/task"
)

const EventContractVersion = task.EventContractVersion

type TaskEventFixture struct {
	Version           string                `json:"version"`
	ContractVersion   string                `json:"contract_version"`
	SingleTaskStream  []SingleTaskFrame     `json:"single_task_stream"`
	MultiTaskStream   []TaskEnvelopeFrame   `json:"multi_task_stream"`
	MultiThreadStream []ThreadEnvelopeFrame `json:"multi_thread_stream"`
	ErrorResponse     ErrorResponse         `json:"error_response"`
}

type SingleTaskFrame struct {
	Event string            `json:"event"`
	ID    string            `json:"id"`
	Data  task.EventPayload `json:"data"`
}

type TaskEnvelopeFrame struct {
	Event string       `json:"event"`
	ID    string       `json:"id"`
	Data  TaskEnvelope `json:"data"`
}

type TaskEnvelope struct {
	TaskID  string            `json:"task_id"`
	Payload task.EventPayload `json:"payload"`
}

type ThreadEnvelopeFrame struct {
	Event string         `json:"event"`
	ID    string         `json:"id"`
	Data  ThreadEnvelope `json:"data"`
}

type ThreadEnvelope struct {
	ThreadID string            `json:"thread_id"`
	TaskID   string            `json:"task_id"`
	Payload  task.EventPayload `json:"payload"`
}

type ErrorResponse struct {
	Status int    `json:"status"`
	Error  string `json:"error"`
}

type EventCatalogFixture struct {
	Version         string              `json:"version"`
	ContractVersion string              `json:"contract_version"`
	Events          []EventCatalogFrame `json:"events"`
}

type EventCatalogFrame struct {
	Event string            `json:"event"`
	Data  task.EventPayload `json:"data"`
}

func DaemonEventFixture() TaskEventFixture {
	return TaskEventFixture{
		Version:         EventContractVersion,
		ContractVersion: EventContractVersion,
		SingleTaskStream: []SingleTaskFrame{
			{
				Event: string(task.EventTaskCreated),
				ID:    "event-001",
				Data: task.EventPayload{
					Message: "created",
					Origin:  string(task.OriginBackground),
					Kind:    string(task.AutomationKindBackground),
					Risk:    string(task.AutomationRiskSafe),
					Source:  "fixture",
					Trigger: "manual-smoke",
				},
			},
			{
				Event: string(task.EventToolCall),
				ID:    "event-002",
				Data:  task.EventPayload{Tool: "read", ToolCallID: "fixture-call-1", Input: "README.md"},
			},
			{
				Event: string(task.EventToolResult),
				ID:    "event-003",
				Data:  task.EventPayload{Tool: "read", ToolCallID: "fixture-call-1", ToolResultID: "fixture-call-1-result", Output: "Liora"},
			},
			{
				Event: string(task.EventCompleted),
				ID:    "event-004",
				Data:  task.EventPayload{Status: string(task.StatusCompleted)},
			},
		},
		MultiTaskStream: []TaskEnvelopeFrame{
			{
				Event: string(task.EventSummary),
				ID:    "event-101",
				Data: TaskEnvelope{
					TaskID:  "task-002",
					Payload: task.EventPayload{Message: "background summary"},
				},
			},
			{
				Event: string(task.EventPermissionRequest),
				ID:    "event-102",
				Data: TaskEnvelope{
					TaskID: "task-003",
					Payload: task.EventPayload{
						Tool:    "run",
						Risk:    "dangerous_shell",
						Reason:  "requires approval",
						Origin:  string(task.OriginSchedule),
						Kind:    string(task.AutomationKindSchedule),
						Source:  "cron:nightly",
						Trigger: "0 2 * * *",
					},
				},
			},
		},
		MultiThreadStream: []ThreadEnvelopeFrame{
			{
				Event: "thread_message.received",
				ID:    "event-201",
				Data: ThreadEnvelope{
					ThreadID: "thread-002",
					TaskID:   "task-004",
					Payload:  task.EventPayload{Message: "handoff received"},
				},
			},
		},
		ErrorResponse: ErrorResponse{Status: 404, Error: "task not found"},
	}
}

func DaemonEventCatalogFixture() EventCatalogFixture {
	definitions := task.EventDefinitions()
	events := make([]EventCatalogFrame, 0, len(definitions))
	for _, definition := range definitions {
		events = append(events, EventCatalogFrame{
			Event: string(definition.Type),
			Data:  sampleEventPayload(definition.Type),
		})
	}
	return EventCatalogFixture{
		Version:         EventContractVersion,
		ContractVersion: EventContractVersion,
		Events:          events,
	}
}

func sampleEventPayload(eventType task.EventType) task.EventPayload {
	switch eventType {
	case task.EventTaskCreated:
		return task.EventPayload{Message: "created", Origin: string(task.OriginBackground), Kind: string(task.AutomationKindBackground), Risk: string(task.AutomationRiskSafe), Source: "fixture", Trigger: "manual-smoke"}
	case task.EventTaskQueued:
		return task.EventPayload{Message: "queued", Status: string(task.StatusQueued)}
	case task.EventPlanning:
		return task.EventPayload{Message: "planning task"}
	case task.EventPlanReady:
		return task.EventPayload{Steps: "read README.md\nsummarize"}
	case task.EventReplanning:
		return task.EventPayload{Message: "replanning after tool result", Reason: "read missing.txt: no such file", ReplanReason: "read missing.txt: no such file", RetryCount: 0}
	case task.EventToolCall:
		return task.EventPayload{Tool: "read", ToolCallID: "catalog-call-1", Input: "README.md"}
	case task.EventToolResult:
		return task.EventPayload{Tool: "read", ToolCallID: "catalog-call-1", ToolResultID: "catalog-call-1-result", Input: "README.md", Output: "Liora", Status: "ok"}
	case task.EventTodoUpdated:
		return task.EventPayload{ID: "todo-001", Action: "complete", Target: "tests", Message: "write tests", ParentTaskID: "task-001"}
	case task.EventTranscriptEntry:
		return task.EventPayload{Kind: "assistant", Message: "summary persisted"}
	case task.EventArtifactReference:
		return task.EventPayload{Tool: "shell", Path: ".liora/tool-results/context.txt", Message: "full output reference", TokenEstimate: 2048}
	case task.EventCompactBoundary:
		return task.EventPayload{Message: "compacted before resume", TokenBudget: 512, TokenEstimate: 128}
	case task.EventPromptContextSnapshot:
		return task.EventPayload{Message: "Prompt context snapshot", Target: "sha256:fixture", Output: "Prompt context session-001\nHash: sha256:fixture", TokenBudget: 4096, TokenEstimate: 256, SourceItemCount: 4}
	case task.EventSummary:
		return task.EventPayload{Message: "background summary"}
	case task.EventDiff:
		return task.EventPayload{Diff: "--- a/app.txt\n+++ b/app.txt\n"}
	case task.EventSandboxRun:
		return task.EventPayload{Message: "shell executor: local"}
	case task.EventSandboxWorkspace:
		return task.EventPayload{Message: "workspace mode: copy"}
	case task.EventPatchApply:
		return task.EventPayload{Action: "apply", Message: "patch applied", Status: "ok"}
	case task.EventPermissionRequest:
		return task.EventPayload{Tool: "run", Input: "rm -rf tmp", Risk: "dangerous_shell", Reason: "requires approval"}
	case task.EventPermissionApproved:
		return task.EventPayload{Message: "approved", Status: "approved"}
	case task.EventPermissionDenied:
		return task.EventPayload{Message: "denied", Status: "denied"}
	case task.EventHookRun:
		return task.EventPayload{Action: "PreToolUse", Status: "ok", Source: "workspace", Message: "checked command"}
	case task.EventScheduleTriggered:
		return task.EventPayload{ID: "schedule-001", Trigger: "0 2 * * *", Message: "nightly audit"}
	case task.EventSubagentStarted:
		return task.EventPayload{ID: "agent-001", ParentTaskID: "task-001", Message: "review started", Status: "running"}
	case task.EventSubagentCompleted:
		return task.EventPayload{ID: "agent-001", ParentTaskID: "task-001", Message: "review completed", Status: string(task.StatusCompleted)}
	case task.EventUserInputRequest:
		return task.EventPayload{ID: "input-001", Message: "need clarification", Reason: "missing target"}
	case task.EventUserInputReceived:
		return task.EventPayload{ID: "input-001", Message: "use the current workspace"}
	case task.EventError:
		return task.EventPayload{Message: "failed at step 1/1", Status: string(task.StatusFailed)}
	case task.EventCompleted:
		return task.EventPayload{Message: "completed", Status: string(task.StatusCompleted)}
	case task.EventCancelled:
		return task.EventPayload{Message: "cancelled", Status: string(task.StatusCancelled)}
	default:
		return task.EventPayload{Message: string(eventType)}
	}
}

func ParseDaemonEventFixture(reader io.Reader) (TaskEventFixture, error) {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return TaskEventFixture{}, fmt.Errorf("read daemon event fixture: %w", err)
	}

	var raw rawTaskEventFixture
	if err := json.Unmarshal(payload, &raw); err != nil {
		return TaskEventFixture{}, fmt.Errorf("decode daemon event fixture: %w", err)
	}
	var fixture TaskEventFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		return TaskEventFixture{}, fmt.Errorf("decode daemon event fixture: %w", err)
	}
	if strings.TrimSpace(fixture.ContractVersion) == "" {
		return TaskEventFixture{}, fmt.Errorf("contract_version is required")
	}
	if fixture.ContractVersion != EventContractVersion {
		return TaskEventFixture{}, fmt.Errorf("unsupported contract_version %q", fixture.ContractVersion)
	}
	if fixture.Version != EventContractVersion {
		return TaskEventFixture{}, fmt.Errorf("unsupported version %q", fixture.Version)
	}
	if err := validateRawTaskEventFixture(raw); err != nil {
		return TaskEventFixture{}, err
	}
	return fixture, nil
}

type rawTaskEventFixture struct {
	Version           string                   `json:"version"`
	ContractVersion   string                   `json:"contract_version"`
	SingleTaskStream  []rawSingleTaskFrame     `json:"single_task_stream"`
	MultiTaskStream   []rawTaskEnvelopeFrame   `json:"multi_task_stream"`
	MultiThreadStream []rawThreadEnvelopeFrame `json:"multi_thread_stream"`
	ErrorResponse     *ErrorResponse           `json:"error_response"`
}

type rawSingleTaskFrame struct {
	Event string          `json:"event"`
	ID    string          `json:"id"`
	Data  json.RawMessage `json:"data"`
}

type rawTaskEnvelopeFrame struct {
	Event string           `json:"event"`
	ID    string           `json:"id"`
	Data  *rawTaskEnvelope `json:"data"`
}

type rawTaskEnvelope struct {
	TaskID  string          `json:"task_id"`
	Payload json.RawMessage `json:"payload"`
}

type rawThreadEnvelopeFrame struct {
	Event string             `json:"event"`
	ID    string             `json:"id"`
	Data  *rawThreadEnvelope `json:"data"`
}

type rawThreadEnvelope struct {
	ThreadID string          `json:"thread_id"`
	TaskID   string          `json:"task_id"`
	Payload  json.RawMessage `json:"payload"`
}

func validateRawTaskEventFixture(fixture rawTaskEventFixture) error {
	for i, frame := range fixture.SingleTaskStream {
		if err := validateFrameIdentity("single_task_stream", i, frame.Event, frame.ID); err != nil {
			return err
		}
		if err := validateRawPayload("single_task_stream", i, "data", frame.Data); err != nil {
			return err
		}
	}
	for i, frame := range fixture.MultiTaskStream {
		if err := validateFrameIdentity("multi_task_stream", i, frame.Event, frame.ID); err != nil {
			return err
		}
		if frame.Data == nil {
			return fmt.Errorf("multi_task_stream[%d].data is required", i)
		}
		if strings.TrimSpace(frame.Data.TaskID) == "" {
			return fmt.Errorf("multi_task_stream[%d].data.task_id is required", i)
		}
		if err := validateRawPayload("multi_task_stream", i, "data.payload", frame.Data.Payload); err != nil {
			return err
		}
	}
	for i, frame := range fixture.MultiThreadStream {
		if err := validateFrameIdentity("multi_thread_stream", i, frame.Event, frame.ID); err != nil {
			return err
		}
		if frame.Data == nil {
			return fmt.Errorf("multi_thread_stream[%d].data is required", i)
		}
		if strings.TrimSpace(frame.Data.ThreadID) == "" {
			return fmt.Errorf("multi_thread_stream[%d].data.thread_id is required", i)
		}
		if strings.TrimSpace(frame.Data.TaskID) == "" {
			return fmt.Errorf("multi_thread_stream[%d].data.task_id is required", i)
		}
		if err := validateRawPayload("multi_thread_stream", i, "data.payload", frame.Data.Payload); err != nil {
			return err
		}
	}
	if fixture.ErrorResponse == nil {
		return fmt.Errorf("error_response is required")
	}
	if fixture.ErrorResponse.Status <= 0 {
		return fmt.Errorf("error_response.status is required")
	}
	if strings.TrimSpace(fixture.ErrorResponse.Error) == "" {
		return fmt.Errorf("error_response.error is required")
	}
	return nil
}

func validateFrameIdentity(stream string, index int, event string, id string) error {
	if strings.TrimSpace(event) == "" {
		return fmt.Errorf("%s[%d].event is required", stream, index)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s[%d].id is required", stream, index)
	}
	return nil
}

func validateRawPayload(stream string, index int, field string, payload json.RawMessage) error {
	if len(payload) == 0 || string(payload) == "null" {
		return fmt.Errorf("%s[%d].%s is required", stream, index, field)
	}
	return nil
}

func WriteDaemonEventFixture(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(DaemonEventFixture()); err != nil {
		return fmt.Errorf("encode daemon event fixture: %w", err)
	}
	return nil
}

func WriteDaemonEventCatalogFixture(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(DaemonEventCatalogFixture()); err != nil {
		return fmt.Errorf("encode daemon event catalog fixture: %w", err)
	}
	return nil
}
