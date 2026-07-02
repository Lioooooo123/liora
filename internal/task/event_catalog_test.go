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
