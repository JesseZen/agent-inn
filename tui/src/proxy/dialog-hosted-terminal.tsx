import { createMemo } from "solid-js"
import { reconcile } from "solid-js/store"
import { TextAttributes } from "@opentui/core"
import { DialogSelect, type DialogSelectOption, type DialogSelectProps } from "../ui/dialog-select"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { DialogAlert } from "../ui/dialog-alert"
import { launchProxySession, setupHostedTerminalSession, type ProxyLaunchOptions } from "./launch"
import { rebindHostedSession } from "./hosted-session-rebind"
import { useSync } from "../context/sync"
import { useProject } from "../context/project"
import { deleteHostedTerminalSession, DialogHostedTerminalDelete } from "./dialog-hosted-terminal-delete"
import { DialogHostedTerminalBulkActions } from "./dialog-hosted-terminal-bulk-actions"
import type { HostedSessionSnapshot } from "./hosted-session-contract"
import { Global } from "@agent-inn/core/global"
import { useWorkerFrecency } from "./worker-frecency-context"
import { useLanguage } from "../context/language"
import { useTheme } from "../context/theme"
import { hostedSessionMarker, hostedSessionMarkerColor } from "./hosted-session-presentation"

type HostedTerminalSurface = "dialog" | "popup"

type HostedTerminalOption =
  | {
      type: "create"
    }
  | {
      type: "delete"
    }
  | {
      type: "bulk-actions"
    }
  | {
      type: "refresh"
    }
  | {
      type: "session"
      session: HostedSessionSnapshot
    }

export function DialogHostedTerminal(props: { mode?: HostedTerminalSurface; onClose?: () => void } = {}) {
  const sdk = useSDK()
  const dialog = useDialog()
  const sync = useSync()
  const project = useProject()
  const workerFrecency = useWorkerFrecency()
  const { t } = useLanguage()
  const { theme } = useTheme()
  const mode = props.mode ?? "dialog"
  const executable = import.meta.env?.AINN_EXECUTABLE || undefined
  const workerSections = createMemo(() => workerFrecency.sections(sync.data.workers))
  const sessions = createMemo(() => Object.values(sync.data.hosted_sessions))

  async function openHostedTerminal(opts: ProxyLaunchOptions) {
    if (mode === "popup") return setupHostedTerminalSession(opts)
    return launchProxySession(opts)
  }

  async function refreshSessions() {
    await sync.hosted.refresh()
  }

  const options = createMemo<DialogSelectOption<HostedTerminalOption>[]>(() => [
    ...(mode === "popup"
      ? [
          {
            title: t("common.refresh"),
            value: { type: "refresh" as const },
            description: t("proxy.hosted.refreshDescription"),
            category: t("proxy.hosted.categoryAction"),
          },
        ]
      : []),
    {
      title: t("proxy.hosted.create"),
      value: { type: "create" },
      description: t("proxy.hosted.createDescription"),
      category: t("proxy.hosted.categoryAction"),
    },
    {
      title: t("common.delete"),
      value: { type: "delete" },
      description: t("proxy.hosted.deleteDescription"),
      category: t("proxy.hosted.categoryAction"),
    },
    ...sessions().map((session) => {
      const worker = session.worker.missing ? t("proxy.hosted.missingWorker", { id: session.worker.id }) : session.worker.name
      const marker = hostedSessionMarker(session)
      return {
        title: session.session_label,
        value: { type: "session" as const, session },
        description: `${worker} • ${session.status}${session.turn.needs_input ? ` • ${t("proxy.hosted.waitingForInput")}` : ""}`,
        category: t("proxy.hosted.existing"),
        gutter: () => (
          <text
            fg={hostedSessionMarkerColor(theme, marker)}
            attributes={marker.bold ? TextAttributes.BOLD : undefined}
          >
            {marker.symbol}
          </text>
        ),
      }
    }),
    {
      title: t("proxy.hosted.bulk"),
      value: { type: "bulk-actions" },
      description: t("proxy.hosted.bulkDescription"),
      category: t("proxy.hosted.categoryBulk"),
    },
  ])

  async function createSession() {
    dialog.push(() => (
      <DialogWorkerPicker
        title={t("proxy.hosted.chooseWorker")}
        placeholder={t("proxy.worker.search")}
        recentWorkers={workerSections().recent}
        workers={workerSections().rest}
        onSelect={async (worker) => {
          const basePath = project.instance.directory() || sync.path.directory
          const workspace = await DialogPrompt.show(dialog, t("proxy.command.launchWorker"), {
            placeholder: t("proxy.hosted.workspace"),
            description: () => <text>{t("proxy.hosted.launchDescription")}</text>,
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
          const label = await DialogPrompt.show(dialog, t("proxy.hosted.createTitle"), {
            placeholder: t("proxy.hosted.sessionLabel"),
            value: defaultLabel,
            description: () => <text>{t("proxy.hosted.labelDescription")}</text>,
          })
          if (label === null) return
          const sessionLabel = label || defaultLabel
          if (sessions().some((session) => session.session_label === sessionLabel)) {
            await DialogAlert.show(dialog, t("proxy.hosted.createFailed"), t("proxy.hosted.labelExists", { label: sessionLabel }))
            return
          }
          try {
            const settings = await sdk.client.getSettings()
            const launched = await openHostedTerminal({
              executable,
              workerPort: worker.port,
              profile: worker.id,
              configDir: Global.Path.config,
              workspace: workspace || undefined,
              mode: "hosted-terminal",
              sessionLabel,
              opener: settings.settings.terminal.opener,
              tmuxSocketName: settings.settings.terminal.tmux.socket_name,
              tmuxHostSession: settings.settings.terminal.tmux.host_session,
            })
            if (launched) workerFrecency.record(worker.id)
            await refreshSessions()
            dialog.pop()
          } catch (err) {
            await DialogAlert.show(dialog, t("proxy.hosted.createFailed"), String(err instanceof Error ? err.message : err))
          }
        }}
      />
    ))
  }

  function changeSessionWorker(session: HostedSessionSnapshot) {
    const currentWorkerID = session.worker.id
    const currentWorker = sync.data.workers.find((worker) => worker.id === currentWorkerID)
    const currentLauncher = currentWorker?.launcher ?? "codex"
    const workers = sync.data.workers.filter((worker) => (worker.launcher ?? "codex") === currentLauncher)
    dialog.push(() => (
      <DialogWorkerPicker
        title={t("proxy.hosted.changeWorkerTitle", { session: session.session_label })}
        placeholder={t("proxy.worker.search")}
        workers={workers}
        onSelect={async (worker) => {
          try {
            const { launched } = await rebindHostedSession({
              client: sdk.client,
              session,
              worker,
              configDir: Global.Path.config,
              executable,
              launchMode: mode === "popup" ? "setup-only" : "open",
            })
            if (launched) workerFrecency.record(worker.id)
            await refreshSessions()
            dialog.pop()
          } catch (err) {
            await DialogAlert.show(dialog, t("proxy.hosted.changeWorkerFailed"), String(err instanceof Error ? err.message : err))
          }
        }}
      />
    ))
  }

  async function renameSession(session: HostedSessionSnapshot) {
    const label = await DialogPrompt.show(dialog, t("proxy.hosted.renameTitle"), {
      placeholder: t("proxy.hosted.sessionLabel"),
      value: session.session_label,
      description: () => <text>{t("proxy.hosted.labelDescription")}</text>,
    })
    if (label === null) return
    try {
      const updated = await sdk.client.patchHostedSession(session.session_id, { session_label: label })
      sync.set("hosted_sessions", updated.session_id, updated)
      await refreshSessions()
    } catch (err) {
      await DialogAlert.show(dialog, t("proxy.hosted.renameFailed"), String(err instanceof Error ? err.message : err))
    }
  }

  async function duplicateSession(session: HostedSessionSnapshot) {
    try {
      const duplicated = await sdk.client.duplicateHostedSession(session.session_id)
      const duplicatedWorkerID = duplicated.worker.id
      const settings = await sdk.client.getSettings()
      const launched = await openHostedTerminal({
        executable,
        workerPort: duplicated.worker.port,
        profile: duplicatedWorkerID,
        configDir: Global.Path.config,
        mode: "hosted-terminal",
        sessionID: duplicated.session_id,
        opener: settings.settings.terminal.opener,
        tmuxSocketName: settings.settings.terminal.tmux.socket_name,
        tmuxHostSession: settings.settings.terminal.tmux.host_session,
      })
      if (launched) workerFrecency.record(duplicatedWorkerID)
      sync.set("hosted_sessions", duplicated.session_id, duplicated)
      await refreshSessions()
    } catch (err) {
      await DialogAlert.show(dialog, t("proxy.hosted.duplicateFailed"), String(err instanceof Error ? err.message : err))
    }
  }

  async function markSessionUnread(session: HostedSessionSnapshot) {
    try {
      const updated = await sdk.client.markHostedSessionUnread(session.session_id)
      sync.set("hosted_sessions", updated.session_id, updated)
      await refreshSessions()
    } catch (err) {
      await DialogAlert.show(dialog, t("proxy.hosted.markUnreadFailed"), String(err instanceof Error ? err.message : err))
    }
  }

  const actions = createMemo<NonNullable<DialogSelectProps<HostedTerminalOption>["actions"]>>(() => [
    {
      command: "session.change_worker",
      title: t("proxy.hosted.actionWorker"),
      hidden: (option) => {
        if (option?.value.type !== "session") return true
        const session = option.value.session
        return session.turn.state === "running"
      },
      onTrigger: (option) => {
        if (option.value.type !== "session") return
        changeSessionWorker(option.value.session)
      },
    },
    {
      command: "session.rename",
      title: t("proxy.hosted.actionRename"),
      hidden: (option) => option?.value.type !== "session",
      onTrigger: (option) => {
        if (option.value.type !== "session") return
        void renameSession(option.value.session)
      },
    },
    {
      command: "session.duplicate",
      title: t("proxy.hosted.actionDuplicate"),
      hidden: (option) => option?.value.type !== "session",
      onTrigger: (option) => {
        if (option.value.type !== "session") return
        void duplicateSession(option.value.session)
      },
    },
    {
      command: "session.mark_unread",
      title: t("proxy.hosted.actionUnread"),
      hidden: (option) => {
        if (option?.value.type !== "session") return true
        const session = option.value.session
        const terminal =
          session.turn.state === "done" ||
          session.turn.state === "failed" ||
          session.turn.state === "interrupted"
        if (!terminal) return true
        return session.turn.unread
      },
      onTrigger: (option) => {
        if (option.value.type !== "session") return
        void markSessionUnread(option.value.session)
      },
    },
    {
      command: "session.delete",
      title: t("proxy.hosted.actionDelete"),
      hidden: (option) => option?.value.type !== "session",
      onTrigger: (option) => {
        if (option.value.type !== "session") return
        void deleteHostedTerminalSession({
          sdk,
          dialog,
          session: option.value.session,
          refreshSessions,
          t,
          onDeleted: (session) => {
            const next = { ...sync.data.hosted_sessions }
            delete next[session.session_id]
            sync.set("hosted_sessions", reconcile(next))
          },
        })
      },
    },
  ])

  return (
    <DialogSelect
      title={t("proxy.hosted.title")}
      onClose={props.onClose}
      locked={mode === "popup" && dialog.stack.length > 0}
      options={options()}
      placeholder={t("proxy.hosted.search")}
      actions={actions()}
      onSelect={(option) => {
        if (option.value.type === "refresh") {
          void refreshSessions()
          return
        }
        if (option.value.type === "create") {
          void createSession()
          return
        }
        if (option.value.type === "delete") {
          dialog.push(() => (
            <DialogHostedTerminalDelete
              initialSessions={sessions()}
              onSessionsChanged={(nextSessions) => {
                sync.set("hosted_sessions", reconcile(Object.fromEntries(nextSessions.map((session) => [session.session_id, session]))))
              }}
            />
          ))
          return
        }
        if (option.value.type === "bulk-actions") {
          dialog.push(() => <DialogHostedTerminalBulkActions sessions={sessions()} mode={mode} onComplete={refreshSessions} />)
          return
        }
        const session = option.value.session
        const currentWorkerID = session.worker.id
        void sdk.client.getSettings().then(async (settings) => {
          try {
            const launched = await openHostedTerminal({
              executable,
              workerPort: session.worker.port,
              profile: currentWorkerID,
              configDir: Global.Path.config,
              mode: "hosted-terminal",
              sessionID: session.session_id,
              opener: settings.settings.terminal.opener,
              tmuxSocketName: settings.settings.terminal.tmux.socket_name,
              tmuxHostSession: settings.settings.terminal.tmux.host_session,
            })
            if (launched) workerFrecency.record(currentWorkerID)
            if (mode === "popup") await refreshSessions()
          } catch (err) {
            await DialogAlert.show(dialog, t("proxy.hosted.openFailed"), String(err instanceof Error ? err.message : err))
          }
        })
      }}
    />
  )
}
