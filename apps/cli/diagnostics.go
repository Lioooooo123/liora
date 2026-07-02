package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

type diagnosticsReport struct {
	GeneratedAt string                  `json:"generated_at"`
	Metadata    diagnosticsMetadata     `json:"metadata"`
	Schema      diagnosticsSchemaStatus `json:"schema_status"`
	Events      diagnosticsEventSummary `json:"event_summary"`
	Logs        diagnosticsLogSummary   `json:"log_summary"`
}

type diagnosticsMetadata struct {
	Workspace           string   `json:"workspace"`
	Provider            string   `json:"provider"`
	Model               string   `json:"model"`
	Profile             string   `json:"profile,omitempty"`
	BaseURL             string   `json:"base_url,omitempty"`
	APIKey              string   `json:"api_key"`
	DaemonAuth          string   `json:"daemon_auth,omitempty"`
	Sandbox             string   `json:"sandbox,omitempty"`
	PatchMode           bool     `json:"patch_mode"`
	PermissionMode      string   `json:"permission_mode,omitempty"`
	NetworkDefault      string   `json:"network_default,omitempty"`
	NetworkAllowlist    []string `json:"network_allowlist,omitempty"`
	MCPServerCount      int      `json:"mcp_server_count"`
	ScheduleTotal       int      `json:"schedule_total"`
	ScheduleEnabled     int      `json:"schedule_enabled"`
	HookTotal           int      `json:"hook_total"`
	HookEnabled         int      `json:"hook_enabled"`
	RawPayloadsExported bool     `json:"raw_payloads_exported"`
}

type diagnosticsSchemaStatus struct {
	State           string                   `json:"state"`
	CurrentVersion  int                      `json:"current_version"`
	MigrationStatus string                   `json:"migration_status"`
	Recoverable     bool                     `json:"recoverable,omitempty"`
	Tables          []diagnosticsSchemaTable `json:"tables,omitempty"`
}

type diagnosticsSchemaTable struct {
	Name      string `json:"name"`
	Present   bool   `json:"present"`
	Retention string `json:"retention,omitempty"`
}

type diagnosticsEventSummary struct {
	Total                  int            `json:"total"`
	ByType                 map[string]int `json:"by_type,omitempty"`
	ByStatus               map[string]int `json:"by_status,omitempty"`
	ErrorEvents            int            `json:"error_events"`
	MalformedPayloadEvents int            `json:"malformed_payload_events"`
	LastEventAt            string         `json:"last_event_at,omitempty"`
	Unavailable            bool           `json:"unavailable,omitempty"`
}

type diagnosticsLogSummary struct {
	Source          string `json:"source"`
	RawLogsExported bool   `json:"raw_logs_exported"`
	ErrorEvents     int    `json:"error_events"`
	Note            string `json:"note"`
}

func writeDiagnostics(path string, config llm.Config, reportContext doctorReportContext) error {
	report, err := buildDiagnosticsReport(config, reportContext, time.Now().UTC())
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if path == "-" {
		_, err = os.Stdout.Write(payload)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func buildDiagnosticsReport(config llm.Config, reportContext doctorReportContext, now time.Time) (diagnosticsReport, error) {
	resolved, err := llm.ResolveConfig(config)
	if err != nil {
		return diagnosticsReport{}, err
	}
	schemaReport := reportContext.Schema
	if schemaReport == nil && reportContext.Store != nil {
		schemaReport = loadSchemaReport(reportContext.Store)
	}
	metadata := diagnosticsMetadata{
		Workspace:           redactedPresence(reportContext.Workspace),
		Provider:            resolved.Provider,
		Model:               resolved.Model,
		Profile:             emptyOmit(resolved.Profile),
		BaseURL:             redactDiagnosticURL(resolved.BaseURL),
		APIKey:              credentialState(resolved.APIKey),
		RawPayloadsExported: false,
	}
	if reportContext.Runtime != nil {
		runtimeStatus := reportContext.Runtime
		metadata.DaemonAuth = emptyOmit(runtimeStatus.DaemonAuth)
		metadata.Sandbox = emptyOmit(runtimeStatus.Sandbox)
		metadata.PatchMode = runtimeStatus.PatchMode
		metadata.PermissionMode = emptyOmit(runtimeStatus.PermissionMode)
		if runtimeStatus.NetworkDefaultDeny {
			metadata.NetworkDefault = "deny"
		} else {
			metadata.NetworkDefault = "allow"
		}
		metadata.NetworkAllowlist = append([]string(nil), runtimeStatus.NetworkAllowlist...)
		sort.Strings(metadata.NetworkAllowlist)
	}
	if reportContext.Store != nil {
		if mcpConfig, err := reportContext.Store.LoadMCPConfig(); err == nil {
			metadata.MCPServerCount = len(mcpConfig.Servers)
		}
		if schedules, err := automationConfigSummaryFor(reportContext.Store, "schedules"); err == nil {
			metadata.ScheduleTotal = schedules.Total
			metadata.ScheduleEnabled = schedules.Enabled
		}
		if hooks, err := automationConfigSummaryFor(reportContext.Store, "hooks"); err == nil {
			metadata.HookTotal = hooks.Total
			metadata.HookEnabled = hooks.Enabled
		}
	}
	events := diagnosticsEventSummary{ByType: map[string]int{}, ByStatus: map[string]int{}}
	if reportContext.Store != nil {
		var eventErr error
		events, eventErr = collectDiagnosticsEventSummary(reportContext.Store)
		if eventErr != nil {
			events = diagnosticsEventSummary{Unavailable: true}
		}
	}
	return diagnosticsReport{
		GeneratedAt: now.Format(time.RFC3339),
		Metadata:    metadata,
		Schema:      diagnosticsSchemaFromReport(schemaReport),
		Events:      events,
		Logs: diagnosticsLogSummary{
			Source:          "task_events",
			RawLogsExported: false,
			ErrorEvents:     events.ErrorEvents,
			Note:            "raw payloads, transcripts, tool output, logs, credential values, cookies, API keys, and PII are omitted",
		},
	}, nil
}

func diagnosticsSchemaFromReport(report *store.SchemaReport) diagnosticsSchemaStatus {
	if report == nil {
		return diagnosticsSchemaStatus{State: "unknown"}
	}
	status := diagnosticsSchemaStatus{
		State:           report.State,
		CurrentVersion:  report.CurrentVersion,
		MigrationStatus: report.MigrationStatus,
		Recoverable:     report.Recoverable,
	}
	for _, table := range report.Tables {
		status.Tables = append(status.Tables, diagnosticsSchemaTable{
			Name:      table.Name,
			Present:   table.Present,
			Retention: string(table.Retention),
		})
	}
	return status
}

func collectDiagnosticsEventSummary(persistentStore *store.Store) (diagnosticsEventSummary, error) {
	db, err := persistentStore.OpenDB()
	if err != nil {
		return diagnosticsEventSummary{}, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, payload_json, created_at FROM task_events ORDER BY created_at ASC`)
	if err != nil {
		return diagnosticsEventSummary{}, err
	}
	defer rows.Close()
	summary := diagnosticsEventSummary{ByType: map[string]int{}, ByStatus: map[string]int{}}
	for rows.Next() {
		var eventType string
		var payloadJSON string
		var createdAt string
		if err := rows.Scan(&eventType, &payloadJSON, &createdAt); err != nil {
			return diagnosticsEventSummary{}, err
		}
		summary.Total++
		summary.ByType[eventType]++
		if strings.TrimSpace(createdAt) != "" {
			summary.LastEventAt = createdAt
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			summary.MalformedPayloadEvents++
			continue
		}
		status := strings.TrimSpace(payload.Status)
		if status != "" {
			summary.ByStatus[status]++
		}
		if eventType == "task.error" || status == "error" || status == "failed" {
			summary.ErrorEvents++
		}
	}
	if err := rows.Err(); err != nil {
		return diagnosticsEventSummary{}, err
	}
	return summary, nil
}

func credentialState(value string) string {
	if strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "configured:redacted"
}

func redactedPresence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return "configured"
}

func emptyOmit(value string) string {
	return strings.TrimSpace(value)
}
