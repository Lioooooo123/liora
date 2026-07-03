package task

import (
	"fmt"
	"strings"
	"time"
)

type EventFamily string

const (
	EventContractVersion = "2026-06-30.task-events.v1"

	EventFamilyTask       EventFamily = "task"
	EventFamilyTool       EventFamily = "tool"
	EventFamilyTodo       EventFamily = "todo"
	EventFamilyTranscript EventFamily = "transcript"
	EventFamilyArtifact   EventFamily = "artifact"
	EventFamilyContext    EventFamily = "context"
	EventFamilyApproval   EventFamily = "approval"
	EventFamilyHook       EventFamily = "hook"
	EventFamilySchedule   EventFamily = "schedule"
	EventFamilySubagent   EventFamily = "subagent"
	EventFamilySandbox    EventFamily = "sandbox"
	EventFamilyUserInput  EventFamily = "user_input"
)

type EventCompatibility string

const (
	EventCompatibilityAdditive EventCompatibility = "additive"
	EventCompatibilityBreaking EventCompatibility = "breaking"
)

type EventDefinition struct {
	Type          EventType          `json:"type"`
	Family        EventFamily        `json:"family"`
	IntroducedIn  string             `json:"introduced_in"`
	Compatibility EventCompatibility `json:"compatibility"`
}

var eventDefinitions = []EventDefinition{
	eventDefinition(EventTaskCreated, EventFamilyTask),
	eventDefinition(EventTaskQueued, EventFamilyTask),
	eventDefinition(EventPlanning, EventFamilyTask),
	eventDefinition(EventPlanReady, EventFamilyTask),
	eventDefinition(EventReplanning, EventFamilyTask),
	eventDefinition(EventToolCall, EventFamilyTool),
	eventDefinition(EventToolResult, EventFamilyTool),
	eventDefinition(EventTodoUpdated, EventFamilyTodo),
	eventDefinition(EventTranscriptEntry, EventFamilyTranscript),
	eventDefinition(EventArtifactReference, EventFamilyArtifact),
	eventDefinition(EventCompactBoundary, EventFamilyContext),
	eventDefinition(EventPromptContextSnapshot, EventFamilyContext),
	eventDefinition(EventSummary, EventFamilyTask),
	eventDefinition(EventDiff, EventFamilyTask),
	eventDefinition(EventSandboxRun, EventFamilySandbox),
	eventDefinition(EventSandboxWorkspace, EventFamilySandbox),
	eventDefinition(EventPatchApply, EventFamilyTask),
	eventDefinition(EventPermissionRequest, EventFamilyApproval),
	eventDefinition(EventPermissionApproved, EventFamilyApproval),
	eventDefinition(EventPermissionDenied, EventFamilyApproval),
	eventDefinition(EventHookRun, EventFamilyHook),
	eventDefinition(EventScheduleTriggered, EventFamilySchedule),
	eventDefinition(EventSubagentStarted, EventFamilySubagent),
	eventDefinition(EventSubagentCompleted, EventFamilySubagent),
	eventDefinition(EventUserInputRequest, EventFamilyUserInput),
	eventDefinition(EventUserInputReceived, EventFamilyUserInput),
	eventDefinition(EventError, EventFamilyTask),
	eventDefinition(EventCompleted, EventFamilyTask),
	eventDefinition(EventCancelled, EventFamilyTask),
}

var requiredEventTypes = []EventType{
	EventTaskCreated,
	EventTaskQueued,
	EventPlanning,
	EventPlanReady,
	EventReplanning,
	EventToolCall,
	EventToolResult,
	EventTodoUpdated,
	EventTranscriptEntry,
	EventArtifactReference,
	EventCompactBoundary,
	EventPromptContextSnapshot,
	EventSummary,
	EventDiff,
	EventSandboxRun,
	EventSandboxWorkspace,
	EventPatchApply,
	EventPermissionRequest,
	EventPermissionApproved,
	EventPermissionDenied,
	EventHookRun,
	EventScheduleTriggered,
	EventSubagentStarted,
	EventSubagentCompleted,
	EventUserInputRequest,
	EventUserInputReceived,
	EventError,
	EventCompleted,
	EventCancelled,
}

var eventDefinitionsByType = func() map[EventType]EventDefinition {
	index := make(map[EventType]EventDefinition, len(eventDefinitions))
	for _, definition := range eventDefinitions {
		index[definition.Type] = definition
	}
	return index
}()

func eventDefinition(eventType EventType, family EventFamily) EventDefinition {
	return EventDefinition{
		Type:          eventType,
		Family:        family,
		IntroducedIn:  EventContractVersion,
		Compatibility: EventCompatibilityAdditive,
	}
}

func EventDefinitions() []EventDefinition {
	definitions := make([]EventDefinition, len(eventDefinitions))
	copy(definitions, eventDefinitions)
	return definitions
}

func EventDefinitionFor(eventType EventType) (EventDefinition, bool) {
	definition, ok := eventDefinitionsByType[eventType]
	return definition, ok
}

func ValidateEventCatalogCompatibility() error {
	return validateEventCatalogCompatibility(eventDefinitions, EventContractVersion)
}

func validateEventCatalogCompatibility(definitions []EventDefinition, contractVersion string) error {
	contractVersion = strings.TrimSpace(contractVersion)
	if contractVersion == "" {
		return fmt.Errorf("event contract version is required")
	}
	seen := make(map[EventType]EventDefinition, len(definitions))
	for _, definition := range definitions {
		if strings.TrimSpace(string(definition.Type)) == "" {
			return fmt.Errorf("event definition type is required")
		}
		if strings.TrimSpace(string(definition.Family)) == "" {
			return fmt.Errorf("%s requires event family", definition.Type)
		}
		if strings.TrimSpace(definition.IntroducedIn) == "" {
			return fmt.Errorf("%s requires introduced_in", definition.Type)
		}
		if definition.Compatibility != EventCompatibilityAdditive && definition.Compatibility != EventCompatibilityBreaking {
			return fmt.Errorf("%s has unknown compatibility %q", definition.Type, definition.Compatibility)
		}
		if definition.IntroducedIn == contractVersion && definition.Compatibility == EventCompatibilityBreaking {
			return fmt.Errorf("%s is a breaking event change and requires a contract version bump", definition.Type)
		}
		if prior, ok := seen[definition.Type]; ok {
			return fmt.Errorf("duplicate event definition %q in families %q and %q", definition.Type, prior.Family, definition.Family)
		}
		seen[definition.Type] = definition
	}
	for _, required := range requiredEventTypes {
		if _, ok := seen[required]; !ok {
			return fmt.Errorf("event %q is missing from event catalog", required)
		}
	}
	return nil
}

func ValidateEvent(eventType EventType, payload EventPayload) error {
	if strings.TrimSpace(string(eventType)) == "" {
		return fmt.Errorf("event type is required")
	}
	if _, ok := EventDefinitionFor(eventType); !ok {
		return fmt.Errorf("unknown event type %q", eventType)
	}
	if strings.TrimSpace(payload.ExpiresAt) != "" {
		if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(payload.ExpiresAt)); err != nil {
			return fmt.Errorf("%s has invalid payload.expires_at", eventType)
		}
	}
	if strings.TrimSpace(payload.StaleAt) != "" {
		if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(payload.StaleAt)); err != nil {
			return fmt.Errorf("%s has invalid payload.stale_at", eventType)
		}
	}
	if payload.TimeoutSeconds < 0 {
		return fmt.Errorf("%s has invalid payload.timeout_seconds", eventType)
	}
	switch eventType {
	case EventToolCall, EventToolResult:
		if strings.TrimSpace(payload.Tool) == "" {
			return fmt.Errorf("%s requires payload.tool", eventType)
		}
	case EventTodoUpdated, EventTranscriptEntry, EventArtifactReference, EventCompactBoundary, EventPromptContextSnapshot, EventHookRun, EventScheduleTriggered, EventSubagentStarted, EventSubagentCompleted:
		if !hasEventNarrative(payload) {
			return fmt.Errorf("%s requires payload.message, payload.action, payload.target, or payload.id", eventType)
		}
	}
	return nil
}

func hasEventNarrative(payload EventPayload) bool {
	return strings.TrimSpace(payload.Message) != "" ||
		strings.TrimSpace(payload.Action) != "" ||
		strings.TrimSpace(payload.Target) != "" ||
		strings.TrimSpace(payload.ID) != ""
}
