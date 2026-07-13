import { afterEach, expect, mock, spyOn, test } from "bun:test"
import {
  activeHostedSession,
  defaultWorker,
  json,
  mountHostedTerminalApp,
  mountHostedTerminalPopupApp,
  staleHostedSessionA,
  staleHostedSessionB,
  wait,
} from "./proxy-hosted-terminal.fixture"
import type { HostedSessionSummary } from "../src/proxy/backend"
import * as launchModule from "../src/proxy/launch"
import type { ProxyLaunchOptions } from "../src/proxy/launch"

afterEach(() => {
  mock.restore()
})

function installLaunchMock() {
  spyOn(launchModule, "launchProxySession").mockResolvedValue(true)
  spyOn(launchModule, "setupHostedTerminalSession").mockResolvedValue(true)
}

async function openBulkActions(app: Awaited<ReturnType<typeof mountHostedTerminalApp>>) {
  await app.openHostedTerminalPicker()
  await wait(async () => {
    await app.setup.renderOnce()
    return app.setup.captureCharFrame().includes("Hosted Terminal")
  })
  app.api().keymap.dispatchCommand("dialog.select.end")
  app.api().keymap.dispatchCommand("dialog.select.submit")
  await wait(async () => {
    await app.setup.renderOnce()
    return app.setup.captureCharFrame().includes("Bulk session actions")
  })
}

async function selectFirstTwoSessions(app: Awaited<ReturnType<typeof mountHostedTerminalApp>>) {
  app.api().keymap.dispatchCommand("dialog.select.next")
  app.api().keymap.dispatchCommand("dialog.select.next")
  app.api().keymap.dispatchCommand("dialog.select.next")
  app.api().keymap.dispatchCommand("session.bulk.toggle")
  app.api().keymap.dispatchCommand("dialog.select.next")
  app.api().keymap.dispatchCommand("session.bulk.toggle")
  await app.setup.renderOnce()
}

test("bulk session actions open every selected hosted session", async () => {
  const opened: ProxyLaunchOptions[] = []
  spyOn(launchModule, "launchProxySession").mockImplementation(async (opts) => {
    opened.push(opts)
    return true
  })
  spyOn(launchModule, "setupHostedTerminalSession").mockResolvedValue(true)
  const runningSession = { ...activeHostedSession, turn_state: "running" as const }
  const otherWorker = { ...defaultWorker, id: "other-cli", name: "other-cli", port: 4321 }
  const otherSession = { ...staleHostedSessionA, worker_id: otherWorker.id, worker_name: otherWorker.name, worker_port: otherWorker.port }
  let listCalls = 0
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker, otherWorker] })
    if (url.pathname === "/api/hosted-sessions") {
      listCalls += 1
      return json({ sessions: [runningSession, otherSession] })
    }
    return undefined
  })

  try {
    await openBulkActions(app)
    await selectFirstTwoSessions(app)
    const listCallsBeforeOpen = listCalls
    app.api().keymap.dispatchCommand("dialog.select.home")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(() => opened.length === 2)
    await wait(async () => {
      await app.setup.renderOnce()
      return !app.setup.captureCharFrame().includes("Bulk session actions")
    })

    expect({
      opened: opened.map(({ workerPort, profile, mode, sessionID }) => ({ workerPort, profile, mode, sessionID })),
      refreshed: listCalls > listCallsBeforeOpen,
    }).toEqual({
      opened: [
        { workerPort: 1234, profile: "test-cli", mode: "hosted-terminal", sessionID: "hs_1" },
        { workerPort: 4321, profile: "other-cli", mode: "hosted-terminal", sessionID: "hs_2" },
      ],
      refreshed: true,
    })
  } finally {
    await app.cleanup()
  }
})

test("popup bulk session actions set up every selected hosted session", async () => {
  const openSessionIDs: Array<string | undefined> = []
  const setupSessionIDs: Array<string | undefined> = []
  spyOn(launchModule, "launchProxySession").mockImplementation(async (opts) => {
    openSessionIDs.push(opts.sessionID)
    return true
  })
  spyOn(launchModule, "setupHostedTerminalSession").mockImplementation(async (opts) => {
    setupSessionIDs.push(opts.sessionID)
    return true
  })
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions") return json({ sessions: [activeHostedSession, staleHostedSessionA] })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    for (let index = 0; index < 5; index++) app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Bulk session actions")
    })
    for (let index = 0; index < 3; index++) app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    for (let index = 0; index < 4; index++) app.setup.mockInput.pressArrow("up")
    app.setup.mockInput.pressEnter()
    await wait(() => setupSessionIDs.length === 2)

    expect({ openSessionIDs, setupSessionIDs }).toEqual({
      openSessionIDs: [],
      setupSessionIDs: ["hs_1", "hs_2"],
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("bulk session actions stop opening after a launch failure", async () => {
  const attemptedSessionIDs: Array<string | undefined> = []
  spyOn(launchModule, "launchProxySession").mockImplementation(async (opts) => {
    attemptedSessionIDs.push(opts.sessionID)
    if (opts.sessionID === "hs_2") throw new Error("cannot restore hs_2")
    return true
  })
  spyOn(launchModule, "setupHostedTerminalSession").mockResolvedValue(true)
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions") return json({ sessions: [activeHostedSession, staleHostedSessionA, staleHostedSessionB] })
    return undefined
  })

  try {
    await openBulkActions(app)
    await selectFirstTwoSessions(app)
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("session.bulk.toggle")
    app.api().keymap.dispatchCommand("dialog.select.home")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Open hosted sessions failed")
    })
    const alertFrame = app.setup.captureCharFrame()
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Bulk session actions")
    })
    const bulkFrame = app.setup.captureCharFrame()

    expect({
      attemptedSessionIDs,
      alertVisible: alertFrame.includes("cannot restore hs_2"),
      bulkDialogVisible: bulkFrame.includes("Bulk session actions"),
    }).toEqual({
      attemptedSessionIDs: ["hs_1", "hs_2"],
      alertVisible: true,
      bulkDialogVisible: true,
    })
  } finally {
    await app.cleanup()
  }
})

test("bulk session actions delete every selected hosted session", async () => {
  const deleteRequests: string[] = []
  let sessions: HostedSessionSummary[] = [activeHostedSession, staleHostedSessionA, staleHostedSessionB]
  const app = await mountHostedTerminalApp((url, request) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") return json({ sessions })
    if (url.pathname.startsWith("/api/hosted-sessions/") && request.method === "DELETE") {
      const sessionID = url.pathname.split("/").at(-1) ?? ""
      deleteRequests.push(sessionID)
      sessions = sessions.filter((session) => session.session_id !== sessionID)
      return json({ session_id: sessionID })
    }
    return undefined
  })

  try {
    await openBulkActions(app)
    await selectFirstTwoSessions(app)

    app.api().keymap.dispatchCommand("dialog.select.home")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Delete hosted sessions")
    })
    app.setup.mockInput.pressEnter()
    await wait(() => deleteRequests.length === 2)

    expect({ deleteRequests, remainingSessionIDs: sessions.map((session) => session.session_id) }).toEqual({
      deleteRequests: ["hs_1", "hs_2"],
      remainingSessionIDs: ["hs_3"],
    })
  } finally {
    await app.cleanup()
  }
})

test("bulk session actions rebind every selected session to one compatible worker", async () => {
  installLaunchMock()
  const localWorker = { ...defaultWorker, id: "local-cli", name: "local-cli", port: 11200 }
  const patches: Array<{ session_id: string; worker_id: string }> = []
  let sessions: HostedSessionSummary[] = [staleHostedSessionA, staleHostedSessionB]
  const app = await mountHostedTerminalApp(async (url, request) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker, localWorker] })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") return json({ sessions })
    if (url.pathname.startsWith("/api/hosted-sessions/") && request.method === "PATCH") {
      const sessionID = url.pathname.split("/").at(-1) ?? ""
      const body = (await request.json()) as { worker_id: string }
      patches.push({ session_id: sessionID, worker_id: body.worker_id })
      sessions = sessions.map((session) =>
        session.session_id === sessionID ? { ...session, worker_id: body.worker_id, worker_name: localWorker.name, worker_port: localWorker.port } : session,
      )
      return json(sessions.find((session) => session.session_id === sessionID))
    }
    return undefined
  })

  try {
    await openBulkActions(app)
    await selectFirstTwoSessions(app)

    app.api().keymap.dispatchCommand("dialog.select.home")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Change worker") && app.setup.captureCharFrame().includes("local-cli")
    })
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(() => patches.length === 2)

    expect({ patches, sessionWorkerIDs: sessions.map((session) => session.worker_id) }).toEqual({
      patches: [
        { session_id: "hs_2", worker_id: "local-cli" },
        { session_id: "hs_3", worker_id: "local-cli" },
      ],
      sessionWorkerIDs: ["local-cli", "local-cli"],
    })
  } finally {
    await app.cleanup()
  }
})

test("bulk session actions reject changing a running session worker", async () => {
  installLaunchMock()
  const runningSession = { ...activeHostedSession, turn_state: "running" as const }
  const patches: Array<{ session_id: string; worker_id: string }> = []
  const app = await mountHostedTerminalApp(async (url, request) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") return json({ sessions: [runningSession] })
    if (url.pathname.startsWith("/api/hosted-sessions/") && request.method === "PATCH") {
      const body = (await request.json()) as { worker_id: string }
      patches.push({ session_id: "hs_1", worker_id: body.worker_id })
      return json(runningSession)
    }
    return undefined
  })

  try {
    await openBulkActions(app)
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("session.bulk.toggle")
    app.api().keymap.dispatchCommand("dialog.select.home")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Change hosted session worker failed")
    })

    expect({ patches }).toEqual({ patches: [] })
  } finally {
    await app.cleanup()
  }
})
