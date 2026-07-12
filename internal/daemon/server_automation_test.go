package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
)

type backgroundBlockingExecutor struct {
	started chan string
}

func newBackgroundBlockingExecutor() *backgroundBlockingExecutor {
	return &backgroundBlockingExecutor{started: make(chan string, 10)}
}

func (e *backgroundBlockingExecutor) Run(ctx context.Context, _ string, command string) (tools.ShellResult, error) {
	e.started <- command
	<-ctx.Done()
	return tools.ShellResult{ExitCode: -1}, ctx.Err()
}

func TestServerAutomationMetadata_exposesSafeBackgroundTask(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read ."})),
	}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{
		"workspace":`+quote(workspace)+`,
		"prompt":"background inspect",
		"natural":true,
		"run_async":true,
		"origin":"background",
		"automation":{"kind":"background","risk":"safe","source":"workbench"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.Origin != taskpkg.OriginBackground || created.Task.Automation.Kind != taskpkg.AutomationKindBackground || created.Task.Automation.Risk != taskpkg.AutomationRiskSafe {
		t.Fatalf("unexpected created task metadata %#v", created.Task)
	}

	workbenchResp, err := http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatal(err)
	}
	defer workbenchResp.Body.Close()
	if workbenchResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected workbench status %d", workbenchResp.StatusCode)
	}
	var workbench taskpkg.Workbench
	if err := json.NewDecoder(workbenchResp.Body).Decode(&workbench); err != nil {
		t.Fatal(err)
	}
	if len(workbench.RecentTasks) != 1 || workbench.RecentTasks[0].Automation.Risk != taskpkg.AutomationRiskSafe {
		t.Fatalf("unexpected workbench task metadata %#v", workbench.RecentTasks)
	}
}

func TestServerScheduleAndSubagentInheritThreadOrParentModelUnlessOverridden(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()

	thread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Scheduled thread"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(thread.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "strong-model", Profile: "strong"}); err != nil {
		t.Fatal(err)
	}
	schedule := postTaskForStatus(t, server.URL, http.StatusCreated, `{
		"workspace":`+quote(workspace)+`,
		"thread_id":`+quote(thread.ID)+`,
		"prompt":"run scheduled thread",
		"natural":false,
		"origin":"schedule",
		"automation":{"kind":"schedule","risk":"safe","source":"schedule:nightly"}
	}`)
	if schedule.Task.ModelConfig == nil || schedule.Task.ModelConfig.Model != "strong-model" || schedule.Task.ModelConfig.Profile != "strong" || schedule.Task.ModelConfig.Source != "thread_binding" {
		t.Fatalf("expected schedule to inherit thread model config, got %#v", schedule.Task.ModelConfig)
	}

	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "parent task",
		ModelConfig: &taskpkg.ModelConfig{
			Provider: "openai-chat",
			Model:    "cheap-parent",
			Profile:  "cheap",
			Source:   "thread_binding",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	subagent := postTaskForStatus(t, server.URL, http.StatusCreated, `{
		"workspace":`+quote(workspace)+`,
		"prompt":"run inherited child",
		"natural":false,
		"origin":"subagent",
		"parent_task_id":`+quote(parent.ID)+`,
		"automation":{"kind":"subagent","risk":"safe","source":"parent"}
	}`)
	if subagent.Task.ModelConfig == nil || subagent.Task.ModelConfig.Model != "cheap-parent" || subagent.Task.ModelConfig.Profile != "cheap" || subagent.Task.ModelConfig.Source != "parent_task" {
		t.Fatalf("expected subagent to inherit parent model config, got %#v", subagent.Task.ModelConfig)
	}
	override := postTaskForStatus(t, server.URL, http.StatusCreated, `{
		"workspace":`+quote(workspace)+`,
		"prompt":"run override child",
		"natural":false,
		"origin":"subagent",
		"parent_task_id":`+quote(parent.ID)+`,
		"automation":{"kind":"subagent","risk":"safe","source":"parent"},
		"model_config":{"provider":"openai-chat","model":"explicit-model","profile":"explicit"}
	}`)
	if override.Task.ModelConfig == nil || override.Task.ModelConfig.Model != "explicit-model" || override.Task.ModelConfig.Profile != "explicit" {
		t.Fatalf("expected explicit child override to win, got %#v", override.Task.ModelConfig)
	}
}

func TestServerScheduleTriggerCreatesTaskWithScheduleScope(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: scheduled work accepted."}))
	server := httptest.NewServer(NewServer(Config{Repository: repo, Runner: runner, Store: st}))
	defer server.Close()

	schedule, err := st.CreateSchedule(store.CreateScheduleRequest{
		ID:          "schedule-nightly",
		Workspace:   workspace,
		TriggerKind: store.ScheduleTriggerCron,
		Trigger:     "0 2 * * *",
		Prompt:      "run scheduled maintenance",
		Timezone:    "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := repo.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "Scheduled session"})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(server.URL+"/v1/schedules/"+schedule.ID+"/trigger", "application/json", strings.NewReader(`{
		"session_id":`+quote(session.ID)+`,
		"risk":"safe",
		"source":"scheduler:test",
		"missed_runs":2
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected trigger status %d: %s", resp.StatusCode, string(body))
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.Workspace != workspace || created.Task.SessionID != session.ID {
		t.Fatalf("expected workspace/session scope, got %#v", created.Task)
	}
	if created.Task.Origin != taskpkg.OriginSchedule || created.Task.Automation.Trigger != "schedule" {
		t.Fatalf("expected schedule task trigger metadata, got %#v", created.Task)
	}
	if created.Task.ScheduleID != schedule.ID || created.Task.Schedule.ID != schedule.ID || created.Task.Schedule.MissedRuns != 2 {
		t.Fatalf("expected persisted schedule metadata, got %#v", created.Task.Schedule)
	}
	payload := schedulePayload(t, repo, created.Task.ID)
	if payload.ID != schedule.ID || payload.ScheduleID != schedule.ID || payload.Trigger != "schedule" || payload.MissedRuns != 2 || payload.CatchUpRuns != 1 {
		t.Fatalf("expected schedule.triggered payload for schedule task, got %#v", payload)
	}
	workbenchResp, err := http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatal(err)
	}
	defer workbenchResp.Body.Close()
	if workbenchResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected workbench status %d", workbenchResp.StatusCode)
	}
	var workbench taskpkg.Workbench
	if err := json.NewDecoder(workbenchResp.Body).Decode(&workbench); err != nil {
		t.Fatal(err)
	}
	if !taskListContainsID(workbench.BackgroundTasks, created.Task.ID) {
		t.Fatalf("expected schedule task in workbench background tasks, got %#v", workbench.BackgroundTasks)
	}
}

func TestServerScheduleTriggerRejectsDisabledUnknownAndMalformed(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: st}))
	defer server.Close()

	disabled := false
	if _, err := st.CreateSchedule(store.CreateScheduleRequest{
		ID:          "schedule-disabled",
		Workspace:   workspace,
		TriggerKind: store.ScheduleTriggerCron,
		Trigger:     "0 2 * * *",
		Prompt:      "disabled schedule",
		Enabled:     &disabled,
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		path       string
		body       string
		wantStatus int
	}{
		{"missing id", "/v1/schedules//trigger", `{}`, http.StatusNotFound},
		{"unknown id", "/v1/schedules/missing-schedule/trigger", `{}`, http.StatusNotFound},
		{"disabled", "/v1/schedules/schedule-disabled/trigger", `{}`, http.StatusConflict},
		{"malformed risk", "/v1/schedules/schedule-disabled/trigger", `{"risk":"maybe"}`, http.StatusBadRequest},
		{"malformed catchup", "/v1/schedules/schedule-disabled/trigger", `{"missed_runs":-1}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(server.URL+tc.path, "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("unexpected status %d, want %d: %s", resp.StatusCode, tc.wantStatus, string(body))
			}
		})
	}
	tasks, err := repo.ListByWorkspace(t.Context(), workspace, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("malformed or disabled schedule trigger created tasks: %#v", tasks)
	}
}

func TestServerDangerousAutomation_pausesAndCanBeCancelled(t *testing.T) {
	cases := []struct {
		name   string
		origin taskpkg.Origin
		kind   taskpkg.AutomationKind
		source string
	}{
		{"schedule", taskpkg.OriginSchedule, taskpkg.AutomationKindSchedule, "cron:nightly"},
		{"hook", taskpkg.OriginHook, taskpkg.AutomationKindHook, "hook:pre-commit"},
		{"subagent", taskpkg.OriginSubagent, taskpkg.AutomationKindSubagent, "parent-task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			db, err := store.New(t.TempDir()).OpenDB()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := taskpkg.NewRepository(db)
			server := httptest.NewServer(NewServer(Config{
				Repository: repo,
				Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write should-not-run.txt nope"})),
			}))
			defer server.Close()

			parentTaskID := ""
			if tc.origin == taskpkg.OriginSubagent {
				parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
					Workspace: workspace,
					Prompt:    "parent task",
					Natural:   true,
				})
				if err != nil {
					t.Fatal(err)
				}
				parentTaskID = parent.ID
			}
			parentJSON := ""
			if parentTaskID != "" {
				parentJSON = `,"parent_task_id":` + quote(parentTaskID)
			}
			resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{
				"workspace":`+quote(workspace)+`,
				"prompt":"nightly cleanup",
				"natural":true,
				"run_async":true,
				"origin":`+quote(string(tc.origin))+`,
				"automation":{"kind":`+quote(string(tc.kind))+`,"risk":"dangerous","source":`+quote(tc.source)+`,"trigger":"0 2 * * *"}`+parentJSON+`
			}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("unexpected create status %d", resp.StatusCode)
			}
			var created taskpkg.CreateResponse
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			if created.Task.Status != taskpkg.StatusWaitingUser {
				t.Fatalf("expected dangerous automation to wait for user, got %#v", created.Task)
			}
			if created.Task.Origin != tc.origin || created.Task.Automation.Kind != tc.kind || created.Task.Automation.Risk != taskpkg.AutomationRiskDangerous {
				t.Fatalf("unexpected dangerous task metadata %#v", created.Task)
			}
			if created.Task.ParentTaskID != parentTaskID {
				t.Fatalf("unexpected parent task id %q, want %q", created.Task.ParentTaskID, parentTaskID)
			}
			if _, err := os.Stat(workspace + "/should-not-run.txt"); !os.IsNotExist(err) {
				t.Fatalf("dangerous automation started before approval, stat err=%v", err)
			}
			events, err := repo.Events(t.Context(), created.Task.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			requested, ok := permissionRequestedPayload(events)
			if !ok {
				t.Fatalf("expected permission.requested event, got %#v", events)
			}
			if requested.Risk != string(taskpkg.AutomationRiskDangerous) || requested.Origin != string(tc.origin) || requested.Kind != string(tc.kind) || requested.Source != tc.source || requested.ParentTaskID != parentTaskID {
				t.Fatalf("unexpected permission payload %#v", requested)
			}

			cancelResp, err := http.Post(server.URL+"/v1/tasks/"+created.Task.ID+"/cancel", "application/json", strings.NewReader(`{"reason":"not now"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer cancelResp.Body.Close()
			if cancelResp.StatusCode != http.StatusOK {
				t.Fatalf("unexpected cancel status %d", cancelResp.StatusCode)
			}
			cancelled, err := repo.Get(t.Context(), created.Task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cancelled.Status != taskpkg.StatusCancelled {
				t.Fatalf("expected cancelled dangerous automation, got %#v", cancelled)
			}
		})
	}
}

func TestServerAutomationMissingRisk_pausesFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		origin taskpkg.Origin
		kind   taskpkg.AutomationKind
	}{
		{"schedule", taskpkg.OriginSchedule, taskpkg.AutomationKindSchedule},
		{"hook", taskpkg.OriginHook, taskpkg.AutomationKindHook},
		{"subagent", taskpkg.OriginSubagent, taskpkg.AutomationKindSubagent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			db, err := store.New(t.TempDir()).OpenDB()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := taskpkg.NewRepository(db)
			server := httptest.NewServer(NewServer(Config{
				Repository: repo,
				Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write should-not-run.txt nope"})),
			}))
			defer server.Close()

			resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{
				"workspace":`+quote(workspace)+`,
				"prompt":"nightly cleanup",
				"natural":true,
				"run_async":true,
				"origin":`+quote(string(tc.origin))+`,
				"automation":{"kind":`+quote(string(tc.kind))+`,"source":"cron:nightly"}
			}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("unexpected create status %d", resp.StatusCode)
			}
			var created taskpkg.CreateResponse
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			if created.Task.Status != taskpkg.StatusWaitingUser || created.Task.Automation.Risk != taskpkg.AutomationRiskDangerous {
				t.Fatalf("expected missing %s risk to pause dangerous, got %#v", tc.origin, created.Task)
			}
			if _, err := os.Stat(workspace + "/should-not-run.txt"); !os.IsNotExist(err) {
				t.Fatalf("automation started before explicit safe risk, stat err=%v", err)
			}
		})
	}
}

func TestServerAutomationRejectsInvalidBoundary(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read ."})),
	}))
	defer server.Close()

	for _, body := range []string{
		`{"workspace":` + quote(workspace) + `,"prompt":"bad risk","natural":true,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"maybe"}}`,
		`{"workspace":` + quote(workspace) + `,"prompt":"kind mismatch","natural":true,"run_async":true,"origin":"schedule","automation":{"kind":"hook","risk":"dangerous"}}`,
		`{"workspace":` + quote(workspace) + `,"prompt":"subagent kind mismatch","natural":true,"run_async":true,"origin":"subagent","automation":{"kind":"schedule","risk":"dangerous"}}`,
		`{"workspace":` + quote(workspace) + `,"prompt":"unknown origin","natural":true,"run_async":true,"origin":"timer","automation":{"kind":"schedule","risk":"dangerous"}}`,
	} {
		resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			_ = resp.Body.Close()
			t.Fatalf("expected invalid automation request to return 400, got %d for %s", resp.StatusCode, body)
		}
		_ = resp.Body.Close()
	}
	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("invalid automation requests inserted tasks: %#v", tasks)
	}
}

func TestServerBackgroundControls_limitConcurrencyAndExposeCancelHandles(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Background: BackgroundLimits{MaxConcurrent: 1, MaxActive: 4},
	}))
	defer server.Close()

	spawn := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run spawn","natural":false,"run_async":true,"origin":"background","automation":{"kind":"background","risk":"safe"}}`)
	waitBackgroundStarted(t, executor)
	if spawn.Task.Origin != taskpkg.OriginBackground {
		t.Fatalf("unexpected spawn task origin %#v", spawn.Task)
	}

	schedule := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run schedule","natural":false,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe","source":"cron:hourly"}}`)
	if schedule.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected safe schedule to queue behind running spawn, got %#v", schedule.Task)
	}

	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "parent", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	subagent := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run subagent","natural":false,"run_async":true,"origin":"subagent","parent_task_id":`+quote(parent.ID)+`,"automation":{"kind":"subagent","risk":"safe","source":"parent"}}`)
	if subagent.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected safe subagent to queue behind running spawn, got %#v", subagent.Task)
	}

	cancelTask(t, server.URL, spawn.Task.ID, "free slot")
	waitBackgroundStarted(t, executor)
	scheduleAfterStart, err := repo.Get(t.Context(), schedule.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scheduleAfterStart.Status != taskpkg.StatusPlanning && scheduleAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected queued schedule to receive the next running handle, got %#v", scheduleAfterStart)
	}
	cancelTask(t, server.URL, schedule.Task.ID, "done with schedule")
	waitBackgroundStarted(t, executor)
	cancelTask(t, server.URL, subagent.Task.ID, "done with subagent")
}

func TestServerBackgroundControls_resourceLimitCancelLostAndRecover(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Background: BackgroundLimits{MaxConcurrent: 1, MaxActive: 2},
	}))
	defer server.Close()

	first := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run first","natural":false,"run_async":true,"origin":"background","automation":{"kind":"background","risk":"safe"}}`)
	waitBackgroundStarted(t, executor)
	second := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run second","natural":false,"run_async":true,"origin":"background","automation":{"kind":"background","risk":"safe"}}`)
	if second.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected second background task queued, got %#v", second.Task)
	}
	rejectResp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(workspace)+`,"prompt":"run third","natural":false,"run_async":true,"origin":"background","automation":{"kind":"background","risk":"safe"}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = rejectResp.Body.Close()
	if rejectResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected resource-limited background task to return 429, got %d", rejectResp.StatusCode)
	}
	cancelTask(t, server.URL, second.Task.ID, "cancel queued task")
	secondAfterCancel, err := repo.Get(t.Context(), second.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondAfterCancel.Status != taskpkg.StatusCancelled {
		t.Fatalf("expected queued task cancel to persist, got %#v", secondAfterCancel)
	}
	cancelTask(t, server.URL, first.Task.ID, "stop first")

	stale, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  workspace,
		Prompt:     "run recovered",
		Natural:    false,
		RunAsync:   true,
		Origin:     taskpkg.OriginBackground,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindBackground, Risk: taskpkg.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), stale.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	recoveryServer := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Background: BackgroundLimits{MaxConcurrent: 1, MaxActive: 2},
	}))
	defer recoveryServer.Close()
	lost, err := repo.Get(t.Context(), stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lost.Status != taskpkg.StatusLost {
		t.Fatalf("expected stale running background task to be marked lost on daemon start, got %#v", lost)
	}
	resp, err := http.Post(recoveryServer.URL+"/v1/tasks/"+stale.ID+"/recover", "application/json", strings.NewReader(`{"reason":"resume after restart"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected recover status %d", resp.StatusCode)
	}
	waitBackgroundStarted(t, executor)
	events, err := repo.Events(t.Context(), stale.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPayloadStatus(events, taskpkg.StatusLost) || !hasPayloadStatus(events, taskpkg.StatusRecovered) {
		t.Fatalf("expected lost and recovered event payloads, got %#v", events)
	}
	cancelTask(t, recoveryServer.URL, stale.ID, "cleanup recovered")
}

func TestServerForegroundThreadScheduler_allowsCrossThreadParallelismAndSessionFIFO(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Store:      st,
		Foreground: ForegroundLimits{MaxConcurrent: 2, MaxActive: 5},
	}))
	defer server.Close()
	firstThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Thread A"})
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Thread B"})
	if err != nil {
		t.Fatal(err)
	}

	first := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"run first","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	if first.Task.SessionID != firstThread.ID {
		t.Fatalf("expected thread task to reuse thread id as session id, got %#v", first.Task)
	}
	second := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(secondThread.ID)+`,"prompt":"run second","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	if second.Task.SessionID != secondThread.ID {
		t.Fatalf("expected second thread task to reuse thread id as session id, got %#v", second.Task)
	}

	sameThread := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"run third","natural":false,"run_async":true}`)
	if sameThread.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected same-thread foreground turn to queue FIFO, got %#v", sameThread.Task)
	}
	updatedThread, err := st.GetConversationThread(firstThread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedThread.LastTaskID != sameThread.Task.ID {
		t.Fatalf("expected thread last task to track queued turn %q, got %q", sameThread.Task.ID, updatedThread.LastTaskID)
	}

	cancelTask(t, server.URL, first.Task.ID, "free thread slot")
	waitBackgroundStarted(t, executor)
	sameAfterStart, err := repo.Get(t.Context(), sameThread.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sameAfterStart.Status != taskpkg.StatusPlanning && sameAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected same-thread queued turn to start after first turn cancelled, got %#v", sameAfterStart)
	}
	cancelTask(t, server.URL, second.Task.ID, "cleanup second")
	cancelTask(t, server.URL, sameThread.Task.ID, "cleanup same-thread")
}

func TestServerForegroundThreadScheduler_limitsPerWorkspaceWithoutStarvingOtherWorkspaces(t *testing.T) {
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Store:      st,
		Foreground: ForegroundLimits{MaxConcurrent: 2, MaxActive: 6, MaxPerWorkspace: 1},
	}))
	defer server.Close()
	firstThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspaceA, Title: "Workspace A first"})
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspaceA, Title: "Workspace A queued"})
	if err != nil {
		t.Fatal(err)
	}
	otherThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspaceB, Title: "Workspace B runnable"})
	if err != nil {
		t.Fatal(err)
	}

	first := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspaceA)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"run first","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	second := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspaceA)+`,"thread_id":`+quote(secondThread.ID)+`,"prompt":"run second","natural":false,"run_async":true}`)
	if second.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected same-workspace foreground over workspace cap to queue, got %#v", second.Task)
	}
	if !taskQueuedMessageContains(t, repo, second.Task.ID, "workspace concurrency limit") {
		t.Fatalf("expected queued event to expose workspace limit reason")
	}

	other := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspaceB)+`,"thread_id":`+quote(otherThread.ID)+`,"prompt":"run other","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	otherAfterStart, err := repo.Get(t.Context(), other.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if otherAfterStart.Status != taskpkg.StatusPlanning && otherAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected other workspace task to run while workspace A is capped, got %#v", otherAfterStart)
	}

	cancelTask(t, server.URL, first.Task.ID, "free workspace slot")
	waitBackgroundStarted(t, executor)
	secondAfterStart, err := repo.Get(t.Context(), second.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondAfterStart.Status != taskpkg.StatusPlanning && secondAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected same-workspace queued task to start after slot frees, got %#v", secondAfterStart)
	}
	cancelTask(t, server.URL, other.Task.ID, "cleanup other")
	cancelTask(t, server.URL, second.Task.ID, "cleanup second")
}

func TestServerForegroundThreadScheduler_queuesOverCapAndFailsClosed(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Store:      st,
		Foreground: ForegroundLimits{MaxConcurrent: 1, MaxActive: 5},
	}))
	defer server.Close()
	firstThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Thread A"})
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Thread B"})
	if err != nil {
		t.Fatal(err)
	}

	first := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"run first","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	second := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(secondThread.ID)+`,"prompt":"run second","natural":false,"run_async":true}`)
	if second.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected cross-thread foreground over scheduler cap to queue, got %#v", second.Task)
	}
	workbenchResp, err := http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatal(err)
	}
	defer workbenchResp.Body.Close()
	var workbench taskpkg.Workbench
	if err := json.NewDecoder(workbenchResp.Body).Decode(&workbench); err != nil {
		t.Fatal(err)
	}
	if !taskListContainsID(workbench.ActiveTasks, first.Task.ID) || !taskListContainsID(workbench.QueuedTasks, second.Task.ID) {
		t.Fatalf("expected workbench to expose active first and queued second, got %#v", workbench)
	}
	sameThread := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"run third","natural":false,"run_async":true}`)
	if sameThread.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected later same-thread turn to queue, got %#v", sameThread.Task)
	}

	for _, body := range []string{
		`{"workspace":` + quote(workspace) + `,"thread_id":"   ","prompt":"run blank","natural":false,"run_async":true}`,
		`{"workspace":` + quote(t.TempDir()) + `,"thread_id":` + quote(firstThread.ID) + `,"prompt":"run mismatch","natural":false,"run_async":true}`,
		`{"workspace":` + quote(workspace) + `,"thread_id":` + quote(firstThread.ID) + `,"session_id":"session-other","prompt":"run mismatched session","natural":false,"run_async":true}`,
	} {
		resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid thread request to fail closed with 400, got %d for %s", resp.StatusCode, body)
		}
	}

	cancelTask(t, server.URL, first.Task.ID, "free first slot")
	waitBackgroundStarted(t, executor)
	secondAfterStart, err := repo.Get(t.Context(), second.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondAfterStart.Status != taskpkg.StatusPlanning && secondAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected oldest eligible cross-thread task to start first, got %#v", secondAfterStart)
	}
	firstAfterCancel, err := repo.Get(t.Context(), first.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAfterCancel.Status != taskpkg.StatusCancelled {
		t.Fatalf("expected cancelled source thread task to stay cancelled, got %#v", firstAfterCancel)
	}
	sameStillQueued, err := repo.Get(t.Context(), sameThread.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sameStillQueued.Status != taskpkg.StatusQueued {
		t.Fatalf("expected later same-thread turn not to overtake older queued thread, got %#v", sameStillQueued)
	}
	cancelTask(t, server.URL, second.Task.ID, "free second slot")
	waitBackgroundStarted(t, executor)
	sameAfterStart, err := repo.Get(t.Context(), sameThread.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sameAfterStart.Status != taskpkg.StatusPlanning && sameAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected same-thread queued task to start after older queued thread exits, got %#v", sameAfterStart)
	}
	cancelTask(t, server.URL, sameThread.Task.ID, "cleanup same-thread")
}

func TestServerThreadRaceLeakRegression_concurrentEvalCancelAndFanIn(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	handler := newServer(Config{
		Repository: repo,
		Runner:     runner,
		Store:      st,
		Foreground: ForegroundLimits{MaxConcurrent: 2, MaxActive: 4},
	})
	server := httptest.NewServer(handler.routes())
	defer server.Close()

	firstThread := createTestThread(t, server.URL, workspace, "race-first")
	secondThread := createTestThread(t, server.URL, workspace, "race-second")
	thirdThread := createTestThread(t, server.URL, workspace, "race-third")
	first := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"run first","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	second := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(secondThread.ID)+`,"prompt":"run second","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	third := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thirdThread.ID)+`,"prompt":"run third","natural":false,"run_async":true}`)
	if third.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected third concurrent thread task to queue at foreground cap, got %#v", third.Task)
	}

	streamCtx, cancelStream := context.WithCancel(t.Context())
	defer cancelStream()
	streamDone := make(chan streamResult, 1)
	streamURL := server.URL + "/v1/tasks/events/stream?task_id=" + url.QueryEscape(first.Task.ID) + "&task_id=" + url.QueryEscape(second.Task.ID) + "&task_id=" + url.QueryEscape(third.Task.ID)
	go readStreamUntil(streamCtx, streamURL, third.Task.ID, taskpkg.EventTaskQueued, streamDone)
	result := waitStreamResult(t, streamDone)
	if !strings.Contains(result.body, third.Task.ID) || !strings.Contains(result.body, "event: "+string(taskpkg.EventTaskQueued)) {
		t.Fatalf("fan-in stream did not expose queued third task:\n%s", result.body)
	}

	cancelTask(t, server.URL, first.Task.ID, "free one foreground slot")
	waitBackgroundStarted(t, executor)
	waitUntil(t, 3*time.Second, func() bool {
		firstAfter, err := repo.Get(t.Context(), first.Task.ID)
		if err != nil || firstAfter.Status != taskpkg.StatusCancelled {
			return false
		}
		secondAfter, err := repo.Get(t.Context(), second.Task.ID)
		if err != nil || !isForegroundActive(secondAfter.Status) {
			return false
		}
		thirdAfter, err := repo.Get(t.Context(), third.Task.ID)
		return err == nil && isForegroundActive(thirdAfter.Status)
	})

	cancelTask(t, server.URL, second.Task.ID, "cleanup second")
	cancelTask(t, server.URL, third.Task.ID, "cleanup third")
	waitUntil(t, 3*time.Second, func() bool {
		return !handler.isRunning(first.Task.ID) && !handler.isRunning(second.Task.ID) && !handler.isRunning(third.Task.ID)
	})
}

func TestServerScheduleWorkspaceConflicts_coalescesCatchUpBehindForeground(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Store:      st,
		Background: BackgroundLimits{MaxConcurrent: 1, MaxActive: 5},
		Foreground: ForegroundLimits{MaxConcurrent: 1, MaxActive: 5},
	}))
	defer server.Close()
	thread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Foreground"})
	if err != nil {
		t.Fatal(err)
	}
	foreground := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thread.ID)+`,"prompt":"run foreground","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)

	schedule := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run catchup","natural":false,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe","source":"schedule:nightly","trigger":"0 2 * * *"},"schedule":{"id":"schedule-nightly","missed_runs":5}}`)
	if schedule.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected catch-up schedule to queue behind workspace foreground work, got %#v", schedule.Task)
	}
	payload := schedulePayload(t, repo, schedule.Task.ID)
	if payload.ID != "schedule-nightly" || payload.MissedRuns != 5 || payload.CatchUpPolicy != string(taskpkg.ScheduleCatchUpPolicyRunOnce) || payload.CatchUpRuns != 1 {
		t.Fatalf("expected bounded run-once catch-up payload, got %#v", payload)
	}

	cancelTask(t, server.URL, foreground.Task.ID, "free foreground workspace slot")
	waitBackgroundStarted(t, executor)
	scheduleAfterStart, err := repo.Get(t.Context(), schedule.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scheduleAfterStart.Status != taskpkg.StatusPlanning && scheduleAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected schedule catch-up to start after foreground clears, got %#v", scheduleAfterStart)
	}
	cancelTask(t, server.URL, schedule.Task.ID, "cleanup schedule")
}

func TestServerScheduleWorkspaceConflicts_rejectsMalformedAndPreventsOvertake(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     runner,
		Store:      st,
		Background: BackgroundLimits{MaxConcurrent: 1, MaxActive: 8},
		Foreground: ForegroundLimits{MaxConcurrent: 1, MaxActive: 5},
	}))
	defer server.Close()
	thread, err := st.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Foreground"})
	if err != nil {
		t.Fatal(err)
	}
	first := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thread.ID)+`,"prompt":"run first","natural":false,"run_async":true}`)
	waitBackgroundStarted(t, executor)
	second := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thread.ID)+`,"prompt":"run second","natural":false,"run_async":true}`)
	if second.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected second foreground turn queued, got %#v", second.Task)
	}
	schedule := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run catchup","natural":false,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe","source":"schedule:hourly","trigger":"0 * * * *"},"schedule":{"id":"schedule-hourly","catch_up_policy":"limited","missed_runs":99,"max_catch_up_runs":50}}`)
	if schedule.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected schedule catch-up queued behind foreground, got %#v", schedule.Task)
	}
	payload := schedulePayload(t, repo, schedule.Task.ID)
	if payload.CatchUpPolicy != string(taskpkg.ScheduleCatchUpPolicyLimited) || payload.MissedRuns != 99 || payload.CatchUpRuns != taskpkg.ScheduleCatchUpDefaultMax {
		t.Fatalf("expected limited catch-up to clamp to default max, got %#v", payload)
	}
	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "parent", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	subagent := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"prompt":"run child","natural":false,"run_async":true,"origin":"subagent","parent_task_id":`+quote(parent.ID)+`,"automation":{"kind":"subagent","risk":"safe","source":"parent"}}`)
	if subagent.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected subagent queued behind workspace foreground work, got %#v", subagent.Task)
	}

	for _, body := range []string{
		`{"workspace":` + quote(workspace) + `,"prompt":"run bad policy","natural":false,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe"},"schedule":{"id":"bad","catch_up_policy":"infinite","missed_runs":2}}`,
		`{"workspace":` + quote(workspace) + `,"prompt":"run negative","natural":false,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe"},"schedule":{"id":"bad","missed_runs":-1}}`,
		`{"workspace":` + quote(workspace) + `,"prompt":"run wrong origin","natural":false,"run_async":true,"origin":"background","automation":{"kind":"background","risk":"safe"},"schedule":{"id":"bad","missed_runs":1}}`,
		`{"workspace":` + quote(otherWorkspace) + `,"prompt":"run cross workspace child","natural":false,"run_async":true,"origin":"subagent","parent_task_id":` + quote(parent.ID) + `,"automation":{"kind":"subagent","risk":"safe","source":"parent"}}`,
	} {
		resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected malformed schedule/subagent request to fail closed with 400, got %d for %s", resp.StatusCode, body)
		}
	}

	cancelTask(t, server.URL, first.Task.ID, "free first foreground slot")
	waitBackgroundStarted(t, executor)
	secondAfterStart, err := repo.Get(t.Context(), second.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondAfterStart.Status != taskpkg.StatusPlanning && secondAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected queued foreground to start before schedule/subagent, got %#v", secondAfterStart)
	}
	scheduleStillQueued, err := repo.Get(t.Context(), schedule.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scheduleStillQueued.Status != taskpkg.StatusQueued && scheduleStillQueued.Status != taskpkg.StatusDraft {
		t.Fatalf("expected schedule not to overtake queued foreground, got %#v", scheduleStillQueued)
	}
	cancelTask(t, server.URL, second.Task.ID, "free second foreground slot")
	waitBackgroundStarted(t, executor)
	scheduleAfterStart, err := repo.Get(t.Context(), schedule.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scheduleAfterStart.Status != taskpkg.StatusPlanning && scheduleAfterStart.Status != taskpkg.StatusRunning {
		t.Fatalf("expected schedule to start after foreground queue clears, got %#v", scheduleAfterStart)
	}
	cancelTask(t, server.URL, schedule.Task.ID, "cleanup schedule")
	waitBackgroundStarted(t, executor)
	cancelTask(t, server.URL, subagent.Task.ID, "cleanup subagent")
}

func TestServerRestartRecovery_explainsQueuedWaitingBackgroundAndScheduleState(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	queuedForeground, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "queued foreground", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queue(t.Context(), queuedForeground.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "waiting approval", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), waiting.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), waiting.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed", Status: string(taskpkg.StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}
	background, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  workspace,
		Prompt:     "run background",
		Natural:    false,
		Origin:     taskpkg.OriginBackground,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindBackground, Risk: taskpkg.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), background.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	schedule, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  workspace,
		Prompt:     "run schedule",
		Natural:    false,
		Origin:     taskpkg.OriginSchedule,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindSchedule, Risk: taskpkg.AutomationRiskSafe, Source: "schedule:nightly", Trigger: "0 2 * * *"},
		Schedule:   taskpkg.ScheduleMetadata{ID: "schedule-nightly", MissedRuns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queue(t.Context(), schedule.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), schedule.ID, taskpkg.EventScheduleTriggered, taskpkg.EventPayload{ID: "schedule-nightly", Message: "catch up", Status: string(taskpkg.StatusQueued)}); err != nil {
		t.Fatal(err)
	}

	_ = newServer(Config{Repository: repo})
	for _, item := range []struct {
		id     string
		status taskpkg.Status
	}{
		{queuedForeground.ID, taskpkg.StatusQueued},
		{waiting.ID, taskpkg.StatusWaitingUser},
		{background.ID, taskpkg.StatusLost},
		{schedule.ID, taskpkg.StatusQueued},
	} {
		task, err := repo.Get(t.Context(), item.id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != item.status {
			t.Fatalf("expected %s to recover as %s, got %#v", item.id, item.status, task)
		}
		events, err := repo.Events(t.Context(), item.id, 20)
		if err != nil {
			t.Fatal(err)
		}
		if !hasRestartRecoveryPayload(events, item.status) && item.status != taskpkg.StatusLost {
			t.Fatalf("expected restart recovery explanation for %s, got %#v", item.id, events)
		}
		if item.status == taskpkg.StatusLost && !hasPayloadStatus(events, taskpkg.StatusLost) {
			t.Fatalf("expected lost explanation for %s, got %#v", item.id, events)
		}
	}
}

func TestServerRestartRecovery_isIdempotentAndIgnoresTerminalAndMalformedHistory(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	queued, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "queued", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queue(t.Context(), queued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_events (id, task_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?)`, "event_bad_restart_payload", queued.ID, string(taskpkg.EventTaskQueued), "{", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	terminal, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "done", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), terminal.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	_ = newServer(Config{Repository: repo})
	firstEvents, err := repo.Events(t.Context(), queued.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	firstCount := countRestartRecoveryPayloads(firstEvents, taskpkg.StatusQueued)
	if firstCount != 1 {
		t.Fatalf("expected one restart recovery event after first startup, got %d events=%#v", firstCount, firstEvents)
	}
	_ = newServer(Config{Repository: repo})
	secondEvents, err := repo.Events(t.Context(), queued.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	secondCount := countRestartRecoveryPayloads(secondEvents, taskpkg.StatusQueued)
	if secondCount != firstCount {
		t.Fatalf("expected restart recovery to be idempotent, before=%d after=%d events=%#v", firstCount, secondCount, secondEvents)
	}
	terminalAfter, err := repo.Get(t.Context(), terminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminalAfter.Status != taskpkg.StatusCompleted {
		t.Fatalf("expected terminal task to remain completed, got %#v", terminalAfter)
	}
}

func TestServerRestartWorkbenchListsBackgroundTasksAndOutputs(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	queued := createBackgroundTaskForWorkbench(t, repo, workspace, "queued background", taskpkg.StatusQueued)
	lost := createBackgroundTaskForWorkbench(t, repo, workspace, "running background", taskpkg.StatusRunning)
	completed := createBackgroundTaskForWorkbench(t, repo, workspace, "completed background", taskpkg.StatusCompleted)
	other := createBackgroundTaskForWorkbench(t, repo, otherWorkspace, "other background", taskpkg.StatusCompleted)
	foreground, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "foreground done", Natural: true, Origin: taskpkg.OriginForeground})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), foreground.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), queued.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "run", Output: "queued output before restart", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	artifactURI := "artifact://artifacts/sessions/" + completed.SessionID + "/tasks/" + completed.ID + "/tool-results/out.txt"
	if err := repo.AppendEvent(t.Context(), completed.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "run", Output: "completed output before restart", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), completed.ID, taskpkg.EventArtifactReference, taskpkg.EventPayload{Tool: "run", Path: artifactURI, Message: "completed artifact output"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 110; i++ {
		if err := repo.AppendEvent(t.Context(), completed.ID, taskpkg.EventAssistantDelta, taskpkg.EventPayload{Message: "streaming"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AppendEvent(t.Context(), completed.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "run", Output: "completed output before restart", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), lost.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "run", Output: "lost output before restart", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	var workbench taskpkg.Workbench
	resp, err := http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace) + "&limit=20")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected workbench status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&workbench); err != nil {
		t.Fatal(err)
	}
	if !taskListContainsID(workbench.BackgroundTasks, queued.ID) || !taskListContainsID(workbench.BackgroundTasks, lost.ID) || !taskListContainsID(workbench.BackgroundTasks, completed.ID) {
		t.Fatalf("expected all workspace background tasks, got %#v", workbench.BackgroundTasks)
	}
	if taskListContainsID(workbench.BackgroundTasks, other.ID) || taskListContainsID(workbench.BackgroundTasks, foreground.ID) {
		t.Fatalf("background tasks leaked other workspace or foreground tasks: %#v", workbench.BackgroundTasks)
	}
	if !taskListContainsID(workbench.BackgroundUnfinishedTasks, queued.ID) || !taskListContainsID(workbench.BackgroundLostTasks, lost.ID) || !taskListContainsID(workbench.BackgroundCompletedTasks, completed.ID) {
		t.Fatalf("unexpected background buckets unfinished=%#v lost=%#v completed=%#v", workbench.BackgroundUnfinishedTasks, workbench.BackgroundLostTasks, workbench.BackgroundCompletedTasks)
	}
	lostOutput, ok := backgroundOutputForTask(workbench.BackgroundOutputs, lost.ID)
	if !ok || lostOutput.Status != taskpkg.StatusLost || !strings.Contains(lostOutput.Output, "lost output before restart") {
		t.Fatalf("expected lost output summary, ok=%t output=%#v", ok, lostOutput)
	}
	completedOutput, ok := backgroundOutputForTask(workbench.BackgroundOutputs, completed.ID)
	if !ok || completedOutput.ArtifactURI != artifactURI || completedOutput.ArtifactTailHint != "/artifact "+artifactURI+" tail" || !strings.Contains(completedOutput.Output, "completed output before restart") {
		t.Fatalf("expected completed artifact output summary, ok=%t output=%#v", ok, completedOutput)
	}
}

func TestServerRestartWorkbenchBackgroundOutputsAreScopedAndStable(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	noOutput := createBackgroundTaskForWorkbench(t, repo, workspace, "draft with no output", taskpkg.StatusDraft)
	malformed := createBackgroundTaskForWorkbench(t, repo, workspace, "malformed output", taskpkg.StatusCompleted)
	foreground, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "foreground", Natural: true, Origin: taskpkg.OriginForeground})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), foreground.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_events (id, task_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?)`, "event_bad_background_output", malformed.ID, string(taskpkg.EventToolResult), "{", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	first := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer first.Close()
	limitedWorkbench := getWorkbench(t, first.URL, workspace, 1)
	firstWorkbench := getWorkbench(t, first.URL, workspace, 10)
	firstEvents, err := repo.Events(t.Context(), noOutput.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	firstCount := countRestartRecoveryPayloads(firstEvents, taskpkg.StatusDraft)
	second := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer second.Close()
	secondWorkbench := getWorkbench(t, second.URL, workspace, 10)

	if len(limitedWorkbench.BackgroundTasks) > 1 {
		t.Fatalf("limit should bound background task listing, got %#v", limitedWorkbench.BackgroundTasks)
	}
	if taskListContainsID(firstWorkbench.BackgroundTasks, foreground.ID) {
		t.Fatalf("foreground task leaked into background listing: %#v", firstWorkbench.BackgroundTasks)
	}
	output, ok := backgroundOutputForTask(secondWorkbench.BackgroundOutputs, noOutput.ID)
	if !ok || output.Output != "" || output.ArtifactURI != "" {
		t.Fatalf("missing output should produce empty-but-present summary, ok=%t output=%#v", ok, output)
	}
	secondEvents, err := repo.Events(t.Context(), noOutput.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	secondCount := countRestartRecoveryPayloads(secondEvents, taskpkg.StatusDraft)
	if firstCount != secondCount {
		t.Fatalf("restart recovery should stay idempotent, before=%d after=%d", firstCount, secondCount)
	}
}

func createBackgroundTaskForWorkbench(t *testing.T, repo *taskpkg.Repository, workspace string, prompt string, status taskpkg.Status) taskpkg.Task {
	t.Helper()
	created, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  workspace,
		Prompt:     prompt,
		Natural:    false,
		Origin:     taskpkg.OriginBackground,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindBackground, Risk: taskpkg.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	switch status {
	case taskpkg.StatusDraft:
	case taskpkg.StatusQueued:
		if err := repo.Queue(t.Context(), created.ID); err != nil {
			t.Fatal(err)
		}
	default:
		if err := repo.UpdateStatus(t.Context(), created.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	task, err := repo.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func getWorkbench(t *testing.T, baseURL string, workspace string, limit int) taskpkg.Workbench {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/workbench?workspace=" + url.QueryEscape(workspace) + "&limit=" + strconv.Itoa(limit))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected workbench status %d", resp.StatusCode)
	}
	var workbench taskpkg.Workbench
	if err := json.NewDecoder(resp.Body).Decode(&workbench); err != nil {
		t.Fatal(err)
	}
	return workbench
}

func backgroundOutputForTask(outputs []taskpkg.BackgroundTaskOutput, taskID string) (taskpkg.BackgroundTaskOutput, bool) {
	for _, output := range outputs {
		if output.TaskID == taskID {
			return output, true
		}
	}
	return taskpkg.BackgroundTaskOutput{}, false
}

func TestServerSessionTerminalIsolation_keepsQueuedWaitsAndSubagentTerminalsIndependent(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	handler := newServer(Config{Repository: repo})
	server := httptest.NewServer(handler.routes())
	defer server.Close()
	blocker, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "active", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), blocker.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	queued, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "queued", SessionID: blocker.SessionID, Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queue(t.Context(), queued.ID); err != nil {
		t.Fatal(err)
	}
	approval, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "approval", SessionID: blocker.SessionID, Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), approval.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), approval.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed", Status: string(taskpkg.StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}
	input, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "input", SessionID: blocker.SessionID, Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), input.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), input.ID, taskpkg.EventUserInputRequest, taskpkg.EventPayload{Message: "which file?", Status: string(taskpkg.StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}
	subagent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:      workspace,
		Prompt:         "subagent",
		SessionID:      blocker.SessionID,
		Natural:        false,
		Origin:         taskpkg.OriginSubagent,
		ParentTaskID:   blocker.ID,
		Automation:     taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindSubagent, Risk: taskpkg.AutomationRiskSafe},
		ApprovalGrants: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), subagent.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}

	cancelTask(t, server.URL, queued.ID, "drop queued")
	if err := repo.UpdateStatus(t.Context(), blocker.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	handler.startNextQueuedAfter(blocker.ID)
	cancelTask(t, server.URL, approval.ID, "approval no longer needed")
	inputResp, err := http.Post(server.URL+"/v1/tasks/"+input.ID+"/input", "application/json", strings.NewReader(`{"message":"notes.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = inputResp.Body.Close()
	if inputResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected input resume accepted, got %d", inputResp.StatusCode)
	}
	cancelTask(t, server.URL, subagent.ID, "stop child")

	want := map[string]taskpkg.Status{
		blocker.ID:  taskpkg.StatusCompleted,
		queued.ID:   taskpkg.StatusCancelled,
		approval.ID: taskpkg.StatusCancelled,
		input.ID:    taskpkg.StatusDraft,
		subagent.ID: taskpkg.StatusCancelled,
	}
	for id, status := range want {
		task, err := repo.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != status {
			t.Fatalf("expected %s status %s, got %#v", id, status, task)
		}
	}
}

func TestServerSessionTerminalIsolation_rejectsResumeAfterTerminalStates(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()
	approval, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "approval", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), approval.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), approval.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed", Status: string(taskpkg.StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}
	cancelTask(t, server.URL, approval.ID, "terminal")
	approvalResp, err := http.Post(server.URL+"/v1/tasks/"+approval.ID+"/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = approvalResp.Body.Close()
	if approvalResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected terminal approval resume to conflict, got %d", approvalResp.StatusCode)
	}
	input, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "input", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), input.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), input.ID, taskpkg.EventUserInputRequest, taskpkg.EventPayload{Message: "which file?", Status: string(taskpkg.StatusWaitingUser), ExpiresAt: formatTestTime(time.Now().Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.MarkExpiredTasksStale(t.Context(), time.Now(), "expired"); err != nil {
		t.Fatal(err)
	}
	inputResp, err := http.Post(server.URL+"/v1/tasks/"+input.ID+"/input", "application/json", strings.NewReader(`{"message":"notes.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = inputResp.Body.Close()
	if inputResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected stale input resume to conflict, got %d", inputResp.StatusCode)
	}
	for id, status := range map[string]taskpkg.Status{approval.ID: taskpkg.StatusCancelled, input.ID: taskpkg.StatusStale} {
		task, err := repo.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != status {
			t.Fatalf("expected terminal status %s for %s, got %#v", status, id, task)
		}
	}
}

func formatTestTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func hasRestartRecoveryPayload(events []taskpkg.Event, status taskpkg.Status) bool {
	return countRestartRecoveryPayloads(events, status) > 0
}

func countRestartRecoveryPayloads(events []taskpkg.Event, status taskpkg.Status) int {
	count := 0
	for _, event := range events {
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		if payload.Reason == "restart_recovery" && payload.Status == string(status) {
			count++
		}
	}
	return count
}

func schedulePayload(t *testing.T, repo *taskpkg.Repository, taskID string) taskpkg.EventPayload {
	t.Helper()
	events, err := repo.Events(t.Context(), taskID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != taskpkg.EventScheduleTriggered {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatalf("missing schedule.triggered event for %s: %#v", taskID, events)
	return taskpkg.EventPayload{}
}

func waitBackgroundStarted(t *testing.T, executor *backgroundBlockingExecutor) {
	t.Helper()
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background task did not receive a running handle")
	}
}

type streamResult struct {
	body string
	err  error
}

func readStreamUntil(ctx context.Context, streamURL string, taskID string, eventType taskpkg.EventType, done chan<- streamResult) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		done <- streamResult{err: err}
		return
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		done <- streamResult{err: err}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		done <- streamResult{err: errors.New(resp.Status)}
		return
	}
	var body strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		body.WriteString(line)
		body.WriteByte('\n')
		if strings.Contains(body.String(), taskID) && strings.Contains(body.String(), "event: "+string(eventType)) {
			done <- streamResult{body: body.String()}
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		done <- streamResult{body: body.String(), err: err}
		return
	}
	done <- streamResult{body: body.String()}
}

func waitStreamResult(t *testing.T, done <-chan streamResult) streamResult {
	t.Helper()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("event stream did not reach expected frame")
		return streamResult{}
	}
}

func taskQueuedMessageContains(t *testing.T, repo *taskpkg.Repository, taskID string, text string) bool {
	t.Helper()
	events, err := repo.Events(t.Context(), taskID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != taskpkg.EventTaskQueued {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload.Message, text) {
			return true
		}
	}
	return false
}

func taskListContainsID(tasks []taskpkg.Task, taskID string) bool {
	for _, task := range tasks {
		if task.ID == taskID {
			return true
		}
	}
	return false
}

func isForegroundActive(status taskpkg.Status) bool {
	return status == taskpkg.StatusPlanning || status == taskpkg.StatusRunning
}

func cancelTask(t *testing.T, baseURL string, taskID string, reason string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/v1/tasks/"+taskID+"/cancel", "application/json", strings.NewReader(`{"reason":`+quote(reason)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected cancel status %d", resp.StatusCode)
	}
}

func hasPayloadStatus(events []taskpkg.Event, status taskpkg.Status) bool {
	for _, event := range events {
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		if payload.Status == string(status) {
			return true
		}
	}
	return false
}

func permissionRequestedPayload(events []taskpkg.Event) (taskpkg.EventPayload, bool) {
	for _, event := range events {
		if event.Type != taskpkg.EventPermissionRequest {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			return taskpkg.EventPayload{}, false
		}
		return payload, true
	}
	return taskpkg.EventPayload{}, false
}
