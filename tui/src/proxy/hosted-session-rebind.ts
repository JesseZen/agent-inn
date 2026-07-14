import { launchProxySession, setupHostedTerminalSession } from "./launch"
import type { HostedSessionSnapshot, PatchHostedSessionRequest } from "./hosted-session-contract"
import type { ProxySettingsResponse, WorkerSummary } from "./backend"

type HostedSessionClient = {
  patchHostedSession(sessionID: string, patch: PatchHostedSessionRequest): Promise<HostedSessionSnapshot>
  getSettings(): Promise<ProxySettingsResponse>
}

type HostedSessionLaunchMode = "open" | "setup-only"

export async function rebindHostedSession(input: {
  client: HostedSessionClient
  session: HostedSessionSnapshot
  worker: WorkerSummary
  configDir: string
  executable?: string
  launchMode: HostedSessionLaunchMode
}) {
  const updated = await input.client.patchHostedSession(input.session.session_id, { worker_id: input.worker.id })
  if (input.session.status !== "active" || input.session.turn.state === "running") return { launched: false, session: updated }

  const settings = await input.client.getSettings()
  const launch = input.launchMode === "setup-only" ? setupHostedTerminalSession : launchProxySession
  const launched = await launch({
    executable: input.executable,
    workerPort: input.worker.port,
    profile: input.worker.id,
    configDir: input.configDir,
    mode: "hosted-terminal",
    sessionID: updated.session_id,
    opener: settings.settings.terminal.opener,
    tmuxSocketName: settings.settings.terminal.tmux.socket_name,
    tmuxHostSession: settings.settings.terminal.tmux.host_session,
  })
  return { launched, session: updated }
}
