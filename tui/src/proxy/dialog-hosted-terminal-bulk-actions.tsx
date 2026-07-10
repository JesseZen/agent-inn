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
        title: "Toggle hosted session selection",
        category: "Dialog",
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
      title: "Change worker",
      value: { type: "change-worker" },
      description: "Move selected sessions to one worker",
      category: "Action",
    },
    {
      title: "Delete selected",
      value: { type: "delete" },
      description: "Delete selected hosted sessions",
      category: "Action",
    },
    ...props.sessions.map((session) => {
      const worker = session.worker?.missing ? `missing worker: ${session.worker_id}` : session.worker?.name ?? session.worker_name
      return {
        title: session.session_label,
        value: { type: "session" as const, session },
        description: `${worker} • ${session.status}${session.turn_state === "running" ? " • running" : ""}`,
        category: "Hosted sessions",
        gutter: () => <text>{selectedIDs().has(session.session_id) ? "✓" : "○"}</text>,
      }
    }),
  ])

  function chooseWorker() {
    const sessions = selectedSessions()
    if (sessions.length === 0) return
    if (sessions.some((session) => session.turn_state === "running")) {
      void DialogAlert.show(dialog, "Change hosted session worker failed", "Stop running sessions before changing their worker.")
      return
    }
    dialog.push(() => (
      <DialogWorkerPicker
        title="Change worker"
        placeholder="Search compatible workers..."
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
              await DialogAlert.show(dialog, "Change hosted session worker failed", String(err instanceof Error ? err.message : err))
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
        "Delete hosted sessions",
        `Delete ${sessions.length} selected hosted session${sessions.length === 1 ? "" : "s"}?`,
      )
      if (!confirmed) return
      try {
        for (const session of sessions) {
          await sdk.client.deleteHostedSession(session.session_id)
        }
        await props.onComplete()
        dialog.pop()
      } catch (err) {
        await DialogAlert.show(dialog, "Delete hosted sessions failed", String(err instanceof Error ? err.message : err))
      }
    })()
  }

  return (
    <DialogSelect
      title="Bulk session actions"
      options={options()}
      placeholder="Search hosted sessions..."
      footerHints={[
        { title: `${selectedSessions().length} selected`, label: "" },
        { title: "toggle", label: toggleShortcut() },
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
