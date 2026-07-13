import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import { Effect } from "effect"
import { Global } from "@agent-inn/core/global"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory } from "./fixture/tui-sdk"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { tmpdir } from "./fixture/fixture"

test("proxy tui home screen renders visible content after startup", async () => {
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
  const calls = createFetch()
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
          async start() {
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

    await ready
    let frame = ""
    const deadline = Date.now() + 5000
    while (Date.now() < deadline) {
      await setup.renderOnce()
      frame = setup.captureCharFrame()
      if (frame.includes("Ask anything")) break
    }
    setup.renderer.destroy()
    await task

    expect(frame.includes("Ask anything")).toBe(true)
  } finally {
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
    await tmp[Symbol.asyncDispose]()
  }
})
