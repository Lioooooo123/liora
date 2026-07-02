package hook

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/permission"
)

type Event string

const (
	EventSessionStart      Event = "SessionStart"
	EventPreToolUse        Event = "PreToolUse"
	EventPostToolUse       Event = "PostToolUse"
	EventPermissionRequest Event = "PermissionRequest"
	EventTaskComplete      Event = "TaskComplete"
	EventTaskFail          Event = "TaskFail"
	EventScheduleTrigger   Event = "ScheduleTrigger"
	EventCompact           Event = "Compact"
)

type RunStatus string

const (
	RunStatusOK      RunStatus = "ok"
	RunStatusFailed  RunStatus = "failed"
	RunStatusTimeout RunStatus = "timeout"
)

type Hook struct {
	ID        string
	Event     Event
	Command   string
	Enabled   bool
	Risk      string
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type RunRecord struct {
	ID              string
	HookID          string
	Event           Event
	Workspace       string
	Payload         string
	Status          RunStatus
	ExitCode        int
	Stdout          string
	Stderr          string
	OutputTruncated bool
	ReplayOfRunID   string
	CreatedAt       time.Time
}

type SaveRequest struct {
	ID      string
	Event   Event
	Command string
	Enabled bool
}

type RunListOptions struct {
	HookID string
	Limit  int
}

func normalizeHook(request SaveRequest) (Hook, error) {
	id := strings.TrimSpace(request.ID)
	if id == "" {
		return Hook{}, errors.New("hook id is required")
	}
	event, err := NormalizeEvent(request.Event)
	if err != nil {
		return Hook{}, err
	}
	command := strings.TrimSpace(request.Command)
	if command == "" {
		return Hook{}, errors.New("hook command is required")
	}
	classified, _ := permission.Classify("hook", string(event)+" "+command, false)
	return Hook{
		ID:      id,
		Event:   event,
		Command: command,
		Enabled: request.Enabled,
		Risk:    classified.Risk,
		Reason:  classified.Reason,
	}, nil
}

func NormalizeEvent(event Event) (Event, error) {
	switch Event(strings.TrimSpace(string(event))) {
	case EventSessionStart:
		return EventSessionStart, nil
	case EventPreToolUse:
		return EventPreToolUse, nil
	case EventPostToolUse:
		return EventPostToolUse, nil
	case EventPermissionRequest:
		return EventPermissionRequest, nil
	case EventTaskComplete:
		return EventTaskComplete, nil
	case EventTaskFail:
		return EventTaskFail, nil
	case EventScheduleTrigger:
		return EventScheduleTrigger, nil
	case EventCompact:
		return EventCompact, nil
	default:
		return "", fmt.Errorf("unknown hook event %q", event)
	}
}
