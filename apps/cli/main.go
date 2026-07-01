package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/config"
	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/llm"
	mcppkg "github.com/Lioooooo123/liora/internal/mcp"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/runtime"
	"github.com/Lioooooo123/liora/internal/sandbox"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/Lioooooo123/liora/internal/tui"
	"github.com/Lioooooo123/liora/internal/tuisession"
	"github.com/mattn/go-isatty"
)

var version = "dev"

func main() {
	if err := config.LoadDefaultEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	workspacePath := flag.String("workspace", ".", "workspace directory")
	prompt := flag.String("prompt", "", "newline-separated agent steps")
	interactive := flag.Bool("interactive", false, "start an interactive terminal UI")
	natural := flag.Bool("natural", false, "treat prompt/stdin as natural language and ask an LLM to plan tool steps")
	daemonMode := flag.Bool("daemon", false, "start the local Liora core daemon")
	daemonAddr := flag.String("daemon-addr", "127.0.0.1:18080", "daemon listen address")
	tuiDaemon := flag.Bool("tui-daemon", false, "run interactive TUI through the local daemon event stream")
	sessionID := flag.String("session", "", "attach interactive TUI requests to an existing daemon session id")
	forceNewSession := flag.Bool("new-session", false, "start a fresh interactive session instead of auto-resume")
	doctor := flag.Bool("doctor", false, "print resolved LLM provider configuration and exit without calling the API")
	llmProvider := flag.String("llm-provider", getenvAny("LIORA_LLM_PROVIDER", "OPENAI_PROVIDER", ""), "LLM provider: openai-chat, openai-responses, deepseek, anthropic, gemini")
	llmBaseURL := flag.String("llm-base-url", defaultLLMBaseURL(), "LLM API base URL")
	llmAPIKey := flag.String("llm-api-key", getenvAny("LIORA_LLM_API_KEY", "OPENAI_API_KEY", ""), "LLM API key")
	llmModel := flag.String("llm-model", getenvAny("LIORA_LLM_MODEL", "OPENAI_MODEL", ""), "LLM model name")
	traceOut := flag.String("trace-out", "", "write trace events to a JSONL file")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	seenFlags := parsedFlagNames()
	if shouldIgnoreLegacyBaseURL(*llmProvider, seenFlags["llm-provider"], seenFlags["llm-base-url"]) {
		*llmBaseURL = ""
	}
	if *versionFlag {
		fmt.Println("liora " + version)
		return
	}

	steps := *prompt
	if steps == "" && flag.NArg() > 0 {
		steps = strings.Join(flag.Args(), " ")
	}
	defaultInteractive := steps == "" && !*natural && *traceOut == ""

	llmConfig := llm.Config{
		Provider: *llmProvider,
		BaseURL:  *llmBaseURL,
		APIKey:   *llmAPIKey,
		Model:    *llmModel,
	}
	if *doctor {
		if err := printDoctor(llmConfig); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	llmClient, err := llm.NewClient(llmConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	planner := llm.NewPlanner(llmClient)
	persistentStore := store.New("")
	sandboxExecutor := sandbox.FromEnv()
	patchMode := boolEnvDefault("LIORA_PATCH_MODE", true)

	if *daemonMode {
		db, err := persistentStore.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		repo := taskpkg.NewRepository(db)
		server := daemon.NewServer(daemon.Config{
			Repository: repo,
			Runner:     newTaskRunner(repo, planner, persistentStore, sandboxExecutor, patchMode),
			Store:      persistentStore,
		})
		fmt.Printf("Liora daemon listening on %s (sandbox=%s patch_mode=%t)\n", *daemonAddr, sandbox.Label(sandboxExecutor), patchMode)
		if err := http.ListenAndServe(*daemonAddr, server); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	workspace, err := tools.NewWorkspace(*workspacePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	turnRuntime := runtime.FromWorkspace(workspace, planner, persistentStore)
	turnRuntime.SetSandbox(sandboxExecutor)
	turnRuntime.SetPermissionChecker(permissionPolicy(false))

	if *interactive || defaultInteractive {
		baseURL := daemonBaseURL(*daemonAddr)
		var embedded *embeddedDaemon
		coreLabel := "external daemon"
		if !*tuiDaemon {
			embedded, err = startEmbeddedDaemon(persistentStore, planner, sandboxExecutor, patchMode)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			defer embedded.close()
			baseURL = embedded.baseURL
			coreLabel = "embedded daemon"
		}
		client, err := daemonclient.New(baseURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := client.Health(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "daemon is not reachable:", err)
			os.Exit(1)
		}

		daemonSession := tuisession.NewDaemonSubmitter(client, workspace.Root(), true, strings.TrimSpace(*sessionID), *forceNewSession)
		tuiConfig := tui.Config{
			Workspace: workspace.Root(),
			Model:     llmLabel(llmConfig),
			Core:      coreLabel,
			Safety:    safetyLabel(patchMode),
			Commands: tui.CommandChain{
				doctorCommand{
					config: llmConfig,
					report: doctorReportContext{
						Workspace: workspace.Root(),
						Core:      coreLabel,
						Safety:    safetyLabel(patchMode),
					},
				},
				daemonSession,
				turnRuntime,
			},
		}
		if useFullScreenTUI() {
			if err := tui.RunProgram(context.Background(), tuiConfig, daemonSession); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		app := tui.New(tuiConfig, daemonSession)
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
		fmt.Fprintln(os.Stderr, "usage: liora -workspace /path -prompt $'read file\\nrun go test ./...'")
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
	runner.SetShellExecutor(sandboxExecutor)
	runner.SetSkillReader(persistentStore)
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

type embeddedDaemon struct {
	server  *http.Server
	db      interface{ Close() error }
	baseURL string
}

func startEmbeddedDaemon(persistentStore *store.Store, planner *llm.Planner, executor sandbox.Executor, patchMode bool) (*embeddedDaemon, error) {
	db, err := persistentStore.OpenDB()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	repo := taskpkg.NewRepository(db)
	server := &http.Server{Handler: daemon.NewServer(daemon.Config{
		Repository: repo,
		Runner:     newTaskRunner(repo, planner, persistentStore, executor, patchMode),
		Store:      persistentStore,
	})}
	embedded := &embeddedDaemon{
		server:  server,
		db:      db,
		baseURL: "http://" + listener.Addr().String(),
	}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "embedded daemon stopped:", err)
		}
	}()
	return embedded, nil
}

func (d *embeddedDaemon) close() {
	if d == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if d.server != nil {
		_ = d.server.Shutdown(ctx)
	}
	if d.db != nil {
		_ = d.db.Close()
	}
}

func daemonBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

// full-screen Bubble Tea TUI requires a real terminal; piped/CI callers
// (e.g. tui-smoke) fall back to the line-based renderer. LIORA_FORCE_GO_TUI
// forces the line renderer even on a TTY for debugging.
func useFullScreenTUI() bool {
	if toBool(os.Getenv("LIORA_FORCE_GO_TUI")) {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

func toBool(v string) bool {
	s := strings.TrimSpace(strings.ToLower(v))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

func newTaskRunner(repo *taskpkg.Repository, planner *llm.Planner, persistentStore *store.Store, executor sandbox.Executor, patchMode bool) *taskpkg.Runner {
	runner := taskpkg.NewRunner(repo, planner)
	runner.SetStore(persistentStore)
	runner.SetSandbox(executor)
	runner.SetPatchMode(patchMode)
	runner.SetPermissionPolicy(permissionPolicy(patchMode))
	return runner
}

func permissionPolicy(patchMode bool) permission.Policy {
	mode := permission.ModeAuto
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LIORA_PERMISSION")), string(permission.ModePrompt)) {
		mode = permission.ModePrompt
	}
	return permission.Policy{Mode: mode, AllowWritesInPatchMode: patchMode}
}

func boolEnvDefault(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

func getenvAny(names ...string) string {
	if len(names) == 0 {
		return ""
	}
	fallback := names[len(names)-1]
	for _, name := range names[:len(names)-1] {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return fallback
}

func defaultLLMBaseURL() string {
	if value := os.Getenv("LIORA_LLM_BASE_URL"); value != "" {
		return value
	}
	if strings.TrimSpace(os.Getenv("LIORA_LLM_PROVIDER")) != "" {
		return ""
	}
	return getenvAny("OPENAI_BASE_URL", "")
}

func parsedFlagNames() map[string]bool {
	seen := map[string]bool{}
	flag.Visit(func(f *flag.Flag) {
		seen[f.Name] = true
	})
	return seen
}

func shouldIgnoreLegacyBaseURL(provider string, providerFlagSet bool, baseURLFlagSet bool) bool {
	if baseURLFlagSet || strings.TrimSpace(os.Getenv("LIORA_LLM_BASE_URL")) != "" {
		return false
	}
	if strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")) == "" {
		return false
	}
	namespacedProviderSet := strings.TrimSpace(os.Getenv("LIORA_LLM_PROVIDER")) != ""
	if !providerFlagSet && !namespacedProviderSet {
		return false
	}
	return llm.NormalizeProvider(provider) != llm.ProviderOpenAIChat
}

func llmLabel(config llm.Config) string {
	provider := llm.NormalizeProvider(config.Provider)
	if provider == "" {
		provider = llm.ProviderOpenAIChat
	}
	if config.Model == "" {
		return llm.ProviderDisplayName(provider)
	}
	return llm.ProviderDisplayName(provider) + " / " + config.Model
}

func safetyLabel(patchMode bool) string {
	if patchMode {
		return "patch-first"
	}
	return "direct-write"
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
