package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreOpenDBConfiguresSQLiteBusyTimeout(t *testing.T) {
	db, err := New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 10000 {
		t.Fatalf("expected busy_timeout=10000, got %d", timeout)
	}
}

func TestStorePersistsGoal(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	if err := s.SetGoal("ship MCP support"); err != nil {
		t.Fatal(err)
	}
	goal, ok, err := New(root).Goal()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || goal != "ship MCP support" {
		t.Fatalf("unexpected goal ok=%v value=%q", ok, goal)
	}
	if err := s.ClearGoal(); err != nil {
		t.Fatal(err)
	}
	_, ok, err = New(root).Goal()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected goal to be cleared")
	}
}

func TestStorePersistsAndSearchesMemories(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	created, err := s.CreateMemoryWithOptions(CreateMemoryRequest{
		Text:       "remember MCP config format",
		Kind:       "preference",
		Source:     "test",
		Importance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Text != "remember MCP config format" || created.Kind != "preference" || created.Source != "test" || created.Importance != 5 || !created.Enabled {
		t.Fatalf("unexpected created memory %#v", created)
	}
	if err := s.AddMemory("prefer concise TUI output"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "liora.db")); err != nil {
		t.Fatalf("expected sqlite database to be created: %v", err)
	}

	memories, err := New(root).ListMemories(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 2 || memories[0].ID == "" || memories[0].Text != "remember MCP config format" {
		t.Fatalf("unexpected memories %#v", memories)
	}
	if memories[1].Kind != "note" || memories[1].Importance != 3 || !memories[1].Enabled {
		t.Fatalf("unexpected memory defaults %#v", memories[0])
	}

	updated, err := s.UpdateMemory(created.ID, UpdateMemoryRequest{Text: stringPtr("prefer MCP config blocks"), Importance: intPtr(4)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Text != "prefer MCP config blocks" || updated.Kind != "preference" || updated.Importance != 4 || !updated.Enabled {
		t.Fatalf("unexpected updated memory %#v", updated)
	}
	if _, err := s.SetMemoryEnabled(created.ID, false); err != nil {
		t.Fatal(err)
	}
	matches, err := s.SearchMemories("tui", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || !strings.Contains(strings.ToLower(matches[0].Text), "tui") {
		t.Fatalf("unexpected search result %#v", matches)
	}
	matches, err = s.SearchMemories("mcp", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected disabled memory hidden by default, got %#v", matches)
	}
	all, err := s.SearchMemoriesWithOptions("mcp", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != created.ID || all[0].Enabled {
		t.Fatalf("expected disabled memory with include disabled, got %#v", all)
	}
	if _, err := s.SetMemoryEnabled(created.ID, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.GetMemory(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled {
		t.Fatalf("expected memory re-enabled, got %#v", enabled)
	}
	if _, err := s.CreateMemoryWithOptions(CreateMemoryRequest{Text: "bad", Kind: "unknown", Importance: 3}); err == nil {
		t.Fatal("expected unknown kind to fail")
	}
	if _, err := s.CreateMemoryWithOptions(CreateMemoryRequest{Text: "bad", Kind: "note", Importance: 9}); err == nil {
		t.Fatal("expected invalid importance to fail")
	}
}

func TestStorePersistsAndFiltersPermissionRules(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	created, err := s.CreatePermissionRule(CreatePermissionRuleRequest{
		Action:    PermissionRuleAlwaysAllow,
		Workspace: "/repo",
		Tool:      "run",
		Risk:      "network",
		Reason:    "trusted endpoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Action != PermissionRuleAlwaysAllow || created.Workspace != "/repo" || created.Tool != "run" || created.Risk != "network" || !created.Enabled {
		t.Fatalf("unexpected created rule %#v", created)
	}
	reloaded := New(root)
	rules, err := reloaded.ListPermissionRules(PermissionRuleListOptions{Workspace: "/repo", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != created.ID || rules[0].Reason != "trusted endpoint" {
		t.Fatalf("unexpected rules %#v", rules)
	}
	other, err := reloaded.ListPermissionRules(PermissionRuleListOptions{Workspace: "/other", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("expected workspace filter to hide rule, got %#v", other)
	}
	for _, request := range []CreatePermissionRuleRequest{
		{Action: PermissionRuleAction("bad"), Workspace: "/repo"},
		{Action: PermissionRuleAlwaysAllow},
		{Action: PermissionRuleAlwaysAllow, Tool: "unknown"},
		{Action: PermissionRuleAlwaysAllow, Risk: "unknown"},
	} {
		if _, err := s.CreatePermissionRule(request); err == nil {
			t.Fatalf("expected invalid permission rule request to fail: %#v", request)
		}
	}
}

func TestStorePersistsAndFiltersSchedules(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	oneShot, err := s.CreateSchedule(CreateScheduleRequest{
		ID:              "release-cutover",
		Workspace:       "/repo",
		TriggerKind:     ScheduleTriggerOneShot,
		Trigger:         "2026-07-02T10:30:00+08:00",
		Prompt:          "prepare release",
		Timezone:        "Asia/Shanghai",
		QuietHoursStart: "22:00",
		QuietHoursEnd:   "07:30",
	})
	if err != nil {
		t.Fatal(err)
	}
	interval, err := s.CreateSchedule(CreateScheduleRequest{
		ID:          "index-refresh",
		Workspace:   "/repo",
		TriggerKind: ScheduleTriggerInterval,
		Trigger:     "2h30m",
		Prompt:      "refresh index",
		Timezone:    "Local",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	cron, err := s.CreateSchedule(CreateScheduleRequest{
		ID:          "nightly-audit",
		Workspace:   "/repo",
		TriggerKind: ScheduleTriggerCron,
		Trigger:     "0 2 * * *",
		Prompt:      "audit",
		Timezone:    "Asia/Shanghai",
		Enabled:     &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}

	if oneShot.TriggerKind != ScheduleTriggerOneShot || oneShot.QuietHoursStart != "22:00" || oneShot.QuietHoursEnd != "07:30" || !oneShot.Enabled {
		t.Fatalf("unexpected one-shot schedule %#v", oneShot)
	}
	if interval.TriggerKind != ScheduleTriggerInterval || interval.Timezone != "Local" {
		t.Fatalf("unexpected interval schedule %#v", interval)
	}
	if cron.Enabled {
		t.Fatalf("expected disabled cron schedule, got %#v", cron)
	}

	visible, err := New(root).ListSchedules(ScheduleListOptions{Workspace: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 {
		t.Fatalf("expected disabled schedule hidden by default, got %#v", visible)
	}
	all, err := New(root).ListSchedules(ScheduleListOptions{Workspace: "/repo", IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected include disabled to return all schedules, got %#v", all)
	}

	updatedQuietHours := ScheduleQuietHours{Start: "23:00", End: "06:00"}
	updatedPrompt := "audit and summarize"
	updated, err := s.UpdateSchedule("nightly-audit", UpdateScheduleRequest{
		Prompt:     &updatedPrompt,
		QuietHours: &updatedQuietHours,
		Enabled:    boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Prompt != updatedPrompt || updated.QuietHoursStart != "23:00" || updated.QuietHoursEnd != "06:00" || !updated.Enabled {
		t.Fatalf("unexpected updated schedule %#v", updated)
	}
	toggled, err := s.SetScheduleEnabled("nightly-audit", false)
	if err != nil {
		t.Fatal(err)
	}
	if toggled.Enabled {
		t.Fatalf("expected schedule disabled, got %#v", toggled)
	}
	if err := s.DeleteSchedule("nightly-audit"); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.ListSchedules(ScheduleListOptions{Workspace: "/repo", IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected deleted schedule to disappear, got %#v", remaining)
	}
	if _, err := s.GetSchedule("nightly-audit"); err == nil {
		t.Fatal("expected deleted schedule lookup to fail")
	}
}

func TestStoreRejectsMalformedSchedulesWithoutPartialRows(t *testing.T) {
	tests := []struct {
		name    string
		request CreateScheduleRequest
	}{
		{name: "blank id", request: validScheduleRequest("", ScheduleTriggerCron, "0 2 * * *")},
		{name: "blank prompt", request: CreateScheduleRequest{ID: "bad", TriggerKind: ScheduleTriggerCron, Trigger: "0 2 * * *", Prompt: " "}},
		{name: "unknown kind", request: validScheduleRequest("bad-kind", ScheduleTriggerKind("weekly"), "0 2 * * *")},
		{name: "bad one-shot timestamp", request: validScheduleRequest("bad-once", ScheduleTriggerOneShot, "tomorrow")},
		{name: "zero interval", request: validScheduleRequest("zero-interval", ScheduleTriggerInterval, "0s")},
		{name: "bad interval", request: validScheduleRequest("bad-interval", ScheduleTriggerInterval, "soon")},
		{name: "bad cron expression", request: validScheduleRequest("bad-cron", ScheduleTriggerCron, "0 2 * * * *")},
		{name: "unknown timezone", request: scheduleRequestWithTimezone("bad-zone", "Mars/Olympus")},
		{name: "partial quiet hours", request: scheduleRequestWithQuietHours("partial-quiet", "22:00", "")},
		{name: "invalid quiet hours", request: scheduleRequestWithQuietHours("bad-quiet", "25:00", "07:30")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(t.TempDir())
			if _, err := s.CreateSchedule(tt.request); err == nil {
				t.Fatalf("expected malformed schedule to fail: %#v", tt.request)
			}
			schedules, err := s.ListSchedules(ScheduleListOptions{IncludeDisabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(schedules) != 0 {
				t.Fatalf("expected no partial schedule rows, got %#v", schedules)
			}
		})
	}
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func validScheduleRequest(id string, kind ScheduleTriggerKind, trigger string) CreateScheduleRequest {
	return CreateScheduleRequest{
		ID:          id,
		Workspace:   "/repo",
		TriggerKind: kind,
		Trigger:     trigger,
		Prompt:      "run scheduled task",
		Timezone:    "Asia/Shanghai",
	}
}

func scheduleRequestWithTimezone(id string, timezone string) CreateScheduleRequest {
	request := validScheduleRequest(id, ScheduleTriggerCron, "0 2 * * *")
	request.Timezone = timezone
	return request
}

func scheduleRequestWithQuietHours(id string, start string, end string) CreateScheduleRequest {
	request := validScheduleRequest(id, ScheduleTriggerCron, "0 2 * * *")
	request.QuietHoursStart = start
	request.QuietHoursEnd = end
	return request
}

func TestStoreMigratesLegacyJSONLMemoryToSQLite(t *testing.T) {
	root := t.TempDir()
	file, err := os.OpenFile(filepath.Join(root, "memory.jsonl"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(file).Encode(Memory{Text: "legacy cozy memory", CreatedAt: time.Date(2026, 6, 25, 1, 2, 3, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	memories, err := New(root).ListMemories(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || memories[0].Text != "legacy cozy memory" {
		t.Fatalf("unexpected migrated memories %#v", memories)
	}

	if err := New(root).AddMemory("new sqlite memory"); err != nil {
		t.Fatal(err)
	}
	memories, err = New(root).ListMemories(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected migration to be idempotent, got %#v", memories)
	}
}

func TestStoreInitializesTaskTables(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	db, err := s.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"tasks", "task_events"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %s: %v", table, err)
		}
	}
}

func TestScanSkillsFromGlobalAndWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "review", "SKILL.md"), []byte("---\nname: Code Review\ndescription: Review code changes\n---\nBody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".liora", "skills", "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".liora", "skills", "tests", "SKILL.md"), []byte("# Test Skill\nGenerate tests"), 0o600); err != nil {
		t.Fatal(err)
	}

	skills, err := New(root).ScanSkills(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %#v", skills)
	}
	if skills[0].Name != "review" || skills[0].Title != "Code Review" || skills[0].Description != "Review code changes" {
		t.Fatalf("unexpected first skill %#v", skills[0])
	}
	if skills[1].Name != "tests" || skills[1].Title != "Test Skill" {
		t.Fatalf("unexpected second skill %#v", skills[1])
	}
}

func TestScanSkillsDoesNotReadFullBodies(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".liora", "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Review Skill\n" + strings.Repeat("large body line\n", 10_000) + "MUST_NOT_LOAD_BODY\n"
	if err := os.WriteFile(filepath.Join(workspace, ".liora", "skills", "review", "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	skills, err := New(root).ScanSkills(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected one skill, got %#v", skills)
	}
	if skills[0].Body != "" {
		t.Fatalf("ScanSkills should only load metadata, got body length %d", len(skills[0].Body))
	}
}

func TestScanSkillsWorkspaceOverridesGlobalMetadata(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".liora", "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "review", "SKILL.md"), []byte("# Global Review\nUse global rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".liora", "skills", "review", "SKILL.md"), []byte("# Workspace Review\nUse workspace rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	skills, err := New(root).ScanSkills(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected one visible skill after workspace override, got %#v", skills)
	}
	if skills[0].Name != "review" || skills[0].Source != "workspace" || skills[0].Title != "Workspace Review" {
		t.Fatalf("expected workspace metadata to win, got %#v", skills[0])
	}
}

func TestReadSkillReturnsPagedBody(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".liora", "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		"# Review Skill",
		"line one",
		"line two",
		"line three",
	}, "\n")
	if err := os.WriteFile(filepath.Join(workspace, ".liora", "skills", "review", "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := New(root).ReadSkill(workspace, "review", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Review Skill") || !strings.Contains(out, "2\tline one") || !strings.Contains(out, "3\tline two") {
		t.Fatalf("unexpected paged skill body:\n%s", out)
	}
}

func TestReadSkillPrefersWorkspaceSkill(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".liora", "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "review", "SKILL.md"), []byte("# Global Review\nUse global rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".liora", "skills", "review", "SKILL.md"), []byte("# Workspace Review\nUse workspace rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := New(root).ReadSkill(workspace, "review", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Workspace Review") || strings.Contains(out, "Global Review") {
		t.Fatalf("expected workspace skill to win, got:\n%s", out)
	}
}

func TestReadSkillRejectsMissingSkill(t *testing.T) {
	_, err := New(t.TempDir()).ReadSkill(t.TempDir(), "missing", 1, 1)
	if err == nil || !strings.Contains(err.Error(), `skill "missing" not found`) {
		t.Fatalf("expected missing skill error, got %v", err)
	}
}

func TestReadSkillRejectsEmptyName(t *testing.T) {
	_, err := New(t.TempDir()).ReadSkill(t.TempDir(), "", 1, 1)
	if err == nil || !strings.Contains(err.Error(), "skill name is required") {
		t.Fatalf("expected empty skill name error, got %v", err)
	}
}

func TestReadSkillRejectsSymlinkedSkillFile(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# Escaped\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(workspace, ".liora", "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := New(root).ReadSkill(workspace, "review", 1, 1)
	if err == nil || !strings.Contains(err.Error(), "symlink is not allowed") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestMCPConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	cfg := MCPConfig{Servers: map[string]MCPServerConfig{
		"local": {Command: "go", Args: []string{"run", "./server"}, Env: map[string]string{"A": "B"}},
	}}
	if err := s.SaveMCPConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(root).LoadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Servers["local"].Command != "go" || loaded.Servers["local"].Args[0] != "run" {
		t.Fatalf("unexpected config %#v", loaded)
	}
}
