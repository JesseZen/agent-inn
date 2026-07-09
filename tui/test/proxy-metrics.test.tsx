import { expect, test } from "bun:test"
import { mountProxyApp, wait } from "./proxy-commands.fixture"

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
