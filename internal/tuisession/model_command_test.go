package tuisession

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func TestDaemonSubmitterModelProfilesListsAndSelectsConfiguredCatalog(t *testing.T) {
	// Given
	t.Setenv("LIORA_LLM_PROFILES", `{
		"cheap": {
			"provider": "deepseek",
			"model": "deepseek-chat",
			"base_url": "https:\/\/user:pass@proxy.example.test/v1?token=query-secret#fragment-secret",
			"api_key": "cheap-secret",
			"profile": "cheap"
		},
		"strong": {
			"provider": "anthropic",
			"model": "claude-sonnet-4",
			"api_key": "strong-secret"
		}
	}`)
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Catalog Switch"})
	if err != nil {
		t.Fatal(err)
	}
	if _, handled, err := submitter.HandleCommand(t.Context(), "/thread "+thread.ID); err != nil || !handled {
		t.Fatalf("failed to switch thread handled=%v err=%v", handled, err)
	}

	// When
	defaultModel, handled, err := submitter.HandleCommand(t.Context(), "/model")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Available profiles: cheap, strong", "/model profiles", "/model set <profile>"} {
		if !handled || !strings.Contains(defaultModel, want) {
			t.Fatalf("expected /model output to contain %q handled=%v output=%q", want, handled, defaultModel)
		}
	}

	// When
	profiles, handled, err := submitter.HandleCommand(t.Context(), "/model profiles")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Model profiles:", "- cheap: deepseek/deepseek-chat", "base_url=https://proxy.example.test/v1", "api_key=***", "- strong: anthropic/claude-sonnet-4"} {
		if !handled || !strings.Contains(profiles, want) {
			t.Fatalf("expected /model profiles output to contain %q handled=%v output=%q", want, handled, profiles)
		}
	}
	for _, secret := range []string{"cheap-secret", "strong-secret", "user", "pass", "query-secret", "fragment-secret"} {
		if strings.Contains(profiles, secret) {
			t.Fatalf("expected /model profiles to redact %q, got %q", secret, profiles)
		}
	}

	// When
	updated, handled, err := submitter.HandleCommand(t.Context(), "/model set cheap")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Updated thread model " + thread.ID, "Provider: deepseek", "Model: deepseek-chat", "Profile: cheap", "Base URL: https://proxy.example.test/v1"} {
		if !handled || !strings.Contains(updated, want) {
			t.Fatalf("expected catalog set output to contain %q handled=%v output=%q", want, handled, updated)
		}
	}
	for _, secret := range []string{"user", "pass", "query-secret", "fragment-secret"} {
		if strings.Contains(updated, secret) {
			t.Fatalf("expected catalog set output to redact %q, got %q", secret, updated)
		}
	}
	config, err := client.GetThreadModelConfig(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if config.Provider != "deepseek" || config.Model != "deepseek-chat" || config.BaseURL != "https://user:pass@proxy.example.test/v1?token=query-secret#fragment-secret" || config.Profile != "cheap" {
		t.Fatalf("unexpected persisted catalog model config %#v", config)
	}
}
