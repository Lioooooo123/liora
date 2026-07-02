import { z } from "zod"

export const EVENT_CONTRACT_VERSION = "2026-06-30.task-events.v1" as const
const nonBlankStringSchema = z.string().refine((value) => value.trim().length > 0, {
  message: "Required",
})

export const eventPayloadSchema = z
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
    latency_ms: z.number().int().optional(),
    retry_count: z.number().int().optional(),
    stop_reason: z.string().optional(),
    replan_reason: z.string().optional(),
  })
  .strict()

export const singleTaskFrameSchema = z
  .object({
    event: nonBlankStringSchema,
    id: nonBlankStringSchema,
    data: eventPayloadSchema,
  })
  .strict()

export const taskEnvelopeSchema = z
  .object({
    task_id: nonBlankStringSchema,
    payload: eventPayloadSchema,
  })
  .strict()

export const taskEnvelopeFrameSchema = z
  .object({
    event: nonBlankStringSchema,
    id: nonBlankStringSchema,
    data: taskEnvelopeSchema,
  })
  .strict()

export const threadEnvelopeSchema = z
  .object({
    thread_id: nonBlankStringSchema,
    task_id: nonBlankStringSchema,
    payload: eventPayloadSchema,
  })
  .strict()

export const threadEnvelopeFrameSchema = z
  .object({
    event: nonBlankStringSchema,
    id: nonBlankStringSchema,
    data: threadEnvelopeSchema,
  })
  .strict()

export const errorResponseSchema = z
  .object({
    status: z.number().int().positive(),
    error: nonBlankStringSchema,
  })
  .strict()

export const daemonEventFixtureSchema = z
  .object({
    version: z.literal(EVENT_CONTRACT_VERSION),
    contract_version: z.literal(EVENT_CONTRACT_VERSION),
    single_task_stream: z.array(singleTaskFrameSchema),
    multi_task_stream: z.array(taskEnvelopeFrameSchema),
    multi_thread_stream: z.array(threadEnvelopeFrameSchema),
    error_response: errorResponseSchema,
  })
  .strict()

export const daemonEventCatalogFrameSchema = z
  .object({
    event: nonBlankStringSchema,
    data: eventPayloadSchema,
  })
  .strict()

export const daemonEventCatalogFixtureSchema = z
  .object({
    version: z.literal(EVENT_CONTRACT_VERSION),
    contract_version: z.literal(EVENT_CONTRACT_VERSION),
    events: z.array(daemonEventCatalogFrameSchema),
  })
  .strict()

export type EventPayload = z.infer<typeof eventPayloadSchema>
export type SingleTaskFrame = z.infer<typeof singleTaskFrameSchema>
export type TaskEnvelope = z.infer<typeof taskEnvelopeSchema>
export type TaskEnvelopeFrame = z.infer<typeof taskEnvelopeFrameSchema>
export type ThreadEnvelope = z.infer<typeof threadEnvelopeSchema>
export type ThreadEnvelopeFrame = z.infer<typeof threadEnvelopeFrameSchema>
export type ErrorResponse = z.infer<typeof errorResponseSchema>
export type DaemonEventFixture = z.infer<typeof daemonEventFixtureSchema>
export type DaemonEventCatalogFrame = z.infer<typeof daemonEventCatalogFrameSchema>
export type DaemonEventCatalogFixture = z.infer<typeof daemonEventCatalogFixtureSchema>

export type TaskEventView = {
  readonly event: string
  readonly id: string
  readonly data: EventPayload
}

export type TaskEventsView = {
  readonly task_id: string
  readonly events: readonly TaskEventView[]
}

export type ThreadEventsView = {
  readonly thread_id: string
  readonly task_id: string
  readonly events: readonly TaskEventView[]
}

export type DaemonEventView = {
  readonly version: typeof EVENT_CONTRACT_VERSION
  readonly contract_version: typeof EVENT_CONTRACT_VERSION
  readonly tasks: readonly TaskEventsView[]
  readonly threads: readonly ThreadEventsView[]
}

export function parseDaemonEventFixture(input: unknown): DaemonEventFixture {
  return daemonEventFixtureSchema.parse(input)
}

export function parseDaemonEventCatalogFixture(input: unknown): DaemonEventCatalogFixture {
  return daemonEventCatalogFixtureSchema.parse(input)
}

export function reduceDaemonEventFixture(
  fixture: DaemonEventFixture,
  singleTaskID: string,
): DaemonEventView {
  const eventsByTask = new Map<string, readonly TaskEventView[]>()
  const eventsByThread = new Map<string, ThreadEventsView>()

  for (const frame of fixture.single_task_stream) {
    eventsByTask.set(singleTaskID, [
      ...(eventsByTask.get(singleTaskID) ?? []),
      { event: frame.event, id: frame.id, data: frame.data },
    ])
  }
  for (const frame of fixture.multi_task_stream) {
    const taskID = frame.data.task_id
    eventsByTask.set(taskID, [
      ...(eventsByTask.get(taskID) ?? []),
      { event: frame.event, id: frame.id, data: frame.data.payload },
    ])
  }
  for (const frame of fixture.multi_thread_stream) {
    const prior = eventsByThread.get(frame.data.thread_id)
    const event = { event: frame.event, id: frame.id, data: frame.data.payload }
    eventsByThread.set(frame.data.thread_id, {
      thread_id: frame.data.thread_id,
      task_id: frame.data.task_id,
      events: [...(prior?.events ?? []), event],
    })
  }

  return {
    version: fixture.version,
    contract_version: fixture.contract_version,
    tasks: Array.from(eventsByTask, ([taskID, events]) => ({
      task_id: taskID,
      events,
    })),
    threads: Array.from(eventsByThread.values()),
  }
}

export * from "./client"
