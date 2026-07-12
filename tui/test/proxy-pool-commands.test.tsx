import { expect, test } from "bun:test"
import { InputRenderable, TextareaRenderable } from "@opentui/core"
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

const attachedPool: UpstreamPool = {
  ...pool,
  workers: ["app"],
  readiness: [
    {
      upstream: "openai",
      pool: "codex-ha",
      mode: "protocol",
      authoritative: true,
      readiness: "ready",
      eligible: true,
      ok: true,
      status_code: 200,
      latency_ms: 12,
    },
    {
      upstream: "anthropic",
      pool: "codex-ha",
      mode: "protocol",
      authoritative: true,
      readiness: "ready",
      eligible: true,
      ok: true,
      status_code: 200,
      latency_ms: 18,
    },
  ],
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

test("pool editor disables and re-enables automatic failover", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool] })
  try {
    await openPoolEditor(app)
    await selectPoolEditorOption(app, "Automatic Failover")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Disabled")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 1)
    expect(app.calls.patchUpstreamPool).toEqual([{ id: "codex-ha", body: { mode: "disabled" } }])

    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: codex-ha")
    })
    await selectPoolEditorOption(app, "Automatic Failover")
    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 2)
    expect(app.calls.patchUpstreamPool).toEqual([
      { id: "codex-ha", body: { mode: "disabled" } },
      { id: "codex-ha", body: { mode: "active" } },
    ])
  } finally {
    await app.cleanup()
  }
})

test("pool editor keeps mode picker open when saving fails", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool], patchUpstreamPoolError: "save failed" })
  try {
    await openPoolEditor(app)
    await selectPoolEditorOption(app, "Automatic Failover")
    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 1)
    await wait(async () => {
      await app.render()
      return app.lines().join(" ").includes("save failed")
    })
    expect({
      calls: app.calls.patchUpstreamPool,
      picker: app.frame().includes("Automatic Failover: codex-ha"),
    }).toEqual({
      calls: [{ id: "codex-ha", body: { mode: "disabled" } }],
      picker: true,
    })
  } finally {
    await app.cleanup()
  }
})

test("pool editor renders paused and none without deadlines", async () => {
  const cases: Array<{ pool: UpstreamPool; nextProbe: string }> = [
    {
      pool: { ...pool, mode: "disabled", probe_state: "paused", next_probe_at: undefined },
      nextProbe: "Next Probe paused",
    },
    {
      pool: { ...pool, mode: "active", probe_state: "idle", next_probe_at: undefined },
      nextProbe: "Next Probe none",
    },
  ]
  for (const item of cases) {
    const app = await mountProxyApp({ upstreamPools: [item.pool], height: 80 })
    try {
      await openPoolEditor(app)
      expect(app.frame()).toContain(item.nextProbe)
    } finally {
      await app.cleanup()
    }
  }
})

test("pool editor refreshes authoritative readiness", async () => {
  const app = await mountProxyApp({ upstreamPools: [attachedPool] })
  try {
    await openPoolEditor(app)
    await selectPoolEditorOption(app, "Refresh Readiness")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.probeUpstreamPool.length === 1)
    expect({
      probeUpstreamPool: app.calls.probeUpstreamPool,
      testUpstream: app.calls.testUpstream,
    }).toEqual({
      probeUpstreamPool: ["codex-ha"],
      testUpstream: [],
    })
  } finally {
    await app.cleanup()
  }
})

test("pool editor switches to an eligible member normally", async () => {
  const app = await mountProxyApp({ upstreamPools: [attachedPool] })
  try {
    await openPoolEditor(app)
    await selectPoolEditorOption(app, "Switch Active Upstream")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("anthropic")
    expect(app.frame()).toContain("ready")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.switchUpstreamPool.length === 1)
    expect(app.calls.switchUpstreamPool).toEqual([
      { id: "codex-ha", body: { upstream: "anthropic", mode: "normal" } },
    ])
  } finally {
    await app.cleanup()
  }
})

test("pool editor confirms force for an ineligible member", async () => {
  const blockedPool: UpstreamPool = {
    ...attachedPool,
    readiness: attachedPool.readiness.map((item) => item.upstream === "anthropic"
      ? { ...item, readiness: "not_ready", eligible: false, ok: false, status_code: 503, error: "server_error" }
      : item),
  }
  const app = await mountProxyApp({ upstreamPools: [blockedPool] })
  try {
    await openPoolEditor(app)
    await selectPoolEditorOption(app, "Switch Active Upstream")
    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Force switch")
    })
    expect(app.calls.switchUpstreamPool).toEqual([])
    app.mockInput.pressArrow("left")
    app.mockInput.pressEnter()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Switch Active Upstream: codex-ha") && !app.frame().includes("Force switch")
    })
    expect(app.calls.switchUpstreamPool).toEqual([])
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Force switch")
    })
    expect(app.calls.switchUpstreamPool).toEqual([])
    app.mockInput.pressEnter()
    await wait(() => app.calls.switchUpstreamPool.length === 1)
    expect(app.calls.switchUpstreamPool).toEqual([
      { id: "codex-ha", body: { upstream: "anthropic", mode: "force" } },
    ])
  } finally {
    await app.cleanup()
  }
})

test("pool editor hides switch action without attached workers", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool], height: 80 })
  try {
    await openPoolEditor(app)
    const frame = app.frame()
    expect({
      refresh: frame.includes("Refresh Readiness"),
      switch: frame.includes("Switch Active Upstream"),
    }).toEqual({
      refresh: true,
      switch: false,
    })
  } finally {
    await app.cleanup()
  }
})

test("pool editor fits status and actions at narrow width", async () => {
  const app = await mountProxyApp({ upstreamPools: [attachedPool], width: 44, height: 80 })
  try {
    await openPoolEditor(app)
    const lines = app.frame().split("\n")
    const status = lines.filter((line) => ["Mode active", "Probe State", "Next Probe"].some((value) => line.includes(value)))
    const mode = lines.filter((line) => line.includes("Automatic Failover"))
    const actions = lines.filter((line) => ["Switch Active Upstream", "Refresh Readiness", "Delete Pool"].some((value) => line.includes(value)))
    expect({
      status: status.map((line) => line.trim()),
      mode: mode.map((line) => line.trim()),
      actions: actions.map((line) => line.trim()),
      withinWidth: [...status, ...mode, ...actions].every((line) => Bun.stringWidth(line) <= 44),
    }).toEqual({
      status: [
        expect.stringContaining("Mode active"),
        expect.stringContaining("Probe State stable"),
        expect.stringContaining("Next Probe 2026-07-13T02:45:00Z"),
      ],
      mode: [expect.stringContaining("Automatic Failover active")],
      actions: [
        expect.stringContaining("Switch Active Upstream"),
        expect.stringContaining("Refresh Readiness"),
        expect.stringContaining("Delete Pool"),
      ],
      withinWidth: true,
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy pools reorder members and edit circuit breaker", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool] })
  try {
    await openPoolEditor(app)
    await selectPoolEditorOption(app, "1. openai")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Move Down")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 1)
    expect(app.calls.patchUpstreamPool[0]).toEqual({ id: "codex-ha", body: { upstreams: ["anthropic", "openai"] } })

    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: codex-ha") && !app.frame().includes("Pool Member:")
    })
    app.mockInput.pressEscape()
    await app.render()
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: codex-ha")
    })
    await selectPoolEditorOption(app, "Failure Threshold")
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
    await selectPoolEditorOption(app, "Add Upstream")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Add Member: codex-ha")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 1)
    expect(app.calls.patchUpstreamPool[0]).toEqual({ id: "codex-ha", body: { upstreams: ["openai", "anthropic", "relay"] } })
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Pool: codex-ha")
    })
    await selectPoolEditorOption(app, "3. relay")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Pool Member: relay")
    await runCommand(app, "dialog.select.next")
    expect(app.frame()).toContain("Remove")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchUpstreamPool.length === 2)
    expect(app.calls.patchUpstreamPool[1]).toEqual({ id: "codex-ha", body: { upstreams: ["openai", "anthropic"] } })
  } finally {
    await app.cleanup()
  }
})

test("proxy pools refresh readiness and delete pool", async () => {
  const app = await mountProxyApp({ upstreamPools: [pool] })
  try {
    await openPoolEditor(app)
    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.probeUpstreamPool.length === 1)
    expect(app.calls.probeUpstreamPool).toEqual(["codex-ha"])

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

async function selectPoolEditorOption(app: Awaited<ReturnType<typeof mountProxyApp>>, title: string) {
  await wait(() => app.setup.renderer.currentFocusedRenderable instanceof InputRenderable)
  const filter = app.setup.renderer.currentFocusedRenderable
  if (!(filter instanceof InputRenderable)) throw new Error("expected pool editor filter")
  filter.selectAll()
  await app.mockInput.typeText(title)
  await app.render()
  expect(app.frame()).toContain(title)
}
