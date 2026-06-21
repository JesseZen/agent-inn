import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useDialog } from "../ui/dialog"
import { createMemo } from "solid-js"

export function DialogWorkers() {
  const sync = useSync()
  const dialog = useDialog()

  const options = createMemo<DialogSelectOption<number>[]>(() =>
    sync.data.workers.map((w) => ({
      title: w.name,
      value: w.port,
      description: `:${w.port} • ${w.upstream.name} • ${w.status}`,
      category: w.status === "running" ? "Running" : "Stopped",
    })),
  )

  return (
    <DialogSelect
      title="Workers"
      options={options()}
      placeholder="Search workers..."
      onSelect={(opt) => {
        dialog.clear()
        // TODO: navigate to worker detail
      }}
    />
  )
}
