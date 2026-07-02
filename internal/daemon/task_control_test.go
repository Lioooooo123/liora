package daemon

import (
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func TestTaskControlCreatesSafeChildTask(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	s := newServer(Config{
		Repository: repo,
		Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read ."})),
	})

	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "parent",
		Natural:   true,
		Origin:    taskpkg.OriginForeground,
		Scope: taskpkg.TaskScope{
			Paths:           []string{workspace},
			NetworkHosts:    []string{"api.internal"},
			MCPServers:      []string{"docs"},
			MCPTools:        []string{"docs.search"},
			ApprovalActions: []string{"run"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), parent.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}

	child, err := s.CreateChildTask(t.Context(), parent, taskpkg.ChildTaskRequest{
		Prompt:       "inspect child scope",
		SubagentName: "explorer",
		Role:         "search",
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID != parent.ID || child.Origin != taskpkg.OriginSubagent || child.Automation.Risk != taskpkg.AutomationRiskSafe {
		t.Fatalf("unexpected child metadata %#v", child)
	}
	if child.Automation.Source != "model_tool" || child.Automation.Trigger != "Task" {
		t.Fatalf("unexpected child automation %#v", child.Automation)
	}
	if child.SessionID != parent.SessionID || !child.InheritedScopeFromParent {
		t.Fatalf("expected child to inherit parent thread/session scope, got child=%#v parent=%#v", child, parent)
	}
	if got := child.Scope.Paths; len(got) != 1 || got[0] != workspace {
		t.Fatalf("expected child to default to parent workspace path scope, got %#v", child.Scope)
	}
	if len(child.Scope.NetworkHosts) != 0 || len(child.Scope.MCPServers) != 0 || len(child.Scope.MCPTools) != 0 || len(child.Scope.ApprovalActions) != 0 {
		t.Fatalf("child must not inherit parent capability lists, got %#v", child.Scope)
	}
	parentEvents, err := repo.Events(t.Context(), parent.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(parentEvents, taskpkg.EventSubagentStarted) {
		t.Fatalf("expected parent subagent.started event, got %#v", parentEvents)
	}
	childEvents, err := repo.Events(t.Context(), child.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(childEvents, taskpkg.EventTaskCreated) {
		t.Fatalf("expected child task.created event, got %#v", childEvents)
	}
}

func TestTaskControlRejectsInvalidChildRequests(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	s := newServer(Config{
		Repository: repo,
		Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read ."})),
	})
	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "parent",
		Scope:     taskpkg.TaskScope{Paths: []string{workspace}},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherParent, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "other"})
	if err != nil {
		t.Fatal(err)
	}
	foreignChild, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:    workspace,
		Prompt:       "foreign child",
		ParentTaskID: otherParent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "blank prompt",
			run: func() error {
				_, err := s.CreateChildTask(t.Context(), parent, taskpkg.ChildTaskRequest{Prompt: "   "})
				return err
			},
			want: "prompt is required",
		},
		{
			name: "scope escalation",
			run: func() error {
				_, err := s.CreateChildTask(t.Context(), parent, taskpkg.ChildTaskRequest{
					Prompt: "outside",
					Scope:  taskpkg.TaskScope{Paths: []string{"/etc"}},
				})
				return err
			},
			want: "outside parent scope",
		},
		{
			name: "missing output child",
			run: func() error {
				_, _, err := s.ReadChildTaskOutput(t.Context(), parent, taskpkg.ChildTaskOutputRequest{TaskID: "task_missing"})
				return err
			},
			want: "no rows",
		},
		{
			name: "foreign output child",
			run: func() error {
				_, _, err := s.ReadChildTaskOutput(t.Context(), parent, taskpkg.ChildTaskOutputRequest{TaskID: foreignChild.ID})
				return err
			},
			want: "not a child",
		},
		{
			name: "foreign stop child",
			run: func() error {
				_, err := s.StopChildTask(t.Context(), parent, taskpkg.ChildTaskStopRequest{TaskID: foreignChild.ID})
				return err
			},
			want: "not a child",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestTaskControlReadsOutputAndStopsOnlyChild(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	s := newServer(Config{Repository: repo, Runner: taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read ."}))})
	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), parent.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	child, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:    workspace,
		Prompt:       "child",
		ParentTaskID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), child.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "child summary"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), child.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "read", Output: "tool output"}); err != nil {
		t.Fatal(err)
	}

	got, output, err := s.ReadChildTaskOutput(t.Context(), parent, taskpkg.ChildTaskOutputRequest{TaskID: child.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != child.ID || !strings.Contains(output, "child summary") || !strings.Contains(output, "tool output") {
		t.Fatalf("unexpected output task=%#v output=%q", got, output)
	}
	if err := repo.UpdateStatus(t.Context(), child.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	stopped, err := s.StopChildTask(t.Context(), parent, taskpkg.ChildTaskStopRequest{TaskID: child.ID, Reason: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != taskpkg.StatusCancelled {
		t.Fatalf("expected child cancelled, got %#v", stopped)
	}
	parentAfter, err := repo.Get(t.Context(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parentAfter.Status != taskpkg.StatusRunning {
		t.Fatalf("TaskStop should not mutate parent status, got %#v", parentAfter)
	}
}
