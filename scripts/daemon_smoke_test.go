package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestDaemonSmokeScriptCoversDaemonAPI(t *testing.T) {
	data, err := os.ReadFile("daemon-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"go build", "liora-smoke", "-daemon", "LIORA_PATCH_MODE=1", `TMP_DIR="$(mktemp -d)"`, "rm -rf \"$TMP_DIR\"", "/healthz", "daemon did not become healthy", "/v1/tasks", "sandbox.workspace", "task.completed", "/diff", "/apply", "/cancel", "task.cancelled"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected daemon smoke script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestTUISmokeScriptCoversDaemonBackedTUI(t *testing.T) {
	data, err := os.ReadFile("tui-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"-tui-daemon", "Timeline session_", "tool.result", "/tools", "MCP tools", "mcp fake echo <json arguments>", "/tail 8", "Tail task_", "/timeline", "/cancel", "Cancelled task", "Fake", "chat"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected tui smoke script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestCodingEvalScriptCoversTaskQualityBaseline(t *testing.T) {
	data, err := os.ReadFile("coding-eval.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"Fake", "LIORA_PATCH_MODE=1", "LIORA_PERMISSION=prompt", "natural", "run_async", "multi-file", "config/settings.txt", "docs/guide.txt", "docx-case", "assignment.docx", "Assignment Brief", "mcp-case", "fake_mcp.py", "mcp echo: hello from eval", "external", "replan-case", "task.replanning", "missing-replan.txt", "600000", "truncated", "missing-eval.txt", "task.error", "task.diff", "task.patch_applied", "/timeline", "permission.requested", "permission.approved", "permission.denied", "task.cancelled", "coding eval ok"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected coding eval script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestLioraAuditRunsArchitectureGuard(t *testing.T) {
	data, err := os.ReadFile("liora-1.0-audit.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"architecture-guard.sh", "architecture guard", "TestDaemonSmokeScriptCoversDaemonAPI", "release-supply-chain-audit.sh", "TestReleaseSupplyChainAudit", "event catalog", "TestRepositoryPersistsFirstClassEventsAndProjectsTimeline", "TestRepositoryMaterializesTranscriptEntriesForFirstClassTimelineKinds", "TestRepositoryRejectsMalformedTranscriptMaterializationWithoutPartialRows", "TestRepositoryTranscriptProjectionDrivesContextSearchAndExportWhileEventsRemainFacts", "TestRepositorySearchTimelineUsesMaterializedTranscriptStructuredColumns", "TestRepositorySearchTimelineIgnoresCorruptedRawEventsAndKeepsSessionIsolation", "TestRepositoryTranscriptArtifactReferenceAvoidsInliningLongOutput", "safety gates", "TestRepositoryMaterializesAndResolvesApprovalItems", "TestRepositoryContextEnvelopeReportsBudgetBuckets", "TestRepositoryContextEnvelopeBudgetBucketsRemainStableForEmptyAndTruncatedContext", "TestRepositoryContextEnvelopePacksRelevantSources", "TestRepositoryContextEnvelopePackerExcludesIrrelevantAndTruncatesBudget", "TestRepositoryContextEnvelopeDiagnosticsExplainSelectedPromptSources", "TestRepositoryContextEnvelopeDiagnosticsExcludeOmittedContext", "TestRepositoryCompactSessionWritesManualBoundaryAndContext", "TestRepositoryAutoCompactHonorsBudgetAndAvoidsDuplicateBoundary", "TestRepositoryCompactSessionPersistsBoundarySourceMapping", "TestRepositoryCompactBoundaryMappingHandlesMalformedEmptyAndLegacyFallback", "TestRepositoryCompactSessionContinuesWithPostBoundaryToolPairs", "TestRepositoryAutoCompactResumesAfterNewPostBoundaryToolPair", "TestRunnerWaitsForPermissionBeforeNetworkShell", "TestServerThreadModelAttributionCoversNativeToolLoopAndPlannerFallback", "TestServerToolFailuresRemainObservableAcrossTailTimelineAndTrace", "TestRepositoryWriteAndReadTodosForSession", "TestRunnerCompletesWhenOpenTodoIsExplained", "TestRunnerFailsCompletionWhenOpenTodoIsUnexplained", "TestRunnerStreamsTodoProgressBeforeTaskCompletes", "TestClientWorkbenchDecodesBackgroundOutputs", "TestDaemonEventFormattersShareSemantics", "TestDaemonSubmitterListsAndResumesSessions", "TestDaemonSubmitterShowsPromptContextSourceSummary", "TestDaemonSubmitterPromptContextHandlesEmptyAndOmittedSources", "TestDaemonSubmitterCompactCommandWritesManualAndAutoBoundaries", "TestDaemonSubmitterResumeCommandsHandleEmptyAndUsage", "TestDaemonSubmitterPersistsTodoToolsAndShowsTodoAfterResume", "TestDaemonSubmitterTodoCommandShowsEmptySessionState", "TestDaemonSubmitterTodoCommandSurvivesDaemonRestart", "TestDaemonSubmitterTranscriptAndTodoSurviveDaemonRestart", "TestDaemonSubmitterWorkbenchShowsRestartedBackgroundOutputs", "TestDaemonSubmitterShowsAndWatchesChildTasksAndThreads", "TestDaemonSubmitterWatchChildrenHandlesBoundaryInputs", "TestCapabilityTodoToolsExposeNativeSchemas", "TestServerArtifactPageServesPagedStoreArtifact", "TestDaemonSubmitterPagesLargeArtifactWithoutInliningFullContext", "TestServerRestartWorkbenchListsBackgroundTasksAndOutputs", "TestServerRestartWorkbenchBackgroundOutputsAreScopedAndStable", "TestTaskOutputRejectsMalformedWaitAndLimit", "TestParentTaskUsesTaskOutputAndTaskStopThroughNativeToolLoop"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected 1.0 audit script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestLioraAuditRunsEndToEndSmokeAndEvalGates(t *testing.T) {
	data, err := os.ReadFile("liora-1.0-audit.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"[11/14] daemon smoke",
		"LIORA_AUDIT_DAEMON_ADDR",
		"./scripts/daemon-smoke.sh",
		"[12/14] TUI smoke",
		"LIORA_AUDIT_TUI_DAEMON_ADDR",
		"LIORA_AUDIT_TUI_LLM_ADDR",
		"./scripts/tui-smoke.sh",
		"[13/14] coding eval",
		"LIORA_AUDIT_EVAL_DAEMON_ADDR",
		"LIORA_AUDIT_EVAL_LLM_ADDR",
		"./scripts/coding-eval.sh",
		"[14/14] package smoke",
		`LIORA_DIST_DIR="$AUDIT_TMP/dist"`,
		"./scripts/package-release.sh",
		"./scripts/release-smoke.sh",
		"./scripts/npm-lazy-smoke.sh",
		"./scripts/local-install-smoke.sh",
		"package=$ARCHIVE_PATH",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected 1.0 audit script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestLioraAuditBootstrapsProtocolDependencies_whenNodeModulesMissing(t *testing.T) {
	data, err := os.ReadFile("liora-1.0-audit.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"ensure_protocol_deps",
		"packages/protocol/node_modules/.bin/vitest",
		"packages/protocol/node_modules/zod",
		`rm -rf "$ROOT/node_modules" "$ROOT/packages/protocol/node_modules"`,
		"pnpm install --frozen-lockfile",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected 1.0 audit script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestLioraAuditRunsLoopRetryAndBudgetGuards(t *testing.T) {
	data, err := os.ReadFile("liora-1.0-audit.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"TestToolLoopStopsAtMaxTurns",
		"TestToolLoopStopsOnRepeatedIdenticalFailingToolCall",
		"TestToolLoopStopsOnRepeatedLargeFailingToolOutput",
		"TestToolLoopPersistsLargeToolOutputForModel",
		"TestRuntimeNativeToolLoopAndPlannerFallbackShareDeterministicCodingEvalContract",
		"TestClientRetriesTransientHTTPFailures",
		"TestClientDoesNotRetryAuthenticationFailure",
		"TestTurnRuntimeReplansAfterToolFailure",
		"TestRunnerReplansNaturalTaskAfterToolFailure",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected 1.0 audit script to contain %q, got:\n%s", want, content)
		}
	}
}
