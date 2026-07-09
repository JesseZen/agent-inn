import { expect, test } from "bun:test"
import { mountProxyApp, wait } from "./proxy-commands.fixture"
import type { MetricsRangeName, MetricsResponse } from "../src/proxy/backend"

const METRICS_REFRESH_DELAY_MS = 100

function metricsResponse(range: MetricsRangeName, totalTokens: number): MetricsResponse {
  return {
    range: { name: range, start: "2026-07-10T00:00:00+08:00", end: "2026-07-11T00:00:00+08:00" },
    workers: [{
      worker: "app",
      port: 6767,
      status: "running",
      upstream: "openai",
      live: { window_seconds: 60, in_flight: 0, requests: 1, errors: 0, rpm: 1, tpm: totalTokens, avg_latency_ms: 120, input_tokens: 12, output_tokens: 8, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, total_tokens: totalTokens, unknown_usage_requests: 0 },
      totals: { requests: 1, errors: 0, avg_latency_ms: 120, input_tokens: 12, output_tokens: 8, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, total_tokens: totalTokens, unknown_usage_requests: 0 },
    }],
    skipped_records: 0,
  }
}

test("proxy status opens metrics console with today's totals", async () => {
  const app = await mountProxyApp()
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Worker Metrics") && frame.includes("Today") && frame.includes("app") && frame.includes("RPM")
    })
    expect(app.calls.getMetrics).toEqual(["today"])
    expect(app.frame()).toContain("20 tok")
  } finally {
    await app.cleanup()
  }
})

test("proxy status switches to last 24 hours", async () => {
  const app = await mountProxyApp()
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Metrics")
    })
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.getMetrics.includes("last_24h"))
    expect(app.frame()).toContain("Last 24h")
  } finally {
    await app.cleanup()
  }
})

test("proxy status refreshes active range on metrics update events", async () => {
  const app = await mountProxyApp()
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Metrics")
    })
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.getMetrics.includes("last_24h"))

    app.metrics.tpm = 40
    app.metrics.total_tokens = 40
    app.emitManagerEvent("metrics.updated", { worker: "app", port: 6767, metrics: {} })

    await wait(async () => {
      await app.render()
      return app.calls.getMetrics.filter((range) => range === "last_24h").length === 2 && app.frame().includes("40 tok")
    })

    expect(app.calls.getMetrics).toEqual(["today", "last_24h", "last_24h"])
  } finally {
    await app.cleanup()
  }
})

test("proxy status ignores stale metric responses and coalesces update bursts", async () => {
  let resolveFirstToday!: (value: MetricsResponse) => void
  let resolveFirstLast24h!: (value: MetricsResponse) => void
  let firstTodayPending = true
  let firstLast24hPending = true
  let last24hTokens = 40
  const app = await mountProxyApp({
    metricsResponder: (range) => {
      if (range === "today" && firstTodayPending) {
        firstTodayPending = false
        return new Promise<MetricsResponse>((resolve) => {
          resolveFirstToday = resolve
        })
      }
      if (range === "last_24h" && firstLast24hPending) {
        firstLast24hPending = false
        return new Promise<MetricsResponse>((resolve) => {
          resolveFirstLast24h = resolve
        })
      }
      return metricsResponse(range, range === "last_24h" ? last24hTokens : 10)
    },
  })
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Metrics")
    })
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    expect(app.calls.getMetrics).toEqual(["today"])

    resolveFirstToday(metricsResponse("today", 10))
    await wait(async () => {
      await app.render()
      return app.calls.getMetrics.includes("last_24h")
    })
    await app.render()
    expect(app.frame()).not.toContain("10 tok")

    resolveFirstLast24h(metricsResponse("last_24h", 40))
    await wait(async () => {
      await app.render()
      return app.frame().includes("40 tok")
    })

    last24hTokens = 60
    app.emitManagerEvent("metrics.updated", { worker: "app", port: 6767, metrics: {} })
    app.emitManagerEvent("metrics.updated", { worker: "app", port: 6767, metrics: {} })
    app.emitManagerEvent("metrics.updated", { worker: "app", port: 6767, metrics: {} })

    await wait(async () => {
      await app.render()
      return app.frame().includes("60 tok")
    })

    expect(app.calls.getMetrics).toEqual(["today", "last_24h", "last_24h"])
  } finally {
    await app.cleanup()
  }
})

test("proxy status cancels queued refreshes when the dialog closes", async () => {
  let resolveInitial!: (value: MetricsResponse) => void
  let initialPending = true
  const app = await mountProxyApp({
    metricsResponder: (range) => {
      if (initialPending) {
        initialPending = false
        return new Promise<MetricsResponse>((resolve) => {
          resolveInitial = resolve
        })
      }
      return metricsResponse(range, 40)
    },
  })
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Metrics") && app.calls.getMetrics.length === 1
    })

    app.emitManagerEvent("metrics.updated", { worker: "app", port: 6767, metrics: {} })
    await Bun.sleep(METRICS_REFRESH_DELAY_MS + 50)
    app.api.ui.dialog.clear()
    await app.render()

    resolveInitial(metricsResponse("today", 20))
    await Bun.sleep(METRICS_REFRESH_DELAY_MS)
    await app.render()

    expect(app.calls.getMetrics).toEqual(["today"])
  } finally {
    await app.cleanup()
  }
})

test("proxy status stays closed when worker selection finishes after disposal", async () => {
  const app = await mountProxyApp()
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Metrics") && app.frame().includes("app")
    })

    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    app.api.ui.dialog.clear()

    await Bun.sleep(METRICS_REFRESH_DELAY_MS)
    await app.render()
    expect({
      depth: app.api.ui.dialog.depth,
      workerDetailOpen: app.frame().includes("app (:6767)"),
    }).toEqual({
      depth: 0,
      workerDetailOpen: false,
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy status reports worker selection errors while mounted", async () => {
  const app = await mountProxyApp({
    metricsResponder: (range) => {
      const response = metricsResponse(range, 20)
      response.workers[0].port = 9999
      return response
    },
  })
  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Metrics") && app.frame().includes(":9999")
    })

    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")

    await wait(async () => {
      await app.render()
      return app.frame().includes("unexpected request: /api/workers/9999")
    })
  } finally {
    await app.cleanup()
  }
})
