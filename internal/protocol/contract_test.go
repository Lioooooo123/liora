package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/task"
)

func TestDaemonEventFixture_matches_testdata(t *testing.T) {
	var got bytes.Buffer

	err := WriteDaemonEventFixture(&got)

	if err != nil {
		t.Fatalf("write daemon event fixture: %v", err)
	}
	want, err := os.ReadFile("testdata/daemon-event-stream.json")
	if err != nil {
		t.Fatalf("read daemon event fixture testdata: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got.Bytes())) {
		t.Fatalf("generated fixture differs from testdata\nwant: %s\ngot:  %s", string(want), got.String())
	}
}

func TestDaemonEventFixture_covers_single_and_multi_task_streams(t *testing.T) {
	fixture := DaemonEventFixture()

	if fixture.Version != EventContractVersion {
		t.Fatalf("unexpected fixture version %q", fixture.Version)
	}
	if len(fixture.SingleTaskStream) == 0 {
		t.Fatal("expected single-task stream frames")
	}
	if len(fixture.MultiTaskStream) == 0 {
		t.Fatal("expected multi-task stream frames")
	}
	if fixture.SingleTaskStream[0].Data.Message != "created" {
		t.Fatalf("unexpected single task payload %#v", fixture.SingleTaskStream[0].Data)
	}
	if fixture.SingleTaskStream[0].Data.Origin != "background" || fixture.SingleTaskStream[0].Data.Kind != "background" {
		t.Fatalf("expected automation metadata on task.created, got %#v", fixture.SingleTaskStream[0].Data)
	}
	if fixture.MultiTaskStream[0].Data.TaskID != "task-002" {
		t.Fatalf("unexpected multi task id %q", fixture.MultiTaskStream[0].Data.TaskID)
	}
	if fixture.MultiTaskStream[1].Data.Payload.Origin != "schedule" || fixture.MultiTaskStream[1].Data.Payload.Trigger != "0 2 * * *" {
		t.Fatalf("expected automation metadata on permission payload, got %#v", fixture.MultiTaskStream[1].Data.Payload)
	}
}

func TestDaemonEventFixture_covers_contract_version_thread_stream_and_errors(t *testing.T) {
	var decoded map[string]any
	payload, err := json.Marshal(DaemonEventFixture())
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if decoded["contract_version"] != EventContractVersion {
		t.Fatalf("expected contract_version %q, got %#v", EventContractVersion, decoded["contract_version"])
	}
	if _, ok := decoded["multi_thread_stream"].([]any); !ok {
		t.Fatalf("expected multi_thread_stream array, got %#v", decoded["multi_thread_stream"])
	}
	errorResponse, ok := decoded["error_response"].(map[string]any)
	if !ok {
		t.Fatalf("expected error_response object, got %#v", decoded["error_response"])
	}
	if errorResponse["status"] != float64(404) || errorResponse["error"] != "task not found" {
		t.Fatalf("unexpected error response %#v", errorResponse)
	}
}

func TestDaemonEventCatalogFixture_matches_testdata_and_event_catalog(t *testing.T) {
	var got bytes.Buffer

	err := WriteDaemonEventCatalogFixture(&got)

	if err != nil {
		t.Fatalf("write daemon event catalog fixture: %v", err)
	}
	want, err := os.ReadFile("testdata/daemon-event-catalog.json")
	if err != nil {
		t.Fatalf("read daemon event catalog fixture testdata: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got.Bytes())) {
		t.Fatalf("generated catalog fixture differs from testdata\nwant: %s\ngot:  %s", string(want), got.String())
	}

	fixture := DaemonEventCatalogFixture()
	if fixture.Version != EventContractVersion || fixture.ContractVersion != EventContractVersion {
		t.Fatalf("unexpected catalog fixture version %#v", fixture)
	}
	definitions := taskEventDefinitionTypes()
	if len(fixture.Events) != len(definitions) {
		t.Fatalf("expected %d catalog events, got %d", len(definitions), len(fixture.Events))
	}
	for _, frame := range fixture.Events {
		if !definitions[frame.Event] {
			t.Fatalf("fixture event %q is not in task.EventDefinitions", frame.Event)
		}
	}
}

func TestDaemonEventCatalogCoverageRejectsMissingEvent(t *testing.T) {
	fixture := DaemonEventCatalogFixture()
	if len(fixture.Events) == 0 {
		t.Fatal("expected catalog fixture events")
	}
	fixture.Events = fixture.Events[1:]

	err := validateCatalogFixtureCoverage(fixture)

	if err == nil || !strings.Contains(err.Error(), "missing catalog fixture event") {
		t.Fatalf("expected missing coverage error, got %v", err)
	}
}

func TestParseDaemonEventFixture_rejects_missing_contract_version(t *testing.T) {
	decoded := readFixtureMap(t)
	delete(decoded, "contract_version")
	malformed, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal malformed fixture: %v", err)
	}

	if _, err := ParseDaemonEventFixture(bytes.NewReader(malformed)); err == nil {
		t.Fatal("expected missing contract_version to be rejected")
	}
}

func TestParseDaemonEventFixture_rejects_unsupported_contract_version(t *testing.T) {
	decoded := readFixtureMap(t)
	decoded["contract_version"] = "2099-01-01.task-events.v9"
	malformed, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal malformed fixture: %v", err)
	}

	if _, err := ParseDaemonEventFixture(bytes.NewReader(malformed)); err == nil {
		t.Fatal("expected unsupported contract_version to be rejected")
	}
}

func TestParseDaemonEventFixture_rejects_malformed_frames_and_envelopes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "blank single task event",
			mutate: func(decoded map[string]any) {
				singleTaskFrame(decoded, 0)["event"] = " "
			},
		},
		{
			name: "blank single task id",
			mutate: func(decoded map[string]any) {
				singleTaskFrame(decoded, 0)["id"] = ""
			},
		},
		{
			name: "missing single task data",
			mutate: func(decoded map[string]any) {
				delete(singleTaskFrame(decoded, 0), "data")
			},
		},
		{
			name: "missing task envelope data",
			mutate: func(decoded map[string]any) {
				delete(multiTaskFrame(decoded, 0), "data")
			},
		},
		{
			name: "blank task envelope task id",
			mutate: func(decoded map[string]any) {
				taskEnvelope(decoded, 0)["task_id"] = " "
			},
		},
		{
			name: "missing task envelope payload",
			mutate: func(decoded map[string]any) {
				delete(taskEnvelope(decoded, 0), "payload")
			},
		},
		{
			name: "blank thread envelope thread id",
			mutate: func(decoded map[string]any) {
				threadEnvelope(decoded, 0)["thread_id"] = ""
			},
		},
		{
			name: "blank thread envelope task id",
			mutate: func(decoded map[string]any) {
				threadEnvelope(decoded, 0)["task_id"] = " "
			},
		},
		{
			name: "missing thread envelope payload",
			mutate: func(decoded map[string]any) {
				delete(threadEnvelope(decoded, 0), "payload")
			},
		},
		{
			name: "missing error response",
			mutate: func(decoded map[string]any) {
				delete(decoded, "error_response")
			},
		},
		{
			name: "blank error response",
			mutate: func(decoded map[string]any) {
				errorResponse(decoded)["error"] = ""
			},
		},
		{
			name: "zero error status",
			mutate: func(decoded map[string]any) {
				errorResponse(decoded)["status"] = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded := readFixtureMap(t)
			tt.mutate(decoded)
			malformed, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("marshal malformed fixture: %v", err)
			}

			if _, err := ParseDaemonEventFixture(bytes.NewReader(malformed)); err == nil {
				t.Fatal("expected malformed fixture to be rejected")
			}
		})
	}
}

func taskEventDefinitionTypes() map[string]bool {
	definitions := make(map[string]bool)
	for _, definition := range task.EventDefinitions() {
		definitions[string(definition.Type)] = true
	}
	return definitions
}

func validateCatalogFixtureCoverage(fixture EventCatalogFixture) error {
	seen := make(map[string]bool, len(fixture.Events))
	for _, frame := range fixture.Events {
		if strings.TrimSpace(frame.Event) == "" {
			return fmt.Errorf("catalog fixture event is required")
		}
		if err := task.ValidateEvent(task.EventType(frame.Event), frame.Data); err != nil {
			return err
		}
		seen[frame.Event] = true
	}
	for _, definition := range task.EventDefinitions() {
		if !seen[string(definition.Type)] {
			return fmt.Errorf("missing catalog fixture event %q", definition.Type)
		}
	}
	return nil
}

func readFixtureMap(t *testing.T) map[string]any {
	t.Helper()

	payload, err := os.ReadFile("testdata/daemon-event-stream.json")
	if err != nil {
		t.Fatalf("read daemon event fixture testdata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return decoded
}

func singleTaskFrame(decoded map[string]any, index int) map[string]any {
	return frameFromStream(decoded, "single_task_stream", index)
}

func multiTaskFrame(decoded map[string]any, index int) map[string]any {
	return frameFromStream(decoded, "multi_task_stream", index)
}

func multiThreadFrame(decoded map[string]any, index int) map[string]any {
	return frameFromStream(decoded, "multi_thread_stream", index)
}

func taskEnvelope(decoded map[string]any, index int) map[string]any {
	return objectAt(multiTaskFrame(decoded, index), "data")
}

func threadEnvelope(decoded map[string]any, index int) map[string]any {
	return objectAt(multiThreadFrame(decoded, index), "data")
}

func errorResponse(decoded map[string]any) map[string]any {
	return objectAt(decoded, "error_response")
}

func frameFromStream(decoded map[string]any, stream string, index int) map[string]any {
	return arrayAt(decoded, stream)[index].(map[string]any)
}

func arrayAt(decoded map[string]any, key string) []any {
	return decoded[key].([]any)
}

func objectAt(decoded map[string]any, key string) map[string]any {
	return decoded[key].(map[string]any)
}
