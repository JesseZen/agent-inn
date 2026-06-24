import { expect, mock, test } from "bun:test"
import {
  activeHostedSession,
  defaultWorker,
  directory,
  json,
  mountHostedTerminalApp,
  staleHostedSessionA,
  staleHostedSessionB,
  wait,
} from "./proxy-hosted-terminal.fixture"
import type { HostedSessionSummary } from "../src/proxy/backend"

async function setupHostedTerminal(initialHostedSessions: HostedSessionSummary[] = [activeHostedSession]) {
  const deleteRequests: string[] = []
  let currentHostedSessions = initialHostedSessions.map((session) => ({ ...session }))
  const app = await mountHostedTerminalApp((url, request) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET")
      return json({
        sessions: currentHostedSessions,
      })
    if (url.pathname.startsWith("/api/hosted-sessions/") && request.method === "DELETE") {
      const sessionID = url.pathname.split("/").at(-1) ?? ""
      deleteRequests.push(sessionID)
      currentHostedSessions = currentHostedSessions.filter((session) => session.session_id !== sessionID)
      return json({ session_id: sessionID })
    }
    return undefined
  })

  async function openHostedTerminal() {
    await app.openHostedTerminalPicker()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("solve problem A")
    })
  }

  async function close() {
    await app.cleanup()
  }

  return { setup: app.setup, api: app.api, deleteRequests, openHostedTerminal, close }
}

test("hosted terminal picker shows ctrl d delete hint", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    const frame = app.setup.captureCharFrame()
    expect(frame.includes("Hosted Terminal")).toBe(true)
    expect(frame.includes("Delete Hosted Session")).toBe(false)
    expect(frame.includes("ctrl+d")).toBe(true)
    expect(frame.includes("delete")).toBe(true)

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal picker ctrl d deletes the highlighted session", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("session.delete")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted session") && frame.includes("Delete solve problem A?")
    })
    expect(app.setup.captureCharFrame().includes("Cancel")).toBe(true)

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.deleteRequests.length === 1
    })

    expect(app.deleteRequests).toEqual(["hs_1"])

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page still deletes selected session on enter", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("solve problem A")
    })

    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted session") && frame.includes("Delete solve problem A?")
    })

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.deleteRequests.length === 1
    })

    expect(app.deleteRequests).toEqual(["hs_1"])

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page does not show ctrl d delete hint", async () => {
  const app = await setupHostedTerminal()

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("solve problem A")
    })

    const frame = app.setup.captureCharFrame()
    expect(frame.includes("ctrl+d")).toBe(false)

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page shows GC stale sessions when stale sessions exist", async () => {
  const app = await setupHostedTerminal([activeHostedSession, staleHostedSessionA, staleHostedSessionB])

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("GC stale sessions")
    })

    const frame = app.setup.captureCharFrame()
    expect(frame.includes("Delete Hosted Session")).toBe(true)
    expect(frame.includes("GC stale sessions")).toBe(true)

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})

test("hosted terminal delete page GC deletes all stale sessions after confirmation", async () => {
  const app = await setupHostedTerminal([activeHostedSession, staleHostedSessionA, staleHostedSessionB])

  try {
    await app.openHostedTerminal()

    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && frame.includes("GC stale sessions")
    })

    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted sessions") && frame.includes("Delete all stale sessions?")
    })

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.deleteRequests.length === 2
    })

    expect(app.deleteRequests).toEqual(["hs_2", "hs_3"])

    await app.openHostedTerminal()
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")

    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete Hosted Session") && !frame.includes("GC stale sessions") && !frame.includes("stale problem A")
    })

    await app.close()
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    mock.restore()
  }
})
