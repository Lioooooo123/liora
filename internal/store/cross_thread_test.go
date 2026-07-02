package store

import (
	"errors"
	"strings"
	"testing"
)

func TestStoreCrossThreadHandoffSharesOnlyMinimalFields(t *testing.T) {
	s := New(t.TempDir())
	source, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}

	message, err := s.CreateCrossThreadMessage(CreateCrossThreadMessageRequest{
		FromThreadID:    source.ID,
		ToThreadID:      target.ID,
		TaskID:          "task-001",
		Summary:         "Audit finished; secret material omitted",
		ExplicitContent: "Please inspect the referenced audit output.",
		ArtifactRefs: []CrossThreadArtifactRef{
			{Path: ".liora/artifacts/audit.txt", Summary: "audit output"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.ID == "" || message.FromWorkspace != "/repo-a" || message.ToWorkspace != "/repo-a" {
		t.Fatalf("unexpected message identity/workspace %#v", message)
	}
	if message.TaskID != "task-001" {
		t.Fatalf("expected explicit task reference, got %#v", message)
	}
	if message.Content != message.ExplicitContent || message.Summary == "" || len(message.ArtifactRefs) != 1 {
		t.Fatalf("expected summary, explicit content, and artifact refs only, got %#v", message)
	}
	if message.IncludesPrompt || message.IncludesSecret || message.IncludesMemory || message.IncludesApprovalRule {
		t.Fatalf("handoff must not mark implicit sensitive shares: %#v", message)
	}

	messages, err := s.ListCrossThreadMessages(target.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != message.ID || messages[0].ArtifactRefs[0].Path != ".liora/artifacts/audit.txt" {
		t.Fatalf("unexpected listed messages %#v", messages)
	}
	db, err := s.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var relationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thread_relations WHERE from_thread_id = ? AND to_thread_id = ? AND relation = 'message'`, source.ID, target.ID).Scan(&relationCount); err != nil {
		t.Fatal(err)
	}
	if relationCount != 1 {
		t.Fatalf("expected one thread message relation, got %d", relationCount)
	}
	rendered := messages[0].Summary + "\n" + messages[0].ExplicitContent + "\n" + messages[0].Content + "\n" + messages[0].ArtifactRefs[0].Summary
	for _, forbidden := range []string{"sk-live", "approval: always allow", "remember raw token"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("cross-thread handoff leaked forbidden text %q in %#v", forbidden, messages[0])
		}
	}
}

func TestStoreCrossThreadHandoffRequiresExplicitCrossWorkspaceAuthorization(t *testing.T) {
	s := New(t.TempDir())
	source, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateConversationThread(CreateConversationThreadRequest{Workspace: "/repo-b", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateCrossThreadMessage(CreateCrossThreadMessageRequest{
		FromThreadID:    source.ID,
		ToThreadID:      target.ID,
		Summary:         "Cross workspace summary",
		ExplicitContent: "Please inspect explicit note only.",
	})
	if !errors.Is(err, ErrCrossWorkspaceAuthorizationRequired) {
		t.Fatalf("expected cross-workspace authorization error, got %v", err)
	}

	message, err := s.CreateCrossThreadMessage(CreateCrossThreadMessageRequest{
		FromThreadID:             source.ID,
		ToThreadID:               target.ID,
		Summary:                  "Cross workspace summary",
		ExplicitContent:          "Please inspect explicit note only.",
		CrossWorkspaceAuthorized: true,
		CrossWorkspaceAuthReason: "user explicitly forwarded to repo-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !message.CrossWorkspaceAuthorized || message.CrossWorkspaceAuthReason == "" {
		t.Fatalf("expected cross workspace authorization metadata, got %#v", message)
	}

	_, err = s.CreateCrossThreadMessage(CreateCrossThreadMessageRequest{
		FromThreadID: source.ID,
		ToThreadID:   target.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "summary, explicit content, or artifact reference") {
		t.Fatalf("expected empty handoff to fail, got %v", err)
	}
}
