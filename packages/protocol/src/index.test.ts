import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import {
  EVENT_CONTRACT_VERSION,
  DaemonProtocolError,
  artifactRefSchema,
  capabilitiesResponseSchema,
  contextEnvelopeSchema,
  conversationThreadSchema,
  createDaemonProtocolClient,
  crossThreadMessageSchema,
  hookSpecSchema,
  memorySchema,
  modelCapabilitySchema,
  parseDaemonEventCatalogFixture,
  parseTaskEventPayload,
  parseTaskEventStream,
  parseDaemonEventFixture,
  reduceDaemonEventFixture,
  scheduleSpecSchema,
  threadModelConfigSchema,
} from "./index"

const fixtureUrl = new URL("../../../internal/protocol/testdata/daemon-event-stream.json", import.meta.url)
const eventCatalogUrl = new URL("../../../internal/protocol/testdata/daemon-event-catalog.json", import.meta.url)

function readFixture(): unknown {
  const parsed: unknown = JSON.parse(readFileSync(fixtureUrl, "utf8"))
  return parsed
}

function readEventCatalog(): unknown {
  const parsed: unknown = JSON.parse(readFileSync(eventCatalogUrl, "utf8"))
  return parsed
}

function readFixtureObject(): Record<string, unknown> {
  const fixture = readFixture()
  if (fixture === null || typeof fixture !== "object" || Array.isArray(fixture)) {
    throw new Error("fixture must be a JSON object")
  }
  return { ...fixture }
}

function cloneFixtureObject(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(readFixtureObject())) as Record<string, unknown>
}

function fixtureStream(fixture: Record<string, unknown>, key: string): Array<Record<string, unknown>> {
  const value = fixture[key]
  if (!Array.isArray(value)) {
    throw new Error(`${key} must be an array`)
  }
  return value as Array<Record<string, unknown>>
}

function fixtureObject(value: unknown, key: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${key} must be an object`)
  }
  return value as Record<string, unknown>
}

function expectMalformedFixtureRejected(mutator: (fixture: Record<string, unknown>) => void): void {
  const fixture = cloneFixtureObject()
  mutator(fixture)
  expect(() => parseDaemonEventFixture(fixture)).toThrow()
}

function missingEventCoverage(expected: readonly string[], covered: readonly string[]): string[] {
  const coveredSet = new Set(covered)
  return expected.filter((event) => !coveredSet.has(event))
}

describe("daemon event fixture parser", () => {
  it("parses the Go daemon contract fixture", () => {
    const fixture = parseDaemonEventFixture(readFixture())
    const firstSingleFrame = fixture.single_task_stream.at(0)
    const firstMultiFrame = fixture.multi_task_stream.at(0)
    const firstThreadFrame = fixture.multi_thread_stream.at(0)

    if (firstSingleFrame === undefined || firstMultiFrame === undefined || firstThreadFrame === undefined) {
      throw new Error("fixture must include single-task, multi-task, and multi-thread frames")
    }

    expect(fixture.version).toBe(EVENT_CONTRACT_VERSION)
    expect(fixture.contract_version).toBe(EVENT_CONTRACT_VERSION)
    expect(firstSingleFrame.event).toBe("task.created")
    expect(firstSingleFrame.data.message).toBe("created")
    expect(firstSingleFrame.data.origin).toBe("background")
    expect(firstSingleFrame.data.kind).toBe("background")
    const toolCallFrame = fixture.single_task_stream.find((frame) => frame.event === "tool.call")
    const toolResultFrame = fixture.single_task_stream.find((frame) => frame.event === "tool.result")
    expect(toolCallFrame?.data.tool_call_id).toBe("fixture-call-1")
    expect(toolResultFrame?.data.tool_call_id).toBe(toolCallFrame?.data.tool_call_id)
    expect(toolResultFrame?.data.tool_result_id).toBe("fixture-call-1-result")
    expect(firstMultiFrame.data.task_id).toBe("task-002")
    expect(firstThreadFrame.data.thread_id).toBe("thread-002")
    expect(fixture.error_response.status).toBe(404)
    expect(fixture.error_response.error).toBe("task not found")
  })

  it("reduces single, multi task, and multi thread frames into indexed views", () => {
    const fixture = parseDaemonEventFixture(readFixture())

    const view = reduceDaemonEventFixture(fixture, "task-001")

    expect(view.version).toBe(EVENT_CONTRACT_VERSION)
    expect(view.contract_version).toBe(EVENT_CONTRACT_VERSION)
    expect(view.tasks).toHaveLength(3)
    expect(view.tasks.map((task) => task.task_id)).toEqual(["task-001", "task-002", "task-003"])
    expect(view.tasks[0]?.events[0]?.data.source).toBe("fixture")
    expect(view.tasks[2]?.events[0]?.data.origin).toBe("schedule")
    expect(view.tasks[2]?.events[0]?.data.trigger).toBe("0 2 * * *")
    expect(view.threads.map((thread) => thread.thread_id)).toEqual(["thread-002"])
    expect(view.threads[0]?.task_id).toBe("task-004")
    expect(view.threads[0]?.events[0]?.data.message).toBe("handoff received")
    expect(view.tasks[0]?.events.map((event) => event.event)).toEqual([
      "task.created",
      "tool.call",
      "tool.result",
      "task.completed",
    ])
  })

  it("rejects fixtures without an explicit contract version", () => {
    const withoutContractVersion = readFixtureObject()
    delete withoutContractVersion["contract_version"]

    expect(() => parseDaemonEventFixture(withoutContractVersion)).toThrow()
  })

  it("rejects fixtures with an unsupported contract version", () => {
    const unsupportedContractVersion = readFixtureObject()
    unsupportedContractVersion["contract_version"] = "2099-01-01.task-events.v9"

    expect(() => parseDaemonEventFixture(unsupportedContractVersion)).toThrow()
  })

  it("rejects malformed shared fixture frames and envelopes", () => {
    expectMalformedFixtureRejected((fixture) => {
      fixtureStream(fixture, "single_task_stream")[0]!["event"] = " "
    })
    expectMalformedFixtureRejected((fixture) => {
      fixtureStream(fixture, "single_task_stream")[0]!["id"] = ""
    })
    expectMalformedFixtureRejected((fixture) => {
      delete fixtureStream(fixture, "single_task_stream")[0]!["data"]
    })
    expectMalformedFixtureRejected((fixture) => {
      delete fixtureStream(fixture, "multi_task_stream")[0]!["data"]
    })
    expectMalformedFixtureRejected((fixture) => {
      const envelope = fixtureObject(fixtureStream(fixture, "multi_task_stream")[0]!["data"], "task envelope")
      envelope["task_id"] = " "
    })
    expectMalformedFixtureRejected((fixture) => {
      const envelope = fixtureObject(fixtureStream(fixture, "multi_task_stream")[0]!["data"], "task envelope")
      delete envelope["payload"]
    })
    expectMalformedFixtureRejected((fixture) => {
      const envelope = fixtureObject(fixtureStream(fixture, "multi_thread_stream")[0]!["data"], "thread envelope")
      envelope["thread_id"] = ""
    })
    expectMalformedFixtureRejected((fixture) => {
      const envelope = fixtureObject(fixtureStream(fixture, "multi_thread_stream")[0]!["data"], "thread envelope")
      envelope["task_id"] = " "
    })
    expectMalformedFixtureRejected((fixture) => {
      const envelope = fixtureObject(fixtureStream(fixture, "multi_thread_stream")[0]!["data"], "thread envelope")
      delete envelope["payload"]
    })
    expectMalformedFixtureRejected((fixture) => {
      delete fixture["error_response"]
    })
    expectMalformedFixtureRejected((fixture) => {
      fixtureObject(fixture["error_response"], "error response")["error"] = ""
    })
    expectMalformedFixtureRejected((fixture) => {
      fixtureObject(fixture["error_response"], "error response")["status"] = 0
    })
  })

  it("accepts first-class todo, transcript, hook, schedule, and subagent payload fields", () => {
    const fixtureObject = readFixtureObject()
    fixtureObject["single_task_stream"] = [
      {
        event: "todo.updated",
        id: "1",
        data: {
          id: "todo-001",
          action: "complete",
          target: "tests",
          message: "write tests",
          parent_task_id: "task-001",
        },
      },
      {
        event: "hook.run",
        id: "2",
        data: {
          action: "PreToolUse",
          status: "ok",
          source: "workspace",
          message: "checked command",
          timeout_seconds: 30,
        },
      },
      {
        event: "schedule.triggered",
        id: "3",
        data: {
          id: "schedule-001",
          trigger: "0 2 * * *",
          message: "nightly audit",
          missed_runs: 5,
          catch_up_policy: "run_once",
          catch_up_runs: 1,
          expires_at: "2026-07-02T00:00:00Z",
        },
      },
      {
        event: "subagent.started",
        id: "4",
        data: {
          id: "agent-001",
          parent_task_id: "task-001",
          message: "review started",
        },
      },
      {
        event: "transcript.entry",
        id: "5",
        data: {
          kind: "assistant",
          message: "summary persisted",
        },
      },
    ]

    const fixture = parseDaemonEventFixture(fixtureObject)
    const view = reduceDaemonEventFixture(fixture, "task-001")

    expect(view.tasks[0]?.events.map((event) => event.event)).toEqual([
      "todo.updated",
      "hook.run",
      "schedule.triggered",
      "subagent.started",
      "transcript.entry",
    ])
    expect(view.tasks[0]?.events[0]?.data.parent_task_id).toBe("task-001")
    expect(view.tasks[0]?.events[1]?.data.action).toBe("PreToolUse")
  })

  it("accepts context artifact and compact boundary payload fields", () => {
    const fixtureObject = readFixtureObject()
    fixtureObject["single_task_stream"] = [
      {
        event: "artifact.reference",
        id: "artifact-1",
        data: {
          tool: "shell",
          path: ".liora/tool-results/context.txt",
          message: "full output reference",
          trust: "untrusted",
          content_source: "artifact",
          token_estimate: 2048,
        },
      },
      {
        event: "compact.boundary",
        id: "compact-1",
        data: {
          message: "compacted before resume",
          token_budget: 512,
          token_estimate: 128,
        },
      },
    ]

    const fixture = parseDaemonEventFixture(fixtureObject)
    const view = reduceDaemonEventFixture(fixture, "task-001")

    expect(view.tasks[0]?.events.map((event) => event.event)).toEqual([
      "artifact.reference",
      "compact.boundary",
    ])
    expect(view.tasks[0]?.events[0]?.data.path).toBe(".liora/tool-results/context.txt")
    expect(view.tasks[0]?.events[0]?.data.trust).toBe("untrusted")
    expect(view.tasks[0]?.events[0]?.data.content_source).toBe("artifact")
    expect(view.tasks[0]?.events[1]?.data.token_budget).toBe(512)
  })

  it("accepts provider observability fields on event payloads", () => {
    const fixtureObject = readFixtureObject()
    fixtureObject["single_task_stream"] = [
      {
        event: "task.replanning",
        id: "replan-1",
        data: {
          provider: "openai-chat",
          model: "gpt-test",
          token_estimate: 42,
          latency_ms: 123,
          retry_count: 1,
          stop_reason: "rate_limited",
          replan_reason: "read missing.txt: no such file",
          message: "replanning",
        },
      },
    ]

    const fixture = parseDaemonEventFixture(fixtureObject)
    const event = reduceDaemonEventFixture(fixture, "task-001").tasks[0]?.events[0]?.data

    expect(event?.provider).toBe("openai-chat")
    expect(event?.model).toBe("gpt-test")
    expect(event?.token_estimate).toBe(42)
    expect(event?.latency_ms).toBe(123)
    expect(event?.retry_count).toBe(1)
    expect(event?.stop_reason).toBe("rate_limited")
    expect(event?.replan_reason).toContain("missing.txt")
  })

  it("covers every daemon catalog event through the TS fixture parser and reducer", () => {
    const catalog = parseDaemonEventCatalogFixture(readEventCatalog())
    const expectedEvents = catalog.events.map((event) => event.event)
    const fixture = parseDaemonEventFixture({
      version: EVENT_CONTRACT_VERSION,
      contract_version: EVENT_CONTRACT_VERSION,
      single_task_stream: catalog.events.map((event, index) => ({
        event: event.event,
        id: `catalog-${index + 1}`,
        data: event.data,
      })),
      multi_task_stream: [],
      multi_thread_stream: [],
      error_response: { status: 404, error: "task not found" },
    })

    const view = reduceDaemonEventFixture(fixture, "task-catalog")
    const coveredEvents = view.tasks[0]?.events.map((event) => event.event) ?? []

    expect(catalog.version).toBe(EVENT_CONTRACT_VERSION)
    expect(catalog.contract_version).toBe(EVENT_CONTRACT_VERSION)
    expect(missingEventCoverage(expectedEvents, coveredEvents)).toEqual([])
  })

  it("detects missing TS parser or reducer coverage for a catalog event", () => {
    const catalog = parseDaemonEventCatalogFixture(readEventCatalog())
    const expectedEvents = catalog.events.map((event) => event.event)
    const simulatedCoveredEvents = expectedEvents.slice(1)

    expect(missingEventCoverage(expectedEvents, simulatedCoveredEvents)).toEqual([expectedEvents[0]])
  })

  it("accepts 1.0 protocol shapes for thread, memory, capability, artifact, schedule, and hook surfaces", () => {
    expect(
      conversationThreadSchema.parse({
        id: "thread-001",
        title: "Investigate",
        workspace: "/repo",
        last_task_id: "task-001",
        model_config: { thread_id: "thread-001", provider: "anthropic", model: "claude-audit", base_url: "https://llm.example.test/v1", profile: "work" },
        created_at: "2026-07-01T00:00:00Z",
        updated_at: "2026-07-01T00:00:00Z",
      }).model_config?.model,
    ).toBe("claude-audit")
    expect(
      threadModelConfigSchema.parse({
        thread_id: "thread-001",
        provider: "openai-chat",
        model: "gpt-5",
        base_url: "https://llm.example.test/v1",
        inherited_from_thread_id: "thread-root",
        capability: {
          native_tool_use: true,
          streaming: true,
          vision: true,
          long_context: true,
          json_schema: true,
          max_output_tokens: 4096,
        },
      }).base_url,
    ).toBe("https://llm.example.test/v1")
    expect(
      modelCapabilitySchema.parse({
        native_tool_use: false,
        streaming: true,
        vision: true,
        long_context: true,
        json_schema: true,
        max_output_tokens: 4096,
      }).native_tool_use,
    ).toBe(false)
    expect(
      crossThreadMessageSchema.parse({
        id: "xmsg-001",
        from_thread_id: "thread-a",
        to_thread_id: "thread-b",
        from_workspace: "/repo-a",
        to_workspace: "/repo-a",
        task_id: "task-001",
        role: "handoff",
        content: "explicit note",
        summary: "handoff summary",
        explicit_content: "explicit note",
        artifact_refs: [{ path: ".liora/artifacts/handoff.txt", summary: "handoff artifact" }],
        includes_prompt: false,
        includes_secret: false,
        includes_memory: false,
        includes_approval_rule: false,
        created_at: "2026-07-01T00:00:00Z",
      }).to_thread_id,
    ).toBe("thread-b")
    expect(
      memorySchema.parse({
        id: "mem-001",
        text: "credential is stored in 1Password [REDACTED_EMAIL]",
        kind: "credential_hint",
        source: "manual",
        workspace: "/repo",
        redaction: "pii",
        importance: 4,
        enabled: true,
        created_at: "2026-07-01T00:00:00Z",
        updated_at: "2026-07-01T00:00:00Z",
        expires_at: "2026-07-02T00:00:00Z",
      }).kind,
    ).toBe("credential_hint")
    expect(
      capabilitiesResponseSchema.parse({
        tools: [{ name: "read", usage: "read <path>", kind: "builtin" }],
        mcp_tools: [{ server: "fake", name: "echo", usage: "mcp fake echo <json arguments>", kind: "external" }],
        model_capability: {
          native_tool_use: false,
          streaming: true,
          vision: true,
          long_context: true,
          json_schema: true,
          max_output_tokens: 4096,
        },
      }).model_capability?.vision,
    ).toBe(true)
    expect(
      artifactRefSchema.parse({
        task_id: "task-001",
        tool: "shell",
        path: ".liora/tool-results/out.txt",
        summary: "large output",
        created_at: "2026-07-01T00:00:00Z",
      }).path,
    ).toContain("tool-results")
    expect(scheduleSpecSchema.parse({ id: "schedule-001", trigger: "0 2 * * *", prompt: "audit", enabled: true }).enabled).toBe(
      true,
    )
    expect(hookSpecSchema.parse({ id: "hook-001", event: "PreToolUse", command: "check", enabled: false }).event).toBe(
      "PreToolUse",
    )
  })
})

describe("daemon protocol client", () => {
  it("creates tasks and reads workbench state through daemon HTTP shapes", async () => {
    const calls: Array<{ url: string; init: RequestInit | undefined }> = []
    const client = createDaemonProtocolClient({
      baseUrl: "http://daemon.local/",
      fetch: async (input, init) => {
        calls.push({ url: String(input), init })
        if (String(input) === "http://daemon.local/v1/tasks") {
          expect(init?.method).toBe("POST")
          expect(JSON.parse(String(init?.body))).toMatchObject({
            workspace: "/repo",
            prompt: "inspect",
            run_async: true,
            origin: "background",
            parent_task_id: "task-parent",
            scope: {
              paths: ["/repo/src"],
              network_hosts: ["api.internal"],
              mcp_servers: ["filesystem"],
              mcp_tools: ["filesystem.read"],
              approval_actions: ["apply_patch"],
            },
          })
          return jsonResponse(
            {
              task: daemonTask({
                id: "task-001",
                workspace: "/repo",
                origin: "background",
                automation: { kind: "background", risk: "safe" },
                parent_task_id: "task-parent",
                inherited_scope_from_parent: true,
                scope: {
                  paths: ["/repo/src"],
                  network_hosts: ["api.internal"],
                  mcp_servers: ["filesystem"],
                  mcp_tools: ["filesystem.read"],
                  approval_actions: ["apply_patch"],
                },
                approval_grants: [],
              }),
            },
            202,
          )
        }
        if (String(input) === "http://daemon.local/v1/workbench?workspace=%2Frepo&limit=5") {
          return jsonResponse({
            workspace: "/repo",
            sessions: null,
            threads: [
              {
                id: "thread-001",
                title: "Research",
                workspace: "/repo",
                last_task_id: "task-001",
                transcript_session_id: "thread-001",
                context_session_id: "thread-001",
                lifecycle: "waiting_user",
                model_config: {
                  thread_id: "thread-001",
                  provider: "openai-chat",
                  model: "gpt-5",
                  base_url: "https://llm.example.test/v1",
                  profile: "strong",
                },
                active_tasks: [daemonTask({ id: "task-001", workspace: "/repo", origin: "background" })],
                queued_tasks: null,
                recent_tasks: [daemonTask({ id: "task-001", workspace: "/repo", origin: "background" })],
                pending_approvals: [
                  {
                    task: daemonTask({ id: "task-001", workspace: "/repo", origin: "background" }),
                    request: { message: "approval needed", status: "waiting_user" },
                    item: {
                      id: "evt-approval-001",
                      task_id: "task-001",
                      tool_call_id: "toolcall-001",
                      tool_name: "run",
                      args_preview: "rm -rf build",
                      risk: "dangerous_shell",
                      command_preview: "rm -rf build",
                      diff_preview: "+change",
                      reason: "Command contains rm -rf.",
                      status: "pending",
                      created_at: "2026-07-02T00:00:00Z",
                      updated_at: "2026-07-02T00:00:00Z",
                    },
                  },
                ],
                pending_user_inputs: null,
              },
            ],
            active_tasks: [daemonTask({ id: "task-001", workspace: "/repo", origin: "background" })],
            queued_tasks: null,
            recent_tasks: [daemonTask({ id: "task-001", workspace: "/repo", origin: "background" })],
            pending_approvals: null,
            pending_user_inputs: null,
          })
        }
        throw new Error(`unexpected URL ${String(input)}`)
      },
    })

    const created = await client.createTask({
      workspace: "/repo",
      prompt: "inspect",
      run_async: true,
      origin: "background",
      automation: { kind: "background", risk: "safe" },
      parent_task_id: "task-parent",
      scope: {
        paths: ["/repo/src"],
        network_hosts: ["api.internal"],
        mcp_servers: ["filesystem"],
        mcp_tools: ["filesystem.read"],
        approval_actions: ["apply_patch"],
      },
    })
    const workbench = await client.workbench({ workspace: "/repo", limit: 5 })

    expect(created.task.id).toBe("task-001")
    expect(created.task.automation.risk).toBe("safe")
    expect(created.task.parent_task_id).toBe("task-parent")
    expect(created.task.scope?.paths).toEqual(["/repo/src"])
    expect(created.task.approval_grants).toEqual([])
    expect(workbench.sessions).toEqual([])
    expect(workbench.threads[0]?.lifecycle).toBe("waiting_user")
    expect(workbench.threads[0]?.transcript_session_id).toBe("thread-001")
    expect(workbench.threads[0]?.model_config?.base_url).toBe("https://llm.example.test/v1")
    expect(workbench.threads[0]?.pending_approvals[0]?.request.status).toBe("waiting_user")
    expect(workbench.threads[0]?.pending_approvals[0]?.item?.tool_call_id).toBe("toolcall-001")
    expect(workbench.threads[0]?.pending_approvals[0]?.item?.tool_name).toBe("run")
    expect(workbench.active_tasks[0]?.origin).toBe("background")
    expect(calls.map((call) => call.url)).toEqual([
      "http://daemon.local/v1/tasks",
      "http://daemon.local/v1/workbench?workspace=%2Frepo&limit=5",
    ])
  })

  it("covers daemon session, context, capabilities, and memory HTTP shapes", async () => {
    const calls: string[] = []
    const client = createDaemonProtocolClient({
      baseUrl: "http://daemon.local",
      fetch: async (input, init) => {
        const url = String(input)
        calls.push(`${init?.method ?? "GET"} ${url}`)
        if (url === "http://daemon.local/v1/sessions") {
          if (init?.method === "POST") {
            return jsonResponse({ session: daemonSession({ id: "session-001", title: "Research" }) }, 201)
          }
          return jsonResponse([daemonSession({ id: "session-001", title: "Research" })])
        }
        if (url === "http://daemon.local/v1/sessions/session-001") {
          return jsonResponse(daemonSession({ id: "session-001", title: "Research" }))
        }
        if (url === "http://daemon.local/v1/sessions/session-001/messages?limit=5") {
          return jsonResponse([
            {
              id: "msg-001",
              session_id: "session-001",
              role: "user",
              content: "hello",
              task_id: "task-001",
              created_at: "2026-07-01T00:00:00Z",
            },
          ])
        }
        if (url === "http://daemon.local/v1/sessions/session-001/context?limit=5&token_budget=512") {
          return jsonResponse(daemonContextEnvelope())
        }
        if (url === "http://daemon.local/v1/sessions/session-001/compact" && init?.method === "POST") {
          expect(JSON.parse(String(init.body))).toMatchObject({ mode: "auto", item_limit: 5, token_budget: 512 })
          return jsonResponse(daemonCompactResult())
        }
        if (url === "http://daemon.local/v1/threads" && init?.method === "POST") {
          return jsonResponse(
            {
              id: "thread-001",
              title: "Research",
              workspace: "/repo",
              created_at: "2026-07-01T00:00:00Z",
              updated_at: "2026-07-01T00:00:00Z",
            },
            201,
          )
        }
        if (url === "http://daemon.local/v1/threads?workspace=%2Frepo&limit=10") {
          return jsonResponse([
            {
              id: "thread-001",
              title: "Research",
              workspace: "/repo",
              created_at: "2026-07-01T00:00:00Z",
              updated_at: "2026-07-01T00:00:00Z",
            },
          ])
        }
        if (url === "http://daemon.local/v1/threads?workspace=%2Frepo&limit=10&include_archived=true") {
          return jsonResponse([
            {
              id: "thread-001",
              title: "Research",
              workspace: "/repo",
              archived_at: "2026-07-01T00:05:00Z",
              created_at: "2026-07-01T00:00:00Z",
              updated_at: "2026-07-01T00:05:00Z",
            },
          ])
        }
        if (url === "http://daemon.local/v1/threads/thread-001") {
          if (init?.method === "PATCH") {
            expect(JSON.parse(String(init.body))).toMatchObject({ title: "Renamed", archived: true })
            return jsonResponse({
              id: "thread-001",
              title: "Renamed",
              workspace: "/repo",
              archived_at: "2026-07-01T00:05:00Z",
              created_at: "2026-07-01T00:00:00Z",
              updated_at: "2026-07-01T00:05:00Z",
            })
          }
          return jsonResponse({
            id: "thread-001",
            title: "Research",
            workspace: "/repo",
            created_at: "2026-07-01T00:00:00Z",
            updated_at: "2026-07-01T00:00:00Z",
          })
        }
        if (url === "http://daemon.local/v1/threads/thread-001/model") {
          if (init?.method === "PATCH") {
            expect(JSON.parse(String(init.body))).toMatchObject({
              provider: "openai-chat",
              model: "gpt-5",
              base_url: "https://llm.example.test/v1",
              profile: "strong",
            })
            return jsonResponse({
              thread_id: "thread-001",
              provider: "openai-chat",
              model: "gpt-5",
              base_url: "https://llm.example.test/v1",
              profile: "strong",
            })
          }
          if (init?.method === "DELETE") {
            return new Response(null, { status: 204 })
          }
          return jsonResponse({
            thread_id: "thread-001",
            provider: "openai-chat",
            model: "gpt-5",
            base_url: "https://llm.example.test/v1",
            profile: "strong",
          })
        }
        if (url === "http://daemon.local/v1/threads/thread-002/messages" && init?.method === "POST") {
          expect(JSON.parse(String(init.body))).toMatchObject({
            from_thread_id: "thread-001",
            summary: "handoff summary",
            explicit_content: "explicit note",
          })
          return jsonResponse(
            {
              id: "xmsg-001",
              from_thread_id: "thread-001",
              to_thread_id: "thread-002",
              from_workspace: "/repo",
              to_workspace: "/repo",
              role: "handoff",
              content: "explicit note",
              summary: "handoff summary",
              explicit_content: "explicit note",
              artifact_refs: [{ path: ".liora/artifacts/handoff.txt", summary: "handoff artifact" }],
              includes_prompt: false,
              includes_secret: false,
              includes_memory: false,
              includes_approval_rule: false,
              created_at: "2026-07-01T00:00:00Z",
            },
            201,
          )
        }
        if (url === "http://daemon.local/v1/threads/thread-002/messages?limit=5") {
          return jsonResponse([
            {
              id: "xmsg-001",
              from_thread_id: "thread-001",
              to_thread_id: "thread-002",
              from_workspace: "/repo",
              to_workspace: "/repo",
              role: "handoff",
              content: "explicit note",
              summary: "handoff summary",
              explicit_content: "explicit note",
              artifact_refs: [{ path: ".liora/artifacts/handoff.txt", summary: "handoff artifact" }],
              includes_prompt: false,
              includes_secret: false,
              includes_memory: false,
              includes_approval_rule: false,
              created_at: "2026-07-01T00:00:00Z",
            },
          ])
        }
        if (url === "http://daemon.local/v1/capabilities") {
          return jsonResponse({
            tools: [{ name: "read", usage: "read <path>", kind: "builtin" }],
            mcp_tools: [{ server: "fake", name: "echo", usage: "mcp fake echo <json arguments>", kind: "external" }],
            model_capability: {
              native_tool_use: true,
              streaming: true,
              vision: true,
              long_context: true,
              json_schema: true,
              max_output_tokens: 4096,
            },
          })
        }
        if (url === "http://daemon.local/v1/memories?workspace=%2Frepo&limit=10&include_disabled=true&include_expired=true") {
          return jsonResponse([daemonMemory({ enabled: false, workspace: "/repo" })])
        }
        if (url === "http://daemon.local/v1/memories" && init?.method === "POST") {
          return jsonResponse(daemonMemory({ kind: "preference", text: "prefer dense output" }), 201)
        }
        if (url === "http://daemon.local/v1/memories/mem-001" && init?.method === "PATCH") {
          return jsonResponse(daemonMemory({ text: "prefer explicit output" }))
        }
        if (url === "http://daemon.local/v1/memories/mem-001/disable" && init?.method === "POST") {
          return jsonResponse(daemonMemory({ enabled: false }))
        }
        if (url === "http://daemon.local/v1/memories/mem-001?workspace=%2Frepo" && init?.method === "DELETE") {
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected URL ${url}`)
      },
    })

    const createdSession = await client.createSession({ workspace: "/repo", title: "Research" })
    const sessions = await client.listSessions()
    const session = await client.getSession("session-001")
    const messages = await client.sessionMessages("session-001", { limit: 5 })
    const context = await client.sessionContext("session-001", { item_limit: 5, token_budget: 512 })
    const compact = await client.compactSession("session-001", { mode: "auto", item_limit: 5, token_budget: 512 })
    const thread = await client.createConversationThread({ workspace: "/repo", title: "Research" })
    const threads = await client.listConversationThreads({ workspace: "/repo", limit: 10 })
    const fetchedThread = await client.getConversationThread("thread-001")
    const archivedThread = await client.updateConversationThread("thread-001", { title: "Renamed", archived: true })
    const archivedThreads = await client.listConversationThreads({ workspace: "/repo", limit: 10, include_archived: true })
    const updatedThreadModel = await client.updateThreadModelConfig("thread-001", {
      provider: "openai-chat",
      model: "gpt-5",
      base_url: "https://llm.example.test/v1",
      profile: "strong",
    })
    const fetchedThreadModel = await client.getThreadModelConfig("thread-001")
    await client.deleteThreadModelConfig("thread-001")
    const handoff = await client.createCrossThreadMessage("thread-002", {
      from_thread_id: "thread-001",
      summary: "handoff summary",
      explicit_content: "explicit note",
      artifact_refs: [{ path: ".liora/artifacts/handoff.txt", summary: "handoff artifact" }],
    })
    const handoffs = await client.listCrossThreadMessages("thread-002", { limit: 5 })
    const capabilities = await client.capabilities()
    const memories = await client.listMemories({ workspace: "/repo", limit: 10, include_disabled: true, include_expired: true })
    const memory = await client.createMemory({ text: "prefer dense output", kind: "preference", importance: 4 })
    const updated = await client.updateMemory("mem-001", { text: "prefer explicit output", enabled: true })
    const disabled = await client.setMemoryEnabled("mem-001", false)
    await client.deleteMemory("mem-001", { workspace: "/repo" })

    expect(createdSession.session.title).toBe("Research")
    expect(sessions[0]?.id).toBe("session-001")
    expect(session.workspace).toBe("/repo")
    expect(messages[0]?.task_id).toBe("task-001")
    expect(context.budget.max_tokens).toBe(512)
    expect(context.budget.buckets.map((bucket) => bucket.name)).toEqual([
      "system",
      "user",
      "transcript",
      "memory",
      "tool_result",
      "artifact_preview",
    ])
    expect(context.memories[0]?.text).toContain("compact output")
    expect(context.todos[0]?.priority).toBe("critical")
    expect(context.transcript.find((item) => item.kind === "tool_result")?.tool_call_id).toBe("call-context-001")
    expect(context.transcript.find((item) => item.kind === "tool_result")?.tool_result_id).toBe("call-context-001-result")
    expect(context.pack.sources.map((source) => source.name)).toEqual(["transcript", "todo", "memory", "artifact_preview"])
    expect(context.diagnostics.some((diagnostic) => diagnostic.source === "memory" && diagnostic.reason.includes("workspace"))).toBe(true)
    expect(context.diagnostics.some((diagnostic) => diagnostic.source === "tool_result")).toBe(true)
    expect(compact.compacted).toBe(true)
    expect(compact.boundary?.summary).toContain("Auto compact")
    expect(thread.id).toBe("thread-001")
    expect(threads[0]?.workspace).toBe("/repo")
    expect(fetchedThread.title).toBe("Research")
    expect(archivedThread.archived_at).toBe("2026-07-01T00:05:00Z")
    expect(archivedThreads[0]?.archived_at).toBe("2026-07-01T00:05:00Z")
    expect(updatedThreadModel.base_url).toBe("https://llm.example.test/v1")
    expect(fetchedThreadModel.profile).toBe("strong")
    expect(handoff.includes_secret).toBe(false)
    expect(handoffs[0]?.artifact_refs?.[0]?.path).toContain("handoff")
    expect(capabilities.mcp_tools[0]?.server).toBe("fake")
    expect(capabilities.model_capability?.native_tool_use).toBe(true)
    expect(memories[0]?.workspace).toBe("/repo")
    expect(memory.kind).toBe("preference")
    expect(updated.text).toBe("prefer explicit output")
    expect(disabled.enabled).toBe(false)
    expect(calls).toContain("GET http://daemon.local/v1/capabilities")
  })

  it("parses daemon task SSE frames and event payload JSON", () => {
    const frames = parseTaskEventStream(`event: task.created
id: 1
data: {"seq":1,"id":"evt-1","task_id":"task-001","type":"task.created","payload_json":"{\\"message\\":\\"created\\",\\"origin\\":\\"background\\"}","created_at":"2026-07-01T00:00:00Z"}

`)

    expect(frames).toHaveLength(1)
    expect(frames[0]?.event).toBe("task.created")
    expect(parseTaskEventPayload(frames[0]!.data)).toMatchObject({
      message: "created",
      origin: "background",
    })
  })

  it("rejects daemon errors and malformed SSE frames at the protocol boundary", async () => {
    const client = createDaemonProtocolClient({
      baseUrl: "http://daemon.local",
      fetch: async () => jsonResponse({ error: "bad automation" }, 400),
    })

    await expect(
      client.createTask({ workspace: "/repo", prompt: "bad", run_async: true }),
    ).rejects.toMatchObject({ name: "DaemonProtocolError", status: 400 })
    expect(() => parseTaskEventStream("event: task.created\nid: 1\n\n")).toThrow(DaemonProtocolError)
  })

  it("rejects malformed protocol shapes and invalid client requests", async () => {
    expect(() =>
      memorySchema.parse({
        id: "mem-001",
        text: "bad",
        kind: "shadow",
        importance: 4,
        enabled: true,
        created_at: "2026-07-01T00:00:00Z",
        updated_at: "2026-07-01T00:00:00Z",
      }),
    ).toThrow()
    expect(() =>
      contextEnvelopeSchema.parse({
        session: daemonSession(),
        budget: { max_tokens: 512, estimated_tokens: 128, item_limit: 5, truncated: false, buckets: [] },
        transcript: [],
        summaries: [],
        artifact_refs: [],
        compact_boundaries: [],
        generated_at: "2026-07-01T00:00:00Z",
        extra: "drift",
      }),
    ).toThrow()

    let calls = 0
    const client = createDaemonProtocolClient({
      baseUrl: "http://daemon.local",
      fetch: async () => {
        calls += 1
        return jsonResponse(daemonMemory({ kind: "preference" }))
      },
    })

    expect(() => client.getSession(" ")).toThrow(DaemonProtocolError)
    expect(() => client.getMemory("")).toThrow(DaemonProtocolError)
    expect(() => client.listSessions({ limit: -1 })).toThrow(DaemonProtocolError)
    expect(() => client.listMemories({ limit: -1 })).toThrow(DaemonProtocolError)
    expect(() => client.sessionContext(" ", { item_limit: 5 })).toThrow(DaemonProtocolError)
    expect(() => client.createMemory({ text: "   ", kind: "preference" })).toThrow()
    expect(() => client.updateMemory("mem-001", { importance: 9 })).toThrow()
    expect(calls).toBe(0)
  })
})

function daemonTask(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "task-001",
    session_id: "session-001",
    title: "inspect",
    user_input: "inspect",
    natural: true,
    status: "running",
    workspace: "/repo",
    origin: "foreground",
    automation: {},
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  }
}

function daemonSession(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "session-001",
    title: "Research",
    workspace: "/repo",
    last_task_id: "task-001",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  }
}

function daemonMemory(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "mem-001",
    text: "prefer compact output",
    kind: "note",
    source: "manual",
    workspace: "/repo",
    redaction: "",
    importance: 3,
    enabled: true,
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  }
}

function daemonContextEnvelope() {
  return {
    session: daemonSession(),
    budget: {
      max_tokens: 512,
      estimated_tokens: 128,
      item_limit: 5,
      truncated: false,
      buckets: [
        { name: "system", estimated_tokens: 0, items: 0 },
        { name: "user", estimated_tokens: 8, items: 1 },
        { name: "transcript", estimated_tokens: 32, items: 1 },
        { name: "memory", estimated_tokens: 12, items: 1 },
        { name: "tool_result", estimated_tokens: 40, items: 1 },
        { name: "artifact_preview", estimated_tokens: 48, items: 1 },
      ],
    },
    transcript: [
      {
        id: "msg-001",
        session_id: "session-001",
        task_id: "task-001",
        kind: "message",
        role: "user",
        content: "hello",
        created_at: "2026-07-01T00:00:00Z",
      },
      {
        id: "evt-tool-result",
        session_id: "session-001",
        task_id: "task-001",
        kind: "tool_result",
        tool: "shell",
        tool_call_id: "call-context-001",
        tool_result_id: "call-context-001-result",
        input: "cat notes.md",
        output: "large output",
        status: "ok",
        created_at: "2026-07-01T00:00:00Z",
      },
    ],
    todos: [
      {
        id: "todo-001",
        session_id: "session-001",
        source_task_id: "task-001",
        content: "ship context packer",
        status: "pending",
        priority: "critical",
        created_at: "2026-07-01T00:00:00Z",
        updated_at: "2026-07-01T00:00:00Z",
      },
    ],
    memories: [
      {
        id: "mem-001",
        text: "prefer compact output",
        kind: "preference",
        source: "manual",
        workspace: "/repo",
        importance: 4,
        created_at: "2026-07-01T00:00:00Z",
        updated_at: "2026-07-01T00:00:00Z",
      },
    ],
    summaries: [{ task_id: "task-001", content: "summary", created_at: "2026-07-01T00:00:00Z" }],
    artifact_refs: [
      {
        task_id: "task-001",
        tool: "shell",
        path: ".liora/tool-results/out.txt",
        summary: "large output",
        created_at: "2026-07-01T00:00:00Z",
      },
    ],
    compact_boundaries: [{ task_id: "task-001", summary: "compacted", token_budget: 512, token_estimate: 64, source_start_id: "msg-001", source_end_id: "evt-001", source_item_count: 3, created_at: "2026-07-01T00:00:00Z" }],
    pack: {
      sources: [
        { name: "transcript", selected: 1, available: 5, estimated_tokens: 40, truncated: true },
        { name: "todo", selected: 1, available: 3, estimated_tokens: 16, truncated: true },
        { name: "memory", selected: 1, available: 2, estimated_tokens: 12, truncated: true },
        { name: "artifact_preview", selected: 1, available: 1, estimated_tokens: 48, truncated: false },
      ],
    },
    diagnostics: [
      {
        source: "transcript",
        item_id: "msg-001",
        item_kind: "message",
        reason: "recent user message kept as session history",
        summary: "hello",
        estimated_tokens: 8,
        created_at: "2026-07-01T00:00:00Z",
      },
      {
        source: "tool_result",
        item_id: "evt-tool-result",
        item_kind: "tool_result",
        reason: "tool result retained from the current session transcript with bounded inline output",
        summary: "large output",
        estimated_tokens: 40,
        created_at: "2026-07-01T00:00:00Z",
      },
      {
        source: "memory",
        item_id: "mem-001",
        item_kind: "preference",
        reason: "enabled unexpired memory matched the current workspace and was selected by importance and recency",
        summary: "prefer compact output",
        estimated_tokens: 12,
        created_at: "2026-07-01T00:00:00Z",
      },
    ],
    generated_at: "2026-07-01T00:00:00Z",
  }
}

function daemonCompactResult() {
  return {
    session: daemonSession({ id: "session-001", title: "Research" }),
    mode: "auto",
    compacted: true,
    reason: "threshold",
    token_budget: 512,
    before_estimated_tokens: 2048,
    after_estimated_tokens: 128,
    transcript_items: 8,
    boundary: { task_id: "task-001", summary: "Auto compact: summarized 8 context items", token_budget: 512, token_estimate: 2048, source_start_id: "msg-001", source_end_id: "evt-001", source_item_count: 8, created_at: "2026-07-01T00:00:00Z" },
    generated_at: "2026-07-01T00:00:00Z",
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  })
}
