import { TextAttributes, type RGBA } from "@opentui/core"
import { useTerminalDimensions } from "@opentui/solid"
import { createEffect, createMemo, createSignal, For, Show } from "solid-js"
import { useTheme, type Theme } from "../context/theme"
import { EscHint, useDialog } from "../ui/dialog"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useToast } from "../ui/toast"
import { DialogWorkerStatus } from "./dialog-worker-status"
import { DialogUpstreamEditor } from "./dialog-upstream"
import { computeLayout, TOPOLOGY_COL_GAP, TOPOLOGY_GROUP_GAP, type TopologyGroup, type TopologyNode, type TopologyWorkerRow } from "./topology/layout"
import { computeGroupEdges } from "./topology/edges"
import { isValidDrop, toDropPair, dropLabel } from "./topology/drag"
import type { WorkerSummary, RedactedUpstream } from "./backend"

const TOPOLOGY_DIALOG_WIDTH = 116
const TOPOLOGY_DIALOG_MARGIN = 2
const TOPOLOGY_CONTENT_PADDING = 2
const TOPOLOGY_NODE_MARKER_WIDTH = 2
const TOPOLOGY_NODE_MIN_GAP = 1
const TOPOLOGY_NODE_BORDER_WIDTH = 2

export function DialogTopology() {
  const sync = useSync()
  const dialog = useDialog()
  const sdk = useSDK()
  const toast = useToast()
  const { theme } = useTheme()
  const dimensions = useTerminalDimensions()
  const [hovered, setHovered] = createSignal<string | null>(null)
  const [dragSource, setDragSource] = createSignal<TopologyNode | null>(null)
  const [dragEnded, setDragEnded] = createSignal(false)

  const graphWidth = createMemo(() => Math.max(0, Math.min(TOPOLOGY_DIALOG_WIDTH, dimensions().width - TOPOLOGY_DIALOG_MARGIN) - TOPOLOGY_CONTENT_PADDING))
  const layout = createMemo(() => computeLayout(sync.data.workers, sync.data.upstreams, graphWidth()))
  const hasData = createMemo(() => layout().groupRows.length > 0 || layout().orphanRows.length > 0)
  const related = createMemo(() => {
    const id = hovered()
    const ids = new Set<string>()
    if (!id) return ids
    for (const group of layout().groups) {
      const worker = group.workers.find((worker) => worker.id === id)
      if (group.upstream.id === id) {
        ids.add(group.upstream.id)
        for (const node of group.workers) ids.add(node.id)
        return ids
      }
      if (worker) {
        ids.add(group.upstream.id)
        ids.add(worker.id)
        return ids
      }
    }
    ids.add(id)
    return ids
  })

  createEffect(() => {
    if (hasData()) dialog.setSize("xlarge")
  })

  function handleClick(node: TopologyNode) {
    if (node.kind === "worker") {
      dialog.push(() => <DialogWorkerStatus worker={node.data as WorkerSummary} management />)
      return
    }
    const upstream = node.data as RedactedUpstream
    dialog.push(() => (
      <DialogUpstreamEditor
        name={upstream.name}
        draft={{
          base_url: upstream.base_url,
          api_key: "",
          api_format: upstream.api_format ?? "",
          has_api_key: upstream.has_api_key,
        }}
        mode="saved"
      />
    ))
  }

  async function handleDrop(source: TopologyNode, target: TopologyNode) {
    if (!isValidDrop(source, target)) return
    const { worker, upstream } = toDropPair(source, target)
    const workerData = worker.data as WorkerSummary
    const upstreamData = upstream.data as RedactedUpstream
    if (workerData.upstream.name === upstreamData.name) return
    try {
      await sdk.client.patchWorker(workerData.port, { upstream: upstreamData.name })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: `Switched ${workerData.name} → ${upstreamData.name}`, variant: "success" })
    } catch (err) {
      toast.error(err)
    }
  }

  return (
    <box flexDirection="column">
      <box flexDirection="row" justifyContent="space-between">
        <text fg={theme.text} attributes={TextAttributes.BOLD}>
          Topology
        </text>
        <EscHint dialog={dialog} />
      </box>
      <Show
        when={hasData()}
        fallback={
          <box justifyContent="center" alignItems="center">
            <text fg={theme.textMuted}>No workers or upstreams configured</text>
          </box>
        }
      >
        <box flexDirection="row" gap={2} marginTop={1}>
          <text fg={theme.borderActive}>■ upstream</text>
          <text fg={theme.success}>■ running</text>
          <text fg={theme.warning}>■ missing key</text>
          <text fg={theme.error}>■ failed</text>
        </box>
        <box flexDirection="column" gap={1} marginTop={1}>
          <For each={layout().groupRows}>
            {(row) => (
              <box flexDirection="row" gap={TOPOLOGY_GROUP_GAP}>
                <For each={row.groups}>
                  {(group) => (
                    <TopologyGroupView
                      group={group}
                      hovered={hovered()}
                      related={related()}
                      dragSource={dragSource()}
                      dragEnded={dragEnded()}
                      setHovered={setHovered}
                      setDragSource={setDragSource}
                      setDragEnded={setDragEnded}
                      onClick={handleClick}
                      onDrop={handleDrop}
                      theme={theme}
                    />
                  )}
                </For>
              </box>
            )}
          </For>
          <Show when={layout().orphanRows.length > 0}>
            <box flexDirection="column" gap={1}>
              <text fg={theme.textMuted}>orphan upstreams</text>
              <For each={layout().orphanRows}>
                {(row) => (
                  <box flexDirection="row" gap={TOPOLOGY_COL_GAP}>
                    <For each={row}>
                      {(node) => (
                        <NodeBox
                          node={node}
                          meta="idle"
                          related={related().has(node.id)}
                          hovered={hovered()}
                          dragSource={dragSource()}
                          dragEnded={dragEnded()}
                          setHovered={setHovered}
                          setDragSource={setDragSource}
                          setDragEnded={setDragEnded}
                          onClick={handleClick}
                          onDrop={handleDrop}
                          theme={theme}
                        />
                      )}
                    </For>
                  </box>
                )}
              </For>
            </box>
          </Show>
        </box>
        <DragHint source={dragSource()} hovered={hovered()} layout={layout()} theme={theme} />
      </Show>
    </box>
  )
}

function TopologyGroupView(props: {
  group: TopologyGroup
  hovered: string | null
  related: Set<string>
  dragSource: TopologyNode | null
  dragEnded: boolean
  setHovered: (id: string | null) => void
  setDragSource: (node: TopologyNode | null) => void
  setDragEnded: (ended: boolean) => void
  onClick: (node: TopologyNode) => void
  onDrop: (source: TopologyNode, target: TopologyNode) => void
  theme: Theme
}) {
  const group = createMemo(() => {
    const upstreamMeta = `${props.group.workers.length}`
    const upstream = { ...props.group.upstream, width: nodeRenderWidth(props.group.upstream, upstreamMeta) }
    const workerRows = props.group.workerRows.map((row) => {
      const workers = row.workers.map((node) => {
        const meta = (node.data as WorkerSummary).status
        return { ...node, width: nodeRenderWidth(node, meta) }
      })
      return {
        workers,
        width: workers.reduce((sum, node) => sum + node.width, 0) + TOPOLOGY_COL_GAP * (workers.length - 1),
      }
    })
    const widestWorkerRow = workerRows.reduce((max, row) => Math.max(max, row.width), 0)
    return {
      ...props.group,
      upstream,
      workers: workerRows.flatMap((row) => row.workers),
      workerRows,
      width: Math.max(upstream.width, widestWorkerRow),
    }
  })

  return (
    <box flexDirection="column" width={group().width} alignItems="center">
      <NodeBox
        node={group().upstream}
        meta={`${group().workers.length}`}
        related={props.related.has(group().upstream.id)}
        hovered={props.hovered}
        dragSource={props.dragSource}
        dragEnded={props.dragEnded}
        setHovered={props.setHovered}
        setDragSource={props.setDragSource}
        setDragEnded={props.setDragEnded}
        onClick={props.onClick}
        onDrop={props.onDrop}
        theme={props.theme}
      />
      <For each={group().workerRows}>
        {(row) => (
          <>
            <EdgeRow group={group()} row={row} hoveredId={props.hovered} theme={props.theme} />
            <box flexDirection="row" gap={TOPOLOGY_COL_GAP}>
              <For each={row.workers}>
                {(node) => (
                  <NodeBox
                    node={node}
                    meta={(node.data as WorkerSummary).status}
                    related={props.related.has(node.id)}
                    hovered={props.hovered}
                    dragSource={props.dragSource}
                    dragEnded={props.dragEnded}
                    setHovered={props.setHovered}
                    setDragSource={props.setDragSource}
                    setDragEnded={props.setDragEnded}
                    onClick={props.onClick}
                    onDrop={props.onDrop}
                    theme={props.theme}
                  />
                )}
              </For>
            </box>
          </>
        )}
      </For>
    </box>
  )
}

function NodeBox(props: {
  node: TopologyNode
  meta: string
  related: boolean
  hovered: string | null
  dragSource: TopologyNode | null
  dragEnded: boolean
  setHovered: (id: string | null) => void
  setDragSource: (node: TopologyNode | null) => void
  setDragEnded: (ended: boolean) => void
  onClick: (node: TopologyNode) => void
  onDrop: (source: TopologyNode, target: TopologyNode) => void
  theme: Theme
}) {
  const isHovered = () => props.hovered === props.node.id
  return (
    <box
      width={nodeRenderWidth(props.node, props.meta)}
      height={props.node.height}
      border={true}
      borderColor={nodeColor(props.node, isHovered(), props.related, props.dragSource, props.theme)}
      backgroundColor={props.theme.backgroundPanel}
      justifyContent="center"
      alignItems="center"
      onMouseOver={() => props.setHovered(props.node.id)}
      onMouseOut={() => props.setHovered(null)}
      onMouseDown={() => props.setDragSource(props.node)}
      onMouseDragEnd={() => {
        props.setDragEnded(true)
        queueMicrotask(() => props.setDragSource(null))
      }}
      onMouseDrop={() => {
        const source = props.dragSource
        if (source && source.id !== props.node.id) props.onDrop(source, props.node)
        props.setDragEnded(true)
      }}
      onMouseUp={() => {
        if (props.dragEnded) {
          props.setDragEnded(false)
          return
        }
        props.setDragSource(null)
        props.onClick(props.node)
      }}
    >
      <box flexDirection="row" width="100%" justifyContent="space-between" paddingLeft={1} paddingRight={1}>
        <text fg={props.theme.text} selectable={false} wrapMode="none">
          <span style={{ fg: nodeMarkerColor(props.node, props.theme) }}>▌</span> {props.node.label}
        </text>
        <text fg={props.theme.textMuted} selectable={false} wrapMode="none">{props.meta}</text>
      </box>
    </box>
  )
}

function nodeRenderWidth(node: TopologyNode, meta: string): number {
  return node.label.length + meta.length + TOPOLOGY_NODE_MARKER_WIDTH + TOPOLOGY_NODE_MIN_GAP + TOPOLOGY_CONTENT_PADDING + TOPOLOGY_NODE_BORDER_WIDTH
}

function DragHint(props: {
  source: TopologyNode | null
  hovered: string | null
  layout: ReturnType<typeof computeLayout>
  theme: Theme
}) {
  const target = createMemo(() => {
    const s = props.source
    if (!s) return null
    const all: TopologyNode[] = [
      ...props.layout.groups.flatMap((g) => [g.upstream, ...g.workers]),
      ...props.layout.orphans,
    ]
    return all.find((n) => n.id === props.hovered && n.id !== s.id) ?? null
  })
  return (
    <Show when={props.source}>
      {(src) => (
        <box height={1} marginTop={1}>
          <text fg={props.theme.borderActive}>Drag: {dropLabel(src(), target())}</text>
        </box>
      )}
    </Show>
  )
}

function EdgeRow(props: { group: TopologyGroup; row: TopologyWorkerRow; hoveredId: string | null; theme: Theme }) {
  const edges = createMemo(() => computeGroupEdges(props.group, props.row))
  const isHighlighted = () => props.hoveredId === props.group.upstream.id || props.row.workers.some((w) => w.id === props.hoveredId)
  const line = createMemo(() => {
    const cells = edges().cells
    const maxX = cells.reduce((max, c) => Math.max(max, c.x), -1)
    const map = new Map(cells.map((c) => [c.x, c.char]))
    let s = ""
    for (let x = 0; x <= maxX; x++) {
      s += map.get(x) ?? " "
    }
    return s
  })

  return (
    <box height={1} width={props.group.width}>
      <text fg={isHighlighted() ? props.theme.borderActive : props.theme.textMuted}>{line()}</text>
    </box>
  )
}

function nodeMarkerColor(node: TopologyNode, theme: Theme): RGBA {
  if (node.kind === "upstream") {
    const upstream = node.data as RedactedUpstream
    return upstream.has_api_key ? theme.borderActive : theme.warning
  }
  const status = (node.data as WorkerSummary).status
  if (status === "running") return theme.success
  if (status === "failed") return theme.error
  return theme.textMuted
}

function nodeColor(node: TopologyNode, hovered: boolean, related: boolean, dragSource: TopologyNode | null, theme: Theme): RGBA {
  const src = dragSource
  if (src && src.id === node.id) return theme.borderActive
  if (src && src.kind !== node.kind && hovered) return theme.success
  if (src && src.kind === node.kind && hovered) return theme.error
  if (hovered) return theme.borderActive
  if (related) return theme.borderSubtle
  if (node.kind === "worker") {
    const status = (node.data as WorkerSummary).status
    if (status === "running") return theme.success
    if (status === "failed") return theme.error
    return theme.textMuted
  }
  const upstream = node.data as RedactedUpstream
  return upstream.has_api_key ? theme.success : theme.warning
}
