import { TextAttributes } from "@opentui/core"
import { For, Match, Show, Switch } from "solid-js"
import type { Theme } from "../../context/theme"
import type { DashboardModel, DashboardNode } from "./model"
import { fitDashboardText } from "./model"
import type { DashboardRow } from "./navigation"
import { isValidDashboardDrop } from "./drag"

export function DashboardSummary(props: { model: DashboardModel; theme: Theme }) {
  const items = () => [
    ["UPSTREAMS", props.model.summary.upstreams],
    ["WORKERS", props.model.summary.workers],
    ["SESSIONS", props.model.summary.sessions],
    ["UNBOUND", props.model.summary.unbound],
  ] as const
  return (
    <box flexDirection="row" gap={3} paddingTop={1} paddingBottom={1}>
      <For each={items()}>{([label, value]) => (
        <box flexDirection="row" gap={1}>
          <text fg={props.theme.textMuted} selectable={false}>{label}</text>
          <text fg={value > 0 && label === "UNBOUND" ? props.theme.warning : props.theme.text} attributes={TextAttributes.BOLD} selectable={false}>{value}</text>
        </box>
      )}</For>
    </box>
  )
}

export function DashboardRows(props: {
  rows: DashboardRow[]
  selectedID: string | null
  hoveredID: string | null
  dragSource: DashboardNode | null
  theme: Theme
  availableWidth: number
  onHover(id: string | null): void
  onSelect(id: string): void
  isExpanded(row: DashboardRow): boolean
  onToggle(row: DashboardRow): void
  onActivate(row: DashboardRow): void
  onDragStart(node: DashboardNode): void
  onDragEnd(): void
  onDrop(target: DashboardNode): void
}) {
  let pressedID: string | null = null
  return (
    <For each={props.rows}>{(row) => {
      const selected = () => row.id === props.selectedID
      const hovered = () => row.id === props.hoveredID
      const node = () => row.node
      const dimmed = () => {
        const source = props.dragSource
        const value = node()
        return source !== null && value !== undefined && source.id !== value.id && !isValidDashboardDrop(source, value)
      }
      const meta = () => {
        const value = node()
        if (row.kind === "domain" && value?.kind === "upstream") {
          const upstream = value.data
          if (upstream.missing) return "missing"
          if (!upstream.has_api_key) return "missing key"
          return ""
        }
        if (row.kind === "worker" && value?.kind === "worker") return value.data.status
        if (row.kind === "session" && value?.kind === "session") return value.data.status
        return ""
      }
      const fitted = () => fitDashboardText(node()?.label ?? "", meta(), props.availableWidth - row.depth * 3)
      const foreground = () => {
        const value = node()
        if (dimmed()) return props.theme.textMuted
        if (row.kind === "unbound") return props.theme.warning
        if (value?.kind === "upstream" && (value.data.missing || !value.data.has_api_key)) return props.theme.warning
        if (value?.kind === "worker" && value.data.status === "failed") return props.theme.error
        if (value?.kind === "worker" && value.data.status === "running") return props.theme.text
        if (value?.kind === "session") return props.theme.textMuted
        return props.theme.text
      }
      const label = () => {
        if (row.kind === "unbound") return `⚠ UNBOUND ${row.count ?? 0}`
        if (row.kind === "session-more") return `+${row.count ?? 0} sessions`
        if (row.kind === "domain") return `◆ ${fitted().label}`
        return `└─ ${fitted().label}`
      }
      return (
        <box
          height={1}
          flexDirection="row"
          justifyContent="space-between"
          backgroundColor={selected() ? props.theme.backgroundElement : hovered() ? props.theme.backgroundMenu : undefined}
          onMouseOver={() => props.onHover(row.id)}
          onMouseOut={() => props.onHover(null)}
          onMouseDown={() => {
            pressedID = row.id
            props.onSelect(row.id)
            const value = node()
            if (value && !(value.kind === "session" && value.data.turn_state === "running")) props.onDragStart(value)
          }}
          onMouseUp={() => {
            const value = node()
            if (props.dragSource && value && props.dragSource.id !== value.id) props.onDrop(value)
            else if ((value || row.kind === "unbound") && pressedID === row.id) props.onActivate(row)
            pressedID = null
            props.onDragEnd()
          }}
        >
          <box flexDirection="row">
            <text fg={foreground()} attributes={selected() ? TextAttributes.BOLD : undefined} selectable={false}>
              {selected() ? "›" : " "}{" ".repeat(row.depth * 3)}
            </text>
            <Show when={row.expandable} fallback={<text fg={foreground()} selectable={false}>{"  "}{label()}</text>}>
              <text
                fg={foreground()}
                attributes={selected() ? TextAttributes.BOLD : undefined}
                selectable={false}
                onMouseDown={(event) => {
                  event.stopPropagation()
                  pressedID = null
                  props.onSelect(row.id)
                }}
                onMouseUp={(event) => {
                  event.stopPropagation()
                  props.onToggle(row)
                }}
              >
                {props.isExpanded(row) ? "▾ " : "▸ "}
              </text>
              <text fg={foreground()} attributes={selected() ? TextAttributes.BOLD : undefined} selectable={false}>
                {label()}
              </text>
            </Show>
          </box>
          <Show when={meta()}>
            <text
              fg={dimmed() ? props.theme.textMuted : meta() === "failed" ? props.theme.error : meta() === "missing key" || meta() === "missing" ? props.theme.warning : props.theme.textMuted}
              selectable={false}
            >
              {row.kind === "worker" ? "▌ " : ""}{fitted().meta}
            </text>
          </Show>
        </box>
      )
    }}</For>
  )
}

export function DashboardInspector(props: {
  selected: DashboardRow | null
  source: DashboardNode | null
  target: DashboardNode | null
  theme: Theme
}) {
  return (
    <box height={3} flexDirection="column" paddingTop={1}>
      <Show when={props.source} fallback={
        <Show when={props.selected} fallback={
          <text fg={props.theme.textMuted} selectable={false}>Select a relationship to inspect</text>
        }>
          <box flexDirection="row" gap={3}>
            <text fg={props.theme.textMuted} selectable={false}>
              {props.selected!.node
                ? `${props.selected!.node.kind} · ${props.selected!.node.label}`
                : props.selected!.kind}
            </text>
            <Switch>
              <Match when={props.selected!.kind === "domain"}>
                <text fg={props.theme.textMuted} selectable={false}>enter edit upstream</text>
              </Match>
              <Match when={props.selected!.kind === "worker"}>
                <text fg={props.theme.textMuted} selectable={false}>enter manage worker</text>
              </Match>
              <Match when={props.selected!.kind === "session"}>
                <text fg={props.theme.textMuted} selectable={false}>
                  {props.selected!.node?.kind === "session" && props.selected!.node.data.turn_state === "running"
                    ? "enter open session"
                    : "enter open session · drag to rebind"}
                </text>
              </Match>
              <Match when={props.selected!.kind === "session-more"}>
                <text fg={props.theme.textMuted} selectable={false}>enter show all sessions</text>
              </Match>
              <Match when={props.selected!.kind === "unbound"}>
                <text fg={props.theme.textMuted} selectable={false}>click or ←→ expand/collapse</text>
              </Match>
            </Switch>
          </box>
        </Show>
      }>
        <text fg={props.theme.text} selectable={false}>
          Move  From {props.source!.label}  To {props.target?.label ?? "?"}
        </text>
      </Show>
      <text fg={props.theme.textMuted} selectable={false}>
        ↑↓ select   ←→ collapse/expand   enter open   type to filter   esc close
      </text>
    </box>
  )
}
