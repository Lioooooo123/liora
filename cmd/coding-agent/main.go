package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"coding-agent-mvp/internal/agent"
	"coding-agent-mvp/internal/config"
	"coding-agent-mvp/internal/llm"
	mcppkg "coding-agent-mvp/internal/mcp"
	"coding-agent-mvp/internal/runtime"
	"coding-agent-mvp/internal/store"
	"coding-agent-mvp/internal/tools"
	"coding-agent-mvp/internal/trace"
	"coding-agent-mvp/internal/tui"
)

func main() {
	if err := config.LoadDefaultEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	workspacePath := flag.String("workspace", ".", "workspace directory")
	prompt := flag.String("prompt", "", "newline-separated agent steps")
	interactive := flag.Bool("interactive", false, "start an interactive terminal UI")
	natural := flag.Bool("natural", false, "treat prompt/stdin as natural language and ask an LLM to plan tool steps")
	llmBaseURL := flag.String("llm-base-url", getenv("OPENAI_BASE_URL", "https://api.openai.com/v1"), "OpenAI-compatible API base URL")
	llmModel := flag.String("llm-model", getenv("OPENAI_MODEL", ""), "OpenAI-compatible model name")
	traceOut := flag.String("trace-out", "", "write trace events to a JSONL file")
	flag.Parse()

	steps := *prompt
	if steps == "" && flag.NArg() > 0 {
		steps = strings.Join(flag.Args(), " ")
	}
	defaultInteractive := steps == "" && !*natural && *traceOut == ""

	workspace, err := tools.NewWorkspace(*workspacePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	planner := llm.NewPlanner(llm.NewOpenAICompatibleClient(llm.Config{
		BaseURL: *llmBaseURL,
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		Model:   *llmModel,
	}))
	persistentStore := store.New("")
	turnRuntime := runtime.FromWorkspace(workspace, planner, persistentStore)

	if *interactive || defaultInteractive {
		app := tui.New(tui.Config{
			Workspace: workspace.Root(),
			Model:     *llmModel,
			Commands:  turnRuntime,
		}, turnRuntime)
		if err := app.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if steps == "" {
		data, err := os.ReadFile("/dev/stdin")
		if err == nil {
			steps = string(data)
		}
	}
	if strings.TrimSpace(steps) == "" {
		fmt.Fprintln(os.Stderr, "usage: coding-agent -workspace /path -prompt $'read file\\nrun go test ./...'")
		os.Exit(2)
	}

	if *natural {
		planned, err := planner.Plan(context.Background(), llm.PlanRequest{
			WorkspaceSummary: "workspace root: " + workspace.Root(),
			UserPrompt:       steps,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		steps = planned
		fmt.Println("planned steps:")
		fmt.Println(steps)
	}

	recorder := trace.NewMemoryRecorder()
	runner := agent.New(workspace, recorder)
	manager, err := mcpManagerFromStore(persistentStore)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if manager != nil {
		runner.SetMCP(manager)
	}
	result, err := runner.Run(context.Background(), steps)

	for i, event := range recorder.Events() {
		fmt.Printf("[%d] %s %s\n", i+1, event.Tool, event.Status)
		if strings.TrimSpace(event.Output) != "" {
			fmt.Println(strings.TrimRight(event.Output, "\n"))
		}
	}
	if *traceOut != "" {
		if writeErr := trace.WriteJSONL(*traceOut, recorder.Events()); writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)
			os.Exit(1)
		}
		fmt.Println("trace:", *traceOut)
	}
	fmt.Println("summary:", result.Summary)
	if strings.TrimSpace(result.Diff) != "" {
		fmt.Println("diff:")
		fmt.Print(result.Diff)
	}
	if err != nil {
		os.Exit(1)
	}
}

func getenv(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func mcpManagerFromStore(s *store.Store) (*mcppkg.Manager, error) {
	config, err := s.LoadMCPConfig()
	if err != nil {
		return nil, err
	}
	if len(config.Servers) == 0 {
		return nil, nil
	}
	servers := make(map[string]mcppkg.ServerConfig, len(config.Servers))
	for name, server := range config.Servers {
		servers[name] = mcppkg.ServerConfig{
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
		}
	}
	return mcppkg.NewManager(mcppkg.Config{Servers: servers}), nil
}
