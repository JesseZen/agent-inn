import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"

async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

const activeSession = {
  session_id: "hs_1",
  session_label: "solve problem A",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "active",
} as const

const staleSession1 = {
  session_id: "hs_2",
  session_label: "stale problem A",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "stale",
} as const

const staleSession2 = {
  session_id: "hs_3",
  session_label: "stale problem B",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "stale",
} as const

async function setupHostedTerminal(initialHostedSessions = [activeSession]) {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const deleteRequests: string[] = []
  let currentHostedSessions = initialHostedSessions.map((session) => ({ ...session }))
  const events = createEventSource()
  const calls = createFetch((url, request) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [
          {
            name: "test-cli",
            port: 1234,
            role: "cli",
            upstream: { name: "test", base_url: "", has_api_key: false },
            status: "running",
            snapshot_generation: 0,
            log_level: "info",
          },
        ],
      })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET")
      return json({
        sessions: currentHostedSessions,
      })
    if (url.pathname.startsWith("/api/hosted-sessions/") && request.method === "DELETE") {
      const sessionID = url.pathname.split("/").at(-1) ?? ""
      deleteRequests.push(sessionID)
      currentHostedSessions = currentHostedSessions.filter((session) => session.session_id !== sessionID)
      return json({ session_id: sessionID })
    }
    return undefined
  })

  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })
  const { run } = await import("../src/app")
  const task = Effect.runPromise(
    run({
      url: "http://test",
      directory,
      config: createTuiResolvedConfig({ plugin_enabled: {} }),
      fetch: calls.fetch,
      events: events.source,
      args: {},
      pluginHost: {
        async start(input) {
          api = input.api
          registerProxyCommands(input.api)
          started()
        },
        async dispose() {},
      },
    }).pipe(Effect.provide(Global.defaultLayer)),
  )

  async function openHostedTerminal() {
    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("External window") && frame.includes("Hosted terminal")
    })

    api.keymap.dispatchCommand("dialog.select.next")
    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("solve problem A")
    })
  }

  async function close() {
    setup.renderer.destroy()
    await task
  }

  return { setup, api: () => api, deleteRequests, openHostedTerminal, close }
}

test("hosted terminal picker shows ctrl d delete hint", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    const frame = app.setup.captureCharFrame()
    expect(frame.includes("Hosted Terminal")).toBe(true)
    expect(frame.includes("Delete Hosted Session")).toBe(false)
    expect(frame.includes("ctrl+d")).toBe(true)
    expect(frame.includes("delete")).toBe(true)

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal picker ctrl d deletes the highlighted session", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("session.delete")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted session") && frame.includes("Delete solve problem A?")
    })
    expect(app.setup.captureCharFrame().includes("Cancel")).toBe(true)

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.deleteRequests.length === 1
    })

    expect(app.deleteRequests).toEqual(["hs_1"])

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page still deletes selected session on enter", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("solve problem A")
    })

    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted session") && frame.includes("Delete solve problem A?")
    })

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.deleteRequests.length === 1
    })

    expect(app.deleteRequests).toEqual(["hs_1"])

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page does not show ctrl d delete hint", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("solve problem A")
    })

    const frame = app.setup.captureCharFrame()
    expect(frame.includes("ctrl+d")).toBe(false)

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page shows GC stale sessions when stale sessions exist", async () => {
  const app = await setupHostedTerminal([activeSession, staleSession1, staleSession2])

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("GC stale sessions")
    })

    const frame = app.setup.captureCharFrame()
    expect(frame.includes("Delete Hosted Session")).toBe(true)
    expect(frame.includes("GC stale sessions")).toBe(true)

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page GC deletes all stale sessions after confirmation", async () => {
  const app = await setupHostedTerminal([activeSession, staleSession1, staleSession2])

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("GC stale sessions")
    })

    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted sessions") && frame.includes("Delete all stale sessions?")
    })

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.deleteRequests.length === 2
    })

    expect(app.deleteRequests).toEqual(["hs_2", "hs_3"])

    await app.openHostedTerminal()
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")

    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && !frame.includes("GC stale sessions") && !frame.includes("stale problem A")
    })

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})
