package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/trace"
)

const (
	daemonEventTaskCreated        = "task.created"
	daemonEventTaskQueued         = "task.queued"
	daemonEventPlanning           = "task.planning"
	daemonEventSandboxRun         = "sandbox.run"
	daemonEventSandboxWorkspace   = "sandbox.workspace"
	daemonEventPlanReady          = "task.plan_ready"
	daemonEventReplanning         = "task.replanning"
	daemonEventToolCall           = "tool.call"
	daemonEventToolResult         = "tool.result"
	daemonEventTodoUpdated        = "todo.updated"
	daemonEventTranscriptEntry    = "transcript.entry"
	daemonEventArtifactReference  = "artifact.reference"
	daemonEventCompactBoundary    = "compact.boundary"
	daemonEventPromptContext      = "prompt_context.snapshot"
	daemonEventAssistantDelta     = "assistant.delta"
	daemonEventSummary            = "task.summary"
	daemonEventDiff               = "task.diff"
	daemonEventPatchApply         = "task.patch_applied"
	daemonEventPermissionRequest  = "permission.requested"
	daemonEventPermissionApproved = "permission.approved"
	daemonEventPermissionDenied   = "permission.denied"
	daemonEventHookRun            = "hook.run"
	daemonEventScheduleTriggered  = "schedule.triggered"
	daemonEventSubagentStarted    = "subagent.started"
	daemonEventSubagentCompleted  = "subagent.completed"
	daemonEventUserInputRequest   = "user_input.requested"
	daemonEventUserInputReceived  = "user_input.received"
	daemonEventCompleted          = "task.completed"
	daemonEventCancelled          = "task.cancelled"
	daemonEventError              = "task.error"
)

type eventPayload struct {
	ID            string `json:"id,omitempty"`
	Message       string `json:"message,omitempty"`
	Action        string `json:"action,omitempty"`
	Target        string `json:"target,omitempty"`
	Path          string `json:"path,omitempty"`
	Tool          string `json:"tool,omitempty"`
	Input         string `json:"input,omitempty"`
	Output        string `json:"output,omitempty"`
	Status        string `json:"status,omitempty"`
	Steps         string `json:"steps,omitempty"`
	Diff          string `json:"diff,omitempty"`
	Risk          string `json:"risk,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Origin        string `json:"origin,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Source        string `json:"source,omitempty"`
	Trigger       string `json:"trigger,omitempty"`
	ParentTaskID  string `json:"parent_task_id,omitempty"`
	SourceTaskID  string `json:"source_task_id,omitempty"`
	Priority      string `json:"priority,omitempty"`
	TokenEstimate int    `json:"token_estimate,omitempty"`
	TokenBudget   int    `json:"token_budget,omitempty"`
}

type DaemonEventSection struct {
	Title   string
	Body    string
	Visible bool
}

func FormatDaemonEventUpdate(update StreamUpdate) DaemonEventSection {
	if isDaemonEventHiddenFromChat(update.Type) {
		return DaemonEventSection{}
	}
	payload, err := DecodeDaemonEventPayload(update.PayloadJSON)
	if err != nil {
		return DaemonEventSection{Title: "Event", Body: fmt.Sprintf("%s: malformed payload", update.Type), Visible: true}
	}
	eventType := update.Type
	switch eventType {
	case daemonEventTaskCreated, daemonEventPlanning, daemonEventSandboxRun, daemonEventSandboxWorkspace, daemonEventPlanReady, daemonEventReplanning, daemonEventToolCall:
		return DaemonEventSection{}
	case daemonEventToolResult:
		if payload.Status != "" && payload.Status != string(trace.StatusOK) {
			return DaemonEventSection{Title: "Error", Body: formatToolEvent(payload), Visible: true}
		}
	case daemonEventTodoUpdated:
		return DaemonEventSection{Title: "Todo", Body: formatTodoEvent(payload), Visible: true}
	case daemonEventAssistantDelta:
		if strings.TrimSpace(payload.Message) != "" {
			return DaemonEventSection{Title: "Assistant", Body: payload.Message, Visible: true}
		}
	case daemonEventSummary:
		if strings.TrimSpace(payload.Message) != "" {
			return DaemonEventSection{Title: "Assistant", Body: payload.Message, Visible: true}
		}
	case daemonEventDiff:
		if strings.TrimSpace(payload.Diff) != "" {
			return DaemonEventSection{Title: "Assistant", Body: PatchReadyReply(payload.Diff), Visible: true}
		}
	case daemonEventPermissionRequest:
		body := strings.TrimSpace(payload.Tool + " " + payload.Input)
		if payload.Risk != "" {
			body += "\nRisk: " + payload.Risk
		}
		if payload.Reason != "" {
			body += "\nReason: " + payload.Reason
		}
		body += "\nCommands: /approve to continue, /deny to cancel."
		return DaemonEventSection{Title: "Approval", Body: body, Visible: true}
	case daemonEventPermissionApproved:
		return DaemonEventSection{Title: "Approval", Body: "approved", Visible: true}
	case daemonEventPermissionDenied:
		return DaemonEventSection{Title: "Approval", Body: "denied", Visible: true}
	case daemonEventUserInputRequest:
		if strings.TrimSpace(payload.Message) != "" {
			return DaemonEventSection{Title: "Assistant", Body: payload.Message, Visible: true}
		}
	case daemonEventCompleted:
		return DaemonEventSection{}
	case daemonEventCancelled:
		status := valueOr(payload.Status, "cancelled")
		if payload.Message != "" {
			status += ": " + payload.Message
		}
		return DaemonEventSection{Title: "System", Body: status, Visible: true}
	case daemonEventError:
		return DaemonEventSection{Title: "Error", Body: strings.TrimSpace(payload.Message + "\n" + payload.Output), Visible: true}
	default:
		return DaemonEventSection{Title: "Event", Body: daemonEventLine(eventType, payload), Visible: true}
	}
	return DaemonEventSection{}
}

func isDaemonEventHiddenFromChat(eventType string) bool {
	switch eventType {
	case daemonEventTaskCreated,
		daemonEventTaskQueued,
		daemonEventPlanning,
		daemonEventSandboxRun,
		daemonEventSandboxWorkspace,
		daemonEventPlanReady,
		daemonEventReplanning,
		daemonEventToolCall,
		daemonEventTranscriptEntry,
		daemonEventArtifactReference,
		daemonEventCompactBoundary,
		daemonEventPromptContext,
		daemonEventPatchApply,
		daemonEventHookRun,
		daemonEventScheduleTriggered,
		daemonEventSubagentStarted,
		daemonEventSubagentCompleted,
		daemonEventUserInputReceived:
		return true
	default:
		return false
	}
}

func FormatDaemonEventReplay(eventType string, payloadJSON string) string {
	payload, err := DecodeDaemonEventPayload(payloadJSON)
	if err != nil {
		return fmt.Sprintf("%s: malformed payload", eventType)
	}
	return daemonEventReplay(eventType, payload)
}

func FormatDaemonEventTail(eventType string, payloadJSON string) []string {
	payload, err := DecodeDaemonEventPayload(payloadJSON)
	if err != nil {
		return []string{fmt.Sprintf("%s: malformed payload", eventType)}
	}
	return daemonEventTail(eventType, payload)
}

func FormatDaemonEventWatch(taskID string, eventType string, payloadJSON string) string {
	payload, err := DecodeDaemonEventPayload(payloadJSON)
	if err != nil {
		return fmt.Sprintf("%s %s: malformed payload", taskID, eventType)
	}
	return daemonEventWatch(taskID, eventType, payload)
}

func DecodeDaemonEventPayload(payloadJSON string) (eventPayload, error) {
	var payload eventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return eventPayload{}, err
	}
	return payload, nil
}

func decodeEventPayload(payloadJSON string) eventPayload {
	payload, _ := DecodeDaemonEventPayload(payloadJSON)
	return payload
}

func daemonEventReplay(eventType string, payload eventPayload) string {
	switch eventType {
	case daemonEventPlanReady:
		return string(eventType) + ": " + daemonFirstLine(payload.Steps)
	case daemonEventToolCall, daemonEventToolResult:
		status := payload.Status
		if status != "" {
			status = "[" + status + "] "
		}
		return strings.TrimSpace(string(eventType) + ": " + status + payload.Tool + " " + payload.Input + " " + daemonFirstLine(payload.Output))
	case daemonEventTodoUpdated:
		return strings.TrimSpace(string(eventType) + ": " + formatTodoEvent(payload))
	case daemonEventAssistantDelta:
		return string(eventType) + ": " + payload.Message
	case daemonEventSummary:
		return string(eventType) + ": " + payload.Message
	case daemonEventDiff:
		return string(eventType) + ": " + daemonFirstLine(payload.Diff)
	case daemonEventCompleted, daemonEventCancelled, daemonEventError:
		return strings.TrimSpace(string(eventType) + ": " + payload.Status + " " + daemonFirstLine(payload.Message))
	case daemonEventPermissionRequest:
		return strings.TrimSpace(string(eventType) + ": " + payload.Tool + " " + payload.Input + " " + payload.Risk + " " + payload.Reason)
	case daemonEventPermissionApproved, daemonEventPermissionDenied:
		return string(eventType) + ": " + payload.Message
	}
	return daemonEventLine(eventType, payload)
}

func daemonEventTail(eventType string, payload eventPayload) []string {
	header := strings.TrimSpace(string(eventType))
	switch eventType {
	case daemonEventPlanReady:
		return daemonAppendPrefixedLines(header, payload.Steps)
	case daemonEventToolCall:
		return []string{strings.TrimSpace(header + ": " + payload.Tool + " " + payload.Input)}
	case daemonEventToolResult:
		status := payload.Status
		if status == "" {
			status = string(trace.StatusOK)
		}
		lines := []string{strings.TrimSpace(header + " [" + status + "]: " + payload.Tool + " " + payload.Input)}
		return append(lines, daemonIndentLines(payload.Output)...)
	case daemonEventTodoUpdated:
		return daemonAppendPrefixedLines(header, formatTodoEvent(payload))
	case daemonEventArtifactReference:
		line := strings.TrimSpace(header + ": " + payload.Path)
		if strings.TrimSpace(payload.Message) != "" {
			line += " " + strings.TrimSpace(payload.Message)
		}
		lines := []string{line}
		if strings.TrimSpace(payload.Path) != "" {
			lines = append(lines, "  use /artifact "+payload.Path+" tail to read the stored output tail")
		}
		return lines
	case daemonEventAssistantDelta, daemonEventSummary:
		return daemonAppendPrefixedLines(header, payload.Message)
	case daemonEventDiff:
		return daemonAppendPrefixedLines(header, payload.Diff)
	case daemonEventPermissionRequest:
		return []string{strings.TrimSpace(header + ": " + payload.Tool + " " + payload.Input + " " + payload.Risk + " " + payload.Reason)}
	case daemonEventCompleted, daemonEventCancelled, daemonEventError, daemonEventReplanning:
		return daemonAppendPrefixedLines(strings.TrimSpace(header+" "+payload.Status), payload.Message)
	default:
		return daemonAppendPrefixedLines(header, daemonEventNarrative(payload))
	}
}

func daemonEventWatch(taskID string, eventType string, payload eventPayload) string {
	prefix := taskID + " " + string(eventType)
	switch eventType {
	case daemonEventPlanReady:
		return prefix + ": " + daemonFirstLine(payload.Steps)
	case daemonEventToolCall:
		return strings.TrimSpace(prefix + ": " + payload.Tool + " " + payload.Input)
	case daemonEventToolResult:
		status := payload.Status
		if status == "" {
			status = string(trace.StatusOK)
		}
		return strings.TrimSpace(prefix + "[" + status + "]: " + payload.Tool + " " + payload.Input + " " + daemonFirstLine(payload.Output))
	case daemonEventTodoUpdated:
		return strings.TrimSpace(prefix + ": " + formatTodoEvent(payload))
	case daemonEventAssistantDelta, daemonEventSummary:
		return prefix + ": " + daemonFirstLine(payload.Message)
	case daemonEventDiff:
		return prefix + ": " + daemonFirstLine(payload.Diff)
	case daemonEventPermissionRequest:
		return strings.TrimSpace(prefix + ": " + payload.Tool + " " + payload.Input + " " + payload.Risk + " " + payload.Reason)
	case daemonEventCompleted, daemonEventCancelled, daemonEventError, daemonEventReplanning:
		return strings.TrimSpace(prefix + ": " + payload.Status + " " + daemonFirstLine(payload.Message))
	default:
		return strings.TrimSpace(prefix + ": " + daemonFirstLine(daemonEventNarrative(payload)))
	}
}

func formatTodoEvent(payload eventPayload) string {
	fields := []string{}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"id", payload.ID},
		{"action", payload.Action},
		{"status", payload.Status},
		{"priority", payload.Priority},
		{"source_task_id", payload.SourceTaskID},
		{"content", payload.Message},
	} {
		if strings.TrimSpace(field.value) != "" {
			fields = append(fields, field.name+"="+field.value)
		}
	}
	return strings.Join(fields, " ")
}

func daemonEventLine(eventType string, payload eventPayload) string {
	if narrative := daemonEventNarrative(payload); narrative != "" {
		return string(eventType) + ": " + narrative
	}
	return string(eventType)
}

func daemonEventNarrative(payload eventPayload) string {
	for _, value := range []string{
		payload.Message,
		payload.Action,
		payload.Target,
		payload.ID,
		payload.Path,
		payload.Status,
		payload.Reason,
		payload.Output,
	} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func daemonAppendPrefixedLines(prefix string, value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{prefix}
	}
	lines := []string{prefix + ":"}
	return append(lines, daemonIndentLines(value)...)
}

func daemonIndentLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
		if len(line) > 180 {
			line = line[:177] + "..."
		}
		lines = append(lines, "  "+line)
	}
	return lines
}

func daemonFirstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}
