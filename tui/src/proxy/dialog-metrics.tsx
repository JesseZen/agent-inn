import { TextAttributes } from "@opentui/core"
import { createEffect, createMemo, createSignal, For, on, onCleanup, onMount } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useSDK, type MetricsRangeName, type MetricsResponse } from "../context/sdk"
import { useSync } from "../context/sync"
import { useTheme } from "../context/theme"
import { useToast } from "../ui/toast"
import { DialogWorkerStatus } from "./dialog-worker-status"
import { useLanguage } from "../context/language"
import type { TranslationKey } from "../i18n/en"

type MetricsOption = { type: "worker"; port: number }

const RANGES: Array<{ title: TranslationKey; value: MetricsRangeName }> = [
  { title: "proxy.metrics.today", value: "today" },
  { title: "proxy.metrics.last24Hours", value: "last_24h" },
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
  const { theme } = useTheme()
  const { t } = useLanguage()
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
    dialog.setSize("large")
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

  const rangeDetails = createMemo(() => {
    const result = metrics()
    if (!result) return []
    const details: string[] = []
    if (result.persistence_errors > 0) {
      details.push(t("proxy.metrics.persistenceCount", { count: result.persistence_errors, plural: result.persistence_errors === 1 ? "" : "s" }))
    }
    if (result.query_limited && result.skipped_records > 0) {
      details.push(t("proxy.metrics.queryIncomplete", { count: result.skipped_records }))
    } else if (result.query_limited) {
      details.push(t("proxy.metrics.queryLimited"))
    } else if (result.skipped_records > 0) {
      details.push(t("proxy.metrics.unreadable", { count: result.skipped_records }))
    }
    return details
  })

  const options = createMemo<DialogSelectOption<MetricsOption>[]>(() => {
    const result = metrics()
    return [
      ...(result?.workers ?? []).map((worker) => {
        const liveRates = worker.live_available
          ? `RPM ${worker.live.rpm} • TPM ${worker.live.tpm}`
          : t("proxy.metrics.rpmUnavailable")
        const details: string[] = []
        if (worker.live.dropped_events > 0) {
          details.push(t("proxy.metrics.dropped", { count: worker.live.dropped_events }))
        }
        if (worker.totals.unknown_usage_requests > 0) {
          details.push(t("proxy.metrics.missingUsage", { count: worker.totals.unknown_usage_requests }))
        }
        return {
          title: worker.worker,
          value: { type: "worker" as const, port: worker.port },
          description: `${liveRates} • ${formatTokens(worker.totals.total_tokens)} • ${worker.totals.requests} req • ${worker.totals.errors} err • ${worker.totals.avg_latency_ms} ms`,
          details: details.length > 0 ? details : undefined,
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
      title={t("proxy.metrics.title")}
      titleView={
        <box flexDirection="row" gap={2}>
          <text fg={theme.text} attributes={TextAttributes.BOLD}>{t("proxy.metrics.title")}</text>
          <For each={RANGES}>
            {(item) => {
              const selected = () => range() === item.value
              return (
                <box
                  paddingLeft={1}
                  paddingRight={1}
                  backgroundColor={selected() ? theme.primary : undefined}
                  onMouseUp={() => loadMetrics(item.value)}
                >
                  <text fg={selected() ? theme.selectedListItemText : theme.textMuted} attributes={selected() ? TextAttributes.BOLD : undefined}>
                    {t(item.title)}
                  </text>
                </box>
              )
            }}
          </For>
        </box>
      }
      options={options()}
      placeholder={t("proxy.metrics.search")}
      footer={
        rangeDetails().length > 0 ? (
          <box flexDirection="column">
            <For each={rangeDetails()}>{(detail) => <text fg={theme.warning}>{detail}</text>}</For>
          </box>
        ) : undefined
      }
      bindings={[
        { key: "left", desc: t("proxy.metrics.today"), group: t("category.metrics"), cmd: () => loadMetrics("today") },
        { key: "right", desc: t("proxy.metrics.last24Hours"), group: t("category.metrics"), cmd: () => loadMetrics("last_24h") },
      ]}
    />
  )
}
