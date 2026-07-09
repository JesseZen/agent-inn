import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import type { WorkerSummary } from "../context/sdk"

function upstreamLabel(worker: WorkerSummary) {
  return worker.upstream.missing ? `missing upstream: ${worker.upstream_id}` : worker.upstream.name
}

export function DialogWorkerPicker(props: {
  title: string
  placeholder: string
  workers?: WorkerSummary[]
  recentWorkers?: WorkerSummary[]
  onSelect: (worker: WorkerSummary) => void
}) {
  const sync = useSync()

  const options = createMemo<DialogSelectOption<number>[]>(() => {
    const recentWorkers = props.recentWorkers ?? []
    const recentIDs = new Set(recentWorkers.map((worker) => worker.id))
    const toOption = (worker: WorkerSummary, category: string) => ({
      title: worker.name,
      value: worker.port,
      description: `:${worker.port} • ${upstreamLabel(worker)} • ${worker.status}`,
      category,
      onSelect: () => props.onSelect(worker),
    })

    return [
      ...recentWorkers.map((worker) => toOption(worker, "Recent")),
      ...(props.workers ?? sync.data.workers)
        .filter((worker) => !recentIDs.has(worker.id))
        .map((worker) => toOption(worker, worker.status === "running" ? "Running" : "Stopped")),
    ]
  })

  return <DialogSelect title={props.title} options={options()} placeholder={props.placeholder} />
}
