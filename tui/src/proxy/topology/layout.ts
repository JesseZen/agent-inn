import type { WorkerSummary, RedactedUpstream } from "../backend"

export type TopologyNode = {
  id: string
  kind: "upstream" | "worker"
  label: string
  width: number
  height: number
  data: WorkerSummary | RedactedUpstream
}

export type TopologyWorkerRow = {
  workers: TopologyNode[]
  width: number
}

export type TopologyGroup = {
  upstream: TopologyNode
  workers: TopologyNode[]
  workerRows: TopologyWorkerRow[]
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
  rows: number
}

const NODE_HEIGHT = 3
const NODE_PAD = 2
const COL_GAP = 2
const GROUP_GAP = 4

export const TOPOLOGY_GROUP_GAP = GROUP_GAP
export const TOPOLOGY_COL_GAP = COL_GAP
export const TOPOLOGY_NODE_HEIGHT = NODE_HEIGHT
export const TOPOLOGY_EDGE_ROWS = 1

function nodeWidth(label: string): number {
  return label.length + NODE_PAD + 2
}

function makeNode(kind: "upstream" | "worker", label: string, data: WorkerSummary | RedactedUpstream): TopologyNode {
  return {
    id: `${kind}:${label}`,
    kind,
    label,
    width: nodeWidth(label),
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

function groupWorkers(workers: WorkerSummary[]): Group[] {
  const map = new Map<string, Group>()
  for (const worker of workers) {
    const name = worker.upstream.name
    let group = map.get(name)
    if (!group) {
      group = { upstream: worker.upstream, workers: [] }
      map.set(name, group)
    }
    group.workers.push(worker)
  }
  return [...map.values()].sort((a, b) => a.upstream.name.localeCompare(b.upstream.name))
}

function orphanUpstreams(upstreams: RedactedUpstream[], groups: Group[]): RedactedUpstream[] {
  const used = new Set(groups.map((g) => g.upstream.name))
  return upstreams.filter((u) => !used.has(u.name))
}

export function computeLayout(
  workers: WorkerSummary[],
  upstreams: RedactedUpstream[],
  availableWidth = Number.POSITIVE_INFINITY,
): TopologyLayout {
  if (workers.length === 0 && upstreams.length === 0) {
    return { groups: [], groupRows: [], orphans: [], orphanRows: [], rows: 0 }
  }

  const rawGroups = groupWorkers(workers)
  const orphans = orphanUpstreams(upstreams, rawGroups)
  const groups: TopologyGroup[] = rawGroups.map((group) => {
    const upstreamNode = makeNode("upstream", group.upstream.name, group.upstream)
    const workerNodes = group.workers.map((w) => makeNode("worker", w.name, w))
    const groupAvailableWidth = Math.max(upstreamNode.width, availableWidth)
    const workerRows = packNodes(workerNodes, groupAvailableWidth).map((row) => ({
      workers: row,
      width: nodeRowWidth(row),
    }))
    const widestWorkerRow = workerRows.reduce((max, row) => Math.max(max, row.width), 0)
    return {
      upstream: upstreamNode,
      workers: workerNodes,
      workerRows,
      width: Math.max(upstreamNode.width, widestWorkerRow),
    }
  })

  const orphanNodes = orphans.map((u) => makeNode("upstream", u.name, u))
  const groupRows = packGroups(groups, availableWidth)
  const orphanRows = packNodes(orphanNodes, availableWidth)
  const connectedRows = groupRows.reduce((sum, row) => {
    const rowHeight = Math.max(...row.groups.map((group) => NODE_HEIGHT + group.workerRows.length * (NODE_HEIGHT + 1)))
    return sum + rowHeight
  }, 0)
  const orphanRowHeight = orphanRows.length * NODE_HEIGHT
  return { groups, groupRows, orphans: orphanNodes, orphanRows, rows: connectedRows + orphanRowHeight }
}
