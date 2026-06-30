package protocol

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Lioooooo123/liora/internal/task"
)

const EventContractVersion = "2026-06-30.task-events.v1"

type TaskEventFixture struct {
	Version          string              `json:"version"`
	SingleTaskStream []SingleTaskFrame   `json:"single_task_stream"`
	MultiTaskStream  []TaskEnvelopeFrame `json:"multi_task_stream"`
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

func DaemonEventFixture() TaskEventFixture {
	return TaskEventFixture{
		Version: EventContractVersion,
		SingleTaskStream: []SingleTaskFrame{
			{
				Event: string(task.EventTaskCreated),
				ID:    "event-001",
				Data:  task.EventPayload{Message: "created"},
			},
			{
				Event: string(task.EventToolCall),
				ID:    "event-002",
				Data:  task.EventPayload{Tool: "read", Input: "README.md"},
			},
			{
				Event: string(task.EventToolResult),
				ID:    "event-003",
				Data:  task.EventPayload{Tool: "read", Output: "Liora"},
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
					TaskID:  "task-003",
					Payload: task.EventPayload{Tool: "run", Risk: "dangerous_shell", Reason: "requires approval"},
				},
			},
		},
	}
}

func WriteDaemonEventFixture(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(DaemonEventFixture()); err != nil {
		return fmt.Errorf("encode daemon event fixture: %w", err)
	}
	return nil
}
