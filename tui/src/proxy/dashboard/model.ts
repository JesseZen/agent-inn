import { displaySlice, promptOffsetWidth } from "../../prompt/display"
import type { HostedSessionSummary, RedactedUpstream, WorkerSummary } from "../backend"

const DASHBOARD_TEXT_OVERHEAD = 6
const DASHBOARD_META_GAP = 1
const DASHBOARD_TRUNCATION_MARKER = "…"

export type DashboardNode =
  | { id: string; kind: "upstream"; label: string; data: RedactedUpstream }
  | { id: string; kind: "worker"; label: string; data: WorkerSummary }
  | { id: string; kind: "session"; label: string; data: HostedSessionSummary }

export type DashboardWorkerBranch = {
  worker: Extract<DashboardNode, { kind: "worker" }>
  sessions: Extract<DashboardNode, { kind: "session" }>[]
}

export type DashboardDomain = {
  id: string
  upstream: Extract<DashboardNode, { kind: "upstream" }>
  workers: DashboardWorkerBranch[]
  healthyWorkers: number
  totalWorkers: number
  totalSessions: number
  warning: boolean
}

export type DashboardSummary = {
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
  sessions: HostedSessionSummary[],
): DashboardModel {
  const upstreamByID = new Map(upstreams.map((upstream) => [upstream.id, upstream]))
  const workerIDs = new Set(workers.map((item) => item.id))
  const sessionsByWorkerID = new Map<string, HostedSessionSummary[]>()
  for (const item of sessions) {
    const workerID = item.worker_id ?? item.worker_name
    const workerSessions = sessionsByWorkerID.get(workerID) ?? []
    workerSessions.push(item)
    sessionsByWorkerID.set(workerID, workerSessions)
  }

  const workersByUpstreamID = new Map<string, WorkerSummary[]>()
  const domainUpstreams = new Map<string, RedactedUpstream>()
  for (const item of workers) {
    const domainWorkers = workersByUpstreamID.get(item.upstream_id) ?? []
    domainWorkers.push(item)
    workersByUpstreamID.set(item.upstream_id, domainWorkers)
    domainUpstreams.set(item.upstream_id, upstreamByID.get(item.upstream_id) ?? item.upstream)
  }

  const domains = [...domainUpstreams.entries()]
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

  const usedUpstreamIDs = new Set(workersByUpstreamID.keys())
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
    .filter((item) => !workerIDs.has(item.worker_id ?? item.worker_name))
    .sort((left, right) => left.session_label.localeCompare(right.session_label))
    .map((item) => ({
      id: `session:${item.session_id}`,
      kind: "session" as const,
      label: item.session_label,
      data: item,
    }))

  return {
    summary: {
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

export function fitDashboardText(
  label: string,
  meta: string,
  availableWidth: number,
): { label: string; meta: string } {
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
