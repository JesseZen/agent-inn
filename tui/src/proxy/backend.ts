import type { Agent, Model, Path, Project, Provider } from "@agent-inn/sdk/v2"
import type { EventSource } from "../context/sdk"

export type RedactedUpstream = {
  id: string
  name: string
  base_url?: string
  has_api_key: boolean
  api_format?: string
  missing?: boolean
}

export type UpstreamProbeResult = {
  upstream: string
  ok: boolean
  degraded?: boolean
  status_code: number
  latency_ms: number
  error?: string
}

export type ModuleConfig = {
  enabled: boolean
  params?: Record<string, unknown>
}

export type ProtocolKind = "responses" | "chat_completions" | "anthropic"
export type ProtocolCapability = "input_text" | "tool_calls" | "stream_events"

export type ModuleProtocolSupport = {
  protocols?: ProtocolKind[]
  capabilities?: ProtocolCapability[]
}

export type HookStatus = {
  state: string
  detail?: Record<string, string>
}

export type PluginDefinition = {
  kind: "request_middleware" | "lifecycle_hook"
  source: "builtin" | "external"
  path?: string
}

export type WorkerConfig = {
  role?: string
  launcher?: string
  port: number
  upstream: string
  proxy_url?: string
  log_level?: string
  request_modules?: Record<string, ModuleConfig>
  hooks?: Record<string, ModuleConfig>
}

export type UpstreamProfile = {
  name?: string
  base_url: string
  api_key?: string
  api_format?: string
}

export type ProxyConfig = {
  settings?: ProxySettings
  plugins?: Record<string, PluginDefinition>
  workers?: Record<string, WorkerConfig>
  upstreams?: Record<string, UpstreamProfile>
}

export type MetricsRangeName = "today" | "last_24h"

export type MetricsTotals = {
  requests: number
  errors: number
  avg_latency_ms: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  reasoning_tokens: number
  total_tokens: number
  unknown_usage_requests: number
}

export type WorkerLiveMetrics = MetricsTotals & {
  window_seconds: number
  in_flight: number
  rpm: number
  tpm: number
}

export type WorkerMetricsAggregate = {
  worker: string
  port: number
  status: string
  upstream?: string
  live: WorkerLiveMetrics
  totals: MetricsTotals
}

export type MetricsResponse = {
  range: { name: MetricsRangeName; start: string; end: string }
  workers: WorkerMetricsAggregate[]
  skipped_records: number
}

export type WorkerSummary = {
  id: string
  name: string
  upstream_id: string
  port: number
  role?: string
  launcher?: string
  proxy_url?: string
  proxy_url_redacted?: boolean
  protocol?: ProtocolKind
  upstream: RedactedUpstream
  status: string
  snapshot_generation: number
  log_level: string
  modules?: Record<string, ModuleConfig>
  hooks?: Record<string, ModuleConfig>
  hook_statuses?: Record<string, HookStatus>
  module_support?: Record<string, ModuleProtocolSupport>
  metrics?: WorkerLiveMetrics
}

export type WorkerDetail = WorkerSummary

export type ProxyConfigStatus = {
  generation: number
  dirty: boolean
  last_save_error?: string
}

export type ProxyConfigResponse = {
  config: ProxyConfig
  status: ProxyConfigStatus
}

export type ProxySettings = {
  state_dir: string
  log_dir: string
  launch: {
    default_mode: string
  }
  terminal: {
    host: string
    opener: string
    tmux: {
      socket_name: string
      host_session: string
      host_start_mode: string
      turn_status_hooks: boolean
    }
  }
  metrics: {
    retention_days: number
  }
}

export type ProxySettingsResponse = {
  settings: ProxySettings
  status: ProxyConfigStatus
}

export type HostedTurnState = "idle" | "running" | "done" | "failed" | "interrupted"

export type HostedSessionRecord = {
  session_id: string
  session_label: string
  worker_id?: string
  worker_name: string
  worker_port: number
  worker?: {
    id: string
    name: string
    missing?: boolean
  }
  workspace?: string
  model?: string
  add_dirs?: string[]
  tmux_window_id?: string
  launcher_session_id?: string
  turn_state?: HostedTurnState
  turn_state_reason?: string
  turn_generation?: number
  turn_acknowledged_generation?: number
  created_at: string
  last_opened_at: string
}

export type HostedSessionSummary = HostedSessionRecord & {
  status: "active" | "stale"
}

export type BatchVariant = {
  id: string
  index: number
  hosted_session_id: string
  session_label: string
  worktree_dir: string
}

export type BatchRun = {
  id: string
  title: string
  worker_name: string
  worker_port: number
  model?: string
  source_directory: string
  created_at: string
  variants: BatchVariant[]
}

export type CreateBatchRequest = {
  title: string
  worker_name: string
  count?: number
  source_directory: string
  model?: string
}

function json(value: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(value), {
    ...init,
    headers: {
      "content-type": "application/json",
      ...(init?.headers ?? {}),
    },
  })
}

async function readJSON<T>(response: Response): Promise<T> {
  if (response.ok) return response.json() as Promise<T>
  const body = (await response.json().catch(() => undefined)) as { error?: string; message?: string } | undefined
  throw new Error(body?.error ?? body?.message ?? `${response.status} ${response.statusText}`)
}

async function fetchManager<T>(baseUrl: string, pathname: string, init?: RequestInit): Promise<T> {
  const response = await globalThis.fetch(new URL(pathname, baseUrl), init)
  return readJSON<T>(response)
}

function createModel(providerID: string): Model {
  const modelID = `${providerID}-proxy`
  return {
    id: modelID,
    providerID,
    api: {
      id: modelID,
      url: "",
      npm: "",
    },
    name: `${providerID} Proxy`,
    capabilities: {
      temperature: false,
      reasoning: false,
      attachment: false,
      toolcall: false,
      input: {
        text: true,
        audio: false,
        image: false,
        video: false,
        pdf: false,
      },
      output: {
        text: true,
        audio: false,
        image: false,
        video: false,
        pdf: false,
      },
      interleaved: false,
    },
    cost: {
      input: 0,
      output: 0,
      cache: {
        read: 0,
        write: 0,
      },
    },
    limit: {
      context: 128_000,
      output: 8_192,
    },
    status: "active",
    options: {},
    headers: {},
    release_date: "2026-01-01",
  }
}

export function toAinnUpstreams(upstreams: RedactedUpstream[]): Provider[] {
  return upstreams.map((upstream) => {
    const model = createModel(upstream.id)
    return {
      id: upstream.id,
      name: upstream.name,
      source: "config",
      env: [],
      key: "",
      options: {
        base_url: upstream.base_url ?? "",
        api_format: upstream.api_format,
        has_api_key: upstream.has_api_key,
      },
      models: {
        [model.id]: model,
      },
    }
  })
}

function defaultModels(providers: Provider[]) {
  return Object.fromEntries(providers.map((provider) => [provider.id, Object.keys(provider.models)[0] ?? ""]))
}

function createPath(directory: string): Path {
  return {
    home: process.env.HOME ?? "",
    state: "",
    config: "",
    worktree: directory,
    directory,
  }
}

function createProject(directory: string): Project {
  const now = Date.now()
  return {
    id: "ainn",
    name: "ainn",
    worktree: directory,
    vcs: "git",
    time: {
      created: now,
      updated: now,
    },
    sandboxes: [],
  }
}

function createAgent(providers: Provider[]): Agent[] {
  const first = providers[0]
  const modelID = first ? Object.keys(first.models)[0] : undefined
  return [
    {
      name: "build",
      description: "Proxy manager",
      mode: "primary",
      hidden: false,
      permission: [],
      model:
        first && modelID
          ? {
              providerID: first.id,
              modelID,
            }
          : undefined,
      options: {},
    },
  ]
}

function createLocation(directory: string) {
  return {
    directory,
    project: {
      id: "ainn",
      directory,
    },
  }
}

export function emptyEventSource(): EventSource {
  return {
    subscribe: async () => () => {},
  }
}

export function createProxyFetch(input: { baseUrl: string; directory: string }) {
  return async function proxyFetch(requestInfo: RequestInfo | URL, init?: RequestInit) {
    const request = requestInfo instanceof Request ? requestInfo : undefined
    const url = new URL(request ? request.url : String(requestInfo))
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase()
    const upstreams = toAinnUpstreams(
      await fetchManager<{ upstreams: Record<string, RedactedUpstream> }>(input.baseUrl, "/api/upstreams").then((result) =>
        Object.values(result.upstreams ?? {}),
      ),
    )
    const providerDefault = defaultModels(upstreams)
    const location = createLocation(input.directory)

    if (url.pathname === "/path" && method === "GET") return json(createPath(input.directory))
    if (url.pathname === "/project/current" && method === "GET") return json(createProject(input.directory))
    if (url.pathname === "/project/ainn/directories" && method === "GET") {
      return json([{ directory: input.directory }])
    }
    if (url.pathname === "/experimental/workspace" && method === "GET") return json([])
    if (url.pathname === "/experimental/workspace/status" && method === "GET") return json([])
    if (url.pathname === "/config/providers" && method === "GET") {
      return json({ providers: upstreams, default: providerDefault })
    }
    if (url.pathname === "/provider" && method === "GET") {
      return json({ all: upstreams, default: providerDefault, connected: upstreams.map((upstream) => upstream.id) })
    }
    if (url.pathname === "/command" && method === "GET") return json([])
    if (url.pathname === "/config" && method === "GET") {
      const config = await fetchManager<ProxyConfigResponse>(input.baseUrl, "/api/config")
      return json(config.config)
    }
    if (url.pathname === "/experimental/capabilities" && method === "GET") {
      return json({ backgroundSubagents: false })
    }
    if (url.pathname === "/experimental/console" && method === "GET") {
      return json({ consoleManagedProviders: [], switchableOrgCount: 0 })
    }
    if (url.pathname === "/agent" && method === "GET") return json(createAgent(upstreams))
    if (url.pathname === "/lsp" && method === "GET") return json([])
    if (url.pathname === "/mcp" && method === "GET") return json({})
    if (url.pathname === "/experimental/resource" && method === "GET") return json({})
    if (url.pathname === "/formatter" && method === "GET") return json([])
    if (url.pathname === "/session/status" && method === "GET") return json({})
    if (url.pathname === "/provider/auth" && method === "GET") return json({})
    if (url.pathname === "/session" && method === "GET") return json([])
    if (url.pathname === "/vcs" && method === "GET") return json({ branch: "main" })

    if (url.pathname === "/api/location" && method === "GET") return json(location)
    if (url.pathname === "/api/agent" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/model" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/provider" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/integration" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/reference" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/command" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/skill" && method === "GET") return json({ location, data: [] })

    if (url.pathname.startsWith("/session") && method !== "GET") {
      return json({ message: "Proxy mode does not support chat sessions yet." }, { status: 501 })
    }

    return request ? globalThis.fetch(request) : globalThis.fetch(url, init)
  }
}
