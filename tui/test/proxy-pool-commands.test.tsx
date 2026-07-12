import { expect, test } from "bun:test"
import { TextareaRenderable } from "@opentui/core"
import { resolveSlashCommand } from "../src/keymap"
import type { UpstreamPool } from "../src/proxy/backend"
import { mountProxyApp, openUpstreamManager, openWorkerDetail, runCommand, wait } from "./proxy-commands.fixture"

const pool: UpstreamPool = {
  id: "codex-ha",
  name: "codex-ha",
  mode: "active",
  probe: { stable_interval_seconds: 900, alert_interval_seconds: 60 },
  probe_state: "stable",
  next_probe_at: "2026-07-13T02:45:00Z",
  upstreams: ["openai", "anthropic"],
  circuit_breaker: {
    failure_threshold: 3,
    recovery_success_threshold: 2,
    recovery_wait_seconds: 60,
  },
  active_upstream: "openai",
  workers: [],
  readiness: [],
}

test("proxy pools register a command and upstream cross-link", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool] })
  try {
    await runCommand(app, "proxy.pools")
    expect(app.frame()).toContain("Manage Pools")
    expect(app.frame()).toContain("codex-ha")
    expect(app.frame()).toContain("openai -> anthropic")
    expect(resolveSlashCommand(app.api.keymap, "/pools")).toBe("proxy.pools")

    await openUpstreamManager(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Manage Pools")
  } finally {
    await app.cleanup()
  }
})

test("proxy pools create an ordered pool and open its editor", async () => {
  const app = await mountProxyApp()
  try {
    await runCommand(app, "proxy.pools")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable)
    await app.mockInput.typeText("new-ha")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("First Pool Member")
    })
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: new-ha")
    })
    expect(app.calls.createUpstreamPool).toEqual([{ name: "new-ha", upstreams: ["openai"] }])
  } finally {
    await app.cleanup()
  }
})

test("proxy pools reorder members and edit circuit breaker", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool] })
  try {
    await openPoolEditor(app)
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Move Down")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 1)
    expect(app.calls.patchUpstreamPool[0]).toEqual({ id: "codex-ha", body: { upstreams: ["anthropic", "openai"] } })

    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: codex-ha") && !app.frame().includes("Pool Member:")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable)
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected circuit breaker prompt")
    editor.selectAll()
    await app.mockInput.typeText("5")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 2)
    expect(app.calls.patchUpstreamPool[1]).toEqual({
      id: "codex-ha",
      body: {
        circuit_breaker: {
          failure_threshold: 5,
          recovery_success_threshold: 2,
          recovery_wait_seconds: 60,
        },
      },
    })
  } finally {
    await app.cleanup()
  }
})

test("worker fallback pool binds active upstream in one patch", async () => {
  const activePool = { ...pool, active_upstream: "anthropic", workers: ["cli-openrouter"] }
  const app = await mountProxyApp({ upstreamPools: [activePool] })
  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Fallback Pool: app")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchWorkerBodies.length === 1)
    expect(app.calls.patchWorkerBodies).toEqual([{ upstream_pool: "codex-ha", upstream_id: "anthropic" }])
    await wait(async () => {
      await app.render()
      return app.frame().includes("Fallback Pool") && app.frame().includes("codex-ha")
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy pools add and remove members", async () => {
  const app = await mountProxyApp({
    upstreamPools: [pool],
    upstreams: [
      { id: "openai", name: "openai", has_api_key: true },
      { id: "anthropic", name: "anthropic", has_api_key: true },
      { id: "relay", name: "relay", has_api_key: true },
    ],
  })
  try {
    await openPoolEditor(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Add Member: codex-ha")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 1)
    expect(app.calls.patchUpstreamPool[0]).toEqual({ id: "codex-ha", body: { upstreams: ["openai", "anthropic", "relay"] } })
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: codex-ha") && app.frame().includes("3. relay")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 2)
    expect(app.calls.patchUpstreamPool[1]).toEqual({ id: "codex-ha", body: { upstreams: ["openai", "anthropic"] } })
  } finally {
    await app.cleanup()
  }
})

test("proxy pools test members and delete pool", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool] })
  try {
    await openPoolEditor(app)
    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.testUpstream.length === 2)
    expect(app.calls.testUpstream).toEqual(["openai", "anthropic"])

    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Delete pool")
    app.mockInput.pressEnter()
    await wait(() => app.calls.deleteUpstreamPool.length === 1)
    expect(app.calls.deleteUpstreamPool).toEqual(["codex-ha"])
  } finally {
    await app.cleanup()
  }
})

test("worker fallback pool can be removed", async () => {
  const boundPool = { ...pool, workers: ["app"] }
  const app = await mountProxyApp({
    upstreamPools: [boundPool],
    workers: [{
      id: "app",
      name: "app",
      upstream_id: "openai",
      upstream_pool: "codex-ha",
      port: 6767,
      upstream: { id: "openai", name: "openai", has_api_key: true },
      status: "running",
      snapshot_generation: 1,
      log_level: "simple",
    }],
  })
  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchWorkerBodies.length === 1)
    expect(app.calls.patchWorkerBodies).toEqual([{ upstream_pool: "" }])
  } finally {
    await app.cleanup()
  }
})

async function openPoolEditor(app: Awaited<ReturnType<typeof mountProxyApp>>) {
  await runCommand(app, "proxy.pools")
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.submit")
  await wait(async () => {
    await app.render()
    return app.frame().includes("Edit Pool: codex-ha")
  })
}
