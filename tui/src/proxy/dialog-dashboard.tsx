import { InputRenderable, ScrollBoxRenderable, TextAttributes } from "@opentui/core"
import { useTerminalDimensions } from "@opentui/solid"
import { createEffect, createMemo, createSignal, on, onMount, Show } from "solid-js"
import { useTuiConfig } from "../config"
import { Global } from "@agent-inn/core/global"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { useTheme } from "../context/theme"
import { useBindings } from "../keymap"
import { DIALOG_XLARGE_WIDTH, EscHint, useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { getScrollAcceleration } from "../util/scroll"
import type { HostedSessionSummary, RedactedUpstream, WorkerSummary } from "./backend"
import { DialogHostedTerminal } from "./dialog-hosted-terminal"
import { DialogUpstreamEditor } from "./dialog-upstream"
import { DialogWorkerStatus } from "./dialog-worker-status"
import { buildDashboardModel, type DashboardNode } from "./dashboard/model"
import {
  dashboardCollapse,
  dashboardExpand,
  dashboardInitialState,
  dashboardVisibleRows,
  DASHBOARD_SESSION_PREVIEW_LIMIT,
  moveDashboardSelection,
  scrollDashboardRowIntoView,
  type DashboardNavigationResult,
  type DashboardViewState,
} from "./dashboard/navigation"
import { DashboardInspector, DashboardRows, DashboardSummary } from "./dashboard/rows"
import { dashboardDropPair, isValidDashboardDrop } from "./dashboard/drag"
import { rebindHostedSession } from "./hosted-session-rebind"
import { useWorkerFrecency } from "./worker-frecency-context"

const DASHBOARD_DIALOG_MARGIN = 4
const DASHBOARD_CONTENT_PADDING = 8
const DASHBOARD_VERTICAL_MARGIN = 10
const DASHBOARD_MIN_HEIGHT = 9

export type DashboardSnapshot = {
  state: DashboardViewState | null
  scrollTop: number
}

export function DialogDashboard(props: { snapshot: DashboardSnapshot }) {
  const sync = useSync()
  const dialog = useDialog()
  const sdk = useSDK()
  const toast = useToast()
  const { theme } = useTheme()
  const dimensions = useTerminalDimensions()
  const tuiConfig = useTuiConfig()
  const workerFrecency = useWorkerFrecency()
  const [sessions, setSessions] = createSignal<HostedSessionSummary[]>([])
  const [sessionsLoaded, setSessionsLoaded] = createSignal(false)
  const [hoveredID, setHoveredID] = createSignal<string | null>(null)
  const [dragSource, setDragSource] = createSignal<DashboardNode | null>(null)
  const [state, setStateValue] = createSignal<DashboardViewState>(props.snapshot.state ?? {
    expandedDomains: new Set(),
    expandedSessionGroups: new Set(),
    collapsedSessionGroups: new Set(),
    unboundExpanded: false,
    query: "",
    selectedID: null,
  })
  const model = createMemo(() => buildDashboardModel(sync.data.workers, sync.data.upstreams, sessions()))
  const rows = createMemo(() => dashboardVisibleRows(model(), state()))
  const selectedRow = createMemo(() => rows().find((row) => row.id === state().selectedID) ?? null)
  const dragTarget = createMemo(() => {
    const source = dragSource()
    const target = rows().find((row) => row.id === hoveredID())?.node
    return source && target && isValidDashboardDrop(source, target) ? target : null
  })
  const availableWidth = createMemo(() => Math.max(0, Math.min(DIALOG_XLARGE_WIDTH, dimensions().width - DASHBOARD_DIALOG_MARGIN) - DASHBOARD_CONTENT_PADDING))
  let initialized = props.snapshot.state !== null
  let restoreScroll = initialized
  let input: InputRenderable
  let scroll: ScrollBoxRenderable | undefined

  function setState(next: DashboardViewState) {
    props.snapshot.state = next
    setStateValue(next)
  }

  onMount(() => {
    dialog.setSize("xlarge")
    void sdk.client.listHostedSessions()
      .then(setSessions)
      .catch(toast.error)
      .finally(() => setSessionsLoaded(true))
  })

  createEffect(on([model, sessionsLoaded], ([next, loaded]) => {
    if (!loaded) return
    if (!initialized) {
      initialized = true
      setState(dashboardInitialState(next))
      return
    }
    const domainIDs = new Set(next.domains.map((domain) => domain.id))
    const workerIDs = new Set(next.domains.flatMap((domain) => domain.workers.map((branch) => branch.worker.id)))
    const expandedDomains = new Set([...state().expandedDomains].filter((id) => domainIDs.has(id)))
    const expandedSessionGroups = new Set([...state().expandedSessionGroups].filter((id) => workerIDs.has(id)))
    const collapsedSessionGroups = new Set([...state().collapsedSessionGroups].filter((id) => workerIDs.has(id)))
    for (const domain of next.domains) {
      if (domain.warning) expandedDomains.add(domain.id)
    }
    const candidate = {
      ...state(),
      expandedDomains,
      expandedSessionGroups,
      collapsedSessionGroups,
    }
    const visible = dashboardVisibleRows(next, candidate)
    setState({
      ...candidate,
      selectedID: visible.some((row) => row.id === candidate.selectedID) ? candidate.selectedID : visible[0]?.id ?? null,
    })
    if (restoreScroll) {
      restoreScroll = false
      setTimeout(() => {
        if (scroll && !scroll.isDestroyed) scroll.scrollTop = props.snapshot.scrollTop
      }, 0)
    }
  }))

  function revealSelected(selectedID = state().selectedID) {
    queueMicrotask(() => {
      const index = rows().findIndex((row) => row.id === selectedID)
      scrollDashboardRowIntoView(scroll, index)
    })
  }

  function updateSelection(selectedID: string | null) {
    setState({ ...state(), selectedID })
    revealSelected(selectedID)
  }

  function applyNavigation(result: DashboardNavigationResult) {
    const expandedDomains = new Set(state().expandedDomains)
    const expandedSessionGroups = new Set(state().expandedSessionGroups)
    const collapsedSessionGroups = new Set(state().collapsedSessionGroups)
    if (result.expandID) expandedDomains.add(result.expandID)
    if (result.collapseID) expandedDomains.delete(result.collapseID)
    if (result.toggleSessionGroupID) expandedSessionGroups.add(result.toggleSessionGroupID)
    if (result.expandSessionGroupID) collapsedSessionGroups.delete(result.expandSessionGroupID)
    if (result.collapseSessionGroupID) collapsedSessionGroups.add(result.collapseSessionGroupID)
    const selectedID = result.toggleSessionGroupID
      ? model().domains
        .flatMap((domain) => domain.workers)
        .find((branch) => branch.worker.id === result.toggleSessionGroupID)
        ?.sessions[DASHBOARD_SESSION_PREVIEW_LIMIT]?.id ?? result.selectedID
      : result.selectedID
    setState({
      ...state(),
      expandedDomains,
      expandedSessionGroups,
      collapsedSessionGroups,
      selectedID,
      unboundExpanded: result.unboundExpanded ?? state().unboundExpanded,
    })
    revealSelected(selectedID)
  }

  function isRowExpanded(row: ReturnType<typeof rows>[number]) {
    if (row.kind === "domain" && row.depth === 0) return state().expandedDomains.has(row.id)
    if (row.kind === "worker") return !state().collapsedSessionGroups.has(row.id)
    if (row.kind === "unbound") return state().unboundExpanded
    return false
  }

  function toggleRow(row: ReturnType<typeof rows>[number]) {
    const selectedState = { ...state(), selectedID: row.id }
    applyNavigation(isRowExpanded(row)
      ? dashboardCollapse(rows(), selectedState)
      : dashboardExpand(rows(), selectedState))
  }

  function openNode(node: DashboardNode) {
    props.snapshot.scrollTop = scroll?.scrollTop ?? 0
    if (node.kind === "session") {
      dialog.push(() => <DialogHostedTerminal initialSessions={[node.data]} />)
      return
    }
    if (node.kind === "worker") {
      dialog.push(() => <DialogWorkerStatus worker={node.data} management />)
      return
    }
    dialog.push(() => (
      <DialogUpstreamEditor
        id={node.data.id}
        draft={{
          name: node.data.name,
          base_url: node.data.base_url ?? "",
          api_key: "",
          api_format: node.data.api_format ?? "",
          has_api_key: node.data.has_api_key,
        }}
        mode="saved"
      />
    ))
  }

  async function handleDrop(target: DashboardNode) {
    const source = dragSource()
    if (!source || !isValidDashboardDrop(source, target)) return
    if (source.kind === "session" && target.kind === "worker") {
      try {
        const { launched } = await rebindHostedSession({
          client: sdk.client,
          session: source.data,
          worker: target.data,
          configDir: Global.Path.config,
          executable: import.meta.env?.AINN_EXECUTABLE || undefined,
          launchMode: "open",
        })
        if (launched) workerFrecency.record(target.data.id)
        setSessions(await sdk.client.listHostedSessions())
        toast.show({ message: `Rebound ${source.data.session_label} -> ${target.data.name}`, variant: "success" })
      } catch (error) {
        toast.error(error)
      }
      return
    }
    const { worker, upstream } = dashboardDropPair(source, target)
    if (worker.data.upstream_id === upstream.data.id) return
    try {
      await sdk.client.patchWorker(worker.data.id, { upstream_id: upstream.data.id })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: `Switched ${worker.data.name} -> ${upstream.data.name}`, variant: "success" })
    } catch (error) {
      toast.error(error)
    }
  }

  useBindings(() => ({
    commands: [
      { name: "dashboard.previous", title: "Previous dashboard row", category: "Dashboard", run: () => updateSelection(moveDashboardSelection(rows(), state().selectedID, -1)) },
      { name: "dashboard.next", title: "Next dashboard row", category: "Dashboard", run: () => updateSelection(moveDashboardSelection(rows(), state().selectedID, 1)) },
      { name: "dashboard.collapse", title: "Collapse dashboard row", category: "Dashboard", run: () => applyNavigation(dashboardCollapse(rows(), state())) },
      { name: "dashboard.expand", title: "Expand dashboard row", category: "Dashboard", run: () => applyNavigation(dashboardExpand(rows(), state())) },
      { name: "dashboard.home", title: "First dashboard row", category: "Dashboard", run: () => updateSelection(rows()[0]?.id ?? null) },
      { name: "dashboard.end", title: "Last dashboard row", category: "Dashboard", run: () => updateSelection(rows().at(-1)?.id ?? null) },
      { name: "dashboard.page_up", title: "Dashboard page up", category: "Dashboard", run: () => updateSelection(moveDashboardSelection(rows(), state().selectedID, -(scroll?.viewport.height ?? 1))) },
      { name: "dashboard.page_down", title: "Dashboard page down", category: "Dashboard", run: () => updateSelection(moveDashboardSelection(rows(), state().selectedID, scroll?.viewport.height ?? 1)) },
      {
        name: "dashboard.submit",
        title: "Open dashboard row",
        category: "Dashboard",
        run: () => {
          const row = selectedRow()
          if (!row) return
          if (row.kind === "unbound" || row.kind === "session-more") {
            toggleRow(row)
            return
          }
          openNode(row.node!)
        },
      },
    ],
    bindings: [
      { key: "up", cmd: "dashboard.previous" },
      { key: "down", cmd: "dashboard.next" },
      { key: "left", cmd: "dashboard.collapse" },
      { key: "right", cmd: "dashboard.expand" },
      { key: "home", cmd: "dashboard.home" },
      { key: "end", cmd: "dashboard.end" },
      { key: "pageup", cmd: "dashboard.page_up" },
      { key: "pagedown", cmd: "dashboard.page_down" },
      { key: "return", cmd: "dashboard.submit" },
    ],
  }))

  return (
    <box flexDirection="column" height={Math.max(DASHBOARD_MIN_HEIGHT, dimensions().height - DASHBOARD_VERTICAL_MARGIN)} paddingLeft={4} paddingRight={4}>
      <box flexDirection="row" justifyContent="space-between">
        <text fg={theme.text} attributes={TextAttributes.BOLD} selectable={false}>Dashboard</text>
        <EscHint dialog={dialog} />
      </box>
      <DashboardSummary model={model()} theme={theme} />
      <input
        value={state().query}
        onInput={(query) => {
          const next = { ...state(), query }
          const visible = dashboardVisibleRows(model(), next)
          const normalizedQuery = query.trim().toLocaleLowerCase()
          const selectedID = [...visible]
            .reverse()
            .find((row) => row.node?.label.toLocaleLowerCase().includes(normalizedQuery))?.id ?? visible[0]?.id ?? null
          setState({ ...next, selectedID })
          revealSelected(selectedID)
        }}
        focusedBackgroundColor={theme.backgroundPanel}
        cursorColor={theme.primary}
        focusedTextColor={theme.text}
        ref={(renderable) => {
          input = renderable
          input.traits = { status: "FILTER" }
          setTimeout(() => {
            if (!input.isDestroyed) input.focus()
          }, 1)
        }}
        placeholder="Filter relationships"
        placeholderColor={theme.textMuted}
      />
      <box flexGrow={1} flexShrink={1} paddingTop={1}>
        <Show when={model().summary.upstreams + model().summary.workers + model().summary.sessions > 0} fallback={
          <text fg={theme.textMuted} selectable={false}>No workers, upstreams, or sessions configured</text>
        }>
          <scrollbox
            flexGrow={1}
            flexShrink={1}
            scrollbarOptions={{ visible: false }}
            scrollAcceleration={getScrollAcceleration(tuiConfig)}
            ref={(renderable: ScrollBoxRenderable) => (scroll = renderable)}
          >
            <DashboardRows
              rows={rows()}
              selectedID={state().selectedID}
              hoveredID={hoveredID()}
              dragSource={dragSource()}
              theme={theme}
              availableWidth={availableWidth()}
              onHover={(id) => {
                setHoveredID(id)
                const source = dragSource()
                const row = rows().find((item) => item.id === id)
                if (!source || !row?.node || !isValidDashboardDrop(source, row.node)) return
                const domainID = row.kind === "domain" ? row.id : row.parentID
                if (!domainID?.startsWith("upstream:")) return
                const expandedDomains = new Set(state().expandedDomains)
                expandedDomains.add(domainID)
                setState({ ...state(), expandedDomains })
              }}
              onSelect={updateSelection}
              isExpanded={isRowExpanded}
              onToggle={toggleRow}
              onActivate={(row) => {
                if (row.kind === "unbound" || row.kind === "session-more") {
                  toggleRow(row)
                  return
                }
                openNode(row.node!)
              }}
              onDragStart={setDragSource}
              onDragEnd={() => setDragSource(null)}
              onDrop={(target) => void handleDrop(target)}
            />
          </scrollbox>
        </Show>
      </box>
      <DashboardInspector selected={selectedRow()} source={dragSource()} target={dragTarget()} theme={theme} />
    </box>
  )
}
