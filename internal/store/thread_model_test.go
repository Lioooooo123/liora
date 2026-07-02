package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestStoreThreadModelBindingPersistsProviderModelAndInheritance(t *testing.T) {
	s := New(t.TempDir())
	architect, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Architect"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Batch"})
	if err != nil {
		t.Fatal(err)
	}

	config, err := s.UpdateThreadModelConfig(architect.ID, UpdateThreadModelConfigRequest{
		Provider: "openai-chat",
		Model:    "gpt-5",
		BaseURL:  "https://llm.example.test/v1",
		Profile:  "strong",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ThreadID != architect.ID || config.Provider != "openai-chat" || config.Model != "gpt-5" || config.BaseURL == "" || config.Profile != "strong" {
		t.Fatalf("unexpected explicit model config %#v", config)
	}

	inherited, err := s.UpdateThreadModelConfig(batch.ID, UpdateThreadModelConfigRequest{InheritedFromThreadID: architect.ID})
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ThreadID != batch.ID || inherited.InheritedFromThreadID != architect.ID || inherited.Provider != "" || inherited.Model != "" {
		t.Fatalf("unexpected inherited model config %#v", inherited)
	}

	threads, err := s.ListConversationThreads("/repo-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]*ThreadModelConfig{}
	for _, thread := range threads {
		models[thread.ID] = thread.ModelConfig
	}
	if models[architect.ID] == nil || models[architect.ID].BaseURL == "" {
		t.Fatalf("expected listed architect thread to include model config, got %#v", threads)
	}
	if models[batch.ID] == nil || models[batch.ID].InheritedFromThreadID != architect.ID {
		t.Fatalf("expected listed batch thread to include inherited model config, got %#v", threads)
	}

	if err := s.DeleteThreadModelConfig(architect.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetThreadModelConfig(architect.ID); err != nil || ok {
		t.Fatalf("expected deleted model config to be absent ok=%v err=%v", ok, err)
	}
}

func TestStoreThreadModelBindingValidatesWorkspaceAndRequiredFields(t *testing.T) {
	s := New(t.TempDir())
	local, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Local"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-b", Title: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.UpdateThreadModelConfig(local.ID, UpdateThreadModelConfigRequest{Provider: "openai-chat"})
	if err == nil || !strings.Contains(err.Error(), "requires provider and model") {
		t.Fatalf("expected incomplete binding to fail, got %v", err)
	}
	_, err = s.UpdateThreadModelConfig(local.ID, UpdateThreadModelConfigRequest{InheritedFromThreadID: foreign.ID})
	if err == nil || !strings.Contains(err.Error(), "belongs to workspace") {
		t.Fatalf("expected cross-workspace inheritance to fail, got %v", err)
	}
	if err := s.DeleteThreadModelConfig(local.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleting missing model config to fail with sql.ErrNoRows, got %v", err)
	}
}
