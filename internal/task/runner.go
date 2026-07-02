package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/runtime"
	"github.com/Lioooooo123/liora/internal/sandbox"
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

type Runner struct {
	repo       *Repository
	planner    *llm.Planner
	registry   *llm.Registry
	store      *store.Store
	sandboxRun sandbox.Executor
	patchMode  bool
	permission permission.Policy
}

func NewRunner(repo *Repository, planner *llm.Planner) *Runner {
	return &Runner{repo: repo, planner: planner, store: store.New(""), permission: permission.Policy{Mode: permission.ModeAuto}}
}

func (r *Runner) SetSandbox(executor sandbox.Executor) {
	r.sandboxRun = executor
}

func (r *Runner) SetPatchMode(enabled bool) {
	r.patchMode = enabled
}

func (r *Runner) SetPermissionPolicy(policy permission.Policy) {
	r.permission = policy
}

func (r *Runner) SetStore(s *store.Store) {
	if s != nil {
		r.store = s
	}
}

func (r *Runner) SetLLMRegistry(registry *llm.Registry) {
	r.registry = registry
}

func (r *Runner) Run(ctx context.Context, taskID string) error {
	task, err := r.repo.Get(ctx, taskID)
	if err != nil {
		return err
	}
	task, err = r.resolveAndPersistTaskModel(ctx, task)
	if err != nil {
		return err
	}
	if err := r.repo.UpdateStatus(ctx, task.ID, StatusPlanning); err != nil {
		return err
	}
	_ = r.repo.AppendEvent(ctx, task.ID, EventPlanning, r.eventPayloadWithModel(task, EventPayload{Message: "Planning task"}))

	result, err := r.runTask(ctx, task)
	var permissionErr *permission.RequiredError
	if errors.As(err, &permissionErr) {
		if waitErr := r.waitForPermission(ctx, task.ID, permissionErr.Request); waitErr != nil {
			return waitErr
		}
		return nil
	}
	if isContextCancelled(ctx, err) && r.isTaskCancelled(task.ID) {
		return err
	}
	if strings.TrimSpace(result.plannedSteps) != "" {
		if !result.planReadyEmitted {
			_ = r.repo.AppendEvent(ctx, task.ID, EventPlanReady, r.eventPayloadWithModel(task, EventPayload{Steps: result.plannedSteps}))
		}
	}
	if r.sandboxRun != nil && containsRunStep(result.plannedSteps) {
		_ = r.repo.AppendEvent(ctx, task.ID, EventSandboxRun, r.eventPayloadWithModel(task, EventPayload{Message: "shell executor: " + sandbox.Label(r.sandboxRun)}))
	}
	if strings.TrimSpace(result.answer) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventSummary, r.eventPayloadWithModel(task, EventPayload{Message: result.answer}))
	}
	if len(result.events) > 0 {
		if updateErr := r.repo.UpdateStatus(ctx, task.ID, StatusRunning); updateErr != nil && err == nil {
			err = updateErr
		}
		for _, event := range result.events {
			r.appendTraceEvents(ctx, task.ID, event)
		}
	}
	if strings.TrimSpace(result.summary) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventSummary, r.eventPayloadWithModel(task, EventPayload{Message: result.summary}))
	}
	if strings.TrimSpace(result.diff) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventDiff, r.eventPayloadWithModel(task, EventPayload{Diff: result.diff}))
	}
	if err != nil {
		if isContextCancelled(ctx, err) && r.isTaskCancelled(task.ID) {
			return err
		}
		_ = r.fail(ctx, task.ID, err)
		return err
	}
	if r.isTaskCancelled(task.ID) {
		return context.Canceled
	}
	if result.status == agent.StatusWaitingUser {
		return r.waitForUserInput(ctx, task.ID, result.summary)
	}
	if err := r.enforceTodoCompletionGate(ctx, task, result); err != nil {
		_ = r.fail(ctx, task.ID, err)
		return err
	}
	if err := r.repo.UpdateStatus(ctx, task.ID, StatusCompleted); err != nil {
		return err
	}
	return r.repo.AppendEvent(ctx, task.ID, EventCompleted, r.eventPayloadWithModel(task, EventPayload{Status: string(StatusCompleted)}))
}

func (r *Runner) runTask(ctx context.Context, task Task) (runtimeResult, error) {
	mode := sandbox.WorkspaceModeDirect
	if r.patchMode {
		mode = sandbox.WorkspaceModeCopy
	}
	session, err := sandbox.PrepareWorkspace(task.Workspace, mode)
	if err != nil {
		return runtimeResult{}, err
	}
	defer session.Cleanup()
	_ = r.repo.AppendEvent(ctx, task.ID, EventSandboxWorkspace, r.eventPayloadWithModel(task, EventPayload{Message: "workspace mode: " + string(session.Mode)}))
	if task.Natural {
		planner, err := r.plannerForTask(task)
		if err != nil {
			return runtimeResult{}, err
		}
		turnRuntime, err := runtime.New(session.Root, planner, r.store)
		if err != nil {
			return runtimeResult{}, err
		}
		turnRuntime.SetSandbox(r.sandboxRun)
		turnRuntime.SetPermissionChecker(r.permissionChecker(task))
		turnRuntime.SetToolOutputSink(daemonToolOutputSink{
			root:      r.store.Root(),
			repo:      r.repo,
			taskID:    task.ID,
			sessionID: task.SessionID,
		})
		turnRuntime.SetTodoExecutor(repositoryTodoExecutor{
			repo:         r.repo,
			sessionID:    task.SessionID,
			sourceTaskID: task.ID,
		})
		recorder := newRepositoryRecorder(ctx, r, task.ID)
		planReadyEmitted := false
		prompt, err := r.taskPrompt(ctx, task)
		if err != nil {
			return runtimeResult{}, err
		}
		result, err := turnRuntime.SubmitWithOptions(ctx, prompt, runtime.SubmitOptions{
			Recorder: recorder,
			OnPlan: func(steps string) {
				if strings.TrimSpace(steps) == "" {
					return
				}
				planReadyEmitted = true
				_ = r.repo.AppendEvent(ctx, task.ID, EventPlanReady, r.eventPayloadWithModel(task, EventPayload{Steps: steps}))
			},
			OnReplan: func(attempt int, reason string) {
				_ = r.repo.AppendEvent(ctx, task.ID, EventReplanning, r.eventPayloadWithModel(task, EventPayload{
					Message:      fmt.Sprintf("Replanning after failure, attempt %d", attempt),
					Status:       string(StatusPlanning),
					Reason:       reason,
					ReplanReason: reason,
					RetryCount:   max(0, attempt-1),
				}))
			},
		})
		return runtimeResult{
			answer:           result.Answer,
			plannedSteps:     result.PlannedSteps,
			planReadyEmitted: planReadyEmitted,
			summary:          result.AgentResult.Summary,
			status:           result.AgentResult.Status,
			diff:             result.AgentResult.Diff,
		}, err
	}
	workspace, err := tools.NewWorkspace(session.Root)
	if err != nil {
		return runtimeResult{}, err
	}
	recorder := newRepositoryRecorder(ctx, r, task.ID)
	runner := agent.New(workspace, recorder)
	runner.SetPermissionChecker(r.permissionChecker(task))
	runner.SetSkillReader(r.store)
	runner.SetTodoExecutor(repositoryTodoExecutor{
		repo:         r.repo,
		sessionID:    task.SessionID,
		sourceTaskID: task.ID,
	})
	if r.sandboxRun != nil {
		runner.SetShellExecutor(r.sandboxRun)
	}
	result, err := runner.Run(ctx, task.UserInput)
	return runtimeResult{
		plannedSteps: task.UserInput,
		summary:      result.Summary,
		status:       result.Status,
		diff:         result.Diff,
	}, err
}

func (r *Runner) permissionChecker(task Task) permission.Checker {
	policy := r.permission
	policy.Approved = policy.Approved || task.ApprovalGranted
	policy.AllowWritesInPatchMode = r.patchMode
	return policy
}

func (r *Runner) plannerForTask(task Task) (*llm.Planner, error) {
	if r.registry == nil {
		return r.planner, nil
	}
	if task.ModelConfig == nil {
		return r.planner, nil
	}
	planner, _, err := r.registry.Planner(llm.Config{
		Provider: task.ModelConfig.Provider,
		Model:    task.ModelConfig.Model,
		BaseURL:  task.ModelConfig.BaseURL,
		Profile:  task.ModelConfig.Profile,
		TraceLabels: map[string]string{
			"task_id":    task.ID,
			"thread_id":  task.SessionID,
			"workspace":  task.Workspace,
			"llm_source": task.ModelConfig.Source,
		},
	})
	if err != nil {
		return nil, err
	}
	return planner, nil
}

func (r *Runner) eventPayloadWithModel(task Task, payload EventPayload) EventPayload {
	if task.ModelConfig == nil {
		return payload
	}
	payload.Provider = task.ModelConfig.Provider
	payload.Model = task.ModelConfig.Model
	payload.Profile = task.ModelConfig.Profile
	return payload
}

func (r *Runner) resolveAndPersistTaskModel(ctx context.Context, task Task) (Task, error) {
	config, ok, err := r.resolveTaskModelConfig(task)
	if err != nil {
		return task, err
	}
	if !ok {
		return task, nil
	}
	task.ModelConfig = &config
	if err := r.repo.UpdateTaskModelConfig(ctx, task.ID, config); err != nil {
		return task, err
	}
	return task, nil
}

func (r *Runner) resolveTaskModelConfig(task Task) (ModelConfig, bool, error) {
	if r.registry == nil {
		if task.ModelConfig == nil {
			return ModelConfig{}, false, nil
		}
		config := *task.ModelConfig
		config.Source = firstNonEmpty(config.Source, "task_override")
		return config, true, nil
	}
	var request llm.Config
	source := ""
	if defaults, ok := r.registry.DefaultConfig(); ok {
		request = defaults
		source = "global_default"
	}
	if r.store != nil {
		if workspaceConfig, ok, err := r.store.GetWorkspaceModelConfig(task.Workspace); err != nil {
			return ModelConfig{}, false, err
		} else if ok {
			request.Provider = workspaceConfig.Provider
			request.Model = workspaceConfig.Model
			request.BaseURL = workspaceConfig.BaseURL
			request.Profile = workspaceConfig.Profile
			source = "workspace_default"
		}
		if threadConfig, ok := r.resolvedThreadModelConfig(task); ok {
			request.Provider = threadConfig.Provider
			request.Model = threadConfig.Model
			request.BaseURL = threadConfig.BaseURL
			request.Profile = threadConfig.Profile
			source = "thread_override"
		}
	}
	if task.ModelConfig != nil {
		request.Provider = task.ModelConfig.Provider
		request.Model = task.ModelConfig.Model
		request.BaseURL = task.ModelConfig.BaseURL
		request.Profile = task.ModelConfig.Profile
		source = firstNonEmpty(task.ModelConfig.Source, "task_override")
	}
	if strings.TrimSpace(request.Provider) == "" && strings.TrimSpace(request.Model) == "" && strings.TrimSpace(request.Profile) == "" {
		return ModelConfig{}, false, nil
	}
	if r.registry != nil {
		resolved, err := r.registry.Resolve(request)
		if err != nil {
			return ModelConfig{}, false, err
		}
		return ModelConfig{Provider: resolved.Provider, Model: resolved.Model, BaseURL: resolved.BaseURL, Profile: resolved.Profile, Source: source}, true, nil
	}
	return ModelConfig{Provider: request.Provider, Model: request.Model, BaseURL: request.BaseURL, Profile: request.Profile, Source: source}, true, nil
}

func (r *Runner) resolvedThreadModelConfig(task Task) (store.ThreadModelConfig, bool) {
	if r.store == nil || strings.TrimSpace(task.SessionID) == "" {
		return store.ThreadModelConfig{}, false
	}
	thread, err := r.store.GetConversationThread(task.SessionID)
	if err != nil || thread.Workspace != task.Workspace || thread.ModelConfig == nil {
		return store.ThreadModelConfig{}, false
	}
	config := *thread.ModelConfig
	if strings.TrimSpace(config.InheritedFromThreadID) != "" && (strings.TrimSpace(config.Provider) == "" || strings.TrimSpace(config.Model) == "") {
		inherited, err := r.store.GetConversationThread(config.InheritedFromThreadID)
		if err == nil && inherited.Workspace == task.Workspace && inherited.ModelConfig != nil {
			resolved := *inherited.ModelConfig
			resolved.ThreadID = config.ThreadID
			resolved.InheritedFromThreadID = config.InheritedFromThreadID
			config = resolved
		}
	}
	if strings.TrimSpace(config.Provider) == "" && strings.TrimSpace(config.Model) == "" && strings.TrimSpace(config.Profile) == "" {
		return store.ThreadModelConfig{}, false
	}
	return config, true
}

type runtimeResult struct {
	answer           string
	plannedSteps     string
	planReadyEmitted bool
	summary          string
	status           agent.Status
	diff             string
	events           []trace.Event
}

type repositoryRecorder struct {
	ctx    context.Context
	runner *Runner
	taskID string
	once   sync.Once
}

func newRepositoryRecorder(ctx context.Context, runner *Runner, taskID string) *repositoryRecorder {
	return &repositoryRecorder{ctx: ctx, runner: runner, taskID: taskID}
}

func (r *repositoryRecorder) Record(event trace.Event) {
	r.once.Do(func() {
		_ = r.runner.repo.UpdateStatus(r.ctx, r.taskID, StatusRunning)
	})
	r.runner.appendTraceEvents(r.ctx, r.taskID, event)
}

func (r *Runner) appendTraceEvents(ctx context.Context, taskID string, event trace.Event) {
	task, _ := r.repo.Get(ctx, taskID)
	_ = r.repo.AppendEvent(ctx, taskID, EventToolCall, r.eventPayloadWithModel(task, EventPayload{
		Tool:       event.Tool,
		ToolCallID: event.ToolCallID,
		Input:      event.Input,
	}))
	_ = r.repo.AppendEvent(ctx, taskID, EventToolResult, r.eventPayloadWithModel(task, EventPayload{
		Tool:         event.Tool,
		ToolCallID:   event.ToolCallID,
		ToolResultID: event.ToolResultID,
		Input:        event.Input,
		Output:       event.Output,
		Status:       string(event.Status),
	}))
}

type daemonToolOutputSink struct {
	root      string
	repo      *Repository
	taskID    string
	sessionID string
}

func (s daemonToolOutputSink) PersistToolOutput(ctx context.Context, call llm.ToolCall, output string) (string, error) {
	relPath := filepath.ToSlash(filepath.Join(
		"artifacts",
		"sessions",
		safeArtifactSegment(s.sessionID),
		"tasks",
		safeArtifactSegment(s.taskID),
		"tool-results",
		safeArtifactSegment(call.Name+"-"+call.ID)+"-"+newID("artifact")+".txt",
	))
	absPath := filepath.Join(s.root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(absPath, []byte(output), 0o600); err != nil {
		return "", err
	}
	artifactURI := "artifact://" + relPath
	if s.repo != nil && strings.TrimSpace(s.taskID) != "" {
		_ = s.repo.AppendEvent(ctx, s.taskID, EventArtifactReference, EventPayload{
			Tool:       call.Name,
			ToolCallID: call.ID,
			Path:       artifactURI,
			Message:    "full tool output persisted",
		})
	}
	return artifactURI, nil
}

func safeArtifactSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	lastWasDash := false
	for _, r := range value {
		if builder.Len() >= 96 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			builder.WriteRune(r)
			lastWasDash = false
			continue
		}
		if !lastWasDash {
			builder.WriteByte('-')
			lastWasDash = true
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return strings.Trim(builder.String(), "-")
}

func (r *Runner) fail(ctx context.Context, taskID string, err error) error {
	if updateErr := r.repo.UpdateStatus(ctx, taskID, StatusFailed); updateErr != nil {
		return fmt.Errorf("%w; also failed updating task status: %v", err, updateErr)
	}
	task, _ := r.repo.Get(ctx, taskID)
	_ = r.repo.AppendEvent(ctx, taskID, EventError, r.eventPayloadWithModel(task, EventPayload{Message: err.Error(), Status: string(StatusFailed)}))
	return nil
}

func (r *Runner) waitForPermission(ctx context.Context, taskID string, request permission.Request) error {
	if updateErr := r.repo.UpdateStatus(ctx, taskID, StatusWaitingUser); updateErr != nil {
		return updateErr
	}
	task, _ := r.repo.Get(ctx, taskID)
	_ = r.repo.AppendEvent(ctx, taskID, EventPermissionRequest, r.eventPayloadWithModel(task, EventPayload{
		Message:    "Approval required before continuing.",
		Tool:       request.Tool,
		ToolCallID: newID("toolcall"),
		Input:      request.Input,
		Status:     string(StatusWaitingUser),
		Risk:       request.Risk,
		Reason:     request.Reason,
		ExpiresAt:  ExpiresAtAfter(DefaultWaitExpiry),
	}))
	return nil
}

func (r *Runner) isTaskCancelled(taskID string) bool {
	task, err := r.repo.Get(context.Background(), taskID)
	return err == nil && task.Status == StatusCancelled
}

func isContextCancelled(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
}
