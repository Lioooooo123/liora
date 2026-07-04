package task

import (
	"strings"
	"testing"
)

func TestEventCatalogMetadataTracksContractVersion(t *testing.T) {
	if EventContractVersion == "" {
		t.Fatal("event contract version must be set")
	}
	if err := ValidateEventCatalogCompatibility(); err != nil {
		t.Fatalf("event catalog compatibility: %v", err)
	}

	definitions := EventDefinitions()
	if len(definitions) != len(requiredEventTypes) {
		t.Fatalf("expected %d event definitions, got %d", len(requiredEventTypes), len(definitions))
	}
	for _, definition := range definitions {
		if definition.IntroducedIn != EventContractVersion {
			t.Fatalf("expected %s introduced in %s, got %q", definition.Type, EventContractVersion, definition.IntroducedIn)
		}
		if definition.Compatibility != EventCompatibilityAdditive {
			t.Fatalf("expected %s to be additive, got %q", definition.Type, definition.Compatibility)
		}
	}
}

func TestEventCatalogCompatibilityRejectsBreakingChangeWithoutVersionBump(t *testing.T) {
	definitions := EventDefinitions()
	definitions[0].Compatibility = EventCompatibilityBreaking

	err := validateEventCatalogCompatibility(definitions, EventContractVersion)

	if err == nil || !strings.Contains(err.Error(), "requires a contract version bump") {
		t.Fatalf("expected contract version bump error, got %v", err)
	}
}

func TestEventCatalogCompatibilityRejectsRemovedEvent(t *testing.T) {
	definitions := EventDefinitions()[1:]

	err := validateEventCatalogCompatibility(definitions, EventContractVersion)

	if err == nil || !strings.Contains(err.Error(), "missing from event catalog") {
		t.Fatalf("expected missing event error, got %v", err)
	}
}

func TestEventCatalogDefinesToolLifecycle(t *testing.T) {
	definition, ok := EventDefinitionFor(EventToolLifecycle)
	if !ok {
		t.Fatalf("expected %s in event catalog", EventToolLifecycle)
	}
	if definition.Family != EventFamilyTool {
		t.Fatalf("expected %s family %q, got %q", EventToolLifecycle, EventFamilyTool, definition.Family)
	}
	if err := ValidateEvent(EventToolLifecycle, EventPayload{
		Tool:           "read",
		Phase:          "prepare",
		ToolCallID:     "call_1",
		AccessMode:     "read",
		AccessResource: "path",
		AccessArgument: "README.md",
		BatchID:        "batch-1",
		BatchSize:      2,
	}); err != nil {
		t.Fatalf("expected valid tool lifecycle event: %v", err)
	}
}

func TestEventCatalogRejectsMalformedToolLifecycle(t *testing.T) {
	cases := []struct {
		name    string
		payload EventPayload
		want    string
	}{
		{name: "missing tool", payload: EventPayload{Phase: "prepare"}, want: "payload.tool"},
		{name: "missing phase", payload: EventPayload{Tool: "read"}, want: "payload.phase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEvent(EventToolLifecycle, tc.payload)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestEventCatalogAcceptsWhitespaceAssistantDelta(t *testing.T) {
	for _, message := range []string{" ", "\n", "\n\n"} {
		t.Run(strings.ReplaceAll(message, "\n", "\\n"), func(t *testing.T) {
			if err := ValidateEvent(EventAssistantDelta, EventPayload{Message: message}); err != nil {
				t.Fatalf("expected whitespace assistant delta to stay valid for markdown streaming, got %v", err)
			}
		})
	}
}

func TestEventCatalogRejectsEmptyAssistantDelta(t *testing.T) {
	err := ValidateEvent(EventAssistantDelta, EventPayload{})

	if err == nil || !strings.Contains(err.Error(), "payload.message") {
		t.Fatalf("expected empty assistant delta to require payload.message, got %v", err)
	}
}
