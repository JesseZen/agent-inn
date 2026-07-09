import { expect, mock, test } from "bun:test"
import { activeHostedSession, defaultWorker, json, mountHostedTerminalApp, wait } from "./proxy-hosted-terminal.fixture"
import type { HostedSessionSummary } from "../src/proxy/backend"

const readDoneHostedSession = {
  ...activeHostedSession,
  turn_state: "done",
  turn_generation: 3,
  turn_acknowledged_generation: 3,
} satisfies HostedSessionSummary

test("hosted terminal picker marks a read completed session unread", async () => {
  const markUnreadRequests: string[] = []
  let currentHostedSessions: HostedSessionSummary[] = [{ ...readDoneHostedSession }]
  const app = await mountHostedTerminalApp((url, request) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET")
      return json({
        sessions: currentHostedSessions,
      })
    if (url.pathname === "/api/hosted-sessions/hs_1/mark-unread" && request.method === "POST") {
      markUnreadRequests.push("hs_1")
      currentHostedSessions = currentHostedSessions.map((session) =>
        session.session_id === "hs_1" ? { ...session, turn_acknowledged_generation: 0 } : session,
      )
      return json(currentHostedSessions[0])
    }
    return undefined
  })

  try {
    await app.openHostedTerminalPicker()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("solve problem A")
    })

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    await app.setup.renderOnce()
    expect(app.setup.captureCharFrame()).toContain("unread ctrl+u")

    app.api().keymap.dispatchCommand("session.mark_unread")
    await wait(async () => {
      await app.setup.renderOnce()
      return markUnreadRequests.length === 1
    })

    expect(markUnreadRequests).toEqual(["hs_1"])

    await app.cleanup()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})
