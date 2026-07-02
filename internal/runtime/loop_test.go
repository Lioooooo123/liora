package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/trace"
)

// fakeToolGenerator implements both llm.Generator and llm.ToolCaller so the
// runtime routes it to the native tool-use loop instead of the planner path.
type fakeToolGenerator struct {
	completions    []llm.Completion
	calls          int
	transcripts    [][]llm.Message
	plannerReply   string
	plannerReplies []string
	plannerCalls   int
	disableTools   bool
}

func (f *fakeToolGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	if len(f.plannerReplies) > 0 {
		index := f.plannerCalls
		if index >= len(f.plannerReplies) {
			index = len(f.plannerReplies) - 1
		}
		f.plannerCalls++
		return f.plannerReplies[index], nil
	}
	f.plannerCalls++
	return f.plannerReply, nil
}

func (f *fakeToolGenerator) GenerateWithTools(_ context.Context, messages []llm.Message, _ []llm.ToolSchema) (llm.Completion, error) {
	snapshot := make([]llm.Message, len(messages))
	copy(snapshot, messages)
	f.transcripts = append(f.transcripts, snapshot)
	completion := f.completions[f.calls]
	f.calls++
	return completion, nil
}

func (f *fakeToolGenerator) SupportsTools() bool { return !f.disableTools }

type runtimeToolContract struct {
	fileContent string
	diff        string
	events      []trace.Event
}

type runtimeCodingEvalContract struct {
	files  map[string]string
	tools  []string
	diff   string
	status agent.Status
}

func TestRuntimeNativeToolLoopAndPlannerFallbackShareWriteTraceContract(t *testing.T) {
	native := runRuntimeWriteContract(t, false)
	fallback := runRuntimeWriteContract(t, true)

	if native.fileContent != fallback.fileContent || native.fileContent != "hi there\n" {
		t.Fatalf("native/fallback file content mismatch: native=%q fallback=%q", native.fileContent, fallback.fileContent)
	}
	for _, result := range []runtimeToolContract{native, fallback} {
		if !strings.Contains(result.diff, "note.txt") || !strings.Contains(result.diff, "+hi there") {
			t.Fatalf("expected diff to describe note.txt creation, got:\n%s", result.diff)
		}
		if len(result.events) != 1 {
			t.Fatalf("expected one write trace event, got %#v", result.events)
		}
		event := result.events[0]
		if event.Tool != "write" || event.Input != "note.txt hi there" || event.Status != trace.StatusOK || event.Output != "written note.txt" {
			t.Fatalf("unexpected write trace event %#v", event)
		}
	}
	if native.events[0].ToolCallID != "write_1" || native.events[0].ToolResultID != "write_1-result" {
		t.Fatalf("native trace did not preserve provider tool call id: %#v", native.events[0])
	}
	if fallback.events[0].ToolCallID != "fallback-step-1" || fallback.events[0].ToolResultID != "fallback-step-1-result" {
		t.Fatalf("fallback trace did not synthesize stable step ids: %#v", fallback.events[0])
	}
}

func TestRuntimeNativeToolLoopAndPlannerFallbackShareErrorTraceContract(t *testing.T) {
	native := runRuntimeReadFailureContract(t, false)
	fallback := runRuntimeReadFailureContract(t, true)

	for _, result := range []runtimeToolContract{native, fallback} {
		if len(result.events) != 2 {
			t.Fatalf("expected failed read plus repaired read trace events, got %#v", result.events)
		}
		first := result.events[0]
		if first.Tool != "read" || first.Input != "missing.txt" || first.Status != trace.StatusError {
			t.Fatalf("unexpected read error trace event %#v", first)
		}
		if !strings.Contains(first.Output, "missing.txt") {
			t.Fatalf("expected error output to mention missing file, got %q", first.Output)
		}
		if first.ToolCallID == "" || first.ToolResultID == "" {
			t.Fatalf("expected failed tool result to keep call/result ids, got %#v", first)
		}
		second := result.events[1]
		if second.Tool != "read" || second.Input != "README.md" || second.Status != trace.StatusOK || !strings.Contains(second.Output, "hello") {
			t.Fatalf("unexpected repaired read trace event %#v", second)
		}
		if second.ToolCallID == "" || second.ToolResultID == "" {
			t.Fatalf("expected repaired tool result to keep call/result ids, got %#v", second)
		}
	}
	if native.events[0].ToolCallID != "read_1" || native.events[0].ToolResultID != "read_1-result" || native.events[1].ToolCallID != "read_2" {
		t.Fatalf("native error/recovery trace ids were not preserved: %#v", native.events)
	}
	if fallback.events[0].ToolCallID != "fallback-step-1" || fallback.events[0].ToolResultID != "fallback-step-1-result" || fallback.events[1].ToolCallID != "fallback-step-2" {
		t.Fatalf("fallback error/recovery trace ids were not stable: %#v", fallback.events)
	}
}

func TestRuntimeNativeToolLoopAndPlannerFallbackShareDeterministicCodingEvalContract(t *testing.T) {
	native := runRuntimeCodingEvalContract(t, false)
	fallback := runRuntimeCodingEvalContract(t, true)

	if !reflect.DeepEqual(native.files, fallback.files) {
		t.Fatalf("native/fallback file outputs mismatch:\nnative=%#v\nfallback=%#v", native.files, fallback.files)
	}
	if !reflect.DeepEqual(native.files, map[string]string{
		"README.md":           "# Demo\n",
		"config/settings.txt": "mode=fast\n",
		"docs/guide.txt":      "Use fast mode.\n",
	}) {
		t.Fatalf("unexpected deterministic eval files %#v", native.files)
	}
	if !reflect.DeepEqual(native.tools, []string{"write", "mkdir", "write", "write", "diff"}) || !reflect.DeepEqual(fallback.tools, native.tools) {
		t.Fatalf("unexpected native/fallback tool contract native=%#v fallback=%#v", native.tools, fallback.tools)
	}
	if native.status != agent.StatusCompleted || fallback.status != agent.StatusCompleted {
		t.Fatalf("expected completed status native=%s fallback=%s", native.status, fallback.status)
	}
	for name, diff := range map[string]string{"native": native.diff, "fallback": fallback.diff} {
		for _, want := range []string{"README.md", "config/settings.txt", "docs/guide.txt", "+mode=fast", "+Use fast mode."} {
			if !strings.Contains(diff, want) {
				t.Fatalf("%s diff missing %q:\n%s", name, want, diff)
			}
		}
	}
}

func runRuntimeCodingEvalContract(t *testing.T, fallback bool) runtimeCodingEvalContract {
	t.Helper()
	root := t.TempDir()
	generator := &fakeToolGenerator{
		completions: []llm.Completion{
			{ToolCalls: []llm.ToolCall{
				{ID: "write_readme", Name: "write", Arguments: `{"path":"README.md","content":"# Demo\n"}`},
				{ID: "mkdir_config", Name: "mkdir", Arguments: `{"path":"config"}`},
				{ID: "write_settings", Name: "write", Arguments: `{"path":"config/settings.txt","content":"mode=fast\n"}`},
				{ID: "write_guide", Name: "write", Arguments: `{"path":"docs/guide.txt","content":"Use fast mode.\n"}`},
				{ID: "diff_final", Name: "diff", Arguments: `{}`},
			}},
			{Content: "Deterministic eval complete."},
		},
		plannerReply: strings.Join([]string{
			"write README.md # Demo",
			"mkdir config",
			"write config/settings.txt mode=fast",
			"write docs/guide.txt Use fast mode.",
			"diff",
		}, "\n"),
		disableTools: fallback,
	}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	result, err := runtime.SubmitWithRecorder(t.Context(), "deterministic coding eval", recorder)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, path := range []string{"README.md", "config/settings.txt", "docs/guide.txt"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		files[path] = string(data)
	}
	var tools []string
	for _, event := range recorder.Events() {
		tools = append(tools, event.Tool)
	}
	return runtimeCodingEvalContract{files: files, tools: tools, diff: result.AgentResult.Diff, status: result.AgentResult.Status}
}

func runRuntimeWriteContract(t *testing.T, fallback bool) runtimeToolContract {
	t.Helper()
	root := t.TempDir()
	generator := &fakeToolGenerator{
		completions: []llm.Completion{
			{ToolCalls: []llm.ToolCall{{ID: "write_1", Name: "write", Arguments: `{"path":"note.txt","content":"hi there\n"}`}}},
			{Content: "Created note.txt."},
		},
		plannerReply: "write note.txt hi there",
		disableTools: fallback,
	}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	result, err := runtime.SubmitWithRecorder(t.Context(), "create note.txt", recorder)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed result, got %#v", result.AgentResult)
	}
	data, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return runtimeToolContract{fileContent: string(data), diff: result.AgentResult.Diff, events: recorder.Events()}
}

func runRuntimeReadFailureContract(t *testing.T, fallback bool) runtimeToolContract {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{
		completions: []llm.Completion{
			{ToolCalls: []llm.ToolCall{{ID: "read_1", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
			{ToolCalls: []llm.ToolCall{{ID: "read_2", Name: "read", Arguments: `{"path":"README.md"}`}}},
			{Content: "Recovered."},
		},
		plannerReplies: []string{"read missing.txt", "read README.md"},
		disableTools:   fallback,
	}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	result, err := runtime.SubmitWithRecorder(t.Context(), "read missing.txt", recorder)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed result after repair, got %#v", result.AgentResult)
	}
	return runtimeToolContract{diff: result.AgentResult.Diff, events: recorder.Events()}
}

func TestRuntimeRoutesToToolLoopWhenSupported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"README.md"}`}}},
		{Content: "The readme greets the world."},
	}}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	var plans []string
	result, err := runtime.SubmitWithOptions(t.Context(), "summarize the readme", SubmitOptions{
		OnPlan: func(steps string) { plans = append(plans, steps) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed, got %#v", result.AgentResult)
	}
	if result.AgentResult.Summary != "The readme greets the world." {
		t.Fatalf("unexpected summary %q", result.AgentResult.Summary)
	}
	if generator.calls != 2 {
		t.Fatalf("expected two model turns, got %d", generator.calls)
	}
	if len(plans) != 1 || !strings.Contains(plans[0], "read README.md") {
		t.Fatalf("unexpected plan callbacks %#v", plans)
	}
	if result.PlannedSteps != "read README.md" {
		t.Fatalf("unexpected planned steps %q", result.PlannedSteps)
	}
	if len(result.Events) != 1 || result.Events[0].Tool != "read" || result.Events[0].Status != "ok" {
		t.Fatalf("unexpected events %#v", result.Events)
	}
}

func TestRuntimeToolLoopFeedsErrorAndSignalsReplan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read", Arguments: `{"path":"README.md"}`}}},
		{Content: "Recovered and read the readme."},
	}}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	var replans []string
	result, err := runtime.SubmitWithOptions(t.Context(), "read the readme", SubmitOptions{
		OnReplan: func(_ int, reason string) { replans = append(replans, reason) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed, got %#v", result.AgentResult)
	}
	if len(replans) != 1 || !strings.Contains(replans[0], "read") {
		t.Fatalf("unexpected replan signals %#v", replans)
	}
	if len(result.Events) != 2 {
		t.Fatalf("expected failed read plus repaired read, got %#v", result.Events)
	}
	if result.Events[0].Status != "error" {
		t.Fatalf("expected first event to be an error, got %#v", result.Events[0])
	}
}

func TestRuntimeFallsBackToPlannerWhenLoopDisabled(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{plannerReply: "ANSWER: listing skipped"}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	_, err = runtime.Submit(t.Context(), "list .")
	if err != nil {
		t.Fatal(err)
	}
	if generator.calls != 0 {
		t.Fatalf("expected planner path (no tool-loop calls), got %d", generator.calls)
	}
	if generator.plannerCalls == 0 {
		t.Fatal("expected planner Generate to be used when loop disabled")
	}
}

func TestRuntimeFallsBackToPlannerWhenModelCapabilityDisablesToolLoop(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{plannerReply: "ANSWER: capability fallback", disableTools: true}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runtime.Submit(t.Context(), "list .")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "capability fallback" {
		t.Fatalf("unexpected planner fallback answer %q", result.Answer)
	}
	if generator.calls != 0 {
		t.Fatalf("expected no native tool-loop calls, got %d", generator.calls)
	}
	if generator.plannerCalls == 0 {
		t.Fatal("expected planner path when model capability disables tool-use")
	}
}
