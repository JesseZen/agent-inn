import type { DashboardModel, DashboardNode } from "./model"

export const DASHBOARD_SESSION_PREVIEW_LIMIT = 3

export type DashboardRow = {
  id: string
  kind: "domain" | "upstream" | "worker" | "session" | "session-more" | "unbound"
  depth: 0 | 1 | 2 | 3
  parentID?: string
  node?: DashboardNode
  expandable: boolean
  count?: number
  active?: boolean
}

export type DashboardViewState = {
  expandedDomains: Set<string>
  expandedUpstreams: Set<string>
  expandedSessionGroups: Set<string>
  collapsedSessionGroups: Set<string>
  unboundExpanded: boolean
  query: string
  selectedID: string | null
}

export type DashboardNavigationResult = {
  selectedID: string | null
  expandID?: string
  collapseID?: string
  expandUpstreamID?: string
  collapseUpstreamID?: string
  toggleSessionGroupID?: string
  expandSessionGroupID?: string
  collapseSessionGroupID?: string
  unboundExpanded?: boolean
}

export function dashboardInitialState(model: DashboardModel): DashboardViewState {
  const expandedDomains = new Set(
    model.domains
      .filter(
        (domain) =>
          domain.kind === "pool" || domain.warning || domain.workers.some((branch) => branch.sessions.some((session) => session.data.status === "active")),
      )
      .map((domain) => domain.id),
  )
  return {
    expandedDomains,
    expandedUpstreams: new Set(model.domains.flatMap((domain) => (domain.kind === "pool" ? domain.members.map((member) => member.upstream.id) : []))),
    expandedSessionGroups: new Set(),
    collapsedSessionGroups: new Set(),
    unboundExpanded: false,
    query: "",
    selectedID: model.domains[0]?.id ?? (model.unboundUpstreams.length + model.unboundSessions.length > 0 ? "unbound" : null),
  }
}

export function dashboardVisibleRows(model: DashboardModel, state: DashboardViewState): DashboardRow[] {
  const rows: DashboardRow[] = []
  const query = state.query.trim().toLocaleLowerCase()
  for (const domain of model.domains) {
    const root = domain.kind === "pool" ? domain.pool : domain.upstream
    const domainMatches = root.label.toLocaleLowerCase().includes(query)
    const memberMatches = (
      domain.kind === "pool"
        ? domain.members
        : [
            {
              upstream: domain.upstream,
              workers: domain.workers,
              active: false,
            },
          ]
    )
      .map((member) => ({
        member,
        upstreamMatches: member.upstream.label.toLocaleLowerCase().includes(query),
        branches: member.workers
          .map((branch) => ({
            branch,
            workerMatches: branch.worker.label.toLocaleLowerCase().includes(query),
            sessions: branch.sessions.filter((session) => session.label.toLocaleLowerCase().includes(query)),
          }))
          .filter(
            (match) =>
              query === "" || domainMatches || member.upstream.label.toLocaleLowerCase().includes(query) || match.workerMatches || match.sessions.length > 0,
          ),
      }))
      .filter((match) => query === "" || domainMatches || match.upstreamMatches || match.branches.length > 0)
    if (query !== "" && !domainMatches && memberMatches.length === 0) continue

    rows.push({
      id: domain.id,
      kind: "domain",
      depth: 0,
      node: root,
      expandable: domain.kind === "pool" ? domain.members.length > 0 : domain.workers.length > 0,
    })
    if (query === "" && !state.expandedDomains.has(domain.id)) continue
    for (const memberMatch of memberMatches) {
      const workerDepth = domain.kind === "pool" ? 2 : 1
      if (domain.kind === "pool") {
        rows.push({
          id: memberMatch.member.upstream.id,
          kind: "upstream",
          depth: 1,
          parentID: domain.id,
          node: memberMatch.member.upstream,
          expandable: memberMatch.member.workers.length > 0,
          active: memberMatch.member.active,
        })
        if (query === "" && !state.expandedUpstreams.has(memberMatch.member.upstream.id)) continue
      }
      for (const match of memberMatch.branches) {
        const { branch } = match
        rows.push({
          id: branch.worker.id,
          kind: "worker",
          depth: workerDepth,
          parentID: domain.kind === "pool" ? memberMatch.member.upstream.id : domain.id,
          node: branch.worker,
          expandable: branch.sessions.length > 0,
        })
        if (query === "" && state.collapsedSessionGroups.has(branch.worker.id)) continue
        const visibleSessions =
          query === ""
            ? state.expandedSessionGroups.has(branch.worker.id)
              ? branch.sessions
              : branch.sessions.slice(0, DASHBOARD_SESSION_PREVIEW_LIMIT)
            : domainMatches || match.workerMatches
              ? branch.sessions
              : match.sessions
        for (const session of visibleSessions) {
          rows.push({
            id: session.id,
            kind: "session",
            depth: (workerDepth + 1) as 2 | 3,
            parentID: branch.worker.id,
            node: session,
            expandable: false,
          })
        }
        if (query === "" && !state.expandedSessionGroups.has(branch.worker.id) && branch.sessions.length > DASHBOARD_SESSION_PREVIEW_LIMIT) {
          rows.push({
            id: `session-more:${branch.worker.id}`,
            kind: "session-more",
            depth: (workerDepth + 1) as 2 | 3,
            parentID: branch.worker.id,
            expandable: true,
            count: branch.sessions.length - DASHBOARD_SESSION_PREVIEW_LIMIT,
          })
        }
      }
    }
  }

  const unboundCount = model.unboundUpstreams.length + model.unboundSessions.length
  const unboundUpstreams = model.unboundUpstreams.filter((node) => query === "" || node.label.toLocaleLowerCase().includes(query))
  const unboundSessions = model.unboundSessions.filter((node) => query === "" || node.label.toLocaleLowerCase().includes(query))
  if (unboundCount > 0 && (query === "" || unboundUpstreams.length + unboundSessions.length > 0)) {
    rows.push({
      id: "unbound",
      kind: "unbound",
      depth: 0,
      expandable: true,
      count: unboundCount,
    })
    if (state.unboundExpanded || query !== "") {
      for (const node of unboundUpstreams) {
        rows.push({
          id: node.id,
          kind: "domain",
          depth: 1,
          parentID: "unbound",
          node,
          expandable: false,
        })
      }
      for (const node of unboundSessions) {
        rows.push({
          id: node.id,
          kind: "session",
          depth: 1,
          parentID: "unbound",
          node,
          expandable: false,
        })
      }
    }
  }
  return rows
}

export function moveDashboardSelection(rows: DashboardRow[], selectedID: string | null, delta: number): string | null {
  if (rows.length === 0) return null
  const current = rows.findIndex((row) => row.id === selectedID)
  if (current === -1) return rows[0].id
  return rows[(current + (delta % rows.length) + rows.length) % rows.length].id
}

export function dashboardCollapse(rows: DashboardRow[], state: DashboardViewState): DashboardNavigationResult {
  const selected = rows.find((row) => row.id === state.selectedID)
  if (!selected) return { selectedID: rows[0]?.id ?? null }
  if (selected.kind === "domain" && state.expandedDomains.has(selected.id)) {
    return { selectedID: selected.id, collapseID: selected.id }
  }
  if (selected.kind === "upstream" && state.expandedUpstreams.has(selected.id)) {
    return { selectedID: selected.id, collapseUpstreamID: selected.id }
  }
  if (selected.kind === "unbound" && state.unboundExpanded) {
    return { selectedID: selected.id, unboundExpanded: false }
  }
  if (selected.kind === "worker" && selected.expandable && !state.collapsedSessionGroups.has(selected.id)) {
    return { selectedID: selected.id, collapseSessionGroupID: selected.id }
  }
  if (selected.parentID) return { selectedID: selected.parentID }
  return { selectedID: selected.id }
}

export function dashboardExpand(rows: DashboardRow[], state: DashboardViewState): DashboardNavigationResult {
  const index = rows.findIndex((row) => row.id === state.selectedID)
  if (index === -1) return { selectedID: rows[0]?.id ?? null }
  const selected = rows[index]
  if (selected.kind === "domain" && !state.expandedDomains.has(selected.id)) {
    return { selectedID: selected.id, expandID: selected.id }
  }
  if (selected.kind === "upstream" && !state.expandedUpstreams.has(selected.id)) {
    return { selectedID: selected.id, expandUpstreamID: selected.id }
  }
  if (selected.kind === "unbound" && !state.unboundExpanded) {
    return { selectedID: selected.id, unboundExpanded: true }
  }
  if (selected.kind === "worker" && selected.expandable && state.collapsedSessionGroups.has(selected.id)) {
    return { selectedID: selected.id, expandSessionGroupID: selected.id }
  }
  if (selected.kind === "session-more") {
    return { selectedID: selected.id, toggleSessionGroupID: selected.parentID }
  }
  const child = rows[index + 1]
  if (child?.parentID === selected.id) return { selectedID: child.id }
  return { selectedID: selected.id }
}

export type DashboardScrollViewport = {
  scrollTop: number
  viewport: { height: number }
  scrollTo(value: number): void
}

export function scrollDashboardRowIntoView(scroll: DashboardScrollViewport | undefined, index: number): void {
  if (!scroll || index < 0) return
  if (index < scroll.scrollTop) {
    scroll.scrollTo(index)
    return
  }
  const lastVisibleRow = scroll.scrollTop + scroll.viewport.height - 1
  if (index > lastVisibleRow) scroll.scrollTo(index - scroll.viewport.height + 1)
}
