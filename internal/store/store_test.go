package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	created, err := s.CreateMemory("remember MCP config format")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Text != "remember MCP config format" || created.Source != "manual" {
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
	if memories[0].Kind != "note" || memories[0].Importance != 3 {
		t.Fatalf("unexpected memory defaults %#v", memories[0])
	}

	matches, err := s.SearchMemories("tui", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || !strings.Contains(strings.ToLower(matches[0].Text), "tui") {
		t.Fatalf("unexpected search result %#v", matches)
	}
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
