import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import type { WorkerSummary } from "../context/sdk"
import { useLanguage } from "../context/language"
import type { Translate } from "../i18n/en"

function upstreamLabel(worker: WorkerSummary, t: Translate) {
  return worker.upstream.missing ? t("proxy.worker.missingUpstream", { id: worker.upstream_id }) : worker.upstream.name
}

export function DialogWorkerPicker(props: {
  title: string
  placeholder: string
  workers?: WorkerSummary[]
  recentWorkers?: WorkerSummary[]
  onSelect: (worker: WorkerSummary) => void
}) {
  const sync = useSync()
  const { t } = useLanguage()

  const options = createMemo<DialogSelectOption<number>[]>(() => {
    const recentWorkers = props.recentWorkers ?? []
    const recentIDs = new Set(recentWorkers.map((worker) => worker.id))
    const toOption = (worker: WorkerSummary, category: string) => ({
      title: worker.name,
      value: worker.port,
      description: `:${worker.port} • ${upstreamLabel(worker, t)} • ${worker.status}`,
      category,
      onSelect: () => props.onSelect(worker),
    })

    return [
      ...recentWorkers.map((worker) => toOption(worker, t("proxy.launch.categoryRecent"))),
      ...(props.workers ?? sync.data.workers)
        .filter((worker) => !recentIDs.has(worker.id))
        .map((worker) => toOption(worker, worker.status === "running" ? t("proxy.batch.categoryRunning") : t("proxy.batch.categoryStopped"))),
    ]
  })

  return <DialogSelect title={props.title} options={options()} placeholder={props.placeholder} />
}
