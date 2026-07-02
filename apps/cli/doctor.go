package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func printDoctor(config llm.Config, reportContext doctorReportContext) error {
	if reportContext.Store != nil && reportContext.Schema == nil {
		reportContext.Schema = loadSchemaReport(reportContext.Store)
	}
	report, err := doctorReport(config, reportContext)
	if err != nil {
		return err
	}
	fmt.Println(report)
	return nil
}

type doctorReportContext struct {
	Workspace string
	Core      string
	Safety    string
	Schema    *store.SchemaReport
	Store     *store.Store
	Runtime   *doctorRuntimeStatus
}

type doctorRuntimeStatus struct {
	DaemonAuth         string
	Sandbox            string
	PatchMode          bool
	PermissionMode     string
	NetworkDefaultDeny bool
	NetworkAllowlist   []string
}

func doctorReport(config llm.Config, reportContext doctorReportContext) (string, error) {
	resolved, err := llm.ResolveConfig(config)
	if err != nil {
		return "", err
	}
	keyStatus := "missing"
	if strings.TrimSpace(resolved.APIKey) != "" {
		keyStatus = "configured"
	}
	capability := resolved.Capability
	toolsStatus := "unsupported"
	if capability.NativeToolUse {
		toolsStatus = "supported"
	}
	lines := []string{"Liora doctor"}
	if strings.TrimSpace(reportContext.Workspace) != "" {
		lines = append(lines, "workspace: "+reportContext.Workspace)
	}
	if strings.TrimSpace(reportContext.Core) != "" {
		lines = append(lines, "core: "+reportContext.Core)
	}
	if strings.TrimSpace(reportContext.Safety) != "" {
		lines = append(lines, "safety: "+reportContext.Safety)
	}
	lines = append(lines,
		"provider: "+resolved.Provider,
		"display: "+llm.ProviderDisplayName(resolved.Provider),
		"model: "+resolved.Model,
		"default_model: "+resolved.Model,
		"profile: "+emptyDiagnosticValue(resolved.Profile),
		"base_url: "+redactDiagnosticURL(resolved.BaseURL),
		"api_key: "+keyStatus,
		"credential.api_key: "+keyStatus+" redacted=true",
		"tools: "+toolsStatus,
	)
	lines = append(lines, renderModelCapability(capability)...)
	lines = append(lines, renderRuntimeStatus(reportContext)...)
	lines = append(lines, renderMCPStatus(reportContext)...)
	lines = append(lines, renderAutomationStatus(reportContext)...)
	lines = append(lines, renderThreadModelOverrides(reportContext)...)
	if reportContext.Schema != nil {
		lines = append(lines, renderSchemaReport(*reportContext.Schema)...)
	}
	return strings.Join(lines, "\n"), nil
}

func renderModelCapability(capability llm.ModelCapability) []string {
	return []string{
		fmt.Sprintf("capability.native_tool_use: %t", capability.NativeToolUse),
		fmt.Sprintf("capability.streaming: %t", capability.Streaming),
		fmt.Sprintf("capability.vision: %t", capability.Vision),
		fmt.Sprintf("capability.long_context: %t", capability.LongContext),
		fmt.Sprintf("capability.json_schema: %t", capability.JSONSchema),
		fmt.Sprintf("capability.max_output_tokens: %d", capability.MaxOutputTokens),
	}
}

func renderRuntimeStatus(reportContext doctorReportContext) []string {
	if reportContext.Runtime == nil {
		return nil
	}
	runtimeStatus := reportContext.Runtime
	networkDefault := "allow"
	if runtimeStatus.NetworkDefaultDeny {
		networkDefault = "deny"
	}
	allowlist := "none"
	if len(runtimeStatus.NetworkAllowlist) > 0 {
		allowlist = strings.Join(runtimeStatus.NetworkAllowlist, ",")
	}
	return []string{
		"daemon_auth: " + emptyDiagnosticValue(runtimeStatus.DaemonAuth),
		"sandbox: " + emptyDiagnosticValue(runtimeStatus.Sandbox),
		fmt.Sprintf("patch_mode: %t", runtimeStatus.PatchMode),
		"permission.mode: " + emptyDiagnosticValue(runtimeStatus.PermissionMode),
		"network.default: " + networkDefault,
		"network.allowlist: " + allowlist,
	}
}

func renderMCPStatus(reportContext doctorReportContext) []string {
	if reportContext.Store == nil {
		return nil
	}
	config, err := reportContext.Store.LoadMCPConfig()
	if err != nil {
		return []string{"mcp_status: unavailable"}
	}
	if len(config.Servers) == 0 {
		return []string{"mcp_status: none servers=0"}
	}
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{fmt.Sprintf("mcp_status: configured servers=%d", len(names))}
	for _, name := range names {
		server := config.Servers[name]
		commandStatus := "missing"
		if strings.TrimSpace(server.Command) != "" {
			commandStatus = "configured"
		}
		lines = append(lines, fmt.Sprintf("mcp.server.%s: configured command=%s args=%d env=redacted(%d) tool_count=unknown auth=not_probed", name, commandStatus, len(server.Args), len(server.Env)))
	}
	return lines
}

type automationConfigSummary struct {
	Total    int
	Enabled  int
	Disabled int
}

func renderAutomationStatus(reportContext doctorReportContext) []string {
	if reportContext.Store == nil {
		return nil
	}
	schedules, scheduleErr := automationConfigSummaryFor(reportContext.Store, "schedules")
	hooks, hookErr := automationConfigSummaryFor(reportContext.Store, "hooks")
	lines := make([]string, 0, 2)
	if scheduleErr != nil {
		lines = append(lines, "schedule_status: unavailable")
	} else {
		lines = append(lines, automationStatusLine("schedule", schedules))
	}
	if hookErr != nil {
		lines = append(lines, "hook_status: unavailable")
	} else {
		lines = append(lines, automationStatusLine("hook", hooks))
	}
	return lines
}

func automationConfigSummaryFor(persistentStore *store.Store, table string) (automationConfigSummary, error) {
	db, err := persistentStore.OpenDB()
	if err != nil {
		return automationConfigSummary{}, err
	}
	defer db.Close()
	var summary automationConfigSummary
	if err := db.QueryRow("SELECT COUNT(*), COALESCE(SUM(CASE WHEN enabled <> 0 THEN 1 ELSE 0 END), 0) FROM "+table).Scan(&summary.Total, &summary.Enabled); err != nil {
		return automationConfigSummary{}, err
	}
	summary.Disabled = summary.Total - summary.Enabled
	return summary, nil
}

func automationStatusLine(label string, summary automationConfigSummary) string {
	state := "none"
	if summary.Total > 0 {
		state = "configured"
	}
	return fmt.Sprintf("%s_status: %s total=%d enabled=%d disabled=%d", label, state, summary.Total, summary.Enabled, summary.Disabled)
}

func renderThreadModelOverrides(reportContext doctorReportContext) []string {
	if reportContext.Store == nil || strings.TrimSpace(reportContext.Workspace) == "" {
		return nil
	}
	threads, err := reportContext.Store.ListConversationThreadsWithOptions(store.ConversationThreadListOptions{
		Workspace: reportContext.Workspace,
		Limit:     20,
	})
	if err != nil {
		return []string{"thread_model_overrides: unavailable"}
	}
	lines := []string{"thread_model_overrides: none"}
	for _, thread := range threads {
		if thread.ModelConfig == nil {
			continue
		}
		if len(lines) == 1 && lines[0] == "thread_model_overrides: none" {
			lines[0] = "thread_model_overrides:"
		}
		config := thread.ModelConfig
		line := "thread_model." + thread.ID + ": " + strings.Trim(strings.TrimSpace(config.Provider)+"/"+strings.TrimSpace(config.Model), "/")
		if strings.TrimSpace(config.Profile) != "" {
			line += " profile=" + config.Profile
		}
		if strings.TrimSpace(config.BaseURL) != "" {
			line += " base_url=" + redactDiagnosticURL(config.BaseURL)
		}
		if strings.TrimSpace(config.InheritedFromThreadID) != "" {
			line += " inherits=" + config.InheritedFromThreadID
		}
		lines = append(lines, line)
	}
	return lines
}

func loadSchemaReport(persistentStore *store.Store) *store.SchemaReport {
	if persistentStore == nil {
		return nil
	}
	report, err := persistentStore.SchemaReport()
	if err != nil {
		return &store.SchemaReport{
			State:           "error",
			MigrationStatus: "error",
			Recoverable:     true,
			Error:           "schema status unavailable",
		}
	}
	return &report
}

func renderSchemaReport(report store.SchemaReport) []string {
	lines := []string{
		"database: " + report.State,
	}
	if strings.TrimSpace(report.DBPath) != "" {
		lines = append(lines, "database_path: "+report.DBPath)
	}
	lines = append(lines,
		fmt.Sprintf("schema_version: %d", report.CurrentVersion),
		"migration: "+report.MigrationStatus,
	)
	if report.Recoverable {
		lines = append(lines, "database_recoverable: true")
	}
	if strings.TrimSpace(report.Error) != "" {
		lines = append(lines, "database_error: "+report.Error)
	}
	if strings.TrimSpace(report.BackupPath) != "" {
		lines = append(lines, "database_recovery.backup_path: "+report.BackupPath)
	}
	if strings.TrimSpace(report.ExportCommand) != "" {
		lines = append(lines, "database_recovery.export: "+report.ExportCommand)
	}
	if strings.TrimSpace(report.SafeDeleteCommand) != "" {
		lines = append(lines, "database_recovery.safe_delete: "+report.SafeDeleteCommand)
	}
	for _, table := range report.Tables {
		status := "missing"
		if table.Present {
			status = "present"
		}
		line := "table." + table.Name + ": " + status
		if strings.TrimSpace(string(table.Retention)) != "" {
			line += " retention=" + string(table.Retention)
		}
		if strings.TrimSpace(table.RetentionRationale) != "" {
			line += " reason=" + table.RetentionRationale
		}
		lines = append(lines, line)
	}
	return lines
}

func redactDiagnosticURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func emptyDiagnosticValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
