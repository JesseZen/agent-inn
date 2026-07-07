import { createMemo, createSignal, onMount } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { DialogAlert } from "../ui/dialog-alert"
import { launchProxySession } from "./launch"
import { useSync } from "../context/sync"
import { useProject } from "../context/project"
import { deleteHostedTerminalSession, DialogHostedTerminalDelete } from "./dialog-hosted-terminal-delete"
import type { HostedSessionSummary } from "./backend"
import { Global } from "@agent-inn/core/global"

type HostedTerminalOption =
  | {
      type: "create"
    }
  | {
      type: "delete"
    }
  | {
      type: "session"
      session: HostedSessionSummary
    }

export function DialogHostedTerminal(props: { initialSessions?: HostedSessionSummary[] } = {}) {
  const sdk = useSDK()
  const dialog = useDialog()
  const sync = useSync()
  const project = useProject()
  const [sessions, setSessions] = createSignal<HostedSessionSummary[]>(props.initialSessions ?? [])

  async function refreshSessions() {
    setSessions(await sdk.client.listHostedSessions())
  }

  onMount(() => {
    void refreshSessions()
  })

  const options = createMemo<DialogSelectOption<HostedTerminalOption>[]>(() => [
    {
      title: "Create new session",
      value: { type: "create" },
      description: "Choose a worker, then name a new hosted terminal session",
      category: "Action",
    },
    {
      title: "Delete",
      value: { type: "delete" },
      description: "Delete a hosted terminal session",
      category: "Action",
    },
    ...sessions().map((session) => ({
      title: session.session_label,
      value: { type: "session" as const, session },
      description: `${session.worker_name} • ${session.status}`,
      category: "Existing sessions",
    })),
  ])

  async function createSession() {
    dialog.push(() => (
      <DialogWorkerPicker
        title="Choose worker"
        placeholder="Search workers..."
        onSelect={async (worker) => {
          const basePath = project.instance.directory() || sync.path.directory
          const workspace = await DialogPrompt.show(dialog, "Launch Worker", {
            placeholder: "Workspace directory",
            description: () => <text>Launch this worker in the workspace.</text>,
            value: basePath,
            directoryCompletion: basePath
              ? {
                  basePath,
                }
              : undefined,
          })
          if (workspace === null) return

          let nextCounter = 1
          const prefix = `${worker.name} `
          for (const session of sessions()) {
            if (!session.session_label.startsWith(prefix)) continue
            const value = Number(session.session_label.slice(prefix.length))
            if (Number.isInteger(value) && value >= nextCounter) nextCounter = value + 1
          }
          const defaultLabel = `${worker.name} ${nextCounter}`
          const label = await DialogPrompt.show(dialog, "Create Hosted Session", {
            placeholder: "Session label",
            value: defaultLabel,
            description: () => <text>Label must be unique. It will appear on the tmux tab.</text>,
          })
          if (label === null) return
          const sessionLabel = label || defaultLabel
          if (sessions().some((session) => session.session_label === sessionLabel)) {
            await DialogAlert.show(dialog, "Create hosted session failed", `Session label "${sessionLabel}" already exists.`)
            return
          }
          try {
            const settings = await sdk.client.getSettings()
            await launchProxySession({
              executable: import.meta.env?.AINN_EXECUTABLE || undefined,
              workerPort: worker.port,
              profile: worker.name,
              configDir: Global.Path.config,
              workspace: workspace || undefined,
              mode: "hosted-terminal",
              sessionLabel,
              opener: settings.settings.terminal.opener,
              tmuxSocketName: settings.settings.terminal.tmux.socket_name,
              tmuxHostSession: settings.settings.terminal.tmux.host_session,
            })
            await refreshSessions()
            dialog.pop()
          } catch (err) {
            await DialogAlert.show(dialog, "Create hosted session failed", String(err instanceof Error ? err.message : err))
          }
        }}
      />
    ))
  }

  function changeSessionWorker(session: HostedSessionSummary) {
    const currentWorker = sync.data.workers.find((worker) => worker.name === session.worker_name)
    const currentLauncher = currentWorker?.launcher ?? "codex"
    const workers = sync.data.workers.filter((worker) => (worker.launcher ?? "codex") === currentLauncher)
    dialog.push(() => (
      <DialogWorkerPicker
        title={`Change worker: ${session.session_label}`}
        placeholder="Search workers..."
        workers={workers}
        onSelect={async (worker) => {
          try {
            await sdk.client.patchHostedSession(session.session_id, { worker_name: worker.name })
            await refreshSessions()
            dialog.pop()
          } catch (err) {
            await DialogAlert.show(dialog, "Change hosted session worker failed", String(err instanceof Error ? err.message : err))
          }
        }}
      />
    ))
  }

  async function renameSession(session: HostedSessionSummary) {
    const label = await DialogPrompt.show(dialog, "Rename Hosted Session", {
      placeholder: "Session label",
      value: session.session_label,
      description: () => <text>Label must be unique. It will appear on the tmux tab.</text>,
    })
    if (label === null) return
    try {
      await sdk.client.patchHostedSession(session.session_id, { session_label: label })
      const nextSessions = await sdk.client.listHostedSessions()
      dialog.replace(() => <DialogHostedTerminal initialSessions={nextSessions} />)
    } catch (err) {
      await DialogAlert.show(dialog, "Rename hosted session failed", String(err instanceof Error ? err.message : err))
    }
  }

  async function duplicateSession(session: HostedSessionSummary) {
    try {
      const duplicated = await sdk.client.duplicateHostedSession(session.session_id)
      const settings = await sdk.client.getSettings()
      await launchProxySession({
        executable: import.meta.env?.AINN_EXECUTABLE || undefined,
        workerPort: duplicated.worker_port,
        profile: duplicated.worker_name,
        configDir: Global.Path.config,
        mode: "hosted-terminal",
        sessionID: duplicated.session_id,
        opener: settings.settings.terminal.opener,
        tmuxSocketName: settings.settings.terminal.tmux.socket_name,
        tmuxHostSession: settings.settings.terminal.tmux.host_session,
      })
      await refreshSessions()
    } catch (err) {
      await DialogAlert.show(dialog, "Duplicate hosted session failed", String(err instanceof Error ? err.message : err))
    }
  }

  return (
    <DialogSelect
      title="Hosted Terminal"
      options={options()}
      placeholder="Select hosted session..."
      actions={[
        {
          command: "session.change_worker",
          title: "change worker",
          disabled: (option) => option?.value.type !== "session" || option.value.session.status !== "stale",
          onTrigger: (option) => {
            if (option.value.type !== "session") return
            changeSessionWorker(option.value.session)
          },
        },
        {
          command: "session.rename",
          title: "rename",
          disabled: (option) => option?.value.type !== "session",
          onTrigger: (option) => {
            if (option.value.type !== "session") return
            void renameSession(option.value.session)
          },
        },
        {
          command: "session.duplicate",
          title: "duplicate",
          side: "right",
          disabled: (option) => option?.value.type !== "session",
          onTrigger: (option) => {
            if (option.value.type !== "session") return
            void duplicateSession(option.value.session)
          },
        },
        {
          command: "session.delete",
          title: "delete",
          side: "right",
          disabled: (option) => option?.value.type !== "session",
          onTrigger: (option) => {
            if (option.value.type !== "session") return
            void deleteHostedTerminalSession({
              sdk,
              dialog,
              session: option.value.session,
              refreshSessions,
              onDeleted: (session) => {
                const nextSessions = sessions().filter((item) => item.session_id !== session.session_id)
                dialog.replace(() => <DialogHostedTerminal initialSessions={nextSessions} />)
              },
            })
          },
        },
      ]}
      onSelect={(option) => {
        if (option.value.type === "create") {
          void createSession()
          return
        }
        if (option.value.type === "delete") {
          dialog.push(() => (
            <DialogHostedTerminalDelete
              initialSessions={sessions()}
              onSessionsChanged={(nextSessions) => setSessions(nextSessions)}
            />
          ))
          return
        }
        const session = option.value.session
        void sdk.client.getSettings().then((settings) =>
        void launchProxySession({
          executable: import.meta.env?.AINN_EXECUTABLE || undefined,
          workerPort: session.worker_port,
          profile: session.worker_name,
          configDir: Global.Path.config,
          mode: "hosted-terminal",
          sessionID: session.session_id,
          opener: settings.settings.terminal.opener,
          tmuxSocketName: settings.settings.terminal.tmux.socket_name,
          tmuxHostSession: settings.settings.terminal.tmux.host_session,
        }).catch(async (err) => {
          await DialogAlert.show(dialog, "Open hosted session failed", String(err instanceof Error ? err.message : err))
        }))
      }}
    />
  )
}
