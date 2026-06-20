import { useDialog } from "../ui/dialog"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { DialogWorkerStatus } from "./dialog-worker-status"

export function DialogStatus() {
  const dialog = useDialog()

  return (
    <DialogWorkerPicker
      title="Worker Status"
      placeholder="Search workers..."
      onSelect={(worker) => {
        dialog.replace(() => <DialogWorkerStatus worker={worker} />)
      }}
    />
  )
}
