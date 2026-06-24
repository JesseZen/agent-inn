/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import { expect, test } from "bun:test"
import { mkdir, readFile } from "node:fs/promises"
import path from "node:path"
import { onMount } from "solid-js"
import { KVProvider, useKV } from "../../src/context/kv"
import { tmpdir } from "../fixture/fixture"
import { TestTuiContexts } from "../fixture/tui-environment"

async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

test("KVProvider persist=false keeps test state in memory without touching kv.json", async () => {
  await using tmp = await tmpdir()
  const state = path.join(tmp.path, "state")
  await mkdir(state, { recursive: true })
  const file = path.join(state, "kv.json")
  await Bun.write(file, JSON.stringify({ theme: "codex-proxy" }))

  let ready = false
  let snapshot: Record<string, unknown> | undefined

  function Probe() {
    const kv = useKV()
    onMount(() => {
      snapshot = { theme: kv.get("theme"), sidebar: kv.get("sidebar") }
      kv.set("sidebar", "auto")
      ready = true
    })
    return <box />
  }

  const app = await testRender(() => (
    <TestTuiContexts directory={tmp.path} paths={{ home: tmp.path, state, worktree: tmp.path }}>
      <KVProvider persist={false}>
        <Probe />
      </KVProvider>
    </TestTuiContexts>
  ))

  try {
    await wait(() => ready)
    await Bun.sleep(250)
    expect(snapshot).toEqual({ theme: undefined, sidebar: undefined })
    expect(await readFile(file, "utf8")).toBe(JSON.stringify({ theme: "codex-proxy" }))
  } finally {
    app.renderer.destroy()
  }
})

test("KVProvider persists to kv.json by default", async () => {
  await using tmp = await tmpdir()
  const state = path.join(tmp.path, "state")
  await mkdir(state, { recursive: true })
  const file = path.join(state, "kv.json")
  await Bun.write(file, "{}")
  let ready = false

  function Probe() {
    const kv = useKV()
    onMount(() => {
      kv.set("theme", "codex-proxy")
      ready = true
    })
    return <box />
  }

  const app = await testRender(() => (
    <TestTuiContexts directory={tmp.path} paths={{ home: tmp.path, state, worktree: tmp.path }}>
      <KVProvider>
        <Probe />
      </KVProvider>
    </TestTuiContexts>
  ))

  try {
    await wait(() => ready)
  } finally {
    app.renderer.destroy()
  }

  await wait(async () => {
    try {
      return JSON.parse(await readFile(file, "utf8")).theme === "codex-proxy"
    } catch {
      return false
    }
  })
  expect(JSON.parse(await readFile(file, "utf8"))).toEqual({ theme: "codex-proxy" })
})
