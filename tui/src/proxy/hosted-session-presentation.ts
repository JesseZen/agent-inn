import type { Theme } from "../context/theme"
import type { HostedSessionSnapshot } from "./hosted-session-contract"

export type HostedSessionDisplayMarker = {
  symbol: "?" | "*" | "+" | "!" | "~" | ":"
  tone: "primary" | "success" | "error" | "warning" | "muted"
  bold: boolean
}

export function hostedSessionMarker(session: HostedSessionSnapshot): HostedSessionDisplayMarker {
  if (session.turn.state === "running" && session.turn.needs_input) return { symbol: "?", tone: "warning", bold: true }
  if (session.turn.state === "running") return { symbol: "*", tone: "primary", bold: true }
  if (session.turn.unread) {
    if (session.turn.state === "done") return { symbol: "+", tone: "success", bold: true }
    return { symbol: "!", tone: "error", bold: true }
  }
  if (session.user_marker === "todo") return { symbol: "~", tone: "warning", bold: false }
  if (session.turn.state === "done") return { symbol: "+", tone: "muted", bold: false }
  if (session.turn.state === "failed" || session.turn.state === "interrupted") return { symbol: "!", tone: "muted", bold: false }
  return { symbol: ":", tone: "muted", bold: false }
}

export function hostedSessionMarkerColor(theme: Theme, marker: HostedSessionDisplayMarker) {
  if (marker.tone === "primary") return theme.primary
  if (marker.tone === "success") return theme.success
  if (marker.tone === "error") return theme.error
  if (marker.tone === "warning") return theme.warning
  return theme.textMuted
}
