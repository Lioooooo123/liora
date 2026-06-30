import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import {
  EVENT_CONTRACT_VERSION,
  parseDaemonEventFixture,
  reduceDaemonEventFixture,
} from "./index"

const fixtureUrl = new URL("../../../internal/protocol/testdata/daemon-event-stream.json", import.meta.url)

function readFixture(): unknown {
  const parsed: unknown = JSON.parse(readFileSync(fixtureUrl, "utf8"))
  return parsed
}

describe("daemon event fixture parser", () => {
  it("parses the Go daemon contract fixture", () => {
    const fixture = parseDaemonEventFixture(readFixture())
    const firstSingleFrame = fixture.single_task_stream.at(0)
    const firstMultiFrame = fixture.multi_task_stream.at(0)

    if (firstSingleFrame === undefined || firstMultiFrame === undefined) {
      throw new Error("fixture must include single and multi task frames")
    }

    expect(fixture.version).toBe(EVENT_CONTRACT_VERSION)
    expect(firstSingleFrame.event).toBe("task.created")
    expect(firstSingleFrame.data.message).toBe("created")
    expect(firstMultiFrame.data.task_id).toBe("task-002")
  })

  it("reduces single and multi task frames into a task-indexed view", () => {
    const fixture = parseDaemonEventFixture(readFixture())

    const view = reduceDaemonEventFixture(fixture, "task-001")

    expect(view.version).toBe(EVENT_CONTRACT_VERSION)
    expect(view.tasks).toHaveLength(3)
    expect(view.tasks.map((task) => task.task_id)).toEqual(["task-001", "task-002", "task-003"])
    expect(view.tasks[0]?.events.map((event) => event.event)).toEqual([
      "task.created",
      "tool.call",
      "tool.result",
      "task.completed",
    ])
  })
})
