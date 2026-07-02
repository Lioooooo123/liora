package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func TestDoctorReportIncludesSchemaState(t *testing.T) {
	// Given
	reportContext := doctorReportContext{
		Schema: &store.SchemaReport{
			DBPath:          "/tmp/liora.db",
			State:           "ok",
			CurrentVersion:  store.CurrentSchemaVersion,
			MigrationStatus: "complete",
			Tables: []store.SchemaTableReport{
				{Name: "memories", Present: true, Retention: store.RetentionLongTerm, RetentionRationale: "user personalization state"},
				{Name: "artifact_refs", Present: true, Retention: store.RetentionReferenceOnly, RetentionRationale: "stores artifact paths and summaries"},
			},
		},
	}

	// When
	report, err := doctorReport(llm.Config{Provider: "gemini", Model: "gemini-test"}, reportContext)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"database: ok",
		fmt.Sprintf("schema_version: %d", store.CurrentSchemaVersion),
		"migration: complete",
		"table.memories: present retention=long_term reason=user personalization state",
		"table.artifact_refs: present retention=reference_only reason=stores artifact paths and summaries",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected doctor report to contain %q, got:\n%s", want, report)
		}
	}
}

func TestDoctorReportRedactsAPIKey(t *testing.T) {
	// Given
	config := llm.Config{
		Provider: "anthropic",
		APIKey:   "test-secret",
		Model:    "claude-test",
	}
	reportContext := doctorReportContext{
		Schema: &store.SchemaReport{
			State:           "ok",
			CurrentVersion:  store.CurrentSchemaVersion,
			MigrationStatus: "complete",
		},
	}

	// When
	report, err := doctorReport(config, reportContext)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "test-secret") {
		t.Fatalf("doctor report leaked API key:\n%s", report)
	}
	if !strings.Contains(report, "api_key: configured") {
		t.Fatalf("expected configured key status, got:\n%s", report)
	}
}

func TestDoctorReportIncludesSchemaRecoveryGuidance(t *testing.T) {
	report, err := doctorReport(llm.Config{Provider: "anthropic", Model: "claude-test"}, doctorReportContext{
		Schema: &store.SchemaReport{
			DBPath:            "/tmp/liora.db",
			State:             "error",
			CurrentVersion:    store.CurrentSchemaVersion,
			MigrationStatus:   "error",
			Recoverable:       true,
			Error:             "database open or migration failed; make a read-only backup of liora.db before repair or deletion",
			BackupPath:        "/tmp/liora.db.readonly-backup",
			ExportCommand:     "sqlite3 \"/tmp/liora.db\" .dump > \"/tmp/liora.db.dump.sql\"",
			SafeDeleteCommand: "mv \"/tmp/liora.db\" \"/tmp/liora.db.disabled-after-backup\"",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"database_recoverable: true",
		"database_error: database open or migration failed; make a read-only backup",
		"database_recovery.backup_path: /tmp/liora.db.readonly-backup",
		"database_recovery.export: sqlite3",
		"database_recovery.safe_delete: mv",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected doctor recovery report to contain %q, got:\n%s", want, report)
		}
	}
}

func TestDoctorReportRedactsBaseURLSecrets(t *testing.T) {
	// Given
	config := llm.Config{
		Provider: "openai-chat",
		BaseURL:  "https://user:pass@example.test/v1?token=query-secret#fragment-secret",
		APIKey:   "test-secret",
		Model:    "gpt-test",
	}

	// When
	report, err := doctorReport(config, doctorReportContext{})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user", "pass", "query-secret", "fragment-secret", "test-secret"} {
		if strings.Contains(report, secret) {
			t.Fatalf("doctor report leaked %q:\n%s", secret, report)
		}
	}
	if !strings.Contains(report, "base_url: https://example.test/v1") {
		t.Fatalf("expected sanitized base URL, got:\n%s", report)
	}
}

func TestDoctorReportIncludesProviderCapabilityAndThreadOverrides(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	thread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: "/repo", Title: "Strong"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(thread.ID, store.UpdateThreadModelConfigRequest{
		Provider: "openai-chat",
		Model:    "gpt-5",
		BaseURL:  "https://user:pass@llm.example.test/v1?token=secret#frag",
		Profile:  "strong",
	}); err != nil {
		t.Fatal(err)
	}
	report, err := doctorReport(llm.Config{
		Provider: "openai-chat",
		APIKey:   "doctor-secret",
		Model:    "gpt-5",
		Profile:  "default",
	}, doctorReportContext{Workspace: "/repo", Store: persistentStore})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"default_model: gpt-5",
		"profile: default",
		"credential.api_key: configured redacted=true",
		"capability.native_tool_use: true",
		"capability.json_schema: true",
		"thread_model_overrides:",
		"thread_model." + thread.ID + ": openai-chat/gpt-5 profile=strong base_url=https://llm.example.test/v1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected doctor report to contain %q, got:\n%s", want, report)
		}
	}
	for _, secret := range []string{"doctor-secret", "user", "pass", "token=secret", "frag"} {
		if strings.Contains(report, secret) {
			t.Fatalf("doctor report leaked %q:\n%s", secret, report)
		}
	}
}

func TestDoctorReportIncludesRuntimeMCPAndAutomationStatus(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	disabled := false
	if err := persistentStore.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"disabled": {
			Command:     "node",
			Args:        []string{"disabled.js", "--token", "disabled-secret"},
			Enabled:     &disabled,
			Source:      "workspace",
			Version:     "0.9.0",
			Permissions: []string{"network:api.example.test"},
		},
		"fake": {
			Command:     "python3",
			Args:        []string{"server.py", "--token", "mcp-secret-arg"},
			Env:         map[string]string{"API_KEY": "mcp-secret-env"},
			Source:      "global",
			Version:     "1.2.3",
			Permissions: []string{"filesystem:read", "network:api.example.test"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedules (id, trigger, prompt, enabled) VALUES ('nightly', '0 2 * * *', 'run audit', 1), ('paused', '0 3 * * *', 'paused audit', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hooks (id, event, command, enabled) VALUES ('pre-tool', 'PreToolUse', 'echo ok', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := doctorReport(llm.Config{Provider: "openai-chat", Model: "gpt-test"}, doctorReportContext{
		Store: persistentStore,
		Runtime: &doctorRuntimeStatus{
			DaemonAuth:         "capability-token",
			Sandbox:            "docker",
			PatchMode:          true,
			PermissionMode:     "prompt",
			NetworkDefaultDeny: true,
			NetworkAllowlist:   []string{"api.example.test", "registry.example.test"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"daemon_auth: capability-token",
		"sandbox: docker",
		"patch_mode: true",
		"permission.mode: prompt",
		"network.default: deny",
		"network.allowlist: api.example.test,registry.example.test",
		"mcp_status: configured servers=2 enabled=1 disabled=1",
		"mcp.server.disabled: disabled command=configured args=3 env=redacted(0) permissions=network:api.example.test source=workspace version=0.9.0 tool_count=unknown auth=not_probed",
		"mcp.server.fake: enabled command=configured args=3 env=redacted(1) permissions=filesystem:read,network:api.example.test source=global version=1.2.3 tool_count=unknown auth=not_probed",
		"schedule_status: configured total=2 enabled=1 disabled=1",
		"hook_status: configured total=1 enabled=1 disabled=0",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected doctor report to contain %q, got:\n%s", want, report)
		}
	}
	for _, secret := range []string{"mcp-secret-arg", "mcp-secret-env", "disabled-secret", "--token", "API_KEY"} {
		if strings.Contains(report, secret) {
			t.Fatalf("doctor report leaked %q:\n%s", secret, report)
		}
	}
}
