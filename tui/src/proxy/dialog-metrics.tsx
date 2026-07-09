import { createEffect, createMemo, createSignal, on, onCleanup, onMount } from "solid-js"
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

const METRICS_REFRESH_DELAY_MS = 100

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
  let refreshTimer: ReturnType<typeof setTimeout> | undefined
  let pendingRange: MetricsRangeName | undefined
  let metricsRequestInFlight = false
  let metricsRequestGeneration = 0

  async function requestMetrics() {
    if (metricsRequestInFlight || !pendingRange) return
    const nextRange = pendingRange
    const requestGeneration = metricsRequestGeneration
    pendingRange = undefined
    metricsRequestInFlight = true
    try {
      const result = await sdk.client.getMetrics(nextRange)
      if (requestGeneration === metricsRequestGeneration) setMetrics(result)
    } catch (err) {
      if (requestGeneration === metricsRequestGeneration) toast.error(err)
    } finally {
      metricsRequestInFlight = false
      if (pendingRange) void requestMetrics()
    }
  }

  function loadMetrics(nextRange: MetricsRangeName) {
    if (refreshTimer) {
      clearTimeout(refreshTimer)
      refreshTimer = undefined
    }
    setRange(nextRange)
    metricsRequestGeneration += 1
    pendingRange = nextRange
    void requestMetrics()
  }

  function scheduleMetricsRefresh() {
    metricsRequestGeneration += 1
    if (refreshTimer) return
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined
      pendingRange = range()
      void requestMetrics()
    }, METRICS_REFRESH_DELAY_MS)
  }

  onMount(() => {
    loadMetrics("today")
  })

  createEffect(
    on(
      () => sync.data.metrics_generation,
      () => {
        scheduleMetricsRefresh()
      },
      { defer: true },
    ),
  )

  onCleanup(() => {
    if (refreshTimer) clearTimeout(refreshTimer)
  })

  const options = createMemo<DialogSelectOption<MetricsOption>[]>(() => [
    ...RANGES.map((item) => ({
      title: item.title,
      value: { type: "range" as const, range: item.value },
      description: item.value === range() ? "selected" : "",
      category: "Range",
      onSelect: () => loadMetrics(item.value),
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
