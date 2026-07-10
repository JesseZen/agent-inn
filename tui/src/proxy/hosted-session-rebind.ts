import { launchProxySession } from "./launch"
import type { HostedSessionRecord, HostedSessionSummary, ProxySettingsResponse, WorkerSummary } from "./backend"

type HostedSessionClient = {
  patchHostedSession(sessionID: string, patch: { worker_id: string }): Promise<HostedSessionRecord>
  getSettings(): Promise<ProxySettingsResponse>
}

export async function rebindHostedSession(input: {
  client: HostedSessionClient
  session: HostedSessionSummary
  worker: WorkerSummary
  configDir: string
  executable?: string
}) {
  const updated = await input.client.patchHostedSession(input.session.session_id, { worker_id: input.worker.id })
  if (input.session.status !== "active" || input.session.turn_state === "running") return { launched: false }

  const settings = await input.client.getSettings()
  const launched = await launchProxySession({
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
  return { launched }
}
