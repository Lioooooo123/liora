package task

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/store"
)

func TestRepositoryChildTaskScopeIsBoundedByParent(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: "/repo",
		Prompt:    "parent task",
		Scope: TaskScope{
			Paths:           []string{"/repo", "/tmp/shared"},
			NetworkHosts:    []string{"api.internal"},
			MCPServers:      []string{"filesystem"},
			MCPTools:        []string{"filesystem.read"},
			ApprovalActions: []string{"apply_patch"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	child, err := repo.Create(t.Context(), CreateRequest{
		Workspace:    "/repo",
		Prompt:       "child task",
		ParentTaskID: parent.ID,
		Scope: TaskScope{
			Paths:           []string{"/repo/src"},
			NetworkHosts:    []string{"api.internal"},
			MCPServers:      []string{"filesystem"},
			MCPTools:        []string{"filesystem.read"},
			ApprovalActions: []string{"apply_patch"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID != parent.ID || !child.InheritedScopeFromParent {
		t.Fatalf("expected child to record parent inheritance, got %#v", child)
	}
	if len(child.ApprovalGrants) != 0 {
		t.Fatalf("child must not inherit approval grants, got %#v", child.ApprovalGrants)
	}
	if got := child.Scope.Paths; len(got) != 1 || got[0] != "/repo/src" {
		t.Fatalf("unexpected child path scope %#v", child.Scope)
	}

	got, err := repo.Get(t.Context(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentTaskID != parent.ID || !got.InheritedScopeFromParent || len(got.ApprovalGrants) != 0 {
		t.Fatalf("unexpected persisted child task %#v", got)
	}
	assertSubagentRelation(t, db, parent.ID, child.ID)
}

func TestRepositoryChildTaskDefaultsToParentPathScopeOnly(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: "/repo",
		Prompt:    "parent task",
		Scope: TaskScope{
			Paths:           []string{"/repo", "/tmp/shared"},
			NetworkHosts:    []string{"api.internal"},
			MCPServers:      []string{"filesystem"},
			MCPTools:        []string{"filesystem.read"},
			ApprovalActions: []string{"apply_patch"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	child, err := repo.Create(t.Context(), CreateRequest{
		Workspace:    "/repo",
		Prompt:       "child task",
		ParentTaskID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !child.InheritedScopeFromParent {
		t.Fatalf("expected child to record inherited scope, got %#v", child)
	}
	if got := child.Scope.Paths; len(got) != 2 || got[0] != "/repo" || got[1] != "/tmp/shared" {
		t.Fatalf("expected child to default to parent paths only, got %#v", child.Scope)
	}
	if len(child.Scope.NetworkHosts) != 0 || len(child.Scope.MCPServers) != 0 || len(child.Scope.MCPTools) != 0 || len(child.Scope.ApprovalActions) != 0 {
		t.Fatalf("child must not inherit parent capability lists, got %#v", child.Scope)
	}

	got, err := repo.Get(t.Context(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Scope.Paths) != 2 || got.Scope.Paths[0] != "/repo" || got.Scope.Paths[1] != "/tmp/shared" {
		t.Fatalf("expected persisted child path defaults, got %#v", got.Scope)
	}
	if len(got.Scope.NetworkHosts) != 0 || len(got.Scope.MCPServers) != 0 || len(got.Scope.MCPTools) != 0 || len(got.Scope.ApprovalActions) != 0 {
		t.Fatalf("persisted child must not inherit parent capability lists, got %#v", got.Scope)
	}
}

func TestRepositoryChildTaskInheritsParentModelUnlessOverridden(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: "/repo",
		Prompt:    "parent task",
		ModelConfig: &ModelConfig{
			Provider: "openai-chat",
			Model:    "strong-model",
			Profile:  "strong",
			Source:   "thread_binding",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.Create(t.Context(), CreateRequest{
		Workspace:    "/repo",
		Prompt:       "child task",
		ParentTaskID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ModelConfig == nil || child.ModelConfig.Provider != "openai-chat" || child.ModelConfig.Model != "strong-model" || child.ModelConfig.Profile != "strong" || child.ModelConfig.Source != "parent_task" {
		t.Fatalf("expected child to inherit parent model config, got %#v", child.ModelConfig)
	}
	override, err := repo.Create(t.Context(), CreateRequest{
		Workspace:    "/repo",
		Prompt:       "override child task",
		ParentTaskID: parent.ID,
		ModelConfig: &ModelConfig{
			Provider: "openai-chat",
			Model:    "cheap-model",
			Profile:  "cheap",
			Source:   "task_override",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if override.ModelConfig == nil || override.ModelConfig.Model != "cheap-model" || override.ModelConfig.Profile != "cheap" || override.ModelConfig.Source != "task_override" {
		t.Fatalf("expected explicit child model override to win, got %#v", override.ModelConfig)
	}
}

func TestRepositoryRejectsChildScopeEscalation(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: "/repo",
		Prompt:    "parent task",
		Scope: TaskScope{
			Paths:           []string{"/repo"},
			NetworkHosts:    []string{"api.internal"},
			MCPServers:      []string{"filesystem"},
			MCPTools:        []string{"filesystem.read"},
			ApprovalActions: []string{"apply_patch"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		request CreateRequest
		want    string
	}{
		{
			name: "path outside parent",
			request: CreateRequest{Workspace: "/repo", Prompt: "child", ParentTaskID: parent.ID, Scope: TaskScope{
				Paths: []string{"/etc"},
			}},
			want: "outside parent scope",
		},
		{
			name: "network outside parent",
			request: CreateRequest{Workspace: "/repo", Prompt: "child", ParentTaskID: parent.ID, Scope: TaskScope{
				NetworkHosts: []string{"public.example.com"},
			}},
			want: "outside parent scope",
		},
		{
			name: "mcp tool outside parent",
			request: CreateRequest{Workspace: "/repo", Prompt: "child", ParentTaskID: parent.ID, Scope: TaskScope{
				MCPTools: []string{"filesystem.write"},
			}},
			want: "outside parent scope",
		},
		{
			name: "approval action outside parent",
			request: CreateRequest{Workspace: "/repo", Prompt: "child", ParentTaskID: parent.ID, Scope: TaskScope{
				ApprovalActions: []string{"push"},
			}},
			want: "outside parent scope",
		},
		{
			name: "child auto approves parent",
			request: CreateRequest{
				Workspace:         "/repo",
				Prompt:            "child",
				ParentTaskID:      parent.ID,
				AutoApproveParent: true,
			},
			want: "cannot approve parent",
		},
		{
			name: "child carries approval grants",
			request: CreateRequest{
				Workspace:      "/repo",
				Prompt:         "child",
				ParentTaskID:   parent.ID,
				ApprovalGrants: []string{"apply_patch"},
			},
			want: "approval grants",
		},
		{
			name: "missing parent",
			request: CreateRequest{
				Workspace:    "/repo",
				Prompt:       "child",
				ParentTaskID: "task_missing",
			},
			want: "parent task",
		},
		{
			name: "workspace outside parent",
			request: CreateRequest{
				Workspace:    "/other",
				Prompt:       "child",
				ParentTaskID: parent.ID,
			},
			want: "workspace",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.Create(t.Context(), tc.request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func assertSubagentRelation(t *testing.T, db *sql.DB, parentID string, childID string) {
	t.Helper()
	var relation string
	if err := db.QueryRow(`
		SELECT relation
		FROM subagent_relations
		WHERE parent_task_id = ? AND subagent_task_id = ?
	`, parentID, childID).Scan(&relation); err != nil {
		t.Fatal(err)
	}
	if relation != "child_task" {
		t.Fatalf("unexpected subagent relation %q", relation)
	}
}
