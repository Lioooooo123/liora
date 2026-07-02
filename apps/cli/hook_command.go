package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	hookpkg "github.com/Lioooooo123/liora/internal/hook"
	"github.com/Lioooooo123/liora/internal/store"
)

func handleHookCommand(ctx context.Context, args []string, workspace string, persistentStore *store.Store, stdout io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "hook" {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("hook subcommand is required")
	}
	registry := hookpkg.NewRegistry(persistentStore)
	switch args[1] {
	case "add":
		return true, hookAdd(ctx, args[2:], registry, stdout)
	case "list":
		return true, hookList(ctx, registry, stdout)
	case "enable":
		return true, hookSetEnabled(ctx, args[2:], registry, true, stdout)
	case "disable":
		return true, hookSetEnabled(ctx, args[2:], registry, false, stdout)
	case "run":
		return true, hookRun(ctx, args[2:], workspace, registry, stdout)
	case "replay-failure":
		return true, hookReplay(ctx, args[2:], registry, stdout)
	default:
		return true, fmt.Errorf("unknown hook subcommand %q", args[1])
	}
}

func hookAdd(ctx context.Context, args []string, registry *hookpkg.Registry, stdout io.Writer) error {
	flags := flag.NewFlagSet("hook add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("id", "", "hook id")
	event := flags.String("event", "", "hook event")
	command := flags.String("command", "", "hook command")
	enabled := flags.Bool("enabled", true, "enable hook")
	if err := flags.Parse(args); err != nil {
		return err
	}
	hook, err := registry.Save(ctx, hookpkg.SaveRequest{
		ID:      *id,
		Event:   hookpkg.Event(*event),
		Command: *command,
		Enabled: *enabled,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "hook %s saved event=%s status=%s risk=%s\n", hook.ID, hook.Event, enabledLabel(hook.Enabled), hook.Risk)
	return nil
}

func hookList(ctx context.Context, registry *hookpkg.Registry, stdout io.Writer) error {
	hooks, err := registry.List(ctx, true)
	if err != nil {
		return err
	}
	if len(hooks) == 0 {
		fmt.Fprintln(stdout, "hooks: none")
		return nil
	}
	for _, hook := range hooks {
		fmt.Fprintf(stdout, "%s event=%s status=%s risk=%s command=redacted\n", hook.ID, hook.Event, enabledLabel(hook.Enabled), hook.Risk)
	}
	return nil
}

func hookSetEnabled(ctx context.Context, args []string, registry *hookpkg.Registry, enabled bool, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("hook id is required")
	}
	hook, err := registry.SetEnabled(ctx, args[0], enabled)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "hook %s %s\n", hook.ID, enabledLabel(hook.Enabled))
	return nil
}

func hookRun(ctx context.Context, args []string, workspace string, registry *hookpkg.Registry, stdout io.Writer) error {
	flags := flag.NewFlagSet("hook run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	event := flags.String("event", "", "hook event")
	payload := flags.String("payload", "{}", "hook JSON payload")
	if err := flags.Parse(args); err != nil {
		return err
	}
	runner := hookpkg.NewRunner(registry, hookpkg.RunnerConfig{Timeout: 10 * time.Second})
	err := runner.Run(ctx, hookpkg.Event(*event), hookpkg.RunInput{Workspace: workspace, Payload: *payload})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "hook run ok")
	return nil
}

func hookReplay(ctx context.Context, args []string, registry *hookpkg.Registry, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("hook id is required")
	}
	runner := hookpkg.NewRunner(registry, hookpkg.RunnerConfig{Timeout: 10 * time.Second})
	err := runner.ReplayLatestFailure(ctx, strings.TrimSpace(args[0]))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "hook %s replay ok\n", strings.TrimSpace(args[0]))
	return nil
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
