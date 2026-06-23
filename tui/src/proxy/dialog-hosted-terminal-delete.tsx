import { createMemo, createSignal, onMount } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSDK } from "../context/sdk"
import { useDialog, type DialogContext } from "../ui/dialog"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogAlert } from "../ui/dialog-alert"
import type { HostedSessionSummary } from "./backend"

type SDKContext = ReturnType<typeof useSDK>
type HostedTerminalDeleteOption =
  | {
      type: "gc-stale"
    }
  | {
      type: "session"
      session: HostedSessionSummary
    }

export async function deleteHostedTerminalSession(input: {
  sdk: SDKContext
  dialog: DialogContext
  session: HostedSessionSummary
  refreshSessions: () => Promise<void>
}) {
  const { sdk, dialog, session, refreshSessions } = input
  const confirmed = await DialogConfirm.show(
    dialog,
    "Delete hosted session",
    `Delete ${session.session_label}? This will remove the CAP session record${session.status === "active" ? " and close its tmux window" : ""}.`,
  )
  if (!confirmed) return
  try {
    await sdk.client.deleteHostedSession(session.session_id)
    await refreshSessions()
  } catch (err) {
    await DialogAlert.show(dialog, "Delete hosted session failed", String(err instanceof Error ? err.message : err))
  }
}

export function DialogHostedTerminalDelete() {
  const sdk = useSDK()
  const dialog = useDialog()
  const [sessions, setSessions] = createSignal<HostedSessionSummary[]>([])

  async function refreshSessions() {
    setSessions(await sdk.client.listHostedSessions())
  }

  onMount(() => {
    void refreshSessions()
  })

  const staleSessions = createMemo(() => sessions().filter((session) => session.status === "stale"))
  const options = createMemo<DialogSelectOption<HostedTerminalDeleteOption>[]>(() => [
    ...(staleSessions().length > 0
      ? [
          {
            title: "GC stale sessions",
            value: { type: "gc-stale" as const },
            description: "Delete every stale session currently shown in TUI",
            category: "Action",
          },
        ]
      : []),
    ...sessions()
      .filter((session) => session.status === "active" || session.status === "stale")
      .map((session) => ({
        title: session.session_label,
        value: { type: "session" as const, session },
        description: `${session.worker_name} • ${session.status}`,
        category: session.status === "active" ? "Active sessions" : "Stale sessions",
      })),
  ])

  return (
    <DialogSelect
      title="Delete Hosted Session"
      options={options()}
      placeholder="Select session to delete..."
      onSelect={(option) => {
        if (option.value.type === "gc-stale") {
          void (async () => {
            const confirmed = await DialogConfirm.show(
              dialog,
              "Delete hosted sessions",
              "Delete all stale sessions? This will remove every stale CAP session record currently shown in TUI. Active sessions will not be touched.",
            )
            if (!confirmed) return
            try {
              for (const session of staleSessions()) {
                await sdk.client.deleteHostedSession(session.session_id)
              }
              await refreshSessions()
            } catch (err) {
              await DialogAlert.show(dialog, "Delete hosted sessions failed", String(err instanceof Error ? err.message : err))
            }
          })()
          return
        }
        void deleteHostedTerminalSession({ sdk, dialog, session: option.value.session, refreshSessions })
      }}
    />
  )
}
