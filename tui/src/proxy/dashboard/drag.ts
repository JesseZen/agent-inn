import type { DashboardNode } from "./model"

export type DashboardDropPair = {
  worker: Extract<DashboardNode, { kind: "worker" }>
  upstream: Extract<DashboardNode, { kind: "upstream" }>
}

export function isValidDashboardDrop(source: DashboardNode, target: DashboardNode): boolean {
  if (source.kind === "session") return target.kind === "worker" && source.data.turn.state !== "running"
  return (source.kind === "worker" && target.kind === "upstream") ||
    (source.kind === "upstream" && target.kind === "worker")
}

export function dashboardDropPair(source: DashboardNode, target: DashboardNode): DashboardDropPair {
  if (source.kind === "worker" && target.kind === "upstream") return { worker: source, upstream: target }
  if (source.kind === "upstream" && target.kind === "worker") return { worker: target, upstream: source }
  throw new Error("dashboard drop pair requires worker and upstream")
}

export function dashboardDropLabel(source: DashboardNode, target: DashboardNode | null): string {
  return target ? `${source.label} → ${target.label}` : `${source.label} → ?`
}
