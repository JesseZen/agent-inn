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

const hostedSessions = [
  {
    session_id: "hs_1",
    session_label: "solve problem A",
    worker_name: "test-cli",
    worker_port: 1234,
    created_at: "2026-06-23T00:00:00Z",
    last_opened_at: "2026-06-23T00:00:00Z",
    status: "active",
  },
] as const

async function setupHostedTerminal() {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const deleteRequests: string[] = []
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
        sessions: hostedSessions,
      })
    if (url.pathname === "/api/hosted-sessions/hs_1" && request.method === "DELETE") {
      deleteRequests.push("hs_1")
      return json({ session_id: "hs_1" })
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
