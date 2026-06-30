import { z } from "zod"

export const EVENT_CONTRACT_VERSION = "2026-06-30.task-events.v1" as const

export const eventPayloadSchema = z
  .object({
    message: z.string().optional(),
    tool: z.string().optional(),
    input: z.string().optional(),
    output: z.string().optional(),
    status: z.string().optional(),
    steps: z.string().optional(),
    diff: z.string().optional(),
    risk: z.string().optional(),
    reason: z.string().optional(),
  })
  .strict()

export const singleTaskFrameSchema = z
  .object({
    event: z.string(),
    id: z.string(),
    data: eventPayloadSchema,
  })
  .strict()

export const taskEnvelopeSchema = z
  .object({
    task_id: z.string(),
    payload: eventPayloadSchema,
  })
  .strict()

export const taskEnvelopeFrameSchema = z
  .object({
    event: z.string(),
    id: z.string(),
    data: taskEnvelopeSchema,
  })
  .strict()

export const daemonEventFixtureSchema = z
  .object({
    version: z.literal(EVENT_CONTRACT_VERSION),
    single_task_stream: z.array(singleTaskFrameSchema),
    multi_task_stream: z.array(taskEnvelopeFrameSchema),
  })
  .strict()

export type EventPayload = z.infer<typeof eventPayloadSchema>
export type SingleTaskFrame = z.infer<typeof singleTaskFrameSchema>
export type TaskEnvelope = z.infer<typeof taskEnvelopeSchema>
export type TaskEnvelopeFrame = z.infer<typeof taskEnvelopeFrameSchema>
export type DaemonEventFixture = z.infer<typeof daemonEventFixtureSchema>

export type TaskEventView = {
  readonly event: string
  readonly id: string
  readonly data: EventPayload
}

export type TaskEventsView = {
  readonly task_id: string
  readonly events: readonly TaskEventView[]
}

export type DaemonEventView = {
  readonly version: typeof EVENT_CONTRACT_VERSION
  readonly tasks: readonly TaskEventsView[]
}

export function parseDaemonEventFixture(input: unknown): DaemonEventFixture {
  return daemonEventFixtureSchema.parse(input)
}

export function reduceDaemonEventFixture(
  fixture: DaemonEventFixture,
  singleTaskID: string,
): DaemonEventView {
  const eventsByTask = new Map<string, readonly TaskEventView[]>()

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

  return {
    version: fixture.version,
    tasks: Array.from(eventsByTask, ([taskID, events]) => ({
      task_id: taskID,
      events,
    })),
  }
}
