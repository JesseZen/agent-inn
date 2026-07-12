import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { showNewWorkerDialog } from "./dialog-new-worker"
import { DialogWorkerStatus } from "./dialog-worker-status"
import { useLanguage } from "../context/language"
import type { Translate } from "../i18n/en"

function upstreamLabel(worker: { upstream: { name: string; missing?: boolean }; upstream_id: string }, t: Translate) {
  return worker.upstream.missing ? t("proxy.worker.missingUpstream", { id: worker.upstream_id }) : worker.upstream.name
}

export function DialogWorkers() {
  const sync = useSync()
  const dialog = useDialog()
  const sdk = useSDK()
  const toast = useToast()
  const { t } = useLanguage()

  const options = createMemo<DialogSelectOption<string>[]>(() => [
    { title: t("proxy.worker.create"), value: "create", description: t("proxy.worker.createDescription"), category: t("common.actions") },
    ...sync.data.workers.map((w) => ({
      title: w.name,
      value: `edit:${w.id}`,
      description: `:${w.port} • ${w.launcher ?? "codex"} • ${upstreamLabel(w, t)} • ${w.status}`,
      category: t("proxy.dashboard.workers"),
    })),
  ])

  return (
    <DialogSelect
      title={t("proxy.worker.manage")}
      options={options()}
      placeholder={t("proxy.worker.search")}
      onSelect={async (opt) => {
        if (opt.value === "create") {
          void showNewWorkerDialog(dialog as never, sdk.client as never, toast as never, t)
          return
        }
        const id = opt.value.slice("edit:".length)
        const worker = sync.data.workers.find((w) => w.id === id)
        if (!worker) return
        dialog.push(() => <DialogWorkerStatus worker={worker} management />)
      }}
    />
  )
}
