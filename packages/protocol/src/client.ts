import { z } from "zod"

const nonBlankStringSchema = z.string().refine((value) => value.trim().length > 0, {
  message: "Required",
})
const memoryKindSchema = z.enum(["note", "preference", "rule", "automation", "credential_hint"])

const clientEventPayloadSchema = z
  .object({
    id: z.string().optional(),
    message: z.string().optional(),
    action: z.string().optional(),
    target: z.string().optional(),
    path: z.string().optional(),
    tool: z.string().optional(),
    tool_call_id: z.string().optional(),
    tool_result_id: z.string().optional(),
    input: z.string().optional(),
    output: z.string().optional(),
    status: z.string().optional(),
    steps: z.string().optional(),
    diff: z.string().optional(),
    risk: z.string().optional(),
    reason: z.string().optional(),
    origin: z.string().optional(),
    kind: z.string().optional(),
    source: z.string().optional(),
    trigger: z.string().optional(),
    missed_runs: z.number().int().optional(),
    catch_up_policy: z.string().optional(),
    catch_up_runs: z.number().int().optional(),
    expires_at: z.string().optional(),
    stale_at: z.string().optional(),
    timeout_seconds: z.number().int().optional(),
    trust: z.enum(["trusted", "untrusted"]).optional(),
    content_source: z.string().optional(),
    parent_task_id: z.string().optional(),
    parent_thread_id: z.string().optional(),
    child_thread_id: z.string().optional(),
    subagent_name: z.string().optional(),
    task_id: z.string().optional(),
    session_id: z.string().optional(),
    thread_id: z.string().optional(),
    from_thread_id: z.string().optional(),
    to_thread_id: z.string().optional(),
    relation: z.string().optional(),
    role: z.string().optional(),
    content: z.string().optional(),
    provider: z.string().optional(),
    model: z.string().optional(),
    profile: z.string().optional(),
    memory_id: z.string().optional(),
    artifact_id: z.string().optional(),
    enabled: z.boolean().optional(),
    importance: z.number().int().optional(),
    token_estimate: z.number().int().optional(),
    token_budget: z.number().int().optional(),
  })
  .strict()

export const automationMetadataSchema = z
  .object({
    kind: z.string().optional(),
    risk: z.string().optional(),
    source: z.string().optional(),
    trigger: z.string().optional(),
  })
  .strict()

export const taskScopeSchema = z
  .object({
    paths: z.array(z.string()).optional(),
    network_hosts: z.array(z.string()).optional(),
    mcp_servers: z.array(z.string()).optional(),
    mcp_tools: z.array(z.string()).optional(),
    approval_actions: z.array(z.string()).optional(),
  })
  .strict()

export const modelCapabilitySchema = z
  .object({
    native_tool_use: z.boolean(),
    streaming: z.boolean(),
    vision: z.boolean(),
    long_context: z.boolean(),
    json_schema: z.boolean(),
    max_output_tokens: z.number().int().nonnegative(),
  })
  .strict()

export const taskModelConfigSchema = z
  .object({
    provider: z.string().optional(),
    model: z.string().optional(),
    base_url: z.string().optional(),
    profile: z.string().optional(),
    source: z.string().optional(),
    capability: modelCapabilitySchema.optional(),
  })
  .strict()

export const taskSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional(),
    title: z.string(),
    user_input: z.string(),
    natural: z.boolean(),
    status: z.string(),
    workspace: z.string(),
    origin: z.string(),
    automation: automationMetadataSchema,
    approval_granted: z.boolean().optional(),
    parent_task_id: z.string().optional(),
    parent_thread_id: z.string().optional(),
    child_thread_id: z.string().optional(),
    subagent_name: z.string().optional(),
    role: z.string().optional(),
    scope: taskScopeSchema.optional(),
    inherited_scope_from_parent: z.boolean().optional(),
    approval_grants: z.array(z.string()).optional(),
    model_config: taskModelConfigSchema.optional(),
    created_at: z.string(),
    updated_at: z.string(),
    completed_at: z.string().optional(),
  })
  .strict()

export const createTaskRequestSchema = z
  .object({
    workspace: z.string(),
    prompt: z.string(),
    session_id: z.string().optional(),
    thread_id: nonBlankStringSchema.optional(),
    natural: z.boolean().optional(),
    run_async: z.boolean(),
    queue: z.boolean().optional(),
    origin: z.string().optional(),
    automation: automationMetadataSchema.optional(),
    schedule: z
      .object({
        id: z.string().optional(),
        catch_up_policy: z.string().optional(),
        missed_runs: z.number().int().optional(),
        max_catch_up_runs: z.number().int().optional(),
        catch_up_runs: z.number().int().optional(),
      })
      .strict()
      .optional(),
    parent_task_id: z.string().optional(),
    parent_thread_id: z.string().optional(),
    child_thread_id: z.string().optional(),
    subagent_name: z.string().optional(),
    role: z.string().optional(),
    scope: taskScopeSchema.optional(),
    approval_grants: z.array(z.string()).optional(),
    auto_approve_parent: z.boolean().optional(),
    model_config: taskModelConfigSchema.optional(),
  })
  .strict()

export const createTaskResponseSchema = z.object({ task: taskSchema }).strict()

export const sessionSchema = z
  .object({
    id: z.string(),
    title: z.string(),
    workspace: z.string(),
    last_task_id: z.string().optional(),
    created_at: z.string(),
    updated_at: z.string(),
  })
  .strict()

export const createSessionRequestSchema = z
  .object({
    workspace: z.string(),
    title: z.string().optional(),
  })
  .strict()

export const createSessionResponseSchema = z.object({ session: sessionSchema }).strict()

export const threadModelConfigSchema = z
  .object({
    thread_id: z.string(),
    provider: z.string(),
    model: z.string(),
    base_url: z.string().optional(),
    profile: z.string().optional(),
    inherited_from_thread_id: z.string().optional(),
    capability: modelCapabilitySchema.optional(),
  })
  .strict()

export const conversationThreadSchema = sessionSchema.extend({
  archived_at: z.string().optional(),
  model_config: threadModelConfigSchema.optional(),
})

export const createConversationThreadRequestSchema = z
  .object({
    workspace: nonBlankStringSchema,
    title: z.string().optional(),
  })
  .strict()

export const updateConversationThreadRequestSchema = z
  .object({
    title: z.string().optional(),
    archived: z.boolean().optional(),
  })
  .strict()

export const updateThreadModelConfigRequestSchema = z
  .object({
    provider: z.string().optional(),
    model: z.string().optional(),
    base_url: z.string().optional(),
    profile: z.string().optional(),
    inherited_from_thread_id: z.string().optional(),
  })
  .strict()

export const crossThreadMessageSchema = z
  .object({
    id: z.string(),
    from_thread_id: z.string(),
    to_thread_id: z.string(),
    from_workspace: z.string(),
    to_workspace: z.string(),
    task_id: z.string().optional(),
    role: z.string(),
    content: z.string(),
    summary: z.string().optional(),
    explicit_content: z.string().optional(),
    artifact_refs: z
      .array(
        z
          .object({
            path: z.string(),
            summary: z.string().optional(),
          })
          .strict(),
      )
      .optional(),
    cross_workspace_authorized: z.boolean().optional(),
    cross_workspace_auth_reason: z.string().optional(),
    includes_prompt: z.boolean(),
    includes_secret: z.boolean(),
    includes_memory: z.boolean(),
    includes_approval_rule: z.boolean(),
    created_at: z.string(),
  })
  .strict()

export const createCrossThreadMessageRequestSchema = z
  .object({
    from_thread_id: nonBlankStringSchema,
    to_thread_id: z.string().optional(),
    task_id: z.string().optional(),
    summary: z.string().optional(),
    explicit_content: z.string().optional(),
    artifact_refs: z
      .array(
        z
          .object({
            path: nonBlankStringSchema,
            summary: z.string().optional(),
          })
          .strict(),
      )
      .optional(),
    cross_workspace_authorized: z.boolean().optional(),
    cross_workspace_auth_reason: z.string().optional(),
  })
  .strict()

export const messageSchema = z
  .object({
    id: z.string(),
    session_id: z.string(),
    role: z.string(),
    content: z.string(),
    task_id: z.string().optional(),
    created_at: z.string(),
  })
  .strict()

export const timelineItemSchema = z
  .object({
    id: z.string(),
    session_id: z.string(),
    task_id: z.string().optional(),
    kind: z.string(),
    role: z.string().optional(),
    type: z.string().optional(),
    title: z.string().optional(),
    content: z.string().optional(),
    tool: z.string().optional(),
    tool_call_id: z.string().optional(),
    tool_result_id: z.string().optional(),
    input: z.string().optional(),
    output: z.string().optional(),
    target: z.string().optional(),
    status: z.string().optional(),
    diff: z.string().optional(),
    risk: z.string().optional(),
    reason: z.string().optional(),
    provider: z.string().optional(),
    model: z.string().optional(),
    profile: z.string().optional(),
    created_at: z.string(),
  })
  .strict()

export const contextRequestSchema = z
  .object({
    item_limit: z.number().int().positive().optional(),
    token_budget: z.number().int().positive().optional(),
  })
  .strict()

export const contextBudgetSchema = z
  .object({
    max_tokens: z.number().int(),
    estimated_tokens: z.number().int(),
    item_limit: z.number().int(),
    truncated: z.boolean(),
    buckets: z.array(
      z
        .object({
          name: z.enum(["system", "user", "transcript", "memory", "tool_result", "artifact_preview"]),
          estimated_tokens: z.number().int().nonnegative(),
          items: z.number().int().nonnegative(),
        })
        .strict(),
    ),
  })
  .strict()

export const contextSummarySchema = z
  .object({
    task_id: z.string().optional(),
    content: z.string(),
    created_at: z.string(),
  })
  .strict()

export const contextArtifactRefSchema = z
  .object({
    task_id: z.string().optional(),
    tool: z.string().optional(),
    path: z.string().optional(),
    summary: z.string().optional(),
    created_at: z.string(),
  })
  .strict()

export const contextCompactBoundarySchema = z
  .object({
    task_id: z.string().optional(),
    summary: z.string(),
    token_budget: z.number().int().optional(),
    token_estimate: z.number().int().optional(),
    source_start_id: z.string().optional(),
    source_end_id: z.string().optional(),
    source_item_count: z.number().int().nonnegative().optional(),
    created_at: z.string(),
  })
  .strict()

export const contextTodoSchema = z
  .object({
    id: z.string(),
    session_id: z.string(),
    source_task_id: z.string().optional(),
    content: z.string(),
    status: z.string(),
    priority: z.string(),
    created_at: z.string(),
    updated_at: z.string(),
  })
  .strict()

export const contextMemorySchema = z
  .object({
    id: z.string(),
    text: z.string(),
    kind: z.string(),
    source: z.string().optional(),
    workspace: z.string().optional(),
    importance: z.number().int().nonnegative(),
    created_at: z.string(),
    updated_at: z.string(),
    expires_at: z.string().optional(),
  })
  .strict()

export const contextPackSchema = z
  .object({
    sources: z.array(
      z
        .object({
          name: z.enum(["transcript", "todo", "memory", "artifact_preview"]),
          selected: z.number().int().nonnegative(),
          available: z.number().int().nonnegative(),
          estimated_tokens: z.number().int().nonnegative(),
          truncated: z.boolean(),
        })
        .strict(),
    ),
  })
  .strict()

export const contextDiagnosticSchema = z
  .object({
    source: z.enum(["transcript", "tool_result", "todo", "memory", "artifact_preview"]),
    item_id: z.string().optional(),
    item_kind: z.string().optional(),
    reason: z.string(),
    summary: z.string().optional(),
    estimated_tokens: z.number().int().nonnegative(),
    created_at: z.string().optional(),
  })
  .strict()

export const contextEnvelopeSchema = z
  .object({
    session: sessionSchema,
    budget: contextBudgetSchema,
    transcript: z.array(timelineItemSchema),
    todos: z.array(contextTodoSchema).optional().default([]),
    memories: z.array(contextMemorySchema).optional().default([]),
    summaries: z.array(contextSummarySchema),
    artifact_refs: z.array(contextArtifactRefSchema),
    compact_boundaries: z.array(contextCompactBoundarySchema),
    pack: contextPackSchema.optional().default({ sources: [] }),
    diagnostics: z.array(contextDiagnosticSchema).optional().default([]),
    generated_at: z.string(),
  })
  .strict()

export const compactRequestSchema = z
  .object({
    mode: z.enum(["manual", "auto"]).optional(),
    item_limit: z.number().int().nonnegative().optional(),
    token_budget: z.number().int().nonnegative().optional(),
    reason: z.string().optional(),
  })
  .strict()

export const compactResultSchema = z
  .object({
    session: sessionSchema,
    mode: z.enum(["manual", "auto"]),
    compacted: z.boolean(),
    skipped_reason: z.string().optional(),
    reason: z.string().optional(),
    token_budget: z.number().int().nonnegative(),
    before_estimated_tokens: z.number().int().nonnegative(),
    after_estimated_tokens: z.number().int().nonnegative(),
    transcript_items: z.number().int().nonnegative(),
    boundary: contextCompactBoundarySchema.optional(),
    generated_at: z.string(),
  })
  .strict()

export const memorySchema = z
  .object({
    id: z.string(),
    text: z.string(),
    kind: memoryKindSchema,
    mood: z.string().optional(),
    source: z.string().optional(),
    workspace: z.string().optional(),
    redaction: z.string().optional(),
    importance: z.number().int().min(1).max(5),
    enabled: z.boolean(),
    created_at: z.string(),
    updated_at: z.string(),
    last_used_at: z.string().optional(),
    expires_at: z.string().optional(),
  })
  .strict()

export const createMemoryRequestSchema = z
  .object({
    text: nonBlankStringSchema,
    kind: memoryKindSchema.optional(),
    source: z.string().optional(),
    workspace: z.string().optional(),
    importance: z.number().int().min(1).max(5).optional(),
    expires_at: z.string().optional(),
  })
  .strict()

export const updateMemoryRequestSchema = z
  .object({
    text: nonBlankStringSchema.optional(),
    kind: memoryKindSchema.optional(),
    source: z.string().optional(),
    workspace: z.string().optional(),
    importance: z.number().int().min(1).max(5).optional(),
    enabled: z.boolean().optional(),
    expires_at: z.string().optional(),
  })
  .strict()

export const toolSpecSchema = z
  .object({
    name: z.string(),
    usage: z.string(),
    description: z.string().optional(),
    kind: z.string(),
    server: z.string().optional(),
    input_schema: z.unknown().optional(),
  })
  .strict()

export const capabilitiesResponseSchema = z
  .object({
    tools: z.array(toolSpecSchema),
    mcp_tools: z.array(toolSpecSchema).optional().default([]),
    mcp_error: z.string().optional().default(""),
    model_capability: modelCapabilitySchema.optional(),
  })
  .strict()

export const scheduleSpecSchema = z
  .object({
    id: z.string(),
    trigger: z.string(),
    prompt: z.string(),
    enabled: z.boolean(),
    source: z.string().optional(),
  })
  .strict()

export const hookSpecSchema = z
  .object({
    id: z.string(),
    event: z.string(),
    command: z.string(),
    enabled: z.boolean(),
    source: z.string().optional(),
  })
  .strict()

export const artifactRefSchema = contextArtifactRefSchema

const nullableSessionsSchema = z.array(sessionSchema).nullable().transform((sessions) => sessions ?? [])
const nullableTasksSchema = z.array(taskSchema).nullable().transform((tasks) => tasks ?? [])

export const approvalItemSchema = z
  .object({
    id: z.string(),
    task_id: z.string(),
    tool_call_id: z.string().optional(),
    tool_name: z.string(),
    args_preview: z.string().optional(),
    risk: z.string().optional(),
    command_preview: z.string().optional(),
    diff_preview: z.string().optional(),
    reason: z.string().optional(),
    status: z.string(),
    decision: z.string().optional(),
    decided_by: z.string().optional(),
    resolved_at: z.string().optional(),
    created_at: z.string(),
    updated_at: z.string(),
  })
  .strict()

export const pendingApprovalSchema = z
  .object({
    task: taskSchema,
    request: clientEventPayloadSchema,
    item: approvalItemSchema.optional(),
  })
  .strict()

export const backgroundTaskOutputSchema = z
  .object({
    task_id: z.string(),
    status: z.string(),
    title: z.string().optional(),
    output: z.string().optional(),
    artifact_uri: z.string().optional(),
    artifact_tail_hint: z.string().optional(),
    updated_at: z.string(),
  })
  .strict()

export const threadWorkbenchSchema = z
  .object({
    id: z.string(),
    title: z.string(),
    workspace: z.string(),
    last_task_id: z.string().optional(),
    transcript_session_id: z.string(),
    context_session_id: z.string(),
    lifecycle: z.string(),
    model_config: threadModelConfigSchema.optional(),
    active_tasks: nullableTasksSchema,
    queued_tasks: nullableTasksSchema,
    recent_tasks: nullableTasksSchema,
    pending_approvals: z.array(pendingApprovalSchema).nullable().transform((items) => items ?? []),
    pending_user_inputs: z.array(pendingApprovalSchema).nullable().transform((items) => items ?? []),
  })
  .strict()

export const workbenchSchema = z
  .object({
    workspace: z.string().optional(),
    sessions: nullableSessionsSchema,
    threads: z.array(threadWorkbenchSchema).nullable().transform((threads) => threads ?? []),
    active_tasks: nullableTasksSchema,
    queued_tasks: nullableTasksSchema,
    recent_tasks: nullableTasksSchema,
    background_tasks: nullableTasksSchema.optional().transform((tasks) => tasks ?? []),
    background_unfinished_tasks: nullableTasksSchema.optional().transform((tasks) => tasks ?? []),
    background_lost_tasks: nullableTasksSchema.optional().transform((tasks) => tasks ?? []),
    background_completed_tasks: nullableTasksSchema.optional().transform((tasks) => tasks ?? []),
    background_outputs: z
      .array(backgroundTaskOutputSchema)
      .nullable()
      .optional()
      .transform((outputs) => outputs ?? []),
    pending_approvals: z.array(pendingApprovalSchema).nullable().transform((items) => items ?? []),
    pending_user_inputs: z.array(pendingApprovalSchema).nullable().transform((items) => items ?? []),
  })
  .strict()

export const daemonTaskEventSchema = z
  .object({
    seq: z.number(),
    id: z.string(),
    task_id: z.string(),
    type: z.string(),
    payload_json: z.string(),
    created_at: z.string(),
  })
  .strict()

export const taskEventStreamFrameSchema = z
  .object({
    event: z.string(),
    id: z.string(),
    data: daemonTaskEventSchema,
  })
  .strict()

export type DaemonTask = z.infer<typeof taskSchema>
export type CreateTaskRequest = z.infer<typeof createTaskRequestSchema>
export type CreateTaskResponse = z.infer<typeof createTaskResponseSchema>
export type Session = z.infer<typeof sessionSchema>
export type CreateSessionRequest = z.infer<typeof createSessionRequestSchema>
export type CreateSessionResponse = z.infer<typeof createSessionResponseSchema>
export type ConversationThread = z.infer<typeof conversationThreadSchema>
export type CreateConversationThreadRequest = z.infer<typeof createConversationThreadRequestSchema>
export type UpdateConversationThreadRequest = z.infer<typeof updateConversationThreadRequestSchema>
export type ThreadModelConfig = z.infer<typeof threadModelConfigSchema>
export type UpdateThreadModelConfigRequest = z.infer<typeof updateThreadModelConfigRequestSchema>
export type ModelCapability = z.infer<typeof modelCapabilitySchema>
export type CrossThreadMessage = z.infer<typeof crossThreadMessageSchema>
export type CreateCrossThreadMessageRequest = z.infer<typeof createCrossThreadMessageRequestSchema>
export type Message = z.infer<typeof messageSchema>
export type TimelineItem = z.infer<typeof timelineItemSchema>
export type ContextRequest = z.infer<typeof contextRequestSchema>
export type ContextTodo = z.infer<typeof contextTodoSchema>
export type ContextMemory = z.infer<typeof contextMemorySchema>
export type ContextPack = z.infer<typeof contextPackSchema>
export type ContextDiagnostic = z.infer<typeof contextDiagnosticSchema>
export type ContextEnvelope = z.infer<typeof contextEnvelopeSchema>
export type CompactRequest = z.infer<typeof compactRequestSchema>
export type CompactResult = z.infer<typeof compactResultSchema>
export type Memory = z.infer<typeof memorySchema>
export type CreateMemoryRequest = z.infer<typeof createMemoryRequestSchema>
export type UpdateMemoryRequest = z.infer<typeof updateMemoryRequestSchema>
export type ToolSpec = z.infer<typeof toolSpecSchema>
export type CapabilitiesResponse = z.infer<typeof capabilitiesResponseSchema>
export type ScheduleSpec = z.infer<typeof scheduleSpecSchema>
export type HookSpec = z.infer<typeof hookSpecSchema>
export type ThreadWorkbench = z.infer<typeof threadWorkbenchSchema>
export type Workbench = z.infer<typeof workbenchSchema>
export type DaemonTaskEvent = z.infer<typeof daemonTaskEventSchema>
export type TaskEventStreamFrame = z.infer<typeof taskEventStreamFrameSchema>

type DaemonFetch = (input: string | URL, init?: RequestInit) => Promise<Response>

export class DaemonProtocolError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message)
    this.name = "DaemonProtocolError"
  }
}

export function createDaemonProtocolClient(options: { baseUrl: string; fetch?: DaemonFetch }) {
  const baseUrl = options.baseUrl.replace(/\/+$/, "")
  const fetcher = options.fetch ?? globalThis.fetch
  if (fetcher === undefined) {
    throw new DaemonProtocolError("fetch is not available")
  }

  async function request<T>(
    path: string,
    init: RequestInit,
    schema: z.ZodType<T>,
    okStatuses: readonly number[],
  ): Promise<T> {
    const response = await fetcher(baseUrl + path, {
      ...init,
      headers: {
        "content-type": "application/json",
        ...(init.headers ?? {}),
      },
    })
    const body = await response.text()
    const parsed = body === "" ? undefined : parseJSON(body)
    if (!okStatuses.includes(response.status)) {
      const message =
        typeof parsed === "object" && parsed !== null && "error" in parsed
          ? String((parsed as { error: unknown }).error)
          : response.statusText
      throw new DaemonProtocolError(`daemon API ${response.status}: ${message}`, response.status)
    }
    return schema.parse(parsed)
  }

  return {
    createTask(requestBody: CreateTaskRequest): Promise<CreateTaskResponse> {
      return request(
        "/v1/tasks",
        { method: "POST", body: JSON.stringify(createTaskRequestSchema.parse(requestBody)) },
        createTaskResponseSchema,
        [201, 202],
      )
    },
    createSession(requestBody: CreateSessionRequest): Promise<CreateSessionResponse> {
      return request(
        "/v1/sessions",
        { method: "POST", body: JSON.stringify(createSessionRequestSchema.parse(requestBody)) },
        createSessionResponseSchema,
        [201],
      )
    },
    getSession(sessionID: string): Promise<Session> {
      return request(`/v1/sessions/${pathID("session id", sessionID)}`, { method: "GET" }, sessionSchema, [200])
    },
    listSessions(params: { workspace?: string; limit?: number } = {}): Promise<Session[]> {
      const suffix = listQuery(params)
      return request(`/v1/sessions${suffix}`, { method: "GET" }, z.array(sessionSchema), [200])
    },
    createConversationThread(requestBody: CreateConversationThreadRequest): Promise<ConversationThread> {
      return request(
        "/v1/threads",
        { method: "POST", body: JSON.stringify(createConversationThreadRequestSchema.parse(requestBody)) },
        conversationThreadSchema,
        [201],
      )
    },
    getConversationThread(threadID: string): Promise<ConversationThread> {
      return request(`/v1/threads/${pathID("thread id", threadID)}`, { method: "GET" }, conversationThreadSchema, [200])
    },
    updateConversationThread(threadID: string, requestBody: UpdateConversationThreadRequest): Promise<ConversationThread> {
      return request(
        `/v1/threads/${pathID("thread id", threadID)}`,
        { method: "PATCH", body: JSON.stringify(updateConversationThreadRequestSchema.parse(requestBody)) },
        conversationThreadSchema,
        [200],
      )
    },
    listConversationThreads(params: { workspace?: string; limit?: number; include_archived?: boolean } = {}): Promise<ConversationThread[]> {
      const suffix = threadListQuery(params)
      return request(`/v1/threads${suffix}`, { method: "GET" }, z.array(conversationThreadSchema), [200])
    },
    getThreadModelConfig(threadID: string): Promise<ThreadModelConfig> {
      return request(`/v1/threads/${pathID("thread id", threadID)}/model`, { method: "GET" }, threadModelConfigSchema, [200])
    },
    updateThreadModelConfig(threadID: string, requestBody: UpdateThreadModelConfigRequest): Promise<ThreadModelConfig> {
      return request(
        `/v1/threads/${pathID("thread id", threadID)}/model`,
        { method: "PATCH", body: JSON.stringify(updateThreadModelConfigRequestSchema.parse(requestBody)) },
        threadModelConfigSchema,
        [200],
      )
    },
    deleteThreadModelConfig(threadID: string): Promise<void> {
      return request(`/v1/threads/${pathID("thread id", threadID)}/model`, { method: "DELETE" }, z.void(), [204])
    },
    createCrossThreadMessage(
      toThreadID: string,
      requestBody: CreateCrossThreadMessageRequest,
    ): Promise<CrossThreadMessage> {
      return request(
        `/v1/threads/${pathID("thread id", toThreadID)}/messages`,
        { method: "POST", body: JSON.stringify(createCrossThreadMessageRequestSchema.parse(requestBody)) },
        crossThreadMessageSchema,
        [201],
      )
    },
    listCrossThreadMessages(toThreadID: string, params: { limit?: number } = {}): Promise<CrossThreadMessage[]> {
      assertNonNegativeLimit(params.limit)
      const query = new URLSearchParams()
      if (params.limit !== undefined && params.limit > 0) {
        query.set("limit", String(params.limit))
      }
      const suffix = query.size > 0 ? `?${query.toString()}` : ""
      return request(`/v1/threads/${pathID("thread id", toThreadID)}/messages${suffix}`, { method: "GET" }, z.array(crossThreadMessageSchema), [200])
    },
    sessionMessages(sessionID: string, params: { limit?: number } = {}): Promise<Message[]> {
      const suffix = listQuery(params)
      return request(
        `/v1/sessions/${pathID("session id", sessionID)}/messages${suffix}`,
        { method: "GET" },
        z.array(messageSchema),
        [200],
      )
    },
    sessionContext(sessionID: string, params: ContextRequest = {}): Promise<ContextEnvelope> {
      const query = new URLSearchParams()
      const parsed = contextRequestSchema.parse(params)
      if (parsed.item_limit !== undefined) {
        query.set("limit", String(parsed.item_limit))
      }
      if (parsed.token_budget !== undefined) {
        query.set("token_budget", String(parsed.token_budget))
      }
      const suffix = query.size > 0 ? `?${query.toString()}` : ""
      return request(
        `/v1/sessions/${pathID("session id", sessionID)}/context${suffix}`,
        { method: "GET" },
        contextEnvelopeSchema,
        [200],
      )
    },
    compactSession(sessionID: string, requestBody: CompactRequest = {}): Promise<CompactResult> {
      return request(
        `/v1/sessions/${pathID("session id", sessionID)}/compact`,
        { method: "POST", body: JSON.stringify(compactRequestSchema.parse(requestBody)) },
        compactResultSchema,
        [200],
      )
    },
    capabilities(): Promise<CapabilitiesResponse> {
      return request("/v1/capabilities", { method: "GET" }, capabilitiesResponseSchema, [200])
    },
    listMemories(
      params: { q?: string; workspace?: string; limit?: number; include_disabled?: boolean; include_expired?: boolean } = {},
    ): Promise<Memory[]> {
      assertNonNegativeLimit(params.limit)
      const query = new URLSearchParams()
      if (params.q !== undefined && params.q !== "") {
        query.set("q", params.q)
      }
      if (params.workspace !== undefined && params.workspace !== "") {
        query.set("workspace", params.workspace)
      }
      if (params.limit !== undefined && params.limit > 0) {
        query.set("limit", String(params.limit))
      }
      if (params.include_disabled === true) {
        query.set("include_disabled", "true")
      }
      if (params.include_expired === true) {
        query.set("include_expired", "true")
      }
      const suffix = query.size > 0 ? `?${query.toString()}` : ""
      return request(`/v1/memories${suffix}`, { method: "GET" }, z.array(memorySchema), [200])
    },
    createMemory(requestBody: CreateMemoryRequest): Promise<Memory> {
      return request(
        "/v1/memories",
        { method: "POST", body: JSON.stringify(createMemoryRequestSchema.parse(requestBody)) },
        memorySchema,
        [201],
      )
    },
    getMemory(memoryID: string): Promise<Memory> {
      return request(`/v1/memories/${pathID("memory id", memoryID)}`, { method: "GET" }, memorySchema, [200])
    },
    updateMemory(memoryID: string, requestBody: UpdateMemoryRequest): Promise<Memory> {
      return request(
        `/v1/memories/${pathID("memory id", memoryID)}`,
        { method: "PATCH", body: JSON.stringify(updateMemoryRequestSchema.parse(requestBody)) },
        memorySchema,
        [200],
      )
    },
    setMemoryEnabled(memoryID: string, enabled: boolean): Promise<Memory> {
      const action = enabled ? "enable" : "disable"
      return request(
        `/v1/memories/${pathID("memory id", memoryID)}/${action}`,
        { method: "POST", body: "{}" },
        memorySchema,
        [200],
      )
    },
    deleteMemory(memoryID: string, params: { workspace?: string } = {}): Promise<void> {
      const query = new URLSearchParams()
      if (params.workspace !== undefined && params.workspace !== "") {
        query.set("workspace", params.workspace)
      }
      const suffix = query.size > 0 ? `?${query.toString()}` : ""
      return request(
        `/v1/memories/${pathID("memory id", memoryID)}${suffix}`,
        { method: "DELETE" },
        z.undefined(),
        [204],
      )
    },
    workbench(params: { workspace?: string; limit?: number } = {}): Promise<Workbench> {
      const suffix = listQuery(params)
      return request(`/v1/workbench${suffix}`, { method: "GET" }, workbenchSchema, [200])
    },
  }
}

export function parseTaskEventStream(input: string): TaskEventStreamFrame[] {
  const frames: TaskEventStreamFrame[] = []
  for (const block of input.split(/\n\n+/)) {
    const lines = block.split(/\n/).filter((line) => line.trim() !== "")
    if (lines.length === 0) {
      continue
    }
    let event = ""
    let id = ""
    const data: string[] = []
    for (const line of lines) {
      if (line.startsWith("event:")) {
        event = line.slice("event:".length).trim()
      } else if (line.startsWith("id:")) {
        id = line.slice("id:".length).trim()
      } else if (line.startsWith("data:")) {
        data.push(line.slice("data:".length).trimStart())
      }
    }
    if (event === "" || id === "" || data.length === 0) {
      throw new DaemonProtocolError("malformed daemon SSE frame")
    }
    frames.push(taskEventStreamFrameSchema.parse({ event, id, data: parseJSON(data.join("\n")) }))
  }
  return frames
}

export function parseTaskEventPayload(event: DaemonTaskEvent) {
  return clientEventPayloadSchema.parse(parseJSON(event.payload_json))
}

function parseJSON(input: string): unknown {
  try {
    return JSON.parse(input)
  } catch (error) {
    throw new DaemonProtocolError(error instanceof Error ? error.message : "invalid JSON")
  }
}

function pathID(label: string, value: string): string {
  const trimmed = value.trim()
  if (trimmed === "") {
    throw new DaemonProtocolError(`${label} is required`)
  }
  return encodeURIComponent(trimmed)
}

function assertNonNegativeLimit(limit: number | undefined): void {
  if (limit !== undefined && limit < 0) {
    throw new DaemonProtocolError("limit cannot be negative")
  }
}

function listQuery(params: { workspace?: string; limit?: number }): string {
	assertNonNegativeLimit(params.limit)
	const query = new URLSearchParams()
  if (params.workspace !== undefined && params.workspace !== "") {
    query.set("workspace", params.workspace)
  }
  if (params.limit !== undefined && params.limit > 0) {
    query.set("limit", String(params.limit))
	}
	return query.size > 0 ? `?${query.toString()}` : ""
}

function threadListQuery(params: { workspace?: string; limit?: number; include_archived?: boolean }): string {
	assertNonNegativeLimit(params.limit)
	const query = new URLSearchParams()
	if (params.workspace !== undefined && params.workspace !== "") {
		query.set("workspace", params.workspace)
	}
	if (params.limit !== undefined && params.limit > 0) {
		query.set("limit", String(params.limit))
	}
	if (params.include_archived) {
		query.set("include_archived", "true")
	}
	return query.size > 0 ? `?${query.toString()}` : ""
}
