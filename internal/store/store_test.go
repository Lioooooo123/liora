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

	if err := s.AddMemory("remember MCP config format"); err != nil {
		t.Fatal(err)
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
