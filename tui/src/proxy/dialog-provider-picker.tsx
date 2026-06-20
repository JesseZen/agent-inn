import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"

export function DialogProviderPicker(props: { worker: WorkerSummary }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.providers.map((p) => ({
      title: p.name,
      value: p.name,
      description: `${p.base_url}${p.has_api_key ? "" : " (no key)"}`,
      category: p.name === props.worker.provider.name ? "Current" : "Available",
    })),
  )

  return (
    <DialogSelect
      title={`Switch Upstream: ${props.worker.name}`}
      options={options()}
      placeholder="Search upstreams..."
      current={props.worker.provider.name}
      onSelect={async (opt) => {
        if (opt.value === props.worker.provider.name) {
          dialog.clear()
          return
        }
        try {
          await sdk.client.patchWorker(props.worker.port, { provider: opt.value })
          await sync.bootstrap({ fatal: false })
          toast.show({ message: `Switched ${props.worker.name} to ${opt.value}`, variant: "success" })
        } catch (err) {
          toast.error(err)
        }
        dialog.clear()
      }}
    />
  )
}
