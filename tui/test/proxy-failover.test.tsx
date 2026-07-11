import { expect, test } from "bun:test"
import { TextareaRenderable } from "@opentui/core"
import { mountProxyApp, openUpstreamEditor, openUpstreamManager, openWorkerDetail, runCommand, wait } from "./proxy-commands.fixture"
import { mergePoolReadiness } from "../src/context/sync"

test("upstream manager renders protocol, reachability, unknown, and mixed pool readiness", async () => {
  const cases = [
    {
      readiness: [readiness("pool-a", "unknown", false)],
      text: "—unknown 0/1 pools",
    },
    {
      readiness: [readiness("pool-a", "ready", true, { ok: true, latency_ms: 12 })],
      text: "●12ms 1/1 pools",
    },
    {
      readiness: [
        readiness("pool-a", "not_ready", false, {
          error: "auth_error",
          status_code: 401,
        }),
      ],
      text: "✕auth_error 0/1 pools",
    },
    {
      readiness: [readiness("pool-a", "ready", true, { ok: true }), readiness("pool-b", "not_ready", false, { error: "protocol_error" })],
      text: "✕protocol_error 1/2 pools",
    },
  ]
  for (const item of cases) {
    const app = await mountProxyApp({
      upstreams: [
        {
          id: "openai",
          name: "openai",
          has_api_key: true,
          pool_readiness: item.readiness,
        },
      ],
    })
    try {
      await openUpstreamManager(app)
      expect(app.frame()).toContain(item.text)
    } finally {
      await app.cleanup()
    }
  }

  for (const item of [
    {
      probe: {
        upstream: "openai",
        mode: "reachability" as const,
        authoritative: false,
        readiness: "unknown" as const,
        ok: false,
        degraded: true,
        status_code: 404,
        latency_ms: 7,
        error: "client_error",
      },
      text: "▲reachable 7ms",
    },
    {
      probe: {
        upstream: "openai",
        mode: "reachability" as const,
        authoritative: false,
        readiness: "unknown" as const,
        ok: false,
        status_code: 0,
        latency_ms: 0,
        error: "connection_error",
      },
      text: "✕connection_error",
    },
  ]) {
    const app = await mountProxyApp({
      upstreams: [{ id: "openai", name: "openai", has_api_key: true, pool_readiness: [] }],
      probeResults: [item.probe],
    })
    try {
      await openUpstreamManager(app)
      await runCommand(app, "dialog.select.next")
      await runCommand(app, "dialog.select.submit")
      await wait(async () => {
        await app.render()
        return app.frame().includes(item.text)
      })
    } finally {
      await app.cleanup()
    }
  }
})

test("scheduled readiness updates only its pool binding", () => {
  const upstreams = [
    {
      id: "shared",
      name: "shared",
      has_api_key: true,
      pool_readiness: [readiness("pool-a", "unknown", false), readiness("pool-b", "not_ready", false)],
    },
  ]
  const result = {
    ...readiness("pool-a", "ready", true, { ok: true }),
    upstream: "shared",
  }
  expect(mergePoolReadiness(upstreams, result)).toEqual([
    {
      ...upstreams[0],
      pool_readiness: [result, readiness("pool-b", "not_ready", false)],
    },
  ])
})

test("upstream editor patches the protocol probe model", async () => {
  const app = await mountProxyApp({
    upstreams: [
      {
        id: "openai",
        name: "openai",
        has_api_key: true,
        protocol_probe: { model: "old-model" },
        pool_readiness: [],
      },
    ],
  })
  try {
    await openUpstreamEditor(app, "openai")
    for (let index = 0; index < 4; index++) await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Probe model: openai")
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused probe model prompt")
    editor.selectAll()
    await app.mockInput.typeText("probe-model")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstream.length === 1)
    expect(app.calls.patchUpstream).toEqual([{ id: "openai", body: { protocol_probe: { model: "probe-model" } } }])
  } finally {
    await app.cleanup()
  }
})

test("pooled worker picker scopes readiness and only patches eligible targets", async () => {
  const app = await mountProxyApp({
    workers: [
      {
        id: "app",
        name: "app",
        upstream_id: "primary",
        upstream_pool: "pool-a",
        port: 6767,
        upstream: { id: "primary", name: "primary", has_api_key: true },
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
    upstreams: [
      {
        id: "primary",
        name: "primary",
        has_api_key: true,
        pool_readiness: [readiness("pool-a", "not_ready", false)],
      },
      {
        id: "blocked",
        name: "blocked",
        has_api_key: true,
        pool_readiness: [readiness("pool-a", "not_ready", false), readiness("pool-b", "ready", true, { ok: true })],
      },
      {
        id: "ready",
        name: "ready",
        has_api_key: true,
        pool_readiness: [readiness("pool-a", "ready", true, { ok: true })],
      },
      {
        id: "other-pool",
        name: "other-pool",
        has_api_key: true,
        pool_readiness: [readiness("pool-b", "ready", true, { ok: true })],
      },
      {
        id: "openai",
        name: "openai",
        has_api_key: true,
        pool_readiness: [readiness("pool-b", "ready", true, { ok: true })],
      },
    ],
  })
  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    const frame = app.frame()
    expect(frame).toContain("primary")
    expect(frame).toContain("blocked")
    expect(frame).not.toContain("other-pool")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("target upstream is not eligible")
    expect(app.frame()).toContain("Switch Upstream: app")
    expect(app.calls.patchWorkerBodies).toEqual([])

    await runCommand(app, "dialog.select.next")
    expect(app.frame()).toContain("ready")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchWorkerBodies.length === 1)
    expect(app.calls.patchWorkerBodies).toEqual([{ upstream_id: "ready" }])
  } finally {
    await app.cleanup()
  }
})

function readiness(
  pool: string,
  state: "unknown" | "ready" | "not_ready",
  eligible: boolean,
  result: Partial<{
    ok: boolean
    status_code: number
    latency_ms: number
    error: string
  }> = {},
) {
  return {
    upstream: "",
    pool,
    mode: "protocol" as const,
    authoritative: true,
    readiness: state,
    eligible,
    ok: result.ok ?? false,
    status_code: result.status_code ?? 0,
    latency_ms: result.latency_ms ?? 0,
    error: result.error,
  }
}
