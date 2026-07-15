import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@agent-inn/plugin/tui"
import { Effect } from "effect"
import { Global } from "@agent-inn/core/global"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import { resolveExternalLaunchTarget } from "../src/proxy/dialog-launch"
import { createProxyLaunchCommand } from "../src/proxy/launch"
import { DialogPrompt } from "../src/ui/dialog-prompt"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { tmpdir } from "./fixture/fixture"

async function isolatedGlobalLayer(home: string) {
  const app = "ainn"
  const data = path.join(home, ".local", "share", app)
  const cache = path.join(home, ".cache", app)
  const state = path.join(home, ".local", "state", app)
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), "{}")
  return Global.layerWith({
    home,
    data,
    cache,
    config: path.join(home, ".config", app),
    state,
    tmp: path.join(home, "tmp", app),
    bin: path.join(cache, "bin"),
    log: path.join(data, "log"),
    repos: path.join(data, "repos"),
  })
}

async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

test("external launch resolves a duplicate display name by worker id", () => {
  const upstream = { id: "upstream-test", name: "Test Upstream", has_api_key: false }
  const alpha = {
    id: "alpha-id",
    name: "Shared CLI",
    upstream_id: upstream.id,
    port: 1234,
    role: "cli",
    upstream,
    status: "running",
    snapshot_generation: 0,
    log_level: "info",
  }
  const beta = {
    id: "beta-id",
    name: "Shared CLI",
    upstream_id: upstream.id,
    port: 5678,
    role: "cli",
    upstream,
    status: "running",
    snapshot_generation: 0,
    log_level: "info",
  }

  const target = resolveExternalLaunchTarget([alpha, beta], "beta-id")

  expect({
    target,
    command: createProxyLaunchCommand({ workerPort: target!.workerPort, profile: target!.profile }),
  }).toEqual({
    target: { worker: beta, workerPort: 5678, profile: "beta-id" },
    command: ["ainn", "launch", "--worker", "5678", "--profile", "beta-id"],
  })
})

test("launch dialog enables directory completion with current project directory", async () => {
  await using tmp = await tmpdir()
  const globalLayer = await isolatedGlobalLayer(tmp.path)
  const promptCalls: any[] = []
  const originalShow = DialogPrompt.show
  DialogPrompt.show = async (_dialog: unknown, _title: string, options: any) => {
    promptCalls.push(options)
    return null
  }

  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
    if (url.pathname === "/api/settings")
      return json({
        settings: {
          state_dir: "~/.ainn",
          log_dir: "~/.ainn/logs",
          launch: { default_mode: "external-window" },
          terminal: {
            host: "tmux",
            opener: "default",
            tmux: {
              socket_name: "ainn",
              host_session: "ainn-host",
              host_start_mode: "new-window",
              status_bar_height: 2,
              turn_status_hooks: false,
            },
          },
        },
        status: { generation: 0, dirty: false, last_save_error: "" },
      })
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
      }).pipe(Effect.provide(globalLayer)),
    )

    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      return setup.captureCharFrame().includes("test-cli")
    })

    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => promptCalls.length === 1)

    setup.renderer.destroy()
    await task

    expect(promptCalls).toEqual([
      expect.objectContaining({
        directoryCompletion: {
          basePath: directory,
        },
      }),
    ])
  } finally {
    DialogPrompt.show = originalShow
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})

test("launch directory prompt ESC returns to worker picker", async () => {
  await using tmp = await tmpdir()
  const globalLayer = await isolatedGlobalLayer(tmp.path)
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false, kittyKeyboard: true })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
    if (url.pathname === "/api/settings")
      return json({
        settings: {
          state_dir: "~/.ainn",
          log_dir: "~/.ainn/logs",
          launch: { default_mode: "external-window" },
          terminal: {
            host: "tmux",
            opener: "default",
            tmux: {
              socket_name: "ainn",
              host_session: "ainn-host",
              host_start_mode: "new-window",
              status_bar_height: 2,
              turn_status_hooks: false,
            },
          },
        },
        status: { generation: 0, dirty: false, last_save_error: "" },
      })
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
      }).pipe(Effect.provide(globalLayer)),
    )

    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      return setup.captureCharFrame().includes("test-cli")
    })

    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Launch Worker") && frame.includes(directory)
    })

    setup.mockInput.pressEscape()
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Launch Worker") && frame.includes("test-cli")
    })

    setup.renderer.destroy()
    await task
  } finally {
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})
