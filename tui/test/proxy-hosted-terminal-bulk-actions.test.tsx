import { afterEach, expect, mock, spyOn, test } from "bun:test"
import { activeHostedSession, defaultWorker, json, mountHostedTerminalApp, staleHostedSessionA, staleHostedSessionB, wait } from "./proxy-hosted-terminal.fixture"
import type { HostedSessionSummary } from "../src/proxy/backend"
import * as launchModule from "../src/proxy/launch"

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
  app.api().keymap.dispatchCommand("session.bulk.toggle")
  app.api().keymap.dispatchCommand("dialog.select.next")
  app.api().keymap.dispatchCommand("session.bulk.toggle")
  await app.setup.renderOnce()
}

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
    app.api().keymap.dispatchCommand("session.bulk.toggle")
    app.api().keymap.dispatchCommand("dialog.select.home")
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
