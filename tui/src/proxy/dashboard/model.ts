import { displaySlice, promptOffsetWidth } from "../../prompt/display"
import type { HostedSessionSnapshot } from "../hosted-session-contract"
import type { RedactedUpstream, UpstreamPool, WorkerSummary } from "../backend"

const DASHBOARD_TEXT_OVERHEAD = 6
const DASHBOARD_META_GAP = 1
const DASHBOARD_TRUNCATION_MARKER = "…"

export type DashboardNode =
  | { id: string; kind: "pool"; label: string; data: UpstreamPool }
  | { id: string; kind: "upstream"; label: string; data: RedactedUpstream }
  | { id: string; kind: "worker"; label: string; data: WorkerSummary }
  | { id: string; kind: "session"; label: string; data: HostedSessionSnapshot }

export type DashboardWorkerBranch = {
  worker: Extract<DashboardNode, { kind: "worker" }>
  sessions: Extract<DashboardNode, { kind: "session" }>[]
}

type DashboardDomainSummary = {
  id: string
  workers: DashboardWorkerBranch[]
  healthyWorkers: number
  totalWorkers: number
  totalSessions: number
  warning: boolean
}

export type DashboardPoolMember = {
  upstream: Extract<DashboardNode, { kind: "upstream" }>
  workers: DashboardWorkerBranch[]
  active: boolean
}

export type DashboardDomain = DashboardDomainSummary &
  (
    | {
        kind: "upstream"
        upstream: Extract<DashboardNode, { kind: "upstream" }>
      }
    | {
        kind: "pool"
        pool: Extract<DashboardNode, { kind: "pool" }>
        members: DashboardPoolMember[]
      }
  )

export type DashboardSummary = {
  pools: number
  upstreams: number
  healthyWorkers: number
  workers: number
  sessions: number
  unbound: number
}

export type DashboardModel = {
  summary: DashboardSummary
  domains: DashboardDomain[]
  unboundUpstreams: Extract<DashboardNode, { kind: "upstream" }>[]
  unboundSessions: Extract<DashboardNode, { kind: "session" }>[]
}

export function buildDashboardModel(
  workers: WorkerSummary[],
  upstreams: RedactedUpstream[],
  sessions: HostedSessionSnapshot[],
  pools: UpstreamPool[] = [],
): DashboardModel {
  const upstreamByID = new Map(upstreams.map((upstream) => [upstream.id, upstream]))
  const workerIDs = new Set(workers.map((item) => item.id))
  const sessionsByWorkerID = new Map<string, HostedSessionSnapshot[]>()
  for (const item of sessions) {
    const workerID = item.worker.id
    const workerSessions = sessionsByWorkerID.get(workerID) ?? []
    workerSessions.push(item)
    sessionsByWorkerID.set(workerID, workerSessions)
  }

  const poolIDs = new Set(pools.map((pool) => pool.id))
  const poolWorkers = new Map<string, WorkerSummary[]>()
  const workersByUpstreamID = new Map<string, WorkerSummary[]>()
  const domainUpstreams = new Map<string, RedactedUpstream>()
  for (const item of workers) {
    if (item.upstream_pool && poolIDs.has(item.upstream_pool)) {
      const workers = poolWorkers.get(item.upstream_pool) ?? []
      workers.push(item)
      poolWorkers.set(item.upstream_pool, workers)
      continue
    }
    const domainWorkers = workersByUpstreamID.get(item.upstream_id) ?? []
    domainWorkers.push(item)
    workersByUpstreamID.set(item.upstream_id, domainWorkers)
    domainUpstreams.set(item.upstream_id, upstreamByID.get(item.upstream_id) ?? item.upstream)
  }

  const upstreamDomains: DashboardDomain[] = [...domainUpstreams.entries()]
    .map(([upstreamID, upstream]) => {
      const domainWorkers = (workersByUpstreamID.get(upstreamID) ?? []).sort((left, right) => left.name.localeCompare(right.name))
      const branches = domainWorkers.map((item) => ({
        worker: {
          id: `worker:${item.id}`,
          kind: "worker" as const,
          label: item.name,
          data: item,
        },
        sessions: (sessionsByWorkerID.get(item.id) ?? [])
          .sort((left, right) => left.session_label.localeCompare(right.session_label))
          .map((hostedSession) => ({
            id: `session:${hostedSession.session_id}`,
            kind: "session" as const,
            label: hostedSession.session_label,
            data: hostedSession,
          })),
      }))
      const healthyWorkers = domainWorkers.filter((item) => item.status === "running").length
      return {
        kind: "upstream" as const,
        id: `upstream:${upstreamID}`,
        upstream: {
          id: `upstream:${upstreamID}`,
          kind: "upstream" as const,
          label: upstream.name,
          data: upstream,
        },
        workers: branches,
        healthyWorkers,
        totalWorkers: domainWorkers.length,
        totalSessions: branches.reduce((count, branch) => count + branch.sessions.length, 0),
        warning: upstream.missing === true || !upstream.has_api_key || domainWorkers.some((item) => item.status === "failed"),
      }
    })
    .sort((left, right) => left.upstream.label.localeCompare(right.upstream.label))

  const poolDomains: DashboardDomain[] = pools
    .map((pool) => {
      const members = pool.upstreams.map((upstreamID) => {
        const upstream = upstreamByID.get(upstreamID) ?? {
          id: upstreamID,
          name: upstreamID,
          has_api_key: false,
          missing: true,
        }
        const memberWorkers = (poolWorkers.get(pool.id) ?? [])
          .filter((worker) => worker.upstream_id === upstreamID)
          .sort((left, right) => left.name.localeCompare(right.name))
        return {
          upstream: {
            id: `pool-member:${pool.id}:${upstreamID}`,
            kind: "upstream" as const,
            label: upstream.name,
            data: upstream,
          },
          workers: memberWorkers.map((item) => ({
            worker: {
              id: `worker:${item.id}`,
              kind: "worker" as const,
              label: item.name,
              data: item,
            },
            sessions: (sessionsByWorkerID.get(item.id) ?? [])
              .sort((left, right) => left.session_label.localeCompare(right.session_label))
              .map((hostedSession) => ({
                id: `session:${hostedSession.session_id}`,
                kind: "session" as const,
                label: hostedSession.session_label,
                data: hostedSession,
              })),
          })),
          active: pool.active_upstream === upstreamID,
        }
      })
      const branches = members.flatMap((member) => member.workers)
      const workers = poolWorkers.get(pool.id) ?? []
      return {
        kind: "pool" as const,
        id: `pool:${pool.id}`,
        pool: {
          id: `pool:${pool.id}`,
          kind: "pool" as const,
          label: pool.name,
          data: pool,
        },
        members,
        workers: branches,
        healthyWorkers: workers.filter((worker) => worker.status === "running").length,
        totalWorkers: workers.length,
        totalSessions: branches.reduce((count, branch) => count + branch.sessions.length, 0),
        warning:
          members.some((member) => member.upstream.data.missing || !member.upstream.data.has_api_key) || workers.some((worker) => worker.status === "failed"),
      }
    })
    .sort((left, right) => {
      if (left.kind !== "pool" || right.kind !== "pool") return 0
      return left.pool.label.localeCompare(right.pool.label)
    })
  const domains = [...poolDomains, ...upstreamDomains]

  const usedUpstreamIDs = new Set([...workersByUpstreamID.keys(), ...pools.flatMap((pool) => pool.upstreams)])
  const unboundUpstreams = upstreams
    .filter((upstream) => !usedUpstreamIDs.has(upstream.id))
    .sort((left, right) => left.name.localeCompare(right.name))
    .map((upstream) => ({
      id: `upstream:${upstream.id}`,
      kind: "upstream" as const,
      label: upstream.name,
      data: upstream,
    }))
  const unboundSessions = sessions
    .filter((item) => !workerIDs.has(item.worker.id))
    .sort((left, right) => left.session_label.localeCompare(right.session_label))
    .map((item) => ({
      id: `session:${item.session_id}`,
      kind: "session" as const,
      label: item.session_label,
      data: item,
    }))

  return {
    summary: {
      pools: pools.length,
      upstreams: new Set([...upstreams.map((upstream) => upstream.id), ...domainUpstreams.keys()]).size,
      healthyWorkers: domains.reduce((count, domain) => count + domain.healthyWorkers, 0),
      workers: workers.length,
      sessions: sessions.length,
      unbound: unboundUpstreams.length + unboundSessions.length,
    },
    domains,
    unboundUpstreams,
    unboundSessions,
  }
}

export function fitDashboardText(label: string, meta: string, availableWidth: number): { label: string; meta: string } {
  if (!Number.isFinite(availableWidth)) return { label, meta }
  const contentWidth = Math.max(0, availableWidth - DASHBOARD_TEXT_OVERHEAD)
  if (promptOffsetWidth(label) + DASHBOARD_META_GAP + promptOffsetWidth(meta) <= contentWidth) return { label, meta }
  if (promptOffsetWidth(label) <= contentWidth) return { label, meta: "" }
  if (contentWidth === 0) return { label: "", meta: "" }
  if (contentWidth === 1) return { label: DASHBOARD_TRUNCATION_MARKER, meta: "" }
  return {
    label: `${displaySlice(label, 0, contentWidth - promptOffsetWidth(DASHBOARD_TRUNCATION_MARKER))}${DASHBOARD_TRUNCATION_MARKER}`,
    meta: "",
  }
}
