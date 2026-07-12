import { createMemo, createSignal, onMount } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSDK } from "../context/sdk"
import { useDialog, type DialogContext } from "../ui/dialog"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogAlert } from "../ui/dialog-alert"
import type { HostedSessionSummary } from "./backend"
import { useLanguage } from "../context/language"
import type { Translate } from "../i18n/en"

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
  t: Translate
  onDeleted?: (session: HostedSessionSummary) => Promise<void> | void
}) {
  const { sdk, dialog, session, refreshSessions, onDeleted } = input
  const confirmed = await DialogConfirm.show(
    dialog,
    input.t("proxy.hosted.deleteConfirmTitle"),
    session.status === "active"
      ? input.t("proxy.hosted.deleteActiveConfirm", { session: session.session_label })
      : input.t("proxy.hosted.deleteConfirm", { session: session.session_label, suffix: "" }),
  )
  if (!confirmed) return
  try {
    await sdk.client.deleteHostedSession(session.session_id)
    if (onDeleted) {
      await onDeleted(session)
      return
    }
    await refreshSessions()
  } catch (err) {
    await DialogAlert.show(dialog, input.t("proxy.hosted.deleteFailed"), String(err instanceof Error ? err.message : err))
  }
}

export function DialogHostedTerminalDelete(
  props: { initialSessions?: HostedSessionSummary[]; onSessionsChanged?: (sessions: HostedSessionSummary[]) => void } = {},
) {
  const sdk = useSDK()
  const dialog = useDialog()
  const [sessions, setSessions] = createSignal<HostedSessionSummary[]>(props.initialSessions ?? [])
  const { t } = useLanguage()

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
            title: t("proxy.hosted.gcStale"),
            value: { type: "gc-stale" as const },
            description: t("proxy.hosted.gcDescription"),
            category: t("proxy.hosted.categoryAction"),
          },
        ]
      : []),
    ...sessions()
      .filter((session) => session.status === "active" || session.status === "stale")
      .map((session) => {
        const worker = session.worker?.missing ? t("proxy.hosted.missingWorker", { id: session.worker_id ?? session.worker_name }) : session.worker?.name ?? session.worker_name
        return {
          title: session.session_label,
          value: { type: "session" as const, session },
          description: `${worker} • ${session.status}`,
          category: session.status === "active" ? t("proxy.hosted.activeCategory") : t("proxy.hosted.staleCategory"),
        }
      }),
  ])

  return (
    <DialogSelect
      title={t("proxy.hosted.deleteTitle")}
      options={options()}
      placeholder={t("proxy.hosted.deleteSearch")}
      onSelect={(option) => {
        if (option.value.type === "gc-stale") {
          void (async () => {
            const confirmed = await DialogConfirm.show(
              dialog,
              t("proxy.hosted.deleteManyConfirmTitle"),
              t("proxy.hosted.deleteAllConfirm"),
            )
            if (!confirmed) return
            try {
              const staleSessionIDs = new Set(staleSessions().map((session) => session.session_id))
              for (const session of staleSessions()) {
                await sdk.client.deleteHostedSession(session.session_id)
              }
              const nextSessions = sessions().filter((session) => !staleSessionIDs.has(session.session_id))
              setSessions(nextSessions)
              props.onSessionsChanged?.(nextSessions)
            } catch (err) {
              await DialogAlert.show(dialog, t("proxy.hosted.deleteManyFailed"), String(err instanceof Error ? err.message : err))
            }
          })()
          return
        }
        void deleteHostedTerminalSession({
          sdk,
          dialog,
          session: option.value.session,
          refreshSessions,
          t,
          onDeleted: (session) => {
            const nextSessions = sessions().filter((item) => item.session_id !== session.session_id)
            setSessions(nextSessions)
            props.onSessionsChanged?.(nextSessions)
          },
        })
      }}
    />
  )
}
