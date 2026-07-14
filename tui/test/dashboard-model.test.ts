import { expect, test } from "bun:test"
import { buildDashboardModel, fitDashboardText } from "../src/proxy/dashboard/model"
import {
  dashboardCollapse,
  dashboardExpand,
  dashboardInitialState,
  dashboardVisibleRows,
  moveDashboardSelection,
  scrollDashboardRowIntoView,
} from "../src/proxy/dashboard/navigation"
import { dashboardDropLabel, dashboardDropPair, isValidDashboardDrop } from "../src/proxy/dashboard/drag"
import type { RedactedUpstream, UpstreamPool, WorkerSummary } from "../src/proxy/backend"
import type { HostedSessionSnapshot } from "../src/proxy/hosted-session-contract"
import { hostedSessionMarker } from "../src/proxy/hosted-session-presentation"

const openai: RedactedUpstream = {
  id: "upstream-openai",
  name: "OpenAI Main",
  base_url: "https://api.openai.com/v1",
  has_api_key: true,
}
const worker: WorkerSummary = {
  id: "worker-app",
  name: "App Main",
  upstream_id: openai.id,
  port: 6767,
  upstream: openai,
  status: "running",
  snapshot_generation: 1,
  log_level: "simple",
}
const session: HostedSessionSnapshot = {
  session_id: "hs_1",
  session_label: "Build release",
  worker: { id: worker.id, name: worker.name, port: worker.port, missing: false },
  workspace: "",
  model: "",
  add_dirs: [],
  user_marker: "",
  turn: { state: "running", reason: "", unread: false, needs_input: false },
  created_at: "2026-07-11T00:00:00Z",
  last_opened_at: "2026-07-11T00:00:00Z",
  status: "active",
}

test("hosted session marker preserves waiting unread and todo priority", () => {
  const marker = (turn: HostedSessionSnapshot["turn"], user_marker: HostedSessionSnapshot["user_marker"] = "") =>
    hostedSessionMarker({ ...session, turn, user_marker })

  expect([
    marker({ state: "running", reason: "", unread: false, needs_input: true }, "todo"),
    marker({ state: "running", reason: "", unread: false, needs_input: false }, "todo"),
    marker({ state: "done", reason: "", unread: true, needs_input: false }, "todo"),
    marker({ state: "failed", reason: "", unread: true, needs_input: false }, "todo"),
    marker({ state: "done", reason: "", unread: false, needs_input: false }, "todo"),
    marker({ state: "done", reason: "", unread: false, needs_input: false }),
    marker({ state: "interrupted", reason: "", unread: false, needs_input: false }),
    marker({ state: "idle", reason: "", unread: false, needs_input: false }),
  ]).toEqual([
    { symbol: "?", tone: "warning", bold: true },
    { symbol: "*", tone: "primary", bold: true },
    { symbol: "+", tone: "success", bold: true },
    { symbol: "!", tone: "error", bold: true },
    { symbol: "~", tone: "warning", bold: false },
    { symbol: "+", tone: "muted", bold: false },
    { symbol: "!", tone: "muted", bold: false },
    { symbol: ":", tone: "muted", bold: false },
  ])
})

test("buildDashboardModel preserves stable hierarchy and computes complete summary", () => {
  expect(buildDashboardModel([worker], [openai], [session])).toEqual({
    summary: {
      pools: 0,
      upstreams: 1,
      healthyWorkers: 1,
      workers: 1,
      sessions: 1,
      unbound: 0,
    },
    domains: [
      {
        kind: "upstream",
        id: "upstream:upstream-openai",
        upstream: {
          id: "upstream:upstream-openai",
          kind: "upstream",
          label: "OpenAI Main",
          data: openai,
        },
        workers: [
          {
            worker: {
              id: "worker:worker-app",
              kind: "worker",
              label: "App Main",
              data: worker,
            },
            sessions: [
              {
                id: "session:hs_1",
                kind: "session",
                label: "Build release",
                data: session,
              },
            ],
          },
        ],
        healthyWorkers: 1,
        totalWorkers: 1,
        totalSessions: 1,
        warning: false,
      },
    ],
    unboundUpstreams: [],
    unboundSessions: [],
  })
})

test("buildDashboardModel nests pool workers under their active member and keeps inactive members", () => {
  const fallback = { ...openai, id: "upstream-fallback", name: "Fallback" }
  const pool: UpstreamPool = {
    id: "primary-pool",
    name: "Primary Pool",
    mode: "active",
    probe: { stable_interval_seconds: 900, alert_interval_seconds: 60 },
    probe_state: "stable",
    upstreams: [openai.id, fallback.id],
    circuit_breaker: {
      failure_threshold: 3,
      recovery_success_threshold: 2,
      recovery_wait_seconds: 60,
    },
    active_upstream: openai.id,
    workers: [worker.name],
    readiness: [],
  }
  const pooledWorker = { ...worker, upstream_pool: pool.id }
  const model = buildDashboardModel([pooledWorker], [openai, fallback], [session], [pool])
  const state = dashboardInitialState(model)

  expect({
    summary: model.summary,
    domain: model.domains[0],
    rows: dashboardVisibleRows(model, state),
  }).toEqual({
    summary: {
      pools: 1,
      upstreams: 2,
      healthyWorkers: 1,
      workers: 1,
      sessions: 1,
      unbound: 0,
    },
    domain: {
      kind: "pool",
      id: "pool:primary-pool",
      pool: {
        id: "pool:primary-pool",
        kind: "pool",
        label: "Primary Pool",
        data: pool,
      },
      members: [
        {
          upstream: {
            id: "pool-member:primary-pool:upstream-openai",
            kind: "upstream",
            label: "OpenAI Main",
            data: openai,
          },
          workers: [
            {
              worker: {
                id: "worker:worker-app",
                kind: "worker",
                label: "App Main",
                data: pooledWorker,
              },
              sessions: [
                {
                  id: "session:hs_1",
                  kind: "session",
                  label: "Build release",
                  data: session,
                },
              ],
            },
          ],
          active: true,
        },
        {
          upstream: {
            id: "pool-member:primary-pool:upstream-fallback",
            kind: "upstream",
            label: "Fallback",
            data: fallback,
          },
          workers: [],
          active: false,
        },
      ],
      workers: [
        {
          worker: {
            id: "worker:worker-app",
            kind: "worker",
            label: "App Main",
            data: pooledWorker,
          },
          sessions: [
            {
              id: "session:hs_1",
              kind: "session",
              label: "Build release",
              data: session,
            },
          ],
        },
      ],
      healthyWorkers: 1,
      totalWorkers: 1,
      totalSessions: 1,
      warning: false,
    },
    rows: [
      {
        id: "pool:primary-pool",
        kind: "domain",
        depth: 0,
        node: model.domains[0].kind === "pool" ? model.domains[0].pool : undefined,
        expandable: true,
      },
      {
        id: "pool-member:primary-pool:upstream-openai",
        kind: "upstream",
        depth: 1,
        parentID: "pool:primary-pool",
        node: model.domains[0].kind === "pool" ? model.domains[0].members[0].upstream : undefined,
        expandable: true,
        active: true,
      },
      {
        id: "worker:worker-app",
        kind: "worker",
        depth: 2,
        parentID: "pool-member:primary-pool:upstream-openai",
        node: model.domains[0].workers[0].worker,
        expandable: true,
      },
      {
        id: "session:hs_1",
        kind: "session",
        depth: 3,
        parentID: "worker:worker-app",
        node: model.domains[0].workers[0].sessions[0],
        expandable: false,
      },
      {
        id: "pool-member:primary-pool:upstream-fallback",
        kind: "upstream",
        depth: 1,
        parentID: "pool:primary-pool",
        node: model.domains[0].kind === "pool" ? model.domains[0].members[1].upstream : undefined,
        expandable: false,
        active: false,
      },
    ],
  })
})

test("fitDashboardText preserves metadata until the label needs truncation", () => {
  expect(fitDashboardText("worker-with-a-long-name", "running", 20)).toEqual({
    label: "worker-with-a…",
    meta: "",
  })
})

test("buildDashboardModel keeps missing relationships visible and counts each unbound record", () => {
  const orphan: RedactedUpstream = {
    id: "upstream-orphan",
    name: "Unused",
    has_api_key: false,
  }
  const missingUpstream: RedactedUpstream = {
    id: "upstream-missing",
    name: "Removed provider",
    has_api_key: false,
    missing: true,
  }
  const missingWorker: WorkerSummary = {
    ...worker,
    id: "worker-missing-provider",
    name: "Stranded worker",
    upstream_id: missingUpstream.id,
    upstream: missingUpstream,
    status: "failed",
  }
  const unboundSession: HostedSessionSnapshot = {
    ...session,
    session_id: "hs_orphan",
    session_label: "Lost session",
    worker: { id: "worker-removed", name: "Removed worker", port: worker.port, missing: true },
    turn: { state: "idle", reason: "", unread: false, needs_input: false },
    status: "stale",
  }

  expect(buildDashboardModel([missingWorker], [orphan], [unboundSession])).toEqual({
    summary: {
      pools: 0,
      upstreams: 2,
      healthyWorkers: 0,
      workers: 1,
      sessions: 1,
      unbound: 2,
    },
    domains: [
      {
        kind: "upstream",
        id: "upstream:upstream-missing",
        upstream: {
          id: "upstream:upstream-missing",
          kind: "upstream",
          label: "Removed provider",
          data: missingUpstream,
        },
        workers: [
          {
            worker: {
              id: "worker:worker-missing-provider",
              kind: "worker",
              label: "Stranded worker",
              data: missingWorker,
            },
            sessions: [],
          },
        ],
        healthyWorkers: 0,
        totalWorkers: 1,
        totalSessions: 0,
        warning: true,
      },
    ],
    unboundUpstreams: [
      {
        id: "upstream:upstream-orphan",
        kind: "upstream",
        label: "Unused",
        data: orphan,
      },
    ],
    unboundSessions: [
      {
        id: "session:hs_orphan",
        kind: "session",
        label: "Lost session",
        data: unboundSession,
      },
    ],
  })
})

test("dashboardInitialState expands active and warning domains but leaves quiet domains collapsed", () => {
  const quietUpstream = { ...openai, id: "upstream-quiet", name: "Quiet" }
  const quietWorker = {
    ...worker,
    id: "worker-quiet",
    name: "Quiet worker",
    upstream_id: quietUpstream.id,
    upstream: quietUpstream,
  }
  const failedWorker = {
    ...worker,
    id: "worker-failed",
    name: "Failed worker",
    status: "failed",
  }
  const model = buildDashboardModel([worker, failedWorker, quietWorker], [openai, quietUpstream], [session])

  expect(dashboardInitialState(model)).toEqual({
    expandedDomains: new Set(["upstream:upstream-openai"]),
    expandedUpstreams: new Set(),
    expandedSessionGroups: new Set(),
    collapsedSessionGroups: new Set(),
    unboundExpanded: false,
    query: "",
    selectedID: "upstream:upstream-openai",
  })
})

test("dashboardVisibleRows derives hierarchy, filtered paths, previews, and unbound children", () => {
  const sessions = Array.from({ length: 5 }, (_, index): HostedSessionSnapshot => ({
    ...session,
    session_id: `hs_${index + 1}`,
    session_label: index === 4 ? "ZZ release target" : `Task ${index + 1}`,
    turn: { state: "idle", reason: "", unread: false, needs_input: false },
  }))
  const orphanUpstream = { ...openai, id: "upstream-unused", name: "Unused" }
  const orphanSession = {
    ...session,
    session_id: "hs_orphan",
    session_label: "Detached",
    worker: { id: "gone", name: "gone", port: worker.port, missing: true },
  }
  const model = buildDashboardModel([worker], [openai, orphanUpstream], [...sessions, orphanSession])
  const state = {
    expandedDomains: new Set(["upstream:upstream-openai"]),
    expandedUpstreams: new Set<string>(),
    expandedSessionGroups: new Set<string>(),
    collapsedSessionGroups: new Set<string>(),
    unboundExpanded: false,
    query: "",
    selectedID: "worker:worker-app",
  }

  expect(dashboardVisibleRows(model, state)).toEqual([
    {
      id: "upstream:upstream-openai",
      kind: "domain",
      depth: 0,
      node: model.domains[0].kind === "upstream" ? model.domains[0].upstream : undefined,
      expandable: true,
    },
    {
      id: "worker:worker-app",
      kind: "worker",
      depth: 1,
      parentID: "upstream:upstream-openai",
      node: model.domains[0].workers[0].worker,
      expandable: true,
    },
    {
      id: "session:hs_1",
      kind: "session",
      depth: 2,
      parentID: "worker:worker-app",
      node: model.domains[0].workers[0].sessions[0],
      expandable: false,
    },
    {
      id: "session:hs_2",
      kind: "session",
      depth: 2,
      parentID: "worker:worker-app",
      node: model.domains[0].workers[0].sessions[1],
      expandable: false,
    },
    {
      id: "session:hs_3",
      kind: "session",
      depth: 2,
      parentID: "worker:worker-app",
      node: model.domains[0].workers[0].sessions[2],
      expandable: false,
    },
    {
      id: "session-more:worker:worker-app",
      kind: "session-more",
      depth: 2,
      parentID: "worker:worker-app",
      expandable: true,
      count: 2,
    },
    { id: "unbound", kind: "unbound", depth: 0, expandable: true, count: 2 },
  ])

  expect(
    dashboardVisibleRows(model, {
      ...state,
      collapsedSessionGroups: new Set(["worker:worker-app"]),
      query: "release",
    }),
  ).toEqual([
    {
      id: "upstream:upstream-openai",
      kind: "domain",
      depth: 0,
      node: model.domains[0].kind === "upstream" ? model.domains[0].upstream : undefined,
      expandable: true,
    },
    {
      id: "worker:worker-app",
      kind: "worker",
      depth: 1,
      parentID: "upstream:upstream-openai",
      node: model.domains[0].workers[0].worker,
      expandable: true,
    },
    {
      id: "session:hs_5",
      kind: "session",
      depth: 2,
      parentID: "worker:worker-app",
      node: model.domains[0].workers[0].sessions[4],
      expandable: false,
    },
  ])

  expect(dashboardVisibleRows(model, { ...state, unboundExpanded: true }).slice(-3)).toEqual([
    { id: "unbound", kind: "unbound", depth: 0, expandable: true, count: 2 },
    {
      id: "upstream:upstream-unused",
      kind: "domain",
      depth: 1,
      parentID: "unbound",
      node: model.unboundUpstreams[0],
      expandable: false,
    },
    {
      id: "session:hs_orphan",
      kind: "session",
      depth: 1,
      parentID: "unbound",
      node: model.unboundSessions[0],
      expandable: false,
    },
  ])

  expect(
    dashboardVisibleRows(model, {
      ...state,
      collapsedSessionGroups: new Set(["worker:worker-app"]),
    }),
  ).toEqual([
    {
      id: "upstream:upstream-openai",
      kind: "domain",
      depth: 0,
      node: model.domains[0].kind === "upstream" ? model.domains[0].upstream : undefined,
      expandable: true,
    },
    {
      id: "worker:worker-app",
      kind: "worker",
      depth: 1,
      parentID: "upstream:upstream-openai",
      node: model.domains[0].workers[0].worker,
      expandable: true,
    },
    { id: "unbound", kind: "unbound", depth: 0, expandable: true, count: 2 },
  ])
})

test("dashboard navigation wraps and applies parent, collapse, and expansion transitions", () => {
  const model = buildDashboardModel([worker], [openai], [session])
  const state = {
    expandedDomains: new Set(["upstream:upstream-openai"]),
    expandedUpstreams: new Set<string>(),
    expandedSessionGroups: new Set<string>(),
    collapsedSessionGroups: new Set<string>(),
    unboundExpanded: false,
    query: "",
    selectedID: "worker:worker-app",
  }
  const rows = dashboardVisibleRows(model, state)

  expect({
    downWrap: moveDashboardSelection(rows, rows.at(-1)!.id, 1),
    upWrap: moveDashboardSelection(rows, rows[0].id, -1),
    missing: moveDashboardSelection(rows, "worker:removed", 1),
    childLeft: dashboardCollapse(rows, {
      ...state,
      selectedID: "session:hs_1",
    }),
    workerLeft: dashboardCollapse(rows, state),
    workerRight: dashboardExpand(rows, state),
    domainLeft: dashboardCollapse(rows, {
      ...state,
      selectedID: "upstream:upstream-openai",
    }),
    domainRight: dashboardExpand(rows, {
      ...state,
      selectedID: "upstream:upstream-openai",
    }),
  }).toEqual({
    downWrap: "upstream:upstream-openai",
    upWrap: "session:hs_1",
    missing: "upstream:upstream-openai",
    childLeft: { selectedID: "worker:worker-app" },
    workerLeft: {
      selectedID: "worker:worker-app",
      collapseSessionGroupID: "worker:worker-app",
    },
    workerRight: { selectedID: "session:hs_1" },
    domainLeft: {
      selectedID: "upstream:upstream-openai",
      collapseID: "upstream:upstream-openai",
    },
    domainRight: { selectedID: "worker:worker-app" },
  })

  const collapsedState = {
    ...state,
    expandedDomains: new Set<string>(),
    selectedID: "upstream:upstream-openai",
  }
  expect(dashboardExpand(dashboardVisibleRows(model, collapsedState), collapsedState)).toEqual({
    selectedID: "upstream:upstream-openai",
    expandID: "upstream:upstream-openai",
  })

  const collapsedWorkerState = {
    ...state,
    collapsedSessionGroups: new Set(["worker:worker-app"]),
  }
  expect(dashboardExpand(dashboardVisibleRows(model, collapsedWorkerState), collapsedWorkerState)).toEqual({
    selectedID: "worker:worker-app",
    expandSessionGroupID: "worker:worker-app",
  })
})

test("scrollDashboardRowIntoView moves only far enough to reveal the selected row", () => {
  const calls: number[] = []
  const scroll = {
    scrollTop: 2,
    viewport: { height: 4 },
    scrollTo(value: number) {
      calls.push(value)
    },
  }
  scrollDashboardRowIntoView(scroll, 7)
  expect(calls).toEqual([4])
})

test("dashboard drag semantics preserve worker, upstream, and session constraints", () => {
  const readySession = { ...session, turn: { state: "idle" as const, reason: "", unread: false, needs_input: false } }
  const runningSession = {
    ...session,
    session_id: "hs_running",
    turn: { state: "running" as const, reason: "", unread: false, needs_input: false },
  }
  const model = buildDashboardModel([worker], [openai], [readySession, runningSession])
  const upstreamNode = model.domains[0].kind === "upstream" ? model.domains[0].upstream : undefined
  if (!upstreamNode) throw new Error("expected upstream domain")
  const workerNode = model.domains[0].workers[0].worker
  const readySessionNode = model.domains[0].workers[0].sessions[0]
  const runningSessionNode = model.domains[0].workers[0].sessions[1]

  expect({
    workerToUpstream: isValidDashboardDrop(workerNode, upstreamNode),
    upstreamToWorker: isValidDashboardDrop(upstreamNode, workerNode),
    readySessionToWorker: isValidDashboardDrop(readySessionNode, workerNode),
    runningSessionToWorker: isValidDashboardDrop(runningSessionNode, workerNode),
    pair: dashboardDropPair(upstreamNode, workerNode),
    targetLabel: dashboardDropLabel(workerNode, upstreamNode),
    pendingLabel: dashboardDropLabel(workerNode, null),
  }).toEqual({
    workerToUpstream: true,
    upstreamToWorker: true,
    readySessionToWorker: true,
    runningSessionToWorker: false,
    pair: { worker: workerNode, upstream: upstreamNode },
    targetLabel: "App Main → OpenAI Main",
    pendingLabel: "App Main → ?",
  })
})
