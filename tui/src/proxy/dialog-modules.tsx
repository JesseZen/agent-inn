import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"

export function DialogModules(props: { worker: WorkerSummary }) {
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const modules = props.worker.modules ?? {}
    return Object.entries(modules).map(([name, cfg]) => ({
      title: `${cfg.enabled ? "✓" : "○"} ${name}`,
      value: name,
      description: cfg.enabled ? "enabled" : "disabled",
      category: cfg.enabled ? "Enabled" : "Disabled",
      onSelect: async () => {
        try {
          await sdk.client.toggleModule(props.worker.port, name)
          toast.show({ message: `${name} ${cfg.enabled ? "disabled" : "enabled"}`, variant: "success" })
        } catch (err) {
          toast.error(err)
        }
        dialog.clear()
      },
    }))
  })

  return (
    <DialogSelect
      title={`Modules: ${props.worker.name} (:${props.worker.port})`}
      options={options()}
      placeholder="Search modules..."
    />
  )
}
