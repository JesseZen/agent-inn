import { TextAttributes } from "@opentui/core"
import { For, Match, Show, Switch } from "solid-js"
import type { Theme } from "../../context/theme"
import type { DashboardModel, DashboardNode } from "./model"
import { fitDashboardText } from "./model"
import type { DashboardRow } from "./navigation"
import { isValidDashboardDrop } from "./drag"
import { useLanguage } from "../../context/language"

export function DashboardSummary(props: { model: DashboardModel; theme: Theme }) {
  const { t } = useLanguage()
  const items = () =>
    [
      { label: t("proxy.dashboard.summaryPools"), value: props.model.summary.pools, warning: false },
      { label: t("proxy.dashboard.summaryUpstreams"), value: props.model.summary.upstreams, warning: false },
      { label: t("proxy.dashboard.summaryWorkers"), value: props.model.summary.workers, warning: false },
      { label: t("proxy.dashboard.summarySessions"), value: props.model.summary.sessions, warning: false },
      { label: t("proxy.dashboard.unbound"), value: props.model.summary.unbound, warning: true },
    ] as const
  return (
    <box flexDirection="row" gap={3} paddingTop={1} paddingBottom={1}>
      <For each={items()}>
        {({ label, value, warning }) => (
          <box flexDirection="row" gap={1}>
            <text fg={props.theme.textMuted} selectable={false}>
              {label}
            </text>
            <text fg={value > 0 && warning ? props.theme.warning : props.theme.text} attributes={TextAttributes.BOLD} selectable={false}>
              {value}
            </text>
          </box>
        )}
      </For>
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
  const { t } = useLanguage()
  let pressedID: string | null = null
  return (
    <For each={props.rows}>
      {(row) => {
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
          if ((row.kind === "domain" || row.kind === "upstream") && value?.kind === "upstream") {
            const upstream = value.data
            if (upstream.missing) return t("proxy.dashboard.missing")
            if (!upstream.has_api_key) return t("proxy.dashboard.missingKey")
            return row.active ? t("proxy.pool.active") : ""
          }
          if (value?.kind === "pool") return t("proxy.dashboard.membersCount", { count: value.data.upstreams.length })
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
          if (row.kind === "unbound") return `⚠ ${t("proxy.dashboard.unbound")} ${row.count ?? 0}`
          if (row.kind === "session-more") return t("proxy.dashboard.sessionsCount", { count: row.count ?? 0 })
          if (row.kind === "domain") return `${node()?.kind === "pool" ? "▣" : "◆"} ${fitted().label}`
          if (row.kind === "upstream") return `◆ ${fitted().label}`
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
              if (value && value.kind !== "pool" && !(value.kind === "session" && value.data.turn_state === "running")) props.onDragStart(value)
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
                {selected() ? "›" : " "}
                {" ".repeat(row.depth * 3)}
              </text>
              <Show
                when={row.expandable}
                fallback={
                  <text fg={foreground()} selectable={false}>
                    {"  "}
                    {label()}
                  </text>
                }
              >
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
                fg={
                  dimmed()
                    ? props.theme.textMuted
                    : meta() === "failed"
                      ? props.theme.error
                      : meta() === t("proxy.dashboard.missingKey") || meta() === t("proxy.dashboard.missing")
                        ? props.theme.warning
                        : props.theme.textMuted
                }
                selectable={false}
              >
                {row.kind === "worker" ? "▌ " : ""}
                {fitted().meta}
              </text>
            </Show>
          </box>
        )
      }}
    </For>
  )
}

export function DashboardInspector(props: { selected: DashboardRow | null; source: DashboardNode | null; target: DashboardNode | null; theme: Theme }) {
  const { t } = useLanguage()
  return (
    <box height={3} flexDirection="column" paddingTop={1}>
      <Show
        when={props.source}
        fallback={
          <Show
            when={props.selected}
            fallback={
              <text fg={props.theme.textMuted} selectable={false}>
                {t("proxy.dashboard.inspect")}
              </text>
            }
          >
            <box flexDirection="row" gap={3}>
              <text fg={props.theme.textMuted} selectable={false}>
                {props.selected!.node ? `${props.selected!.node.kind} · ${props.selected!.node.label}` : props.selected!.kind}
              </text>
              <Switch>
                <Match when={props.selected!.kind === "domain" || props.selected!.kind === "upstream"}>
                  <text fg={props.theme.textMuted} selectable={false}>
                    {props.selected!.node?.kind === "pool" ? t("proxy.dashboard.enterEditPool") : t("proxy.dashboard.enterEditUpstream")}
                  </text>
                </Match>
                <Match when={props.selected!.kind === "worker"}>
                  <text fg={props.theme.textMuted} selectable={false}>
                    {t("proxy.dashboard.enterManageWorker")}
                  </text>
                </Match>
                <Match when={props.selected!.kind === "session"}>
                  <text fg={props.theme.textMuted} selectable={false}>
                    {props.selected!.node?.kind === "session" && props.selected!.node.data.turn_state === "running"
                      ? t("proxy.dashboard.enterOpenSession")
                      : t("proxy.dashboard.enterOpenSessionRebind")}
                  </text>
                </Match>
                <Match when={props.selected!.kind === "session-more"}>
                  <text fg={props.theme.textMuted} selectable={false}>
                    {t("proxy.dashboard.enterShowAllSessions")}
                  </text>
                </Match>
                <Match when={props.selected!.kind === "unbound"}>
                  <text fg={props.theme.textMuted} selectable={false}>
                    {t("proxy.dashboard.expandHint")}
                  </text>
                </Match>
              </Switch>
            </box>
          </Show>
        }
      >
        <text fg={props.theme.text} selectable={false}>
          {t("proxy.dashboard.move", { source: props.source!.label, target: props.target?.label ?? "?" })}
        </text>
      </Show>
      <text fg={props.theme.textMuted} selectable={false}>
        {t("proxy.dashboard.hint")}
      </text>
    </box>
  )
}
