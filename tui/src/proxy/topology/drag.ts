import type { TopologyNode } from "./layout"
import type { HostedSessionSummary } from "../backend"

export type DropPair = { worker: TopologyNode; upstream: TopologyNode }

export function isValidDrop(source: TopologyNode, target: TopologyNode): boolean {
  if (source.kind === "session") {
    const session = source.data as HostedSessionSummary
    return target.kind === "worker" && session.turn_state !== "running"
  }
  return (source.kind === "worker" && target.kind === "upstream") || (source.kind === "upstream" && target.kind === "worker")
}

export function toDropPair(source: TopologyNode, target: TopologyNode): DropPair {
  if (source.kind === "worker") return { worker: source, upstream: target }
  return { worker: target, upstream: source }
}

export function dropLabel(source: TopologyNode, target: TopologyNode | null): string {
  return target ? `${source.label} → ${target.label}` : `${source.label} → ?`
}
