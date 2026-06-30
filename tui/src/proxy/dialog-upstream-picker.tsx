import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { useTheme } from "../context/theme"
import { createMemo } from "solid-js"

export function DialogUpstreamPicker(props: { worker: WorkerSummary }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { theme } = useTheme()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.upstreams.map((p) => {
      const probe = sync.data.upstreamProbes[p.name]
      return {
        title: p.name,
        value: p.name,
        description: `${p.base_url}${p.has_api_key ? "" : " (no key)"}`,
        category: p.name === props.worker.upstream.name ? "Current" : "Available",
        footer: !probe ? <span style={{ fg: theme.textMuted }}>—</span>
          : probe.ok ? <span style={{ fg: theme.success }}>●{probe.latency_ms}ms</span>
          : probe.degraded ? <span style={{ fg: theme.warning }}>▲{probe.error || `${probe.latency_ms}ms`}</span>
          : <span style={{ fg: theme.error }}>✕{probe.error || probe.status_code}</span>,
      }
    }),
  )

  return (
    <DialogSelect
      title={`Switch Upstream: ${props.worker.name}`}
      options={options()}
      placeholder="Search upstreams..."
      current={props.worker.upstream.name}
      onSelect={async (opt) => {
        if (opt.value === props.worker.upstream.name) {
          dialog.clear()
          return
        }
        try {
          await sdk.client.patchWorker(props.worker.port, { upstream: opt.value })
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
