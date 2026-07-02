#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="${1:-$ROOT}"
GO_TOOLCHAIN="${GOTOOLCHAIN:-local}"
AUDIT_TMP="$(mktemp -d)"
cleanup() {
  rm -rf "$AUDIT_TMP"
  echo "cleanup: removed $AUDIT_TMP"
}
trap cleanup EXIT

echo "[1/14] protocol package"
(
  cd "$ROOT"
  pnpm --filter @liora/protocol test
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/protocol
)

echo "[2/14] event catalog"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/task -run 'TestEventCatalog|TestRepositoryRejectsUnknownOrMalformedFirstClassEvents|TestRepositoryPersistsFirstClassEventsAndProjectsTimeline|TestRepositoryMaterializesTranscriptEntriesForFirstClassTimelineKinds|TestRepositoryRejectsMalformedTranscriptMaterializationWithoutPartialRows|TestRepositoryTranscriptProjectionDrivesContextSearchAndExportWhileEventsRemainFacts|TestRepositorySearchTimelineUsesMaterializedTranscriptStructuredColumns|TestRepositorySearchTimelineIgnoresCorruptedRawEventsAndKeepsSessionIsolation|TestRepositoryTranscriptArtifactReferenceAvoidsInliningLongOutput|TestRepositoryExpiryStaleMarksWaitScheduleAndHookTasks|TestRepositoryExpiryStaleSkipsNonExpiredTerminalAndRejectsMalformed'
)

echo "[3/14] provider registry"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/llm ./internal/task -run 'TestRegistryResolvesPerRequestProviderConfig|TestRegistryRejectsUnsupportedProvider|TestRegistryResolvesProviderModelCapabilities|TestRegistryConcurrentPlannersKeepRequestConfigIsolated|TestClientSupportsToolsFollowsResolvedCapability|TestClientMetricsAreBucketedByProviderModelAndDoNotBlockSiblingModel|TestRepositoryChildTaskInheritsParentModelUnlessOverridden|TestRunnerRoutesNaturalTaskThroughThreadModelRegistry|TestRunnerResolvesModelBindingHierarchyIntoTaskMetadata'
)

echo "[4/14] context boundary"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/task ./internal/daemon ./internal/daemonclient ./internal/tui ./internal/tuisession -run 'TestRepositoryContextEnvelopeBoundsTranscriptArtifactsAndCompactBoundary|TestRepositoryContextEnvelopeReportsBudgetBuckets|TestRepositoryContextEnvelopeBudgetBucketsRemainStableForEmptyAndTruncatedContext|TestRepositoryContextEnvelopePacksRelevantSources|TestRepositoryContextEnvelopePackerExcludesIrrelevantAndTruncatesBudget|TestRepositoryContextEnvelopeDiagnosticsExplainSelectedPromptSources|TestRepositoryContextEnvelopeDiagnosticsExcludeOmittedContext|TestRepositoryCompactSessionWritesManualBoundaryAndContext|TestRepositoryAutoCompactHonorsBudgetAndAvoidsDuplicateBoundary|TestRepositoryCompactSessionPersistsBoundarySourceMapping|TestRepositoryCompactBoundaryMappingHandlesMalformedEmptyAndLegacyFallback|TestRepositoryCompactSessionContinuesWithPostBoundaryToolPairs|TestRepositoryAutoCompactResumesAfterNewPostBoundaryToolPair|TestRepositoryWriteAndReadTodosForSession|TestRepositoryWriteTodosRejectsMalformedInputsWithoutPartialRowsOrEvents|TestRunnerCompletesWhenOpenTodoIsExplained|TestRunnerFailsCompletionWhenOpenTodoIsUnexplained|TestRunnerAllowsNonCriticalPendingTodoBeforeCompletion|TestRunnerStreamsTodoProgressBeforeTaskCompletes|TestDaemonToolOutputSinkPersistsSessionArtifactAndEvent|TestRunnerPersistsPairedToolCallAndResultIDsForFailures|TestServerServesSessionTranscript|TestServerArtifactPageServesPagedStoreArtifact|TestServerArtifactPageRejectsUnsafeRequests|TestClientSessionLifecycle|TestClientTypedMethodsConstructCanonicalPaths|TestDaemonEventFormattersShareSemantics|TestDaemonEventFormattersHandleMalformedAndUnknownEvents|TestRenderStreamUpdate|TestDaemonSubmitterListsAndResumesSessions|TestDaemonSubmitterShowsPromptContextSourceSummary|TestDaemonSubmitterPromptContextHandlesEmptyAndOmittedSources|TestDaemonSubmitterCompactCommandWritesManualAndAutoBoundaries|TestDaemonSubmitterResumeCommandsHandleEmptyAndUsage|TestDaemonSubmitterModelCommandQueriesAndSwitchesThreadModel|TestDaemonSubmitterPersistsTodoToolsAndShowsTodoAfterResume|TestDaemonSubmitterTodoCommandShowsEmptySessionState|TestDaemonSubmitterTodoCommandSurvivesDaemonRestart|TestDaemonSubmitterTranscriptAndTodoSurviveDaemonRestart|TestDaemonSubmitterPagesLargeArtifactWithoutInliningFullContext|TestDaemonSubmitterArtifactCommandRejectsInvalidInputsWithoutSessionState|TestDaemonSubmitterShowsAndWatchesChildTasksAndThreads|TestDaemonSubmitterWatchChildrenHandlesBoundaryInputs'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/capabilities -run 'TestBuiltinToolsExposePlannerAndHumanViews|TestToolSchemasAreClosedObjects|TestCapabilityTodoToolsExposeNativeSchemas|TestCapabilityTaskControlToolsExposeNativeSchemas'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/runtime -run 'TestRuntimeNativeToolLoopAndPlannerFallbackShareWriteTraceContract|TestRuntimeNativeToolLoopAndPlannerFallbackShareErrorTraceContract|TestRuntimeNativeToolLoopAndPlannerFallbackShareDeterministicCodingEvalContract|TestRuntimeRoutesToToolLoopWhenSupported|TestRuntimeToolLoopFeedsErrorAndSignalsReplan|TestRuntimeFallsBackToPlannerWhenModelCapabilityDisablesToolLoop'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/agent -run 'TestToolLoopStopsAtMaxTurns|TestToolLoopStopsOnRepeatedIdenticalFailingToolCall|TestToolLoopAllowsRepeatedShellCommandWhenFailureOutputChanges|TestToolLoopStopsOnRepeatedLargeFailingToolOutput|TestToolLoopPersistsLargeToolOutputForModel|TestToolLoopPassesTaskControlSchemasToModel|TestToolLoopRoutesTaskControlToolsThroughExecutor|TestToolLoopTaskToolsFailClosedWithoutExecutor'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/llm -run 'TestClientRetriesTransientHTTPFailures|TestClientDoesNotRetryAuthenticationFailure|TestClientMetricsAreBucketedByProviderModelAndDoNotBlockSiblingModel'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/runtime ./internal/task -run 'TestTurnRuntimeReplansAfterToolFailure|TestTurnRuntimeReplanAnswerDoesNotReturnPreviousToolError|TestRunnerReplansNaturalTaskAfterToolFailure'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/daemon -run 'TestServerQueuesSessionTaskAndStartsItAfterActiveTaskFinishes|TestServerForegroundTurnDefaultsToSessionQueueBehindActiveTask|TestServerForegroundTurnQueuePreservesFIFOAndSkipsIndependentSessions'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/daemon ./internal/daemonclient -run 'TestServerAcceptsUserInputAndResumesWaitingTask|TestServerApprovesWaitingPermissionTask|TestServerRejectsWaitingUserWrongRouteAndMalformedWaitType|TestServerSessionTerminalIsolation_keepsQueuedWaitsAndSubagentTerminalsIndependent|TestServerSessionTerminalIsolation_rejectsResumeAfterTerminalStates|TestClientUsesCapabilityTokenForSensitiveAPIs'
)

echo "[5/14] explainable personalization"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/store ./internal/daemon ./internal/daemonclient ./internal/tuisession -run 'TestStorePersistsAndSearchesMemories|TestServerServesMemoryAPI|TestClientMemoryLifecycle|TestDaemonSubmitterHandlesMemoryThroughDaemon'
)

echo "[6/14] safety gates"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/permission ./internal/task ./internal/store -run 'TestPolicyRequiresApprovalForDangerousShellAndExternalTools|TestPolicyClassifiesNetworkAndHookSideEffects|TestPolicyRejectsUntrustedPolicyOverrideAttempts|TestRunnerWaitsForPermissionBeforeDangerousShell|TestRunnerWaitsForPermissionBeforeNetworkShell|TestRepositoryMaterializesAndResolvesApprovalItems|TestAppendEventMarksExternalContentUntrusted|TestUntrustedEventsDoNotGrantApprovalOrChangePolicy|TestStorePrivacyRedactsSecretsPIIAndCredentialHints|TestStoreMemoryCandidateRedactsSecretButKeepsInstructionAsData'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/task ./internal/daemon -run 'TestRepositoryPersistsTaskThreadRelationMetadata|TestRepositoryRejectsInvalidTaskThreadRelations|TestRepositoryChildTaskDefaultsToParentPathScopeOnly|TestRunnerChildTaskDoesNotInheritParentPermissionShortcuts|TestServerTaskRelationMetadataRoundTrips|TestRepositoryTaskMetadata_defaultsPrivilegedAutomationRiskToDangerous|TestRepositoryTaskMetadata_rejectsInvalidAutomationBoundary|TestRepositoryBackgroundTaskCountsLostAndRecover|TestServerScheduleAndSubagentInheritThreadOrParentModelUnlessOverridden|TestServerParallelThreadsRouteStrongAndCheapModelsWithMetadata|TestServerThreadModelAttributionCoversNativeToolLoopAndPlannerFallback|TestServerToolFailuresRemainObservableAcrossTailTimelineAndTrace|TestServerProvider429OnlyFailsMatchingThreadModel|TestServerDangerousAutomation_pausesAndCanBeCancelled|TestServerAutomationMissingRisk_pausesFailClosed|TestServerAutomationRejectsInvalidBoundary|TestServerBackgroundControls_limitConcurrencyAndExposeCancelHandles|TestServerBackgroundControls_resourceLimitCancelLostAndRecover|TestServerForegroundThreadScheduler_allowsCrossThreadParallelismAndSessionFIFO|TestServerForegroundThreadScheduler_queuesOverCapAndFailsClosed|TestServerThreadRaceLeakRegression_concurrentEvalCancelAndFanIn|TestServerScheduleWorkspaceConflicts_coalescesCatchUpBehindForeground|TestServerScheduleWorkspaceConflicts_rejectsMalformedAndPreventsOvertake|TestServerRestartRecovery_explainsQueuedWaitingBackgroundAndScheduleState|TestServerRestartRecovery_isIdempotentAndIgnoresTerminalAndMalformedHistory|TestTaskControlCreatesSafeChildTask|TestTaskControlRejectsInvalidChildRequests|TestTaskControlReadsOutputAndStopsOnlyChild|TestTaskControlChildRunsThroughRunnerPatchModeArtifactsAndEvents|TestTaskControlChildPermissionWaitStaysOnChildTask'
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/daemon ./internal/daemonclient -run 'TestServerCapabilityGateProtectsSensitiveAPIs|TestClientUsesCapabilityTokenForSensitiveAPIs'
)

echo "[7/14] migration fixture"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./internal/store -run 'TestStoreSchemaReportMigratesOldDatabaseFixtureIdempotently|TestStoreSchemaReportReportsCorruptDatabase'
)

echo "[8/14] doctor schema tests"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./apps/cli -run 'TestDoctorReportIncludesSchemaState|TestDoctorReportRedactsAPIKey|TestDoctorReportIncludesProviderCapabilityAndThreadOverrides|TestDoctorReportIncludesRuntimeMCPAndAutomationStatus|TestDiagnosticsReportExportsOnlyRedactedMetadataAndSummaries|TestDiagnosticsReportHandlesEmptyAndMalformedEventPayloads'
)

echo "[9/14] architecture guard"
(
  cd "$ROOT"
  ./scripts/architecture-guard.sh "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./scripts -run 'TestArchitectureGuard|TestDaemonSmokeScriptCoversDaemonAPI|TestPackageReleaseScriptBuildsInstallableArchive|TestReleaseSmokeScriptInstallsArchive|TestNPMLazySmokeScriptExercisesGitHubPackageLazyBuild|TestLocalInstallSmokeScriptRunsDoctorAndWorkspaceSmoke|TestReleaseSupplyChainAudit'
)

echo "[10/14] doctor binary smoke"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go build -o "$AUDIT_TMP/liora" ./apps/cli
  command -v sqlite3 >/dev/null
  mkdir -p "$AUDIT_TMP/liora-home"
  sqlite3 "$AUDIT_TMP/liora-home/liora.db" < "$ROOT/internal/store/testdata/old-liora-v0.1.sql"
  LIORA_HOME="$AUDIT_TMP/liora-home" \
    LIORA_LLM_PROVIDER=anthropic \
    LIORA_LLM_API_KEY=audit-secret \
    LIORA_LLM_MODEL=claude-audit \
    "$AUDIT_TMP/liora" -doctor >"$AUDIT_TMP/doctor.txt"
  grep -q 'database: ok' "$AUDIT_TMP/doctor.txt"
  grep -q "schema_version: ${CURRENT_SCHEMA_VERSION:-14}" "$AUDIT_TMP/doctor.txt"
  grep -q 'migration: complete' "$AUDIT_TMP/doctor.txt"
  grep -q 'api_key: configured' "$AUDIT_TMP/doctor.txt"
  if grep -q 'audit-secret' "$AUDIT_TMP/doctor.txt"; then
    echo "doctor leaked API key" >&2
    exit 1
  fi
  cat "$AUDIT_TMP/doctor.txt"
)

echo "[11/14] daemon smoke"
(
  cd "$ROOT"
  mkdir -p "$AUDIT_TMP/home-daemon"
  LIORA_HOME="$AUDIT_TMP/home-daemon" \
  LIORA_DAEMON_ADDR="${LIORA_AUDIT_DAEMON_ADDR:-127.0.0.1:19501}" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/daemon-smoke.sh "$WORKSPACE"
)

echo "[12/14] TUI smoke"
(
  cd "$ROOT"
  LIORA_TUI_SMOKE_DAEMON_ADDR="${LIORA_AUDIT_TUI_DAEMON_ADDR:-127.0.0.1:19502}" \
  LIORA_TUI_SMOKE_LLM_ADDR="${LIORA_AUDIT_TUI_LLM_ADDR:-127.0.0.1:19503}" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/tui-smoke.sh "$WORKSPACE"
)

echo "[13/14] coding eval"
(
  cd "$ROOT"
  LIORA_EVAL_DAEMON_ADDR="${LIORA_AUDIT_EVAL_DAEMON_ADDR:-127.0.0.1:19504}" \
  LIORA_EVAL_LLM_ADDR="${LIORA_AUDIT_EVAL_LLM_ADDR:-127.0.0.1:19505}" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/coding-eval.sh
)

echo "[14/14] package smoke"
ARCHIVE_PATH="$(
  cd "$ROOT"
  LIORA_DIST_DIR="$AUDIT_TMP/dist" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/package-release.sh | tail -n 1
)"
if [[ -z "$ARCHIVE_PATH" ]]; then
  echo "package-release did not report an archive path" >&2
  exit 1
fi
(
  cd "$ROOT"
  ./scripts/release-smoke.sh "$ARCHIVE_PATH"
  GOTOOLCHAIN="$GO_TOOLCHAIN" ./scripts/npm-lazy-smoke.sh
  GOTOOLCHAIN="$GO_TOOLCHAIN" ./scripts/local-install-smoke.sh
)

echo "[post] scoped diff check"
(
  cd "$ROOT"
  files=(
    internal/store/schema.go \
    internal/store/store.go \
    internal/store/schema_test.go \
    internal/store/testdata/old-liora-v0.1.sql \
    internal/permission/permission.go \
    internal/permission/permission_test.go \
    internal/trace/trace.go \
    internal/agent/agent.go \
    internal/agent/loop.go \
    internal/agent/plan.go \
    internal/agent/task_tools.go \
    internal/agent/task_tools_test.go \
    internal/agent/todo_tools.go \
    internal/agent/tool_loop_support.go \
    internal/capabilities/tools.go \
    internal/capabilities/tools_test.go \
    internal/llm/client.go \
    internal/llm/client_http.go \
    internal/llm/client_retry_test.go \
    internal/runtime/runtime.go \
    internal/runtime/loop_test.go \
    internal/task/event_catalog.go \
    internal/task/event_catalog_test.go \
    internal/task/task.go \
    internal/task/task_control.go \
    internal/task/todo.go \
    internal/task/todo_executor.go \
    internal/task/todo_test.go \
    internal/task/queue.go \
    internal/task/store.go \
    internal/task/store_test.go \
    internal/daemon/server.go \
    internal/daemon/server_queue.go \
    internal/daemon/task_control.go \
    internal/daemon/task_control_test.go \
    internal/daemon/server_automation_test.go \
    internal/daemon/server_model_attribution_test.go \
    internal/daemon/server_turn_failure_test.go \
    internal/daemon/server_test.go \
    internal/daemonclient/client.go \
    internal/daemonclient/client_test.go \
    internal/protocol/contract.go \
    internal/protocol/contract_test.go \
    internal/protocol/testdata/daemon-event-catalog.json \
    internal/protocol/testdata/daemon-event-stream.json \
    internal/tui/completion.go \
    internal/tui/daemon_events.go \
    internal/tui/tui.go \
    internal/tui/tui_test.go \
    internal/tuisession/daemon_submitter.go \
    internal/tuisession/daemon_todo_test.go \
    internal/tuisession/daemon_submitter_test.go \
    apps/cli/doctor.go \
    apps/cli/doctor_test.go \
    apps/cli/diagnostics.go \
    apps/cli/diagnostics_test.go \
    apps/cli/main.go \
    packages/protocol/src/client.ts \
    packages/protocol/src/index.ts \
    packages/protocol/src/index.test.ts \
    scripts/architecture-guard.sh \
    scripts/architecture_guard_test.go \
    scripts/coding-eval.sh \
    scripts/daemon-smoke.sh \
    scripts/daemon_smoke_test.go \
    scripts/install_test.go \
    scripts/liora-1.0-audit.sh \
    scripts/local-install-smoke.sh \
    scripts/npm-lazy-smoke.sh \
    scripts/package-release.sh \
    scripts/release-smoke.sh \
    scripts/release-supply-chain-audit.sh \
    scripts/supply_chain_test.go \
    scripts/tui-smoke.sh \
    README.md \
    docs/release.md \
    implementation-notes.md
  )
  git diff --check -- "${files[@]}"
  for file in "${files[@]}"; do
    if LC_ALL=C grep -nE '[[:blank:]]$' "$file"; then
      echo "trailing whitespace in $file" >&2
      exit 1
    fi
    if LC_ALL=C grep -nE '^(<<<<<<<|=======|>>>>>>>)' "$file"; then
      echo "conflict marker in $file" >&2
      exit 1
    fi
  done
)

echo "liora 1.0 audit ok: $WORKSPACE package=$ARCHIVE_PATH"
