export type HostedTurnState = "idle" | "running" | "done" | "failed" | "interrupted"
export type HostedSessionStatus = "active" | "stale"
export type HostedSessionMarker = "" | "todo"

export type HostedSessionWorkerSnapshot = {
  id: string
  name: string
  port: number
  missing: boolean
}

export type HostedSessionTurnSnapshot = {
  state: HostedTurnState
  reason: string
  unread: boolean
  needs_input: boolean
}

export type HostedSessionSnapshot = {
  session_id: string
  session_label: string
  worker: HostedSessionWorkerSnapshot
  workspace: string
  model: string
  add_dirs: string[]
  status: HostedSessionStatus
  user_marker: HostedSessionMarker
  turn: HostedSessionTurnSnapshot
  created_at: string
  last_opened_at: string
}

export type HostedSessionListResponse = {
  sessions: HostedSessionSnapshot[]
  event_cursor: string
}

export type CreateHostedSessionRequest = {
  worker_id: string
  session_label?: string
  workspace?: string
  model?: string
  add_dirs?: string[]
}

export type PatchHostedSessionRequest =
  | { session_label: string; worker_id?: never; user_marker?: never }
  | { worker_id: string; session_label?: never; user_marker?: never }
  | { user_marker: HostedSessionMarker; session_label?: never; worker_id?: never }

export type ManagerEvent = {
  id: string
  type: string
  payload: Record<string, unknown>
}
