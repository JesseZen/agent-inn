import { createMemo } from "solid-js"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"

export function DialogPoolPicker(props: { worker: WorkerSummary }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const options = createMemo<DialogSelectOption<string>[]>(() => [
    { title: "None", value: "", description: "Disable automatic failover", category: props.worker.upstream_pool ? "Options" : "Current" },
    ...sync.data.upstreamPools.map((pool) => ({
      title: pool.name,
      value: pool.id,
      description: pool.upstreams.join(" -> "),
      details: pool.active_upstream ? [`active: ${pool.active_upstream}`] : undefined,
      category: pool.id === props.worker.upstream_pool ? "Current" : "Pools",
    })),
  ])

  return (
    <DialogSelect
      title={`Fallback Pool: ${props.worker.name}`}
      options={options()}
      placeholder="Select a pool..."
      current={props.worker.upstream_pool ?? ""}
      onSelect={async (option) => {
        if (option.value === (props.worker.upstream_pool ?? "")) {
          dialog.pop()
          return
        }
        try {
          if (!option.value) {
            await sdk.client.patchWorker(props.worker.id, { upstream_pool: "" })
          } else {
            const pool = sync.data.upstreamPools.find((item) => item.id === option.value)!
            await sdk.client.patchWorker(props.worker.id, {
              upstream_pool: pool.id,
              upstream_id: pool.active_upstream || pool.upstreams[0]!,
            })
          }
          await sync.bootstrap({ fatal: false })
          toast.show({ message: `Saved ${props.worker.name} fallback pool: ${option.value || "none"}`, variant: "success" })
          dialog.pop()
        } catch (error) {
          toast.error(error)
        }
      }}
    />
  )
}
