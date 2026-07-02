package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func TestDiagnosticsReportExportsOnlyRedactedMetadataAndSummaries(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	if err := persistentStore.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"secret-server": {
			Command: "python3",
			Args:    []string{"server.py", "--token", "mcp-secret-arg"},
			Env:     map[string]string{"API_KEY": "mcp-secret-env"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedules (id, trigger, prompt, enabled) VALUES ('nightly-secret', '0 2 * * *', 'email ada@example.com', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hooks (id, event, command, enabled) VALUES ('hook-secret', 'PostToolUse', 'curl https://example.test?token=hook-secret', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_events (id, task_id, type, payload_json, created_at) VALUES
		('event_1', 'task_1', 'tool.result', '{"status":"failed","output":"api_key=sk-live-secret and email ada@example.com cookie=session-secret"}', '2026-07-01T00:00:00Z'),
		('event_2', 'task_1', 'task.error', '{"status":"failed","message":"bearer secret-token"}', '2026-07-01T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := buildDiagnosticsReport(llm.Config{
		Provider: "openai-chat",
		BaseURL:  "https://user:pass@example.test/v1?token=query-secret#fragment-secret",
		APIKey:   "doctor-secret",
		Model:    "gpt-test",
	}, doctorReportContext{
		Workspace: "/Users/ada/private-repo",
		Store:     persistentStore,
		Runtime: &doctorRuntimeStatus{
			DaemonAuth:         "capability-token",
			Sandbox:            "local",
			PatchMode:          true,
			PermissionMode:     "prompt",
			NetworkDefaultDeny: true,
			NetworkAllowlist:   []string{"api.example.test"},
		},
	}, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(payload)
	for _, want := range []string{
		`"workspace":"configured"`,
		`"api_key":"configured:redacted"`,
		`"raw_payloads_exported":false`,
		`"mcp_server_count":1`,
		`"schedule_total":1`,
		`"hook_total":1`,
		`"task.error":1`,
		`"tool.result":1`,
		`"failed":2`,
		`"raw_logs_exported":false`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected diagnostics payload to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, forbidden := range []string{
		"doctor-secret",
		"mcp-secret-arg",
		"mcp-secret-env",
		"secret-server",
		"nightly-secret",
		"hook-secret",
		"sk-live-secret",
		"ada@example.com",
		"session-secret",
		"secret-token",
		"query-secret",
		"fragment-secret",
		"/Users/ada/private-repo",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("diagnostics payload leaked %q:\n%s", forbidden, rendered)
		}
	}
}

func TestDiagnosticsReportHandlesEmptyAndMalformedEventPayloads(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_events (id, task_id, type, payload_json, created_at) VALUES
		('event_bad', 'task_1', 'tool.result', '{not-json', '2026-07-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := buildDiagnosticsReport(llm.Config{Provider: "gemini", Model: "gemini-test"}, doctorReportContext{Store: persistentStore}, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Events.Total != 1 || report.Events.MalformedPayloadEvents != 1 {
		t.Fatalf("expected malformed event to be counted without raw payload export, got %#v", report.Events)
	}
	if report.Metadata.APIKey != "missing" || report.Schema.State != "ok" {
		t.Fatalf("expected missing key and ok schema, got metadata=%#v schema=%#v", report.Metadata, report.Schema)
	}
}
