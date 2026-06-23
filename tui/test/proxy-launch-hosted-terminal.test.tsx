import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { homedir } from "node:os"
import path from "node:path"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import { createProxyLaunchCommand, renderProxyLaunchCommand } from "../src/proxy/launch"

async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

test("Global.Path.config defaults to ~/.codex-proxy", () => {
  expect(Global.Path.config).toBe(path.join(homedir(), ".codex-proxy"))
})

test("createProxyLaunchCommand omits --mode for external-window", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "external-window" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli"])
})

test("createProxyLaunchCommand includes --mode hosted-terminal when selected", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli", "--mode", "hosted-terminal"])
})

test("createProxyLaunchCommand includes --config-dir for hosted terminal launches", () => {
  const cmd = createProxyLaunchCommand({
    workerPort: 1234,
    profile: "cli",
    mode: "hosted-terminal",
    configDir: "/tmp/codex-config",
  })
  expect(cmd).toEqual([
    "codex-proxy",
    "launch",
    "--worker",
    "1234",
    "--profile",
    "cli",
    "--config-dir",
    "/tmp/codex-config",
    "--mode",
    "hosted-terminal",
  ])
})

test("createProxyLaunchCommand omits --mode by default", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli"])
})

test("renderProxyLaunchCommand quotes hosted-terminal mode", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  const rendered = renderProxyLaunchCommand(cmd)
  expect(rendered).toContain("'--mode' 'hosted-terminal'")
})

test("launch dialog prompts for mode before worker selection", async () => {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
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
    return undefined
  })

  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  try {
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

    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("External window") && frame.includes("Hosted terminal")
    })

    const frame = setup.captureCharFrame()
    expect(frame.includes("Launch Codex CLI")).toBe(true)
    expect(frame.includes("External window")).toBe(true)
    expect(frame.includes("Hosted terminal")).toBe(true)

    setup.renderer.destroy()
    await task
  } finally {
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})

test("launch dialog opens hosted terminal session menu", async () => {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
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
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            session_id: "hs_1",
            session_label: "solve problem A",
            worker_name: "test-cli",
            worker_port: 1234,
            status: "active",
            created_at: "2026-06-23T00:00:00Z",
            last_opened_at: "2026-06-23T00:00:00Z",
          },
        ],
      })
    return undefined
  })

  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  try {
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

    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Hosted terminal")
    })

    api.keymap.dispatchCommand("dialog.select.next")
    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("solve problem A")
    })

    setup.renderer.destroy()
    await task
  } finally {
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})

test("stale hosted session cannot be opened", async () => {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
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
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            session_id: "hs_1",
            session_label: "solve problem A",
            worker_name: "test-cli",
            worker_port: 1234,
            created_at: "2026-06-23T00:00:00Z",
            last_opened_at: "2026-06-23T00:00:00Z",
            status: "stale",
          },
        ],
      })
    return undefined
  })

  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  try {
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
      return frame.includes("Hosted Terminal") && frame.includes("solve problem A")
    })

    setup.renderer.destroy()
    await task
  } finally {
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})
