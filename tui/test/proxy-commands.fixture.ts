import { mock } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@agent-inn/plugin/tui"
import { Effect } from "effect"
import { Global } from "@agent-inn/core/global"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { tmpdir } from "./fixture/fixture"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import {
  toAinnUpstreams,
  type BatchRun,
  type CreateBatchRequest,
  type HostedSessionSummary,
  type MetricsRangeName,
  type MetricsResponse,
  type ProxyConfigStatus,
  type ProxySettings,
  type PluginDefinition,
  type PoolProbeConfig,
  type PoolSwitchMode,
  type UpstreamPool,
  type CircuitBreaker,
  type RedactedUpstream,
  type UpstreamProbeResult,
  type WorkerSummary,
} from "../src/proxy/backend"
import type { ProxyLaunchOptions } from "../src/proxy/launch"

type ProxySettingsPatch = Omit<Partial<ProxySettings>, "launch" | "terminal" | "metrics"> & {
  launch?: Partial<ProxySettings["launch"]>
  terminal?: Partial<Omit<ProxySettings["terminal"], "tmux">> & {
    tmux?: Partial<ProxySettings["terminal"]["tmux"]>
  }
  metrics?: Partial<ProxySettings["metrics"]>
}

type HarnessUpstream = Omit<RedactedUpstream, "id"> & { id?: string }
type HarnessWorker = Omit<WorkerSummary, "id" | "upstream_id" | "upstream"> & {
  id?: string
  upstream_id?: string
  upstream: HarnessUpstream
}

export async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

function frameLines(frame: string) {
  return frame
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

const defaultBatchVariantCount = 3
const defaultCircuitBreaker: CircuitBreaker = {
  failure_threshold: 3,
  recovery_success_threshold: 2,
  recovery_wait_seconds: 60,
}
const defaultPoolProbe: PoolProbeConfig = {
  stable_interval_seconds: 900,
  alert_interval_seconds: 60,
}

type ProxyHarnessInput = {
  workers?: HarnessWorker[]
  upstreams?: HarnessUpstream[]
  upstreamPools?: UpstreamPool[]
  batches?: BatchRun[]
  batchHostedSessionWindowMode?: "present" | "missing"
  batchSessionLauncher?: (opts: ProxyLaunchOptions) => Promise<boolean>
  hostedSessions?: HostedSessionSummary[]
  hostedSessionsError?: string
  patchWorkerDelayMs?: number
  patchUpstreamError?: string
  strictModuleWorkerIDs?: boolean
  metricsResponder?: (range: MetricsRangeName) => MetricsResponse | Promise<MetricsResponse>
  settings?: ProxySettingsPatch
  probeResults?: UpstreamProbeResult[]
  width?: number
  height?: number
}

function createProxyHarness(input: ProxyHarnessInput = {}) {
  const batchHostedSessionWindowMode = input.batchHostedSessionWindowMode ?? "present"
  const inputUpstreams = input.upstreams ?? [
      {
        id: "openai",
        name: "openai",
        base_url: "https://api.openai.com/v1",
        has_api_key: true,
      },
      {
        id: "anthropic",
        name: "anthropic",
        base_url: "https://api.anthropic.com/v1",
        has_api_key: true,
        api_format: "anthropic",
      },
    ]
  const providers = new Map<string, RedactedUpstream>(
    inputUpstreams.map((upstream) => {
      const id = upstream.id ?? upstream.name
      return [id, { ...upstream, id }] as const
    }),
  )
  const upstreamPools = new Map<string, UpstreamPool>(
    (input.upstreamPools ?? []).map((pool) => [pool.id, { ...pool, upstreams: [...pool.upstreams], workers: [...pool.workers], readiness: [...pool.readiness] }]),
  )

  const workers = new Map<string, WorkerSummary>([
    [
      "app",
      {
        id: "app",
        name: "app",
        upstream_id: "openai",
        port: 6767,
        role: "app",
        protocol: "chat_completions",
        upstream: providers.get("openai")!,
        status: "running",
        snapshot_generation: 3,
        log_level: "simple",
        modules: {
          model_override: { enabled: false, params: { model: "gpt-old" } },
          api_translate: { enabled: true, params: { api_format: "chat_completions" } },
          tool_filter: { enabled: true },
        },
        hooks: {
          config_patch: { enabled: true, params: { config_path: "~/.codex/config.toml", state_dir: "~/.ainn" } },
        },
        hook_statuses: {
          config_patch: { state: "active", detail: { provider_name: "test" } },
        },
        module_support: {
          model_override: { protocols: ["responses", "chat_completions"], capabilities: ["input_text"] },
          api_translate: { protocols: ["responses", "chat_completions"], capabilities: ["input_text", "tool_calls", "stream_events"] },
          tool_filter: { protocols: ["responses"], capabilities: ["tool_calls"] },
          request_log: { protocols: ["responses", "chat_completions", "anthropic"] },
          config_patch: { protocols: ["responses", "chat_completions"] },
        },
      },
    ],
    [
      "cli-openrouter",
      {
        id: "cli-openrouter",
        name: "cli-openrouter",
        upstream_id: "openai",
        port: 11199,
        role: "cli",
        upstream: providers.get("openai")!,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
  ])
  if (input.workers) workers.clear()
  const logs = new Map<number, string[]>([[6767, ["booted", "serving :6767"]]])
  const hostedSessions = [...(input.hostedSessions ?? [])]
  const batches = new Map<string, BatchRun>((input.batches ?? []).map((batchRun) => [batchRun.id, batchRun]))
  const findWorker = (key: string) => workers.get(key) ?? [...workers.values()].find((worker) => String(worker.port) === key)
  const setWorker = (worker: WorkerSummary) => workers.set(worker.id, worker)
  for (const worker of input.workers ?? []) {
    const upstreamID = worker.upstream_id ?? worker.upstream.id ?? worker.upstream.name
    setWorker({
      ...worker,
      id: worker.id ?? worker.name,
      upstream_id: upstreamID,
      upstream: { ...worker.upstream, id: worker.upstream.id ?? upstreamID },
    })
  }
  const metrics = { tpm: 20, total_tokens: 20 }
  const config: {
    status: ProxyConfigStatus
    settings: ProxySettings
    plugins: Record<string, PluginDefinition>
  } = {
    status: {
      generation: 4,
      dirty: true,
      last_save_error: "",
    },
    settings: {
      state_dir: "~/.ainn",
      log_dir: "~/.ainn/logs",
      launch: { default_mode: "hosted-terminal" },
      terminal: {
        host: "tmux",
        opener: "default",
        tmux: {
          socket_name: "ainn",
          host_session: "ainn-host",
          host_start_mode: "new-window",
          turn_status_hooks: false,
        },
      },
      metrics: {
        retention_days: 30,
      },
    },
    plugins: {
      api_translate: { kind: "request_middleware", source: "builtin" },
      tool_filter: { kind: "request_middleware", source: "builtin" },
      model_override: { kind: "request_middleware", source: "external", path: "plugins/request/model_override/plugin.yaml" },
      request_log: { kind: "request_middleware", source: "external", path: "plugins/request/request_log/plugin.yaml" },
      config_patch: { kind: "lifecycle_hook", source: "builtin" },
    },
  }
  if (input.settings) {
    config.settings = {
      ...config.settings,
      ...input.settings,
      launch: { ...config.settings.launch, ...input.settings.launch },
      terminal: {
        ...config.settings.terminal,
        ...input.settings.terminal,
        tmux: { ...config.settings.terminal.tmux, ...input.settings.terminal?.tmux },
      },
      metrics: { ...config.settings.metrics, ...input.settings.metrics },
    }
  }
  const calls = {
    createWorker: [] as Array<{ name: string; port?: number; upstream: string; launcher?: string }>,
    patchWorker: [] as Array<
      | { id: string; name: string }
      | { port: number; upstream?: string; upstream_pool?: string; log_level?: string; launcher?: string; next_port?: number; proxy_url?: string }
    >,
    patchModule: [] as Array<{ port: number; module: string; body: Record<string, unknown> }>,
    getWorkerRoute: [] as string[],
    patchModuleRoute: [] as string[],
    patchWorkerBodies: [] as Array<Record<string, unknown>>,
    patchUpstream: [] as Array<{ id: string; body: { name?: string; base_url?: string; api_key?: string; api_format?: string; protocol_probe?: { model: string } } }>,
    createUpstreamPool: [] as Array<{ name: string; upstreams: string[]; circuit_breaker?: CircuitBreaker }>,
    patchUpstreamPool: [] as Array<{
      id: string
      body: Partial<Pick<UpstreamPool, "mode" | "probe" | "upstreams" | "circuit_breaker">>
    }>,
    switchUpstreamPool: [] as Array<{ id: string; body: { upstream: string; mode: PoolSwitchMode } }>,
    probeUpstreamPool: [] as string[],
    patchSettings: [] as ProxySettingsPatch[],
    deleteWorker: [] as number[],
    deleteUpstream: [] as string[],
    deleteUpstreamPool: [] as string[],
    restartWorker: [] as number[],
    stopWorker: [] as number[],
    saveConfig: 0,
    getLogs: 0,
    listHostedSessions: 0,
    getHostedSession: [] as string[],
    listBatches: 0,
    createBatch: [] as CreateBatchRequest[],
    getBatch: [] as string[],
    deleteBatch: [] as string[],
    patchHostedSession: [] as Array<{ session_id: string; worker_id: string }>,
    testUpstream: [] as string[],
    testAllUpstreams: 0,
    getMetrics: [] as string[],
  }

  const fetch = createFetch(async (url) => {
    if (url.pathname === "/config/providers")
      return json({
        providers: toAinnUpstreams([...providers.values()]),
        default: Object.fromEntries([...providers.keys()].map((name) => [name, `${name}-proxy`])),
      })
    if (url.pathname === "/provider")
      return json({
        all: toAinnUpstreams([...providers.values()]),
        default: Object.fromEntries([...providers.keys()].map((name) => [name, `${name}-proxy`])),
        connected: [...providers.keys()],
      })
    if (url.pathname === "/agent")
      return json([
        {
          name: "build",
          mode: "primary",
          hidden: false,
          permission: [],
          model: { providerID: "openai", modelID: "openai-proxy" },
          options: {},
        },
      ])
    if (url.pathname === "/api/workers")
      return json({
        workers: [...workers.values()],
      })
    if (url.pathname === "/api/metrics") {
      const range = (url.searchParams.get("range") ?? "today") as MetricsRangeName
      calls.getMetrics.push(range)
      if (input.metricsResponder) return json(await input.metricsResponder(range))
      return json({
        range: { name: range, start: "2026-07-10T00:00:00+08:00", end: "2026-07-11T00:00:00+08:00" },
        workers: [{
          worker: "app",
          port: 6767,
          status: "running",
          upstream: "openai",
          live: { window_seconds: 60, in_flight: 0, requests: 1, errors: 0, rpm: 1, tpm: metrics.tpm, avg_latency_ms: 120, input_tokens: 12, output_tokens: 8, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, total_tokens: metrics.total_tokens, unknown_usage_requests: 0, dropped_events: 0 },
          totals: { requests: 1, errors: 0, avg_latency_ms: 120, input_tokens: 12, output_tokens: 8, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, total_tokens: metrics.total_tokens, unknown_usage_requests: 0 },
        }],
        skipped_records: 0,
        persistence_errors: 0,
      })
    }
    if (url.pathname.startsWith("/api/workers/") && url.search === "" && !url.pathname.includes("/modules/")) {
      const workerKey = url.pathname.slice("/api/workers/".length)
      calls.getWorkerRoute.push(decodeURIComponent(workerKey))
      if (input.strictModuleWorkerIDs && [...workers.values()].some((worker) => String(worker.port) === workerKey)) {
        return json({ error: "worker route must use stable ID" }, { status: 404 })
      }
      const worker = findWorker(workerKey)
      if (worker) return json(worker)
    }
    if (url.pathname === "/api/upstreams")
      return json({
        upstreams: Object.fromEntries(providers.entries()),
      })
    if (url.pathname === "/api/upstream-pools")
      return json({ pools: [...upstreamPools.values()] })
    if (url.pathname === "/api/config" && url.search === "") {
      if (url.href.includes("&__method=PUT")) return undefined
      return json({
        config: { plugins: config.plugins, upstream_pools: Object.fromEntries(upstreamPools.entries()) },
        status: config.status,
      })
    }
    if (url.pathname === "/api/config" && url.searchParams.get("__method") === "PUT") {
      return undefined
    }
    if (url.pathname === "/api/config")
      return json({
        config: { plugins: config.plugins, upstream_pools: Object.fromEntries(upstreamPools.entries()) },
        status: config.status,
      })
    if (url.pathname === "/api/settings") {
      return json({
        settings: config.settings,
        status: config.status,
      })
    }
    if (url.pathname === "/api/hosted-sessions") {
      calls.listHostedSessions += 1
      if (input.hostedSessionsError) {
        await Bun.sleep(25)
        return json({ error: input.hostedSessionsError }, { status: 500 })
      }
      return json({ sessions: hostedSessions })
    }
    if (url.pathname.startsWith("/api/hosted-sessions/")) {
      const sessionID = url.pathname.slice("/api/hosted-sessions/".length)
      calls.getHostedSession.push(sessionID)
      const session = hostedSessions.find((item) => item.session_id === sessionID)
      return json(session ?? { error: "hosted session not found" }, session ? undefined : { status: 404 })
    }
    if (url.pathname === "/api/workers/6767/logs") {
      calls.getLogs += 1
      return json({ lines: logs.get(6767) ?? [] })
    }
    if (url.pathname === "/api/workers/6767" && url.searchParams.get("__method") === "PATCH") {
      return undefined
    }
    if (url.pathname === "/api/workers/6767/modules/model_override" && url.searchParams.get("__method") === "PATCH") {
      return undefined
    }
    return undefined
  })

  let managerEvents: ReadableStreamDefaultController<Uint8Array> | undefined
  const managerEventEncoder = new TextEncoder()
  const override = (async (requestInput: RequestInfo | URL, init?: RequestInit) => {
    const request = requestInput instanceof Request ? requestInput : undefined
    const url = new URL(request ? request.url : String(requestInput))
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase()

    const hostedSessionRoute = url.pathname.match(/^\/api\/hosted-sessions\/([^/]+)$/)
    if (hostedSessionRoute && method === "PATCH") {
      const sessionID = decodeURIComponent(hostedSessionRoute[1]!)
      const body = JSON.parse(String(init?.body ?? "null")) as { worker_id: string }
      const worker = findWorker(body.worker_id)!
      const index = hostedSessions.findIndex((session) => session.session_id === sessionID)
      const updated = {
        ...hostedSessions[index],
        worker_id: worker.id,
        worker_name: worker.name,
        worker_port: worker.port,
        worker: { id: worker.id, name: worker.name },
      }
      hostedSessions[index] = updated
      calls.patchHostedSession.push({ session_id: sessionID, worker_id: body.worker_id })
      return json(updated)
    }

    if (url.pathname === "/api/upstream-pools" && method === "POST") {
      const body = JSON.parse(String(init?.body ?? "null")) as { name: string; upstreams: string[]; circuit_breaker?: CircuitBreaker }
      calls.createUpstreamPool.push(body)
      const pool: UpstreamPool = {
        id: body.name,
        name: body.name,
        mode: "active",
        probe: defaultPoolProbe,
        probe_state: "idle",
        upstreams: [...body.upstreams],
        circuit_breaker: body.circuit_breaker ?? defaultCircuitBreaker,
        workers: [],
        readiness: [],
      }
      upstreamPools.set(pool.id, pool)
      return json(pool, { status: 201 })
    }

    const upstreamPoolActionRoute = url.pathname.match(/^\/api\/upstream-pools\/([^/]+)\/(switch|probe)$/)
    if (upstreamPoolActionRoute && method === "POST") {
      const id = decodeURIComponent(upstreamPoolActionRoute[1]!)
      const current = upstreamPools.get(id)!
      if (upstreamPoolActionRoute[2] === "switch") {
        const body = JSON.parse(String(init?.body ?? "null")) as { upstream: string; mode: PoolSwitchMode }
        calls.switchUpstreamPool.push({ id, body })
        const upstream = providers.get(body.upstream)!
        for (const worker of workers.values()) {
          if (worker.upstream_pool === id) {
            setWorker({ ...worker, upstream_id: upstream.id, upstream })
          }
        }
        const pool = { ...current, active_upstream: body.upstream }
        upstreamPools.set(id, pool)
        return json(pool)
      }
      calls.probeUpstreamPool.push(id)
      return json(current)
    }

    const upstreamPoolRoute = url.pathname.match(/^\/api\/upstream-pools\/([^/]+)$/)
    if (upstreamPoolRoute && method === "PATCH") {
      const id = decodeURIComponent(upstreamPoolRoute[1]!)
      const body = JSON.parse(String(init?.body ?? "null")) as Partial<
        Pick<UpstreamPool, "mode" | "probe" | "upstreams" | "circuit_breaker">
      >
      calls.patchUpstreamPool.push({ id, body })
      const current = upstreamPools.get(id)!
      const pool = {
        ...current,
        ...(body.mode !== undefined ? { mode: body.mode } : {}),
        ...(body.probe !== undefined ? { probe: body.probe } : {}),
        ...(body.upstreams !== undefined ? { upstreams: [...body.upstreams] } : {}),
        ...(body.circuit_breaker !== undefined ? { circuit_breaker: body.circuit_breaker } : {}),
      }
      upstreamPools.set(id, pool)
      return json(pool)
    }

    if (upstreamPoolRoute && method === "DELETE") {
      const id = decodeURIComponent(upstreamPoolRoute[1]!)
      calls.deleteUpstreamPool.push(id)
      upstreamPools.delete(id)
      return json({ pool: id })
    }

    const workerRoute = url.pathname.match(/^\/api\/workers\/([^/]+)$/)
    if (workerRoute && method === "PATCH") {
      if (input.patchWorkerDelayMs) await Bun.sleep(input.patchWorkerDelayMs)
      const workerKey = decodeURIComponent(workerRoute[1]!)
      const current = findWorker(workerKey)!
      const body = JSON.parse(String(init?.body ?? "null")) as {
        name?: string
        port?: number
        upstream?: string
        upstream_id?: string
        upstream_pool?: string
        log_level?: string
        launcher?: string
        proxy_url?: string
      }
      calls.patchWorkerBodies.push(body)
      if (body.name !== undefined && body.name !== current.name) {
        calls.patchWorker.push({ id: current.id, name: body.name })
      } else {
        calls.patchWorker.push({
          port: current.port,
          upstream: body.upstream_id ?? body.upstream ?? current.upstream_id,
          ...(body.upstream_pool !== undefined ? { upstream_pool: body.upstream_pool } : {}),
          log_level: body.log_level ?? current.log_level,
          ...(body.port !== undefined && body.port !== current.port ? { next_port: body.port } : {}),
          ...(body.launcher ? { launcher: body.launcher } : {}),
          ...(body.proxy_url !== undefined ? { proxy_url: body.proxy_url } : {}),
        })
      }
      const nextUpstreamID = body.upstream_id ?? body.upstream
      const nextUpstream = nextUpstreamID ? providers.get(nextUpstreamID) : undefined
      if (nextUpstream) {
        setWorker({
          ...current,
          upstream_id: nextUpstream.id,
          upstream: nextUpstream,
        })
      }
      if (body.log_level) {
        setWorker({ ...findWorker(current.id)!, log_level: body.log_level })
      }
      if (body.launcher) {
        setWorker({ ...findWorker(current.id)!, launcher: body.launcher })
      }
      if (body.name !== undefined) {
        setWorker({ ...findWorker(current.id)!, name: body.name })
      }
      if (body.proxy_url !== undefined) {
        setWorker({ ...findWorker(current.id)!, proxy_url: body.proxy_url })
      }
      if (body.upstream_pool !== undefined) {
        for (const [id, pool] of upstreamPools) {
          upstreamPools.set(id, { ...pool, workers: pool.workers.filter((worker) => worker !== current.name) })
        }
        const pool = upstreamPools.get(body.upstream_pool)
        if (pool) upstreamPools.set(pool.id, { ...pool, workers: [...pool.workers, current.name] })
        setWorker({ ...findWorker(current.id)!, upstream_pool: body.upstream_pool || undefined })
      }
      if (body.port !== undefined && body.port !== current.port) {
        const worker = { ...findWorker(current.id)!, port: body.port }
        setWorker(worker)
        return json(worker)
      }
      return json(findWorker(current.id)!)
    }

    if (url.pathname === "/api/workers" && method === "POST") {
      const body = JSON.parse(String(init?.body ?? "null")) as { name: string; port?: number; upstream: string; launcher?: string }
      calls.createWorker.push(body)
      const port = body.port ?? 11201
      setWorker({
        id: body.name,
        name: body.name,
        upstream_id: body.upstream,
        port,
        role: "cli",
        launcher: body.launcher ?? "codex",
        protocol: providers.get(body.upstream)?.api_format === "anthropic" ? "anthropic" : "responses",
        upstream: providers.get(body.upstream)!,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
        modules: {},
        hooks: {},
        module_support: {
          api_translate: { protocols: ["responses", "chat_completions"], capabilities: ["input_text", "tool_calls", "stream_events"] },
          tool_filter: { protocols: ["responses"], capabilities: ["tool_calls"] },
          request_log: { protocols: ["responses", "chat_completions", "anthropic"] },
        },
      })
      return json(findWorker(body.name)!)
    }

    const moduleRoute = url.pathname.match(/^\/api\/workers\/([^/]+)\/modules\/([^/]+)$/)
    if (moduleRoute?.[2] === "model_override" && method === "PATCH") {
      const workerKey = decodeURIComponent(moduleRoute[1]!)
      calls.patchModuleRoute.push(workerKey)
      if (input.strictModuleWorkerIDs && [...workers.values()].some((worker) => String(worker.port) === workerKey)) {
        return json({ error: "module route must use stable worker ID" }, { status: 404 })
      }
      const worker = findWorker(workerKey)!
      const body = JSON.parse(String(init?.body ?? "null")) as { enabled: boolean; params?: { model?: string } }
      calls.patchModule.push({ port: worker.port, module: "model_override", body })
      setWorker({
        ...worker,
        modules: {
          ...worker.modules,
          model_override: body,
        },
      })
      return json({
        worker: worker.name,
        port: worker.port,
        module: {
          name: "model_override",
          enabled: body.enabled,
          params: body.params,
        },
      })
    }

    if (moduleRoute?.[2] === "tool_filter" && method === "PATCH") {
      const workerKey = decodeURIComponent(moduleRoute[1]!)
      calls.patchModuleRoute.push(workerKey)
      if (input.strictModuleWorkerIDs && [...workers.values()].some((worker) => String(worker.port) === workerKey)) {
        return json({ error: "module route must use stable worker ID" }, { status: 404 })
      }
      const worker = findWorker(workerKey)!
      const body = JSON.parse(String(init?.body ?? "null")) as { enabled: boolean; params?: Record<string, unknown> }
      calls.patchModule.push({ port: worker.port, module: "tool_filter", body })
      setWorker({
        ...worker,
        modules: {
          ...worker.modules,
          tool_filter: body,
        },
      })
      return json({
        worker: worker.name,
        port: worker.port,
        module: {
          name: "tool_filter",
          enabled: body.enabled,
          params: body.params,
        },
      })
    }

    const restartRoute = url.pathname.match(/^\/api\/workers\/([^/]+)\/restart$/)
    if (restartRoute && method === "POST") {
      const worker = findWorker(decodeURIComponent(restartRoute[1]!))!
      calls.restartWorker.push(worker.port)
      setWorker({ ...worker, status: "running" })
      return json({ worker: worker.name, status: "running" })
    }

    if (workerRoute && method === "DELETE") {
      const worker = findWorker(decodeURIComponent(workerRoute[1]!))!
      calls.stopWorker.push(worker.port)
      setWorker({ ...worker, status: "stopped" })
      return json({ worker: worker.name, status: "stopped" })
    }

    const workerConfigRoute = url.pathname.match(/^\/api\/workers\/([^/]+)\/config$/)
    if (workerConfigRoute && method === "DELETE") {
      const worker = findWorker(decodeURIComponent(workerConfigRoute[1]!))!
      calls.deleteWorker.push(worker.port)
      workers.delete(worker.id)
      return json({ worker: worker.name })
    }

    if (url.pathname.startsWith("/api/upstreams/") && method === "PATCH") {
      const id = url.pathname.slice("/api/upstreams/".length)
      const body = JSON.parse(String(init?.body ?? "null")) as { name?: string; base_url?: string; api_key?: string; api_format?: string; protocol_probe?: { model: string } }
      calls.patchUpstream.push({ id, body })
      if (input.patchUpstreamError) return json({ error: input.patchUpstreamError }, { status: 409 })
      providers.set(id, {
        id,
        name: body.name ?? providers.get(id)?.name ?? id,
        base_url: body.base_url ?? providers.get(id)?.base_url ?? "",
        api_format: body.api_format ?? providers.get(id)?.api_format,
        protocol_probe: body.protocol_probe ?? providers.get(id)?.protocol_probe,
        has_api_key: body.api_key !== undefined ? Boolean(body.api_key) : providers.get(id)?.has_api_key ?? false,
      })
      for (const worker of workers.values()) {
        if (worker.upstream_id !== id) continue
        setWorker({
          ...worker,
          upstream: providers.get(id)!,
        })
      }
      return json(providers.get(id)!)
    }

    if (url.pathname.startsWith("/api/upstreams/") && method === "DELETE") {
      const id = url.pathname.slice("/api/upstreams/".length)
      calls.deleteUpstream.push(id)
      providers.delete(id)
      return json({ upstream: id })
    }

    if (url.pathname === "/api/upstreams/test" && method === "POST") {
      calls.testAllUpstreams += 1
      return json({
        results: input.probeResults ?? [...providers.values()].map((p) => ({
          upstream: p.id,
          ok: true,
          status_code: 200,
          latency_ms: 120,
          mode: p.protocol_probe ? "protocol" : "reachability",
          authoritative: false,
          readiness: "unknown",
        })),
      })
    }

    if (url.pathname.startsWith("/api/upstreams/") && url.pathname.endsWith("/test") && method === "POST") {
      const id = url.pathname.slice("/api/upstreams/".length, -"/test".length)
      calls.testUpstream.push(id)
      return json({ upstream: id, ok: true, status_code: 200, latency_ms: 120, mode: providers.get(id)?.protocol_probe ? "protocol" : "reachability", authoritative: false, readiness: "unknown" })
    }

    if (url.pathname === "/api/config" && method === "PUT") {
      calls.saveConfig += 1
      config.status = { ...config.status, dirty: false }
      return json({ status: config.status })
    }

    if (url.pathname === "/api/settings" && method === "PATCH") {
      const body = JSON.parse(String(init?.body ?? "null")) as ProxySettingsPatch
      calls.patchSettings.push(body)
      config.settings = {
        ...config.settings,
        ...body,
        launch: { ...config.settings.launch, ...body.launch },
        terminal: {
          ...config.settings.terminal,
          ...body.terminal,
          tmux: { ...config.settings.terminal.tmux, ...body.terminal?.tmux },
        },
        metrics: { ...config.settings.metrics, ...body.metrics },
      }
      config.status = { ...config.status, dirty: false, generation: config.status.generation + 1 }
      return json({ settings: config.settings, status: config.status })
    }

    if (url.pathname === "/api/batches" && method === "GET") {
      calls.listBatches += 1
      return json({ batches: [...batches.values()] })
    }

    if (url.pathname === "/api/batches" && method === "POST") {
      const body = JSON.parse(String(init?.body ?? "null")) as CreateBatchRequest
      calls.createBatch.push(body)
      const workerPort = [...workers.values()].find((worker) => worker.name === body.worker_name)?.port ?? 0
      const batchID = `batch_${batches.size + 1}`
      const variantCount = body.count ?? defaultBatchVariantCount
      const variants = Array.from({ length: variantCount }, (_, index) => {
        const number = index + 1
        const hostedSessionID = `${batchID}_session_${number}`
        hostedSessions.push({
          session_id: hostedSessionID,
          session_label: `${body.title} ${number}`,
          worker_name: body.worker_name,
          worker_port: workerPort,
          workspace: `${body.source_directory}/.worktrees/${body.title.replace(/\s+/g, "-")}-${number}`,
          model: body.model,
          created_at: "2026-07-09T00:00:00Z",
          last_opened_at: "2026-07-09T00:00:00Z",
          status: "active",
          ...(batchHostedSessionWindowMode === "present" ? { tmux_window_id: `@${number}` } : {}),
        })
        return {
          id: `variant_${number}`,
          index: number,
          hosted_session_id: hostedSessionID,
          session_label: `${body.title} ${number}`,
          worktree_dir: `${body.source_directory}/.worktrees/${body.title.replace(/\s+/g, "-")}-${number}`,
        }
      })
      const batchRun: BatchRun = {
        id: batchID,
        title: body.title,
        worker_name: body.worker_name,
        worker_port: workerPort,
        model: body.model,
        source_directory: body.source_directory,
        created_at: "2026-07-09T00:00:00Z",
        variants,
      }
      batches.set(batchRun.id, batchRun)
      return json(batchRun)
    }

    if (url.pathname.startsWith("/api/batches/")) {
      const parts = url.pathname.split("/")
      const batchID = parts[3]
      if (parts.length === 4 && method === "GET") {
        calls.getBatch.push(batchID)
        return json(batches.get(batchID) ?? { error: "batch not found" }, batches.has(batchID) ? undefined : { status: 404 })
      }
      if (parts.length === 4 && method === "DELETE") {
        calls.deleteBatch.push(batchID)
        batches.delete(batchID)
        return json({ batch_id: batchID })
      }
    }

    if (url.pathname === "/api/events") {
      return new Response(new ReadableStream({
        start(controller) {
          managerEvents = controller
        },
      }), {
        headers: { "content-type": "text/event-stream" },
      })
    }

    return fetch.fetch(requestInput, init)
  }) as typeof fetch.fetch

  return {
    calls,
    fetch: override,
    hostedSessions,
    metrics,
    replaceDashboardData(next: {
      workers: HarnessWorker[]
      upstreams: HarnessUpstream[]
      upstreamPools?: UpstreamPool[]
      hostedSessions: HostedSessionSummary[]
    }) {
      providers.clear()
      for (const upstream of next.upstreams) {
        const id = upstream.id ?? upstream.name
        providers.set(id, { ...upstream, id })
      }
      workers.clear()
      for (const worker of next.workers) {
        const upstreamID = worker.upstream_id ?? worker.upstream.id ?? worker.upstream.name
        workers.set(worker.id ?? worker.name, {
          ...worker,
          id: worker.id ?? worker.name,
          upstream_id: upstreamID,
          upstream: { ...worker.upstream, id: worker.upstream.id ?? upstreamID },
        })
      }
      if (next.upstreamPools) {
        upstreamPools.clear()
        for (const pool of next.upstreamPools) {
          upstreamPools.set(pool.id, { ...pool, upstreams: [...pool.upstreams], workers: [...pool.workers], readiness: [...pool.readiness] })
        }
      }
      hostedSessions.splice(0, hostedSessions.length, ...next.hostedSessions)
    },
    emitManagerEvent(type: string, payload: Record<string, unknown> = {}) {
      if (!managerEvents) throw new Error("manager event source not ready")
      managerEvents.enqueue(managerEventEncoder.encode(`event: ${type}\ndata: ${JSON.stringify(payload)}\n\n`))
    },
  }
}

export async function mountProxyApp(input: ProxyHarnessInput & { stateFiles?: Record<string, string> } = {}) {
  const tmp = await tmpdir()
  const home = tmp.path
  const app = "ainn"
  const data = path.join(home, ".local", "share", app)
  const cache = path.join(home, ".cache", app)
  const state = path.join(home, ".local", "state", app)
  const setup = await createTestRenderer({ width: input.width ?? 80, height: input.height ?? 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), "{}")
  for (const [name, content] of Object.entries(input.stateFiles ?? {})) {
    await Bun.write(path.join(state, name), content)
  }
  const events = createEventSource()
  const proxy = createProxyHarness(input)
  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  const [{ run }, { registerProxyCommands }] = await Promise.all([
    import("../src/app"),
    import("../src/proxy/commands"),
  ])
  const task = Effect.runPromise(
    run({
      url: "http://test",
      directory,
      config: createTuiResolvedConfig({ plugin_enabled: {} }),
      fetch: proxy.fetch,
      events: events.source,
      args: {},
      pluginHost: {
        async start(plugin) {
          api = plugin.api
          registerProxyCommands(api, { batchSessionLauncher: input.batchSessionLauncher })
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
  await setup.renderOnce()
  await setup.renderOnce()

  return {
    api,
    calls: proxy.calls,
    emitManagerEvent: proxy.emitManagerEvent,
    hostedSessions: proxy.hostedSessions,
    metrics: proxy.metrics,
    replaceDashboardData(input: Parameters<typeof proxy.replaceDashboardData>[0]) {
      proxy.replaceDashboardData(input)
      proxy.emitManagerEvent("config.changed")
    },
    setup,
    frame() {
      return setup.captureCharFrame()
    },
    lines() {
      return frameLines(setup.captureCharFrame())
    },
    mockInput: setup.mockInput,
    async render() {
      await setup.renderOnce()
    },
    async cleanup() {
      setup.renderer.destroy()
      await task
      mock.restore()
      await tmp[Symbol.asyncDispose]()
    },
  }
}

export type ProxyApp = Awaited<ReturnType<typeof mountProxyApp>>

export async function runCommand(app: ProxyApp, command: string) {
  app.api.keymap.dispatchCommand(command)
  await app.render()
}

export async function openWorkerDetail(app: ProxyApp) {
  await runCommand(app, "proxy.workers")
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.submit")
}

export async function openUpstreamManager(app: ProxyApp) {
  await runCommand(app, "proxy.upstreams")
}

export async function openUpstreamEditor(app: ProxyApp, name: string) {
  await openUpstreamManager(app)
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.submit")
  await wait(async () => {
    await app.render()
    return app.frame().includes(`Edit Upstream: ${name}`)
  })
}
