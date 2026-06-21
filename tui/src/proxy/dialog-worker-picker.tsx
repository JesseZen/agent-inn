import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import type { WorkerSummary } from "../context/sdk"

export function DialogWorkerPicker(props: {
  title: string
  placeholder: string
  onSelect: (worker: WorkerSummary) => void
}) {
  const sync = useSync()

  const options = createMemo<DialogSelectOption<number>[]>(() =>
    sync.data.workers.map((worker) => ({
      title: worker.name,
      value: worker.port,
      description: `:${worker.port} • ${worker.upstream.name} • ${worker.status}`,
      category: worker.status === "running" ? "Running" : "Stopped",
      onSelect: () => props.onSelect(worker),
    })),
  )

  return <DialogSelect title={props.title} options={options()} placeholder={props.placeholder} />
}
