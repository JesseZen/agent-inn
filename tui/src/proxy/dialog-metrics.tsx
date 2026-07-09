import { createEffect, createMemo, createSignal, on, onMount } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useSDK, type MetricsRangeName, type MetricsResponse } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import { DialogWorkerStatus } from "./dialog-worker-status"

type MetricsOption =
  | { type: "range"; range: MetricsRangeName }
  | { type: "worker"; port: number }

const RANGES: Array<{ title: string; value: MetricsRangeName }> = [
  { title: "Today", value: "today" },
  { title: "Last 24h", value: "last_24h" },
]

function formatTokens(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M tok`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K tok`
  return `${value} tok`
}

export function DialogMetrics() {
  const sdk = useSDK()
  const sync = useSync()
  const dialog = useDialog()
  const toast = useToast()
  const [range, setRange] = createSignal<MetricsRangeName>("today")
  const [metrics, setMetrics] = createSignal<MetricsResponse>()

  async function loadMetrics(nextRange: MetricsRangeName) {
    try {
      setRange(nextRange)
      setMetrics(await sdk.client.getMetrics(nextRange))
    } catch (err) {
      toast.error(err)
    }
  }

  onMount(() => {
    void loadMetrics("today")
  })

  createEffect(
    on(
      () => sync.data.metrics_generation,
      () => {
        void loadMetrics(range())
      },
      { defer: true },
    ),
  )

  const options = createMemo<DialogSelectOption<MetricsOption>[]>(() => [
    ...RANGES.map((item) => ({
      title: item.title,
      value: { type: "range" as const, range: item.value },
      description: item.value === range() ? "selected" : "",
      category: "Range",
      onSelect: () => void loadMetrics(item.value),
    })),
    ...(metrics()?.workers ?? []).map((worker) => ({
      title: worker.worker,
      value: { type: "worker" as const, port: worker.port },
      description: `RPM ${worker.live.rpm} • TPM ${worker.live.tpm} • ${formatTokens(worker.totals.total_tokens)} • ${worker.totals.requests} req • ${worker.totals.errors} err • ${worker.totals.avg_latency_ms} ms`,
      footer: `:${worker.port} ${worker.status}`,
      category: "Workers",
      onSelect: async () => {
        const detail = await sdk.client.getWorker(worker.port)
        dialog.replace(() => <DialogWorkerStatus worker={detail} />)
      },
    })),
  ])

  return (
    <DialogSelect
      title="Worker Metrics"
      options={options()}
      placeholder="Search metrics..."
    />
  )
}
