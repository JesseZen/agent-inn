import type { HostedSessionSummary, WorkerSummary, RedactedUpstream } from "../backend"
import { displaySlice, promptOffsetWidth } from "../../prompt/display"

export type TopologyNode = {
  id: string
  kind: "upstream" | "worker" | "session"
  label: string
  meta: string
  displayLabel: string
  displayMeta: string
  width: number
  height: number
  data: WorkerSummary | RedactedUpstream | HostedSessionSummary
}

export type TopologyWorkerRow = {
  workers: TopologyNode[]
  width: number
}

export type TopologyGroup = {
  upstream: TopologyNode
  workers: TopologyNode[]
  workerRows: TopologyWorkerRow[]
  sessions: Record<string, TopologyNode[]>
  width: number
}

export type TopologyGroupRow = {
  groups: TopologyGroup[]
  width: number
}

export type TopologyLayout = {
  groups: TopologyGroup[]
  groupRows: TopologyGroupRow[]
  orphans: TopologyNode[]
  orphanRows: TopologyNode[][]
  unboundSessions: TopologyNode[]
  unboundSessionRows: TopologyNode[][]
  rows: number
}

const NODE_HEIGHT = 3
const NODE_MARKER_WIDTH = 2
const NODE_MIN_GAP = 1
const NODE_CONTENT_PADDING = 2
const NODE_BORDER_WIDTH = 2
const COL_GAP = 2
const GROUP_GAP = 4
const TRUNCATION_MARKER = "…"

export const TOPOLOGY_GROUP_GAP = GROUP_GAP
export const TOPOLOGY_COL_GAP = COL_GAP
export const TOPOLOGY_NODE_HEIGHT = NODE_HEIGHT
export const TOPOLOGY_EDGE_ROWS = 1

function nodeWidth(label: string, meta: string): number {
  const metaGap = meta === "" ? 0 : NODE_MIN_GAP
  return promptOffsetWidth(label) + promptOffsetWidth(meta) + NODE_MARKER_WIDTH + metaGap + NODE_CONTENT_PADDING + NODE_BORDER_WIDTH
}

function truncateDisplay(value: string, width: number): string {
  if (promptOffsetWidth(value) <= width) return value
  if (width <= 0) return ""
  if (width === 1) return TRUNCATION_MARKER
  return `${displaySlice(value, 0, width - promptOffsetWidth(TRUNCATION_MARKER))}${TRUNCATION_MARKER}`
}

function fitNodeDisplay(label: string, meta: string, availableWidth: number) {
  if (!Number.isFinite(availableWidth)) return { displayLabel: label, displayMeta: meta }
  if (nodeWidth(label, meta) <= availableWidth) return { displayLabel: label, displayMeta: meta }
  if (nodeWidth(label, "") <= availableWidth) return { displayLabel: label, displayMeta: "" }

  const labelWidth = availableWidth - NODE_MARKER_WIDTH - NODE_CONTENT_PADDING - NODE_BORDER_WIDTH
  return { displayLabel: truncateDisplay(label, labelWidth), displayMeta: "" }
}

function makeNode(
  kind: "upstream" | "worker" | "session",
  rawId: string,
  label: string,
  meta: string,
  data: WorkerSummary | RedactedUpstream | HostedSessionSummary,
  availableWidth: number,
): TopologyNode {
  const { displayLabel, displayMeta } = fitNodeDisplay(label, meta, availableWidth)
  return {
    id: `${kind}:${rawId}`,
    kind,
    label,
    meta,
    displayLabel,
    displayMeta,
    width: Math.min(nodeWidth(displayLabel, displayMeta), availableWidth),
    height: NODE_HEIGHT,
    data,
  }
}

function nodeRowWidth(nodes: TopologyNode[]): number {
  return nodes.reduce((sum, node) => sum + node.width, 0) + COL_GAP * (nodes.length - 1)
}

function packNodes(nodes: TopologyNode[], availableWidth: number): TopologyNode[][] {
  const rows: TopologyNode[][] = []
  let current: TopologyNode[] = []
  let currentWidth = 0
  for (const node of nodes) {
    const nextWidth = current.length === 0 ? node.width : currentWidth + COL_GAP + node.width
    if (current.length > 0 && nextWidth > availableWidth) {
      rows.push(current)
      current = [node]
      currentWidth = node.width
      continue
    }
    current.push(node)
    currentWidth = nextWidth
  }
  if (current.length > 0) rows.push(current)
  return rows
}

function packGroups(groups: TopologyGroup[], availableWidth: number): TopologyGroupRow[] {
  const rows: TopologyGroupRow[] = []
  let current: TopologyGroup[] = []
  let currentWidth = 0
  for (const group of groups) {
    const nextWidth = current.length === 0 ? group.width : currentWidth + GROUP_GAP + group.width
    if (current.length > 0 && nextWidth > availableWidth) {
      rows.push({ groups: current, width: currentWidth })
      current = [group]
      currentWidth = group.width
      continue
    }
    current.push(group)
    currentWidth = nextWidth
  }
  if (current.length > 0) rows.push({ groups: current, width: currentWidth })
  return rows
}

type Group = {
  upstream: RedactedUpstream
  workers: WorkerSummary[]
}

function groupWorkers(workers: WorkerSummary[], upstreams: RedactedUpstream[]): Group[] {
  const upstreamByID = new Map(upstreams.map((upstream) => [upstream.id, upstream]))
  const map = new Map<string, Group>()
  for (const worker of workers) {
    const upstreamID = worker.upstream_id
    let group = map.get(upstreamID)
    if (!group) {
      group = { upstream: upstreamByID.get(upstreamID) ?? worker.upstream, workers: [] }
      map.set(upstreamID, group)
    }
    group.workers.push(worker)
  }
  return [...map.values()].sort((a, b) => a.upstream.name.localeCompare(b.upstream.name))
}

function orphanUpstreams(upstreams: RedactedUpstream[], groups: Group[]): RedactedUpstream[] {
  const used = new Set(groups.map((g) => g.upstream.id))
  return upstreams.filter((u) => !used.has(u.id))
}

export function computeLayout(
  workers: WorkerSummary[],
  upstreams: RedactedUpstream[],
  availableWidth = Number.POSITIVE_INFINITY,
  sessions: HostedSessionSummary[] = [],
): TopologyLayout {
  if (workers.length === 0 && upstreams.length === 0 && sessions.length === 0) {
    return { groups: [], groupRows: [], orphans: [], orphanRows: [], unboundSessions: [], unboundSessionRows: [], rows: 0 }
  }

  const sessionsByWorker = new Map<string, HostedSessionSummary[]>()
  const workerIDs = new Set(workers.map((worker) => worker.id))
  for (const session of sessions) {
    const workerID = session.worker_id ?? session.worker_name
    const items = sessionsByWorker.get(workerID) ?? []
    items.push(session)
    sessionsByWorker.set(workerID, items)
  }
  const rawGroups = groupWorkers(workers, upstreams)
  const orphans = orphanUpstreams(upstreams, rawGroups)
  const groups: TopologyGroup[] = rawGroups.map((group) => {
    const upstreamNode = makeNode("upstream", group.upstream.id, group.upstream.name, `${group.workers.length}`, group.upstream, availableWidth)
    const workerNodes = group.workers.map((w) => makeNode("worker", w.id, w.name, w.status, w, availableWidth))
    const workerRows = packNodes(workerNodes, availableWidth).map((row) => ({
      workers: row,
      width: nodeRowWidth(row),
    }))
    const topologySessions = Object.fromEntries(
      workerNodes.map((workerNode) => {
        const worker = workerNode.data as WorkerSummary
        const nodes = (sessionsByWorker.get(worker.id) ?? []).map((session) => {
          const node = makeNode("session", session.session_id, session.session_label, session.status, session, workerNode.width)
          return { ...node, width: workerNode.width }
        })
        return [worker.id, nodes]
      }),
    )
    const widestWorkerRow = workerRows.reduce((max, row) => Math.max(max, row.width), 0)
    return {
      upstream: upstreamNode,
      workers: workerNodes,
      workerRows,
      sessions: topologySessions,
      width: Math.max(upstreamNode.width, widestWorkerRow),
    }
  })

  const orphanNodes = orphans.map((u) => makeNode("upstream", u.id, u.name, "idle", u, availableWidth))
  const unboundSessions = sessions
    .filter((session) => !workerIDs.has(session.worker_id ?? session.worker_name))
    .map((session) => {
      const workerID = session.worker_id ?? session.worker_name
      return makeNode("session", session.session_id, session.session_label, `missing worker: ${workerID}`, session, availableWidth)
    })
  const groupRows = packGroups(groups, availableWidth)
  const orphanRows = packNodes(orphanNodes, availableWidth)
  const unboundSessionRows = packNodes(unboundSessions, availableWidth)
  const connectedRows = groupRows.reduce((sum, row) => {
    const rowHeight = Math.max(
      ...row.groups.map((group) =>
        group.workerRows.reduce((height, workerRow) => {
          const sessionCount = Math.max(...workerRow.workers.map((worker) => group.sessions[(worker.data as WorkerSummary).id].length), 0)
          return height + NODE_HEIGHT + 1 + sessionCount * (NODE_HEIGHT + 1)
        }, NODE_HEIGHT),
      ),
    )
    return sum + rowHeight
  }, 0)
  const orphanRowHeight = orphanRows.length * NODE_HEIGHT
  const unboundSessionRowHeight = unboundSessionRows.length * NODE_HEIGHT
  return {
    groups,
    groupRows,
    orphans: orphanNodes,
    orphanRows,
    unboundSessions,
    unboundSessionRows,
    rows: connectedRows + orphanRowHeight + unboundSessionRowHeight,
  }
}
