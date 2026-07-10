import { expect, test } from "bun:test"
import { computeLayout, TOPOLOGY_COL_GAP } from "../src/proxy/topology/layout"
import { computeGroupEdges } from "../src/proxy/topology/edges"
import { isValidDrop, toDropPair, dropLabel } from "../src/proxy/topology/drag"
import { promptOffsetWidth } from "../src/prompt/display"
import type { HostedSessionSummary, WorkerSummary, RedactedUpstream } from "../src/proxy/backend"

function makeUpstream(name: string, hasKey = true): RedactedUpstream {
  return { id: name, name, base_url: `https://${name}.example.com/v1`, has_api_key: hasKey }
}

function makeWorker(name: string, upstream: RedactedUpstream, status = "running"): WorkerSummary {
  return { id: name, name, upstream_id: upstream.id, port: 10000, upstream, status, snapshot_generation: 1, log_level: "simple" }
}

function makeSession(workerID: string, sessionID = "hs_1", status: "active" | "stale" = "active"): HostedSessionSummary {
  return {
    session_id: sessionID,
    session_label: "plan",
    worker_id: workerID,
    worker_name: workerID,
    worker_port: 10000,
    created_at: "2026-07-10T00:00:00Z",
    last_opened_at: "2026-07-10T00:00:00Z",
    status,
  }
}

function sortCells(cells: Array<{ x: number; y: number; char: string }>) {
  return [...cells].sort((a, b) => a.y - b.y || a.x - b.x)
}

function findGroup(layout: ReturnType<typeof computeLayout>, upstreamName: string) {
  return layout.groups.find((g) => g.upstream.label === upstreamName)!
}

test("computeLayout returns empty for no workers and no upstreams", () => {
  expect(computeLayout([], [])).toEqual({ groups: [], groupRows: [], orphans: [], orphanRows: [], unboundSessions: [], unboundSessionRows: [], rows: 0 })
})

test("computeLayout places upstream above single worker", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])

  expect(layout.groups).toHaveLength(1)
  const group = layout.groups[0]
  expect(group.upstream.id).toBe("upstream:openai")
  expect(group.workers[0].id).toBe("worker:app")
  expect(group.upstream).toEqual({
    id: "upstream:openai",
    kind: "upstream",
    label: "openai",
    meta: "1",
    displayLabel: "openai",
    displayMeta: "1",
    width: 14,
    height: 3,
    data: upstream,
  })
  expect(group.workers).toEqual([
    {
      id: "worker:app",
      kind: "worker",
      label: "app",
      meta: "running",
      displayLabel: "app",
      displayMeta: "running",
      width: 17,
      height: 3,
      data: worker,
    },
  ])
  expect(group.workerRows).toEqual([
    {
      workers: [
        {
          id: "worker:app",
          kind: "worker",
          label: "app",
          meta: "running",
          displayLabel: "app",
          displayMeta: "running",
          width: 17,
          height: 3,
          data: worker,
        },
      ],
      width: 17,
    },
  ])
  // group width = max(upstream width 14, worker width 17) = 17
  expect(group.width).toBe(17)
  expect(layout.groupRows).toEqual([{ groups: [group], width: 17 }])
  expect(layout.orphans).toEqual([])
  expect(layout.orphanRows).toEqual([])
})

test("computeLayout uses stable ids while displaying names", () => {
  const upstream = { ...makeUpstream("openai"), id: "upstream-openai", name: "OpenAI Main" }
  const worker = { ...makeWorker("Codex Main", upstream), id: "worker-app", upstream_id: "upstream-openai" }
  const layout = computeLayout([worker], [upstream])
  expect(layout.groups[0].upstream.id).toBe("upstream:upstream-openai")
  expect(layout.groups[0].upstream.label).toBe("OpenAI Main")
  expect(layout.groups[0].workers[0].id).toBe("worker:worker-app")
  expect(layout.groups[0].workers[0].label).toBe("Codex Main")
})

test("computeLayout attaches sessions to workers by stable id", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const session = makeSession("app")
  const layout = computeLayout([worker], [upstream], Number.POSITIVE_INFINITY, [session])

  expect({ sessions: layout.groups[0].sessions, unboundSessions: layout.unboundSessions, unboundSessionRows: layout.unboundSessionRows }).toEqual({
    sessions: {
      app: [
        {
          id: "session:hs_1",
          kind: "session",
          label: "plan",
          meta: "active",
          displayLabel: "plan",
          displayMeta: "active",
          width: 17,
          height: 3,
          data: session,
        },
      ],
    },
    unboundSessions: [],
    unboundSessionRows: [],
  })
})

test("computeLayout keeps sessions whose worker is missing in the unbound section", () => {
  const session = {
    ...makeSession("vanished", "hs_2", "stale"),
    session_label: "orphan plan",
    worker: { id: "vanished", name: "vanished", missing: true },
  }
  const layout = computeLayout([], [], Number.POSITIVE_INFINITY, [session])

  expect({ groups: layout.groups, unboundSessions: layout.unboundSessions, unboundSessionRows: layout.unboundSessionRows }).toEqual({
    groups: [],
    unboundSessions: [
      {
        id: "session:hs_2",
        kind: "session",
        label: "orphan plan",
        meta: "missing worker: vanished",
        displayLabel: "orphan plan",
        displayMeta: "missing worker: vanished",
        width: 42,
        height: 3,
        data: session,
      },
    ],
    unboundSessionRows: [layout.unboundSessions],
  })
})

test("computeLayout groups missing upstream workers by stable upstream id", () => {
  const missingUpstream = { ...makeUpstream("Missing Display"), id: "openai", missing: true }
  const worker = { ...makeWorker("app", missingUpstream), upstream_id: "openai" }
  const layout = computeLayout([worker], [])

  expect(layout.groups).toHaveLength(1)
  expect(layout.groups[0].upstream.id).toBe("upstream:openai")
  expect(layout.groups[0].upstream.label).toBe("Missing Display")
  expect(layout.groups[0].workers[0].id).toBe("worker:app")
})

test("computeLayout groups workers through upstream_id over stale embedded upstream summaries", () => {
  const upstream = { ...makeUpstream("openai"), id: "canonical-openai", name: "OpenAI Current" }
  const staleUpstream = { ...makeUpstream("openai"), id: "stale-openai", name: "OpenAI Stale" }
  const worker = { ...makeWorker("app", staleUpstream), upstream_id: "canonical-openai" }
  const layout = computeLayout([worker], [upstream])

  expect(layout.groups[0].upstream).toEqual({
    id: "upstream:canonical-openai",
    kind: "upstream",
    label: "OpenAI Current",
    meta: "1",
    displayLabel: "OpenAI Current",
    displayMeta: "1",
    width: 22,
    height: 3,
    data: upstream,
  })
  expect(layout.orphans).toEqual([])
})

test("computeLayout sets group width to fit multiple workers", () => {
  const upstream = makeUpstream("ab")
  const w1 = makeWorker("app", upstream)
  const w2 = makeWorker("cli-openrouter", upstream)
  const layout = computeLayout([w1, w2], [upstream])

  const group = layout.groups[0]
  // workers total = 17 + 28 + 2 (COL_GAP) = 47; upstream width = 10
  expect(group.width).toBe(47)
  expect(group.workerRows).toEqual([{ workers: group.workers, width: 47 }])
})

test("computeLayout places multiple upstream groups side by side", () => {
  const up1 = makeUpstream("aaa")
  const up2 = makeUpstream("zzz")
  const w1 = makeWorker("w1", up1)
  const w2 = makeWorker("w2", up2)
  const layout = computeLayout([w1, w2], [up1, up2])

  expect(layout.groups).toHaveLength(2)
  expect(layout.groups[0].upstream.label).toBe("aaa")
  expect(layout.groups[1].upstream.label).toBe("zzz")
})

test("computeLayout shows orphan upstreams without workers", () => {
  const usedUp = makeUpstream("openai")
  const orphanUp = makeUpstream("orphan")
  const worker = makeWorker("app", usedUp)
  const layout = computeLayout([worker], [usedUp, orphanUp])

  expect(layout.orphans).toEqual([
    {
      id: "upstream:orphan",
      kind: "upstream",
      label: "orphan",
      meta: "idle",
      displayLabel: "orphan",
      displayMeta: "idle",
      width: 17,
      height: 3,
      data: orphanUp,
    },
  ])
  expect(layout.orphanRows).toEqual([layout.orphans])
})

test("computeLayout handles worker whose upstream is not in upstreams list", () => {
  const embeddedUp = makeUpstream("embedded")
  const worker = makeWorker("app", embeddedUp)
  const layout = computeLayout([worker], [])

  expect(layout.groups).toHaveLength(1)
  expect(layout.groups[0].upstream.label).toBe("embedded")
})

test("computeLayout wraps groups into rows when width is constrained", () => {
  const up1 = makeUpstream("aaa")
  const up2 = makeUpstream("bbb")
  const up3 = makeUpstream("ccc")
  const w1 = makeWorker("worker-one", up1)
  const w2 = makeWorker("worker-two", up2)
  const w3 = makeWorker("worker-three", up3)

  const layout = computeLayout([w1, w2, w3], [up1, up2, up3], 28)

  expect(layout.groupRows.map((row) => row.groups.map((group) => group.upstream.label))).toEqual([
    ["aaa"],
    ["bbb"],
    ["ccc"],
  ])
})

test("computeLayout keeps groups in one row when width allows", () => {
  const up1 = makeUpstream("aaa")
  const up2 = makeUpstream("bbb")
  const w1 = makeWorker("w1", up1)
  const w2 = makeWorker("w2", up2)

  const layout = computeLayout([w1, w2], [up1, up2], 80)

  expect(layout.groupRows.map((row) => row.groups.map((group) => group.upstream.label))).toEqual([["aaa", "bbb"]])
})

test("computeLayout wraps groups using final rendered node widths", () => {
  const up1 = makeUpstream("aaa")
  const up2 = makeUpstream("bbb")
  const w1 = makeWorker("w1", up1, "running")
  const w2 = makeWorker("w2", up2, "running")

  const layout = computeLayout([w1, w2], [up1, up2], 25)

  expect(layout.groupRows.map((row) => row.groups.map((group) => group.upstream.label))).toEqual([["aaa"], ["bbb"]])
})

test("computeLayout wraps workers inside an oversized group", () => {
  const upstream = makeUpstream("shared")
  const w1 = makeWorker("alpha-worker", upstream)
  const w2 = makeWorker("beta-worker", upstream)
  const w3 = makeWorker("gamma-worker", upstream)

  const layout = computeLayout([w1, w2, w3], [upstream], 26)
  const group = findGroup(layout, "shared")

  expect(group.workerRows.map((row) => row.workers.map((worker) => worker.label))).toEqual([
    ["alpha-worker"],
    ["beta-worker"],
    ["gamma-worker"],
  ])
  expect(group.width).toBeLessThanOrEqual(26)
})

test("computeLayout bounds oversized node display fields to available width", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("worker-with-a-name-that-is-too-wide-for-the-dialog", upstream)

  const layout = computeLayout([worker], [upstream], 24)
  const group = findGroup(layout, "openai")
  const node = group.workers[0]

  expect(node.label).toBe("worker-with-a-name-that-is-too-wide-for-the-dialog")
  expect(node.data).toBe(worker)
  expect(node.width).toBeLessThanOrEqual(24)
  expect(node.displayMeta).toBe("")
  expect(node.displayLabel.endsWith("…")).toBe(true)
  expect(promptOffsetWidth(node.displayLabel)).toBeLessThan(promptOffsetWidth(node.label))
  expect(group.width).toBeLessThanOrEqual(24)
  expect(group.workerRows[0].width).toBeLessThanOrEqual(24)
})

test("computeLayout uses terminal display width for full-width names", () => {
  const upstream = makeUpstream("wide")
  const w1 = makeWorker("界界界界界界", upstream)
  const w2 = makeWorker("ASCII", upstream)

  const layout = computeLayout([w1, w2], [upstream], 36)
  const group = findGroup(layout, "wide")

  expect(group.workers.map((node) => ({
    label: node.label,
    displayLabel: node.displayLabel,
    width: node.width,
  }))).toEqual([
    { label: "界界界界界界", displayLabel: "界界界界界界", width: 26 },
    { label: "ASCII", displayLabel: "ASCII", width: 19 },
  ])
  expect(group.workerRows.map((row) => row.workers.map((worker) => worker.label))).toEqual([["界界界界界界"], ["ASCII"]])
})

test("computeLayout packs orphan upstreams into rows", () => {
  const orphanA = makeUpstream("orphan-a")
  const orphanB = makeUpstream("orphan-b")
  const orphanC = makeUpstream("orphan-c")

  const layout = computeLayout([], [orphanA, orphanB, orphanC], 24)

  expect(layout.orphanRows.map((row) => row.map((node) => node.label))).toEqual([
    ["orphan-a"],
    ["orphan-b"],
    ["orphan-c"],
  ])
})

test("computeLayout is deterministic for same input", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const a = computeLayout([worker], [upstream])
  const b = computeLayout([worker], [upstream])
  expect(a).toEqual(b)
})

test("computeGroupEdges connects same-column worker with vertical line", () => {
  const upstream = makeUpstream("ab")
  const worker = makeWorker("ab", upstream)
  const layout = computeLayout([worker], [upstream])
  const group = findGroup(layout, "ab")
  // group width = 16, upstream center = 8, worker center = 8 -> vertical line
  const edges = computeGroupEdges(group, group.workerRows[0])
  expect(sortCells(edges.cells)).toEqual([{ x: 8, y: 0, char: "│" }])
})

test("computeGroupEdges creates branch when worker is off-center", () => {
  const upstream = makeUpstream("openrouter")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const group = findGroup(layout, "openrouter")
  // group width = 18, upstream center = 9, worker center = 8 -> branch
  const edges = computeGroupEdges(group, group.workerRows[0])
  expect(sortCells(edges.cells)).toEqual([
    { x: 8, y: 0, char: "┌" },
    { x: 9, y: 0, char: "┘" },
  ])
})

test("computeGroupEdges merges shared upstream branch with T-junction", () => {
  const upstream = makeUpstream("openai")
  const w1 = makeWorker("app", upstream)
  const w2 = makeWorker("cli-long-name", upstream)
  const layout = computeLayout([w1, w2], [upstream])
  const group = findGroup(layout, "openai")

  const edges = computeGroupEdges(group, group.workerRows[0])
  const cellMap = new Map(edges.cells.map((c) => [c.x, c.char]))

  // group width = max(14, 17+27+2) = 46
  // upstream center = 23
  // worker centers: w1 (app, width 17) at start 0, center 8; w2 at start 19, center 32
  // T-junction at upstream center: up + left + right = ┴
  expect(cellMap.get(23)).toBe("┴")
  // w1 corner at x=8: down + right = ┌
  expect(cellMap.get(8)).toBe("┌")
  // w2 corner at x=32: down + left = ┐
  expect(cellMap.get(32)).toBe("┐")
  // between w1 and upstream center: ─
  for (let x = 9; x < 23; x++) {
    expect(cellMap.get(x)).toBe("─")
  }
  // between upstream center and w2: ─
  for (let x = 24; x < 32; x++) {
    expect(cellMap.get(x)).toBe("─")
  }
})

test("computeGroupEdges returns empty for group with no workers", () => {
  const orphan = makeUpstream("orphan")
  const layout = computeLayout([], [orphan])
  // orphans don't have groups; we test with a synthetic group instead
  const syntheticGroup = {
    upstream: {
      id: "upstream:orphan",
      kind: "upstream" as const,
      label: "orphan",
      meta: "idle",
      displayLabel: "orphan",
      displayMeta: "idle",
      width: 17,
      height: 3,
      data: orphan,
    },
    workers: [],
    workerRows: [{ workers: [], width: 0 }],
    sessions: {},
    width: 10,
  }
  expect(computeGroupEdges(syntheticGroup, syntheticGroup.workerRows[0]).cells).toEqual([])
})

test("computeGroupEdges is deterministic for same input", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const group = findGroup(layout, "openai")
  const a = computeGroupEdges(group, group.workerRows[0])
  const b = computeGroupEdges(group, group.workerRows[0])
  expect(a).toEqual(b)
})

test("computeGroupEdges uses centers from the selected worker row", () => {
  const upstream = makeUpstream("shared")
  const w1 = makeWorker("alpha-worker", upstream)
  const w2 = makeWorker("beta-worker", upstream)
  const layout = computeLayout([w1, w2], [upstream], 26)
  const group = findGroup(layout, "shared")

  const first = computeGroupEdges(group, group.workerRows[0])
  const second = computeGroupEdges(group, group.workerRows[1])

  expect(first).toEqual({ cells: [{ x: 13, y: 0, char: "│" }] })
  expect(sortCells(second.cells)).toEqual([
    { x: 12, y: 0, char: "┌" },
    { x: 13, y: 0, char: "┘" },
  ])
})

test("computeGroupEdges centers workers using topology column gap", () => {
  const upstream = makeUpstream("shared")
  const w1 = makeWorker("alpha", upstream)
  const w2 = makeWorker("beta", upstream)
  const layout = computeLayout([w1, w2], [upstream])
  const group = findGroup(layout, "shared")

  const edges = computeGroupEdges(group, group.workerRows[0])
  const cellMap = new Map(edges.cells.map((c) => [c.x, c.char]))
  const firstCenter = Math.floor(group.workers[0].width / 2)
  const secondCenter = group.workers[0].width + TOPOLOGY_COL_GAP + Math.floor(group.workers[1].width / 2)

  expect(cellMap.get(firstCenter)).toBe("┌")
  expect(cellMap.get(secondCenter)).toBe("┐")
})

test("isValidDrop accepts worker↔upstream, rejects same kind or same node", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const upstreamNode = layout.groups[0].upstream
  const workerNode = layout.groups[0].workers[0]

  expect(isValidDrop(workerNode, upstreamNode)).toBe(true)
  expect(isValidDrop(upstreamNode, workerNode)).toBe(true)
  expect(isValidDrop(workerNode, workerNode)).toBe(false)
  expect(isValidDrop(upstreamNode, upstreamNode)).toBe(false)
})

test("isValidDrop accepts non-running session to worker and rejects running session", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const readySession = makeSession("app", "hs_1")
  const runningSession = { ...makeSession("app", "hs_2"), turn_state: "running" as const }
  const readyLayout = computeLayout([worker], [upstream], Number.POSITIVE_INFINITY, [readySession])
  const runningLayout = computeLayout([worker], [upstream], Number.POSITIVE_INFINITY, [runningSession])
  const workerNode = readyLayout.groups[0].workers[0]

  expect({
    ready: isValidDrop(readyLayout.groups[0].sessions.app[0], workerNode),
    reversed: isValidDrop(workerNode, readyLayout.groups[0].sessions.app[0]),
    running: isValidDrop(runningLayout.groups[0].sessions.app[0], workerNode),
  }).toEqual({ ready: true, reversed: false, running: false })
})

test("toDropPair identifies worker and upstream roles regardless of source order", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const upstreamNode = layout.groups[0].upstream
  const workerNode = layout.groups[0].workers[0]

  const fromWorker = toDropPair(workerNode, upstreamNode)
  expect(fromWorker.worker).toBe(workerNode)
  expect(fromWorker.upstream).toBe(upstreamNode)

  const fromUpstream = toDropPair(upstreamNode, workerNode)
  expect(fromUpstream.worker).toBe(workerNode)
  expect(fromUpstream.upstream).toBe(upstreamNode)
})

test("dropLabel formats with target or placeholder question mark", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const upstreamNode = layout.groups[0].upstream
  const workerNode = layout.groups[0].workers[0]

  expect(dropLabel(workerNode, upstreamNode)).toBe("app → openai")
  expect(dropLabel(workerNode, null)).toBe("app → ?")
})
