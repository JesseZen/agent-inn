import { createMemo, createSignal } from "solid-js"
import { useTuiConfig } from "../config"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { useDialog } from "../ui/dialog"
import { DialogAlert } from "../ui/dialog-alert"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useBindings, useCommandShortcut } from "../keymap"
import { Global } from "@agent-inn/core/global"
import type { HostedSessionSummary } from "./backend"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { rebindHostedSession } from "./hosted-session-rebind"
import { useWorkerFrecency } from "./worker-frecency-context"
import { useLanguage } from "../context/language"

type BulkSessionOption =
  | { type: "change-worker" }
  | { type: "delete" }
  | { type: "session"; session: HostedSessionSummary }

export function DialogHostedTerminalBulkActions(props: {
  sessions: HostedSessionSummary[]
  mode: "dialog" | "popup"
  onComplete: () => Promise<void>
}) {
  const sdk = useSDK()
  const sync = useSync()
  const dialog = useDialog()
  const tuiConfig = useTuiConfig()
  const workerFrecency = useWorkerFrecency()
  const { t } = useLanguage()
  const toggleShortcut = useCommandShortcut("session.bulk.toggle")
  const [selectedIDs, setSelectedIDs] = createSignal(new Set<string>())
  const [highlightedSession, setHighlightedSession] = createSignal<HostedSessionSummary>()
  const selectedSessions = createMemo(() => props.sessions.filter((session) => selectedIDs().has(session.session_id)))
  const compatibleWorkers = createMemo(() => {
    const launchers = new Set(
      selectedSessions().map((session) => {
        const worker = sync.data.workers.find((worker) => worker.id === (session.worker_id ?? session.worker_name))
        return worker?.launcher ?? "codex"
      }),
    )
    if (launchers.size !== 1) return []
    const launcher = [...launchers][0]
    return sync.data.workers.filter((worker) => (worker.launcher ?? "codex") === launcher)
  })

  function toggleSession(session: HostedSessionSummary) {
    setSelectedIDs((ids) => {
      const next = new Set(ids)
      if (next.has(session.session_id)) next.delete(session.session_id)
      else next.add(session.session_id)
      return next
    })
  }

  useBindings(() => ({
    commands: [
      {
        name: "session.bulk.toggle",
        title: t("proxy.hosted.toggleCommand"),
        category: t("proxy.hosted.categoryDialog"),
        run() {
          const session = highlightedSession()
          if (session) toggleSession(session)
        },
      },
    ],
    bindings: tuiConfig.keybinds.get("session.bulk.toggle"),
  }))

  const options = createMemo<DialogSelectOption<BulkSessionOption>[]>(() => [
    {
      title: t("proxy.hosted.bulkChange"),
      value: { type: "change-worker" },
      description: t("proxy.hosted.bulkChangeDescription"),
      category: t("proxy.hosted.categoryAction"),
    },
    {
      title: t("proxy.hosted.bulkDelete"),
      value: { type: "delete" },
      description: t("proxy.hosted.bulkDeleteDescription"),
      category: t("proxy.hosted.categoryAction"),
    },
    ...props.sessions.map((session) => {
      const worker = session.worker?.missing ? t("proxy.hosted.missingWorker", { id: session.worker_id ?? session.worker_name }) : session.worker?.name ?? session.worker_name
      return {
        title: session.session_label,
        value: { type: "session" as const, session },
        description: `${worker} • ${session.status}${session.turn_state === "running" ? " • running" : ""}`,
        category: t("proxy.hosted.categorySessions"),
        gutter: () => <text>{selectedIDs().has(session.session_id) ? "✓" : "○"}</text>,
      }
    }),
  ])

  function chooseWorker() {
    const sessions = selectedSessions()
    if (sessions.length === 0) return
    if (sessions.some((session) => session.turn_state === "running")) {
      void DialogAlert.show(dialog, t("proxy.hosted.changeWorkerFailed"), t("proxy.hosted.stopRunning"))
      return
    }
    dialog.push(() => (
      <DialogWorkerPicker
        title={t("proxy.hosted.bulkChange")}
        placeholder={t("proxy.hosted.compatibleSearch")}
        workers={compatibleWorkers()}
        onSelect={(worker) => {
          void (async () => {
            try {
              let launched = false
              for (const session of sessions) {
                const result = await rebindHostedSession({
                  client: sdk.client,
                  session,
                  worker,
                  configDir: Global.Path.config,
                  executable: import.meta.env?.AINN_EXECUTABLE || undefined,
                  launchMode: props.mode === "popup" ? "setup-only" : "open",
                })
                launched ||= result.launched
              }
              if (launched) workerFrecency.record(worker.id)
              await props.onComplete()
              dialog.pop()
              dialog.pop()
            } catch (err) {
              await DialogAlert.show(dialog, t("proxy.hosted.changeWorkerFailed"), String(err instanceof Error ? err.message : err))
            }
          })()
        }}
      />
    ))
  }

  function deleteSelected() {
    const sessions = selectedSessions()
    if (sessions.length === 0) return
    void (async () => {
      const confirmed = await DialogConfirm.show(
        dialog,
        t("proxy.hosted.deleteManyConfirmTitle"),
        t("proxy.hosted.deleteSelectedConfirm", { count: sessions.length, plural: sessions.length === 1 ? "" : "s" }),
      )
      if (!confirmed) return
      try {
        for (const session of sessions) {
          await sdk.client.deleteHostedSession(session.session_id)
        }
        await props.onComplete()
        dialog.pop()
      } catch (err) {
        await DialogAlert.show(dialog, t("proxy.hosted.deleteManyFailed"), String(err instanceof Error ? err.message : err))
      }
    })()
  }

  return (
    <DialogSelect
      title={t("proxy.hosted.bulk")}
      options={options()}
      placeholder={t("proxy.hosted.bulkSearch")}
      footerHints={[
        { title: t("proxy.hosted.selected", { count: selectedSessions().length }), label: "" },
        { title: t("proxy.hosted.toggleLabel"), label: toggleShortcut() },
      ]}
      onMove={(option) => {
        setHighlightedSession(option.value.type === "session" ? option.value.session : undefined)
      }}
      onSelect={(option) => {
        if (option.value.type === "change-worker") {
          chooseWorker()
          return
        }
        if (option.value.type === "delete") {
          deleteSelected()
          return
        }
        toggleSession(option.value.session)
      }}
    />
  )
}
