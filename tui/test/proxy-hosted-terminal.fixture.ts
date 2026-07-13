import { mock } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@agent-inn/plugin/tui"
import { Effect } from "effect"
import { Global } from "@agent-inn/core/global"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json, type FetchHandler } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { tmpdir } from "./fixture/fixture"

export async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

export const defaultWorker = {
  id: "test-cli",
  name: "test-cli",
  upstream_id: "test",
  port: 1234,
  role: "cli",
  upstream: { id: "test", name: "test", base_url: "", has_api_key: false },
  status: "running",
  snapshot_generation: 0,
  log_level: "info",
} as const

export const activeHostedSession = {
  session_id: "hs_1",
  session_label: "solve problem A",
  worker_id: "test-cli",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "active",
} as const

export const staleHostedSessionA = {
  session_id: "hs_2",
  session_label: "stale problem A",
  worker_id: "test-cli",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "stale",
} as const

export const staleHostedSessionB = {
  session_id: "hs_3",
  session_label: "stale problem B",
  worker_id: "test-cli",
  worker_name: "test-cli",
  worker_port: 1234,
  created_at: "2026-06-23T00:00:00Z",
  last_opened_at: "2026-06-23T00:00:00Z",
  status: "stale",
} as const

export async function mountHostedTerminalApp(override?: FetchHandler) {
  return mountHostedTerminalAppWithArgs({}, override)
}

export async function mountHostedTerminalPopupApp(override?: FetchHandler) {
  return mountHostedTerminalAppWithArgs({ hostedTerminalPopup: true }, override)
}

async function mountHostedTerminalAppWithArgs(args: { hostedTerminalPopup?: boolean }, override?: FetchHandler) {
  const tmp = await tmpdir()
  const home = tmp.path
  const app = "ainn"
  const data = path.join(home, ".local", "share", app)
  const cache = path.join(home, ".cache", app)
  const state = path.join(home, ".local", "state", app)
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), "{}")
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch(override)
  let api!: TuiPluginApi
  let pluginStarts = 0
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
      args,
      pluginHost: {
        async start(input) {
          pluginStarts += 1
          api = input.api
          registerProxyCommands(input.api)
          started()
        },
        async dispose() {},
      },
    }).pipe(
      Effect.provide(
        Global.layerWith({
          home,
          data,
          cache,
          config: path.join(home, ".config", app),
          state,
          tmp: path.join(home, "tmp", app),
          bin: path.join(cache, "bin"),
          log: path.join(data, "log"),
          repos: path.join(data, "repos"),
        }),
      ),
    ),
  )

  async function renderReady() {
    if (!args.hostedTerminalPopup) await ready
    await setup.renderOnce()
    await setup.renderOnce()
  }

  async function openLaunchDialog() {
    await renderReady()
    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session")
    })
  }

  async function openHostedTerminalPicker() {
    await openLaunchDialog()
  }

  async function cleanup() {
    setup.renderer.destroy()
    await task
    mock.restore()
    await tmp[Symbol.asyncDispose]()
  }

  function currentApi() {
    if (!api) throw new Error("plugin API is not available")
    return api
  }

  return { setup, api: currentApi, pluginStarts: () => pluginStarts, calls, openLaunchDialog, openHostedTerminalPicker, cleanup }
}

export { directory, json }
