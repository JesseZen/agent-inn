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

const METRICS_REFRESH_DELAY_MS = 1000

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
  let disposed = false

  async function requestMetrics() {
    if (disposed || metricsRequestInFlight || !pendingRange) return
    const nextRange = pendingRange
    const requestGeneration = metricsRequestGeneration
    pendingRange = undefined
    metricsRequestInFlight = true
    try {
      const result = await sdk.client.getMetrics(nextRange)
      if (!disposed && requestGeneration === metricsRequestGeneration) setMetrics(result)
    } catch (err) {
      if (!disposed && requestGeneration === metricsRequestGeneration) toast.error(err)
    } finally {
      metricsRequestInFlight = false
      if (!disposed && pendingRange) void requestMetrics()
    }
  }

  function loadMetrics(nextRange: MetricsRangeName) {
    if (disposed) return
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
    if (disposed) return
    metricsRequestGeneration += 1
    if (refreshTimer) return
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined
      if (disposed) return
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
    disposed = true
    metricsRequestGeneration += 1
    if (refreshTimer) clearTimeout(refreshTimer)
    refreshTimer = undefined
    pendingRange = undefined
  })

  const options = createMemo<DialogSelectOption<MetricsOption>[]>(() => {
    const result = metrics()
    return [
      ...RANGES.map((item) => {
        const selected = item.value === range()
        const details: string[] = []
        if (selected && result?.query_limited && result.skipped_records > 0) {
          details.push(`query limit; totals incomplete; ${result.skipped_records} unreadable records`)
        } else if (selected && result?.query_limited) {
          details.push("query limit: persisted totals incomplete")
        } else if (selected && result && result.skipped_records > 0) {
          details.push(`${result.skipped_records} persisted records unreadable`)
        }
        return {
          title: selected && result && result.persistence_errors > 0
            ? `${item.title} • ${result.persistence_errors} manager-session persistence error${result.persistence_errors === 1 ? "" : "s"}`
            : item.title,
          value: { type: "range" as const, range: item.value },
          description: selected ? "selected" : "",
          details: details.length > 0 ? details : undefined,
          category: "Range",
          onSelect: () => loadMetrics(item.value),
        }
      }),
      ...(result?.workers ?? []).map((worker) => {
        const liveRates = worker.live_available
          ? `RPM ${worker.live.rpm} • TPM ${worker.live.tpm}`
          : "RPM unavailable • TPM unavailable"
        return {
          title: worker.live.dropped_events > 0
            ? `${worker.worker} • ${worker.live.dropped_events} live events dropped`
            : worker.worker,
          value: { type: "worker" as const, port: worker.port },
          description: `${liveRates} • ${formatTokens(worker.totals.total_tokens)} • ${worker.totals.requests} req • ${worker.totals.errors} err • ${worker.totals.avg_latency_ms} ms`,
          details: worker.totals.unknown_usage_requests > 0
            ? [`${worker.totals.unknown_usage_requests} requests missing usage; token totals exclude them`]
            : undefined,
          footer: `:${worker.port} ${worker.status}`,
          ...(worker.status === "removed" ? {} : {
            onSelect: async () => {
              try {
                const detail = await sdk.client.getWorker(worker.port)
                if (disposed) return
                dialog.replace(() => <DialogWorkerStatus worker={detail} />)
              } catch (err) {
                if (!disposed) toast.error(err)
              }
            },
          }),
        }
      }),
    ]
  })

  return (
    <DialogSelect
      title="Worker Metrics"
      options={options()}
      placeholder="Search metrics..."
    />
  )
}
