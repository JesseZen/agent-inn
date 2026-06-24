import { mock } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json, type FetchHandler } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"

export async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

export const defaultWorker = {
  name: "test-cli",
  port: 1234,
  role: "cli",
  upstream: { name: "test", base_url: "", has_api_key: false },
  status: "running",
  snapshot_generation: 0,
  log_level: "info",
} as const

export const activeHostedSession = {
  session_id: "hs_1",
  session_label: "solve problem A",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "active",
} as const

export const staleHostedSessionA = {
  session_id: "hs_2",
  session_label: "stale problem A",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "stale",
} as const

export const staleHostedSessionB = {
  session_id: "hs_3",
  session_label: "stale problem B",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "stale",
} as const

export async function mountHostedTerminalApp(override?: FetchHandler) {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch(override)
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

  async function renderReady() {
    await ready
    await setup.renderOnce()
    await setup.renderOnce()
  }

  async function openLaunchDialog() {
    await renderReady()
    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("External window") && frame.includes("Hosted terminal")
    })
  }

  async function openHostedTerminalPicker() {
    await openLaunchDialog()
    api.keymap.dispatchCommand("dialog.select.next")
    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session")
    })
  }

  async function cleanup() {
    setup.renderer.destroy()
    await task
    mock.restore()
  }

  return { setup, api: () => api, calls, openLaunchDialog, openHostedTerminalPicker, cleanup }
}

export { directory, json }
