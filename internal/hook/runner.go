package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTimeout     = 10 * time.Second
	defaultOutputLimit = 64 * 1024
)

type RunnerConfig struct {
	Timeout     time.Duration
	OutputLimit int
}

type Runner struct {
	registry *Registry
	config   RunnerConfig
}

type RunInput struct {
	Workspace string
	Payload   string
}

type RunError struct {
	HookID string
	Status RunStatus
	Detail string
}

func (e *RunError) Error() string {
	return fmt.Sprintf("hook %s failed: %s", e.HookID, e.Detail)
}

func NewRunner(registry *Registry, config RunnerConfig) *Runner {
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.OutputLimit <= 0 {
		config.OutputLimit = defaultOutputLimit
	}
	return &Runner{registry: registry, config: config}
}

func (r *Runner) Run(ctx context.Context, event Event, input RunInput) error {
	event, err := NormalizeEvent(event)
	if err != nil {
		return err
	}
	hooks, err := r.registry.List(ctx, false)
	if err != nil {
		return err
	}
	var runErrs []error
	for _, hook := range hooks {
		if hook.Event != event {
			continue
		}
		if err := r.runOne(ctx, hook, input, ""); err != nil {
			runErrs = append(runErrs, err)
		}
	}
	return errors.Join(runErrs...)
}

func (r *Runner) ReplayLatestFailure(ctx context.Context, hookID string) error {
	failed, err := r.registry.LatestFailedRun(ctx, hookID)
	if err != nil {
		return err
	}
	hook, err := r.registry.Get(ctx, failed.HookID)
	if err != nil {
		return err
	}
	return r.runOne(ctx, hook, RunInput{Workspace: failed.Workspace, Payload: failed.Payload}, failed.ID)
}

func (r *Runner) runOne(ctx context.Context, hook Hook, input RunInput, replayOf string) error {
	runCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", hook.Command)
	if strings.TrimSpace(input.Workspace) != "" {
		cmd.Dir = strings.TrimSpace(input.Workspace)
	}
	cmd.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + strings.TrimSpace(input.Workspace),
		"LIORA_HOOK_ID=" + hook.ID,
		"LIORA_HOOK_EVENT=" + string(hook.Event),
		"LIORA_HOOK_PAYLOAD=" + input.Payload,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{limit: r.config.OutputLimit, buffer: &stdout}
	cmd.Stderr = &limitedWriter{limit: r.config.OutputLimit, buffer: &stderr}
	err := cmd.Run()
	status := RunStatusOK
	exitCode := 0
	if runCtx.Err() != nil {
		status = RunStatusTimeout
		exitCode = -1
	} else if err != nil {
		status = RunStatusFailed
		exitCode = exitCodeFrom(err)
	}
	record, recordErr := r.registry.RecordRun(ctx, RunRecord{
		HookID:          hook.ID,
		Event:           hook.Event,
		Workspace:       strings.TrimSpace(input.Workspace),
		Payload:         input.Payload,
		Status:          status,
		ExitCode:        exitCode,
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		OutputTruncated: outputTruncated(cmd.Stdout, cmd.Stderr),
		ReplayOfRunID:   replayOf,
	})
	if recordErr != nil {
		return recordErr
	}
	if status != RunStatusOK {
		return &RunError{HookID: hook.ID, Status: status, Detail: failureDetail(record)}
	}
	return nil
}

type limitedWriter struct {
	limit     int
	written   int
	truncated bool
	buffer    *bytes.Buffer
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining > 0 {
		chunk := data
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
			w.truncated = true
		}
		if _, err := w.buffer.Write(chunk); err != nil {
			return 0, err
		}
	}
	if len(data) > remaining {
		w.truncated = true
	}
	w.written += len(data)
	return len(data), nil
}

func outputTruncated(writers ...any) bool {
	for _, writer := range writers {
		limited, ok := writer.(*limitedWriter)
		if ok && limited.truncated {
			return true
		}
	}
	return false
}

func exitCodeFrom(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func failureDetail(record RunRecord) string {
	if strings.TrimSpace(record.Stderr) != "" {
		return strings.TrimSpace(record.Stderr)
	}
	if strings.TrimSpace(record.Stdout) != "" {
		return strings.TrimSpace(record.Stdout)
	}
	return string(record.Status)
}
