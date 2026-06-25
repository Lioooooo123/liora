package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvLocalSetsMissingValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=test-key\nOPENAI_MODEL=kimi-k2.7-code\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "")

	if err := LoadDotEnvLocal(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENAI_API_KEY") != "test-key" {
		t.Fatalf("expected API key from env file")
	}
	if os.Getenv("OPENAI_MODEL") != "kimi-k2.7-code" {
		t.Fatalf("expected model from env file")
	}
}

func TestLoadDotEnvLocalDoesNotOverrideExistingEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "shell-key")

	if err := LoadDotEnvLocal(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENAI_API_KEY") != "shell-key" {
		t.Fatalf("expected shell env to win")
	}
}

func TestLoadDefaultEnvReadsUserConfig(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "liora")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte("OPENAI_MODEL=deepseek-v4-pro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_MODEL", "")

	if err := LoadDefaultEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENAI_MODEL") != "deepseek-v4-pro" {
		t.Fatalf("expected model from user config")
	}
}
