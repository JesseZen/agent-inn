import { expect, test } from "bun:test"
import { testRender } from "@opentui/solid"
import { createComponent, onMount } from "solid-js"
import { SDKProvider, useSDK } from "../src/context/sdk"
import type { PoolProbeConfig, UpstreamPool, WorkerDetail } from "../src/proxy/backend"
import type { HostedSessionListResponse, HostedSessionSnapshot } from "../src/proxy/hosted-session-contract"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"

const pool: UpstreamPool = {
  id: "codex-ha",
  name: "codex-ha",
  mode: "active",
  probe: { stable_interval_seconds: 900, alert_interval_seconds: 60 },
  probe_state: "stable",
  next_probe_at: "2026-07-13T02:45:00Z",
  upstreams: ["openai", "anthropic"],
  circuit_breaker: { failure_threshold: 3, recovery_success_threshold: 2, recovery_wait_seconds: 60 },
  active_upstream: "openai",
  workers: ["app"],
  readiness: [],
}

test("worker detail keeps request modules and lifecycle hooks separate", () => {
  const worker: WorkerDetail = {
    id: "app",
    name: "app",
    upstream_id: "openai",
    port: 6767,
    upstream: { id: "openai", name: "openai", base_url: "https://api.openai.com/v1", has_api_key: true },
    protocol: "chat_completions",
    status: "running",
    snapshot_generation: 3,
    log_level: "simple",
    modules: {
      api_translate: { enabled: true, params: { api_format: "chat_completions" } },
    },
    hooks: {
      config_patch: { enabled: true, params: { config_path: "~/.codex/config.toml", state_dir: "~/.ainn" } },
    },
    hook_statuses: {
      config_patch: { state: "active", detail: { provider_name: "test" } },
    },
    module_support: {
      api_translate: { protocols: ["responses", "chat_completions"], capabilities: ["stream_events"] },
    },
  }

  expect(worker.modules).toEqual({
    api_translate: { enabled: true, params: { api_format: "chat_completions" } },
  })
  expect(worker.hooks).toEqual({
    config_patch: { enabled: true, params: { config_path: "~/.codex/config.toml", state_dir: "~/.ainn" } },
  })
  expect(worker.hook_statuses).toEqual({
    config_patch: { state: "active", detail: { provider_name: "test" } },
  })
  expect(worker.module_support).toEqual({
    api_translate: { protocols: ["responses", "chat_completions"], capabilities: ["stream_events"] },
  })
})

test("pool client sends exact patch, switch, and probe requests", async () => {
  const requests: Array<{
    pathname: string
    method: string
    content_type: string | null
    body?: unknown
  }> = []
  const events = createEventSource()
  const fetch = createFetch(async (url, request) => {
    if (!url.pathname.startsWith("/api/upstream-pools/")) return undefined
    requests.push({
      pathname: url.pathname,
      method: request.method,
      content_type: request.headers.get("content-type"),
      ...(request.body ? { body: await request.json() } : {}),
    })
    return json(pool)
  })
  const probe: PoolProbeConfig = { stable_interval_seconds: 600, alert_interval_seconds: 120 }
  let done: Promise<void> | undefined
  let results: UpstreamPool[] = []

  function Probe() {
    const client = useSDK().client
    onMount(() => {
      done = (async () => {
        results = [
          await client.patchUpstreamPool("codex/ha", { mode: "disabled", probe }),
          await client.switchUpstreamPool("codex/ha", { upstream: "anthropic", mode: "force" }),
          await client.probeUpstreamPool("codex/ha"),
        ]
      })()
    })
    return null
  }

  const app = await testRender(() =>
    createComponent(SDKProvider, {
      url: "http://test",
      directory,
      events: events.source,
      fetch: fetch.fetch,
      get children() {
        return createComponent(Probe, {})
      },
    }),
  )
  try {
    await done
  } finally {
    app.renderer.destroy()
  }

  expect(results).toEqual([pool, pool, pool])
  expect(requests).toEqual([
    {
      pathname: "/api/upstream-pools/codex%2Fha",
      method: "PATCH",
      content_type: "application/json",
      body: { mode: "disabled", probe },
    },
    {
      pathname: "/api/upstream-pools/codex%2Fha/switch",
      method: "POST",
      content_type: "application/json",
      body: { upstream: "anthropic", mode: "force" },
    },
    {
      pathname: "/api/upstream-pools/codex%2Fha/probe",
      method: "POST",
      content_type: null,
    },
  ])
})

test("hosted snapshot client preserves the list cursor and whole snapshot", async () => {
  const snapshot: HostedSessionSnapshot = {
    session_id: "hs_1",
    session_label: "solve problem A",
    worker: { id: "cli", name: "CLI", port: 11199, missing: false },
    workspace: "/tmp/work",
    model: "gpt-5.5",
    add_dirs: [],
    status: "active",
    user_marker: "todo",
    turn: { state: "running", reason: "", unread: false, needs_input: true },
    created_at: "2026-07-13T01:02:03Z",
    last_opened_at: "2026-07-13T01:02:03Z",
  }
  const response: HostedSessionListResponse = { sessions: [snapshot], event_cursor: "9007199254740993" }
  const events = createEventSource()
  const fetch = createFetch(async (url) => {
    if (url.pathname === "/api/hosted-sessions") return json(response)
    return undefined
  })
  let result: HostedSessionListResponse | undefined
  let done: Promise<void> | undefined

  function Probe() {
    const client = useSDK().client
    onMount(() => {
      done = client.getHostedSessionList().then((value) => {
        result = value
      })
    })
    return null
  }

  const app = await testRender(() =>
    createComponent(SDKProvider, {
      url: "http://test",
      directory,
      events: events.source,
      fetch: fetch.fetch,
      get children() {
        return createComponent(Probe, {})
      },
    }),
  )
  try {
    await done
  } finally {
    app.renderer.destroy()
  }

  expect(result).toEqual(response)
})
