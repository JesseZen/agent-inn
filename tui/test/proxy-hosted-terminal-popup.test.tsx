import { afterEach, expect, mock, test } from "bun:test"
import { Global } from "@agent-inn/core/global"
import { TextareaRenderable } from "@opentui/core"
import type { HostedSessionSnapshot } from "../src/proxy/hosted-session-contract"
import { activeHostedSession, defaultWorker, directory, json, mountHostedTerminalPopupApp, wait } from "./proxy-hosted-terminal.fixture"

afterEach(() => {
  mock.restore()
})

test("setupHostedTerminalSession only runs no-attach hosted launch", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const launchModule = await import(`../src/proxy/launch?popup-setup=${Date.now()}`)
  const launched = await launchModule.setupHostedTerminalSession({
    executable: "ainn",
    workerPort: 1234,
    profile: "test-cli",
    configDir: Global.Path.config,
    mode: "hosted-terminal",
    sessionID: "hs_1",
  })

  expect(launched).toBe(true)
  expect(spawns).toEqual([
    {
      cmd: "ainn",
      args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-id", "hs_1"],
    },
  ])
})

test("popup mode renders hosted terminal picker without home route", async () => {
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [activeHostedSession],
        event_cursor: "0",
      })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Refresh") && frame.includes("Create new session")
    })

    expect(app.setup.captureCharFrame()).not.toContain("Ask anything")
  } finally {
    await app.cleanup()
  }
})

test("popup mode renders the hosted terminal root directly in the right rail", async () => {
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions") return json({ sessions: [activeHostedSession], event_cursor: "0" })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Hosted Terminal")
    })

    const lines = app.setup.captureCharFrame().split("\n")
    expect({ headerRow: lines.findIndex((line) => line.includes("Hosted Terminal")), hasRootActions: lines.join("\n").includes("Create new session") }).toEqual({
      headerRow: 0,
      hasRootActions: true,
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode root escape exits the right rail", async () => {
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions") return json({ sessions: [activeHostedSession], event_cursor: "0" })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Hosted Terminal")
    })
    app.setup.mockInput.pressEscape()
    await wait(() => app.setup.renderer.isDestroyed)

    expect({ rendererDestroyed: app.setup.renderer.isDestroyed }).toEqual({ rendererDestroyed: true })
  } finally {
    await app.cleanup()
  }
})

test("popup mode mouse close exits the right rail", async () => {
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions") return json({ sessions: [activeHostedSession], event_cursor: "0" })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Hosted Terminal")
    })
    const lines = app.setup.captureCharFrame().split("\n")
    const row = lines.findIndex((line) => line.includes("Hosted Terminal") && line.includes("esc"))
    const column = row >= 0 ? lines[row].indexOf("esc") : -1
    if (row < 0 || column < 0) throw new Error("expected visible popup close affordance")

    await app.setup.mockMouse.click(column, row)
    await wait(() => app.setup.renderer.isDestroyed)

    expect({ rendererDestroyed: app.setup.renderer.isDestroyed }).toEqual({ rendererDestroyed: true })
  } finally {
    await app.cleanup()
  }
})

test("popup mode nested escape returns to the visible right-rail root", async () => {
  let listCalls = 0
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions") {
      listCalls += 1
      return json({ sessions: [activeHostedSession], event_cursor: "0" })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Create new session")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Choose worker")
    })
    app.setup.mockInput.pressEscape()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && !frame.includes("Choose worker")
    })

    const lines = app.setup.captureCharFrame().split("\n")
    expect({
      headerRow: lines.findIndex((line) => line.includes("Hosted Terminal")),
      rendererDestroyed: app.setup.renderer.isDestroyed,
      listCalls,
    }).toEqual({
      headerRow: 0,
      rendererDestroyed: false,
      listCalls: 1,
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode direct-root rename persists the refreshed list without replacing the right rail", async () => {
  const patches: Array<{ session_id: string; session_label: string }> = []
  let listCalls = 0
  let sessions: HostedSessionSnapshot[] = [{ ...activeHostedSession }]
  const app = await mountHostedTerminalPopupApp((url, request) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") {
      listCalls += 1
      return json({ sessions, event_cursor: "0" })
    }
    if (url.pathname === "/api/hosted-sessions/hs_1" && request.method === "PATCH") {
      patches.push({ session_id: "hs_1", session_label: "solve problem B" })
      sessions = sessions.map((session) => ({ ...session, session_label: "solve problem B" }))
      return json(sessions[0])
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressKey("r", { ctrl: true })
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Rename Hosted Session") && app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused hosted session rename prompt")
    editor.selectAll()
    await app.setup.mockInput.typeText("solve problem B")
    await app.setup.renderOnce()
    const promptLines = app.setup.captureCharFrame().split("\n")
    const promptRow = promptLines.findIndex((line) => line.includes("submit"))
    const promptColumn = promptRow >= 0 ? promptLines[promptRow].indexOf("submit") : -1
    if (promptRow < 0 || promptColumn < 0) throw new Error("expected visible rename submit control")
    await app.setup.mockMouse.click(promptColumn, promptRow)
    await wait(() => patches.length === 1 && listCalls === 2)
    await app.setup.renderOnce()

    const frame = app.setup.captureCharFrame()
    const lines = frame.split("\n")
    expect({
      patches,
      hasPreviousLabel: frame.includes("solve problem A"),
      headerRow: lines.findIndex((line) => line.includes("Hosted Terminal")),
      rendererDestroyed: app.setup.renderer.isDestroyed,
      listCalls,
      renamedLabelVisible: frame.includes("solve problem B"),
    }).toEqual({
      patches: [{ session_id: "hs_1", session_label: "solve problem B" }],
      hasPreviousLabel: false,
      headerRow: 0,
      rendererDestroyed: false,
      listCalls: 2,
      renamedLabelVisible: true,
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode direct-root delete persists the remaining list without replacing the right rail", async () => {
  const deleteRequests: string[] = []
  let listCalls = 0
  let sessions = [
    { ...activeHostedSession },
    { ...activeHostedSession, session_id: "hs_2", session_label: "remaining session", status: "stale" as const },
  ]
  const app = await mountHostedTerminalPopupApp((url, request) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") {
      listCalls += 1
      return json({ sessions, event_cursor: "0" })
    }
    if (url.pathname === "/api/hosted-sessions/hs_1" && request.method === "DELETE") {
      deleteRequests.push("hs_1")
      sessions = sessions.filter((session) => session.session_id !== "hs_1")
      return json({ session_id: "hs_1" })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressKey("d", { ctrl: true })
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Delete solve problem A?")
    })
    const confirmLines = app.setup.captureCharFrame().split("\n")
    const confirmRow = confirmLines.findIndex((line) => line.includes("Confirm"))
    const confirmColumn = confirmRow >= 0 ? confirmLines[confirmRow].indexOf("Confirm") : -1
    if (confirmRow < 0 || confirmColumn < 0) throw new Error("expected visible delete confirmation control")
    await app.setup.mockMouse.click(confirmColumn, confirmRow)
    await wait(async () => {
      await app.setup.renderOnce()
      return deleteRequests.length === 1 && listCalls === 2 && !app.setup.captureCharFrame().includes("solve problem A")
    })

    const frame = app.setup.captureCharFrame()
    const lines = frame.split("\n")
    expect({
      deleteRequests,
      hasDeletedSession: frame.includes("solve problem A"),
      headerRow: lines.findIndex((line) => line.includes("Hosted Terminal")),
      rendererDestroyed: app.setup.renderer.isDestroyed,
      listCalls,
      remainingLabelVisible: frame.includes("remaining session"),
    }).toEqual({
      deleteRequests: ["hs_1"],
      hasDeletedSession: false,
      headerRow: 0,
      rendererDestroyed: false,
      listCalls: 2,
      remainingLabelVisible: true,
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode does not start plugin host or render command palette", async () => {
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [activeHostedSession],
        event_cursor: "0",
      })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session")
    })

    await app.setup.renderOnce()

    const frame = app.setup.captureCharFrame()
    expect(app.pluginStarts()).toBe(0)
    expect(frame).toContain("Hosted Terminal")
    expect(frame).not.toContain("Commands")
  } finally {
    await app.cleanup()
  }
})

test("popup mode does not render provider setup with empty providers", async () => {
  const app = await mountHostedTerminalPopupApp((url) => {
    if (["/config/providers", "/provider", "/agent", "/session"].includes(url.pathname)) {
      throw new Error(`popup mode should not fetch ${url.pathname}`)
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session")
    })

    const frame = app.setup.captureCharFrame()
    expect(frame).toContain("Hosted Terminal")
    expect(frame).not.toContain("Connect a provider")
  } finally {
    await app.cleanup()
  }
})

test("popup mode opens existing hosted session with setup only and stays open", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []
  let listCalls = 0
  const popupSession = {
    ...activeHostedSession,
    session_id: "hs_popup",
    worker: { ...activeHostedSession.worker, name: "test" },
    session_label: "popup task",
  }

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions") {
      listCalls += 1
      return json({
        sessions: [popupSession],
        event_cursor: "0",
      })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return listCalls === 2
    })

    expect(spawns).toEqual([
      {
        cmd: import.meta.env?.AINN_EXECUTABLE || "ainn",
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-id", "hs_popup"],
      },
    ])
    expect(app.setup.renderer.isDestroyed).toBe(false)
    expect(listCalls).toBe(2)
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode uses configured executable for hosted setup", async () => {
  const originalExecutable = process.env.AINN_EXECUTABLE
  const configuredExecutable = "/tmp/ainn-popup-bin"
  process.env.AINN_EXECUTABLE = configuredExecutable
  const spawns: Array<{ cmd: string; args: string[] }> = []
  const popupSession = {
    ...activeHostedSession,
    session_id: "hs_popup_exec",
    worker: { ...activeHostedSession.worker, name: "test" },
    session_label: "popup exec task",
  }

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [popupSession],
        event_cursor: "0",
      })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return spawns.length === 1
    })

    expect(spawns).toEqual([
      {
        cmd: configuredExecutable,
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-id", "hs_popup_exec"],
      },
    ])
  } finally {
    if (originalExecutable === undefined) {
      delete process.env.AINN_EXECUTABLE
    } else {
      process.env.AINN_EXECUTABLE = originalExecutable
    }
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode duplicates hosted session with setup only and stays open", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []
  let listCalls = 0

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  let duplicateCalls = 0
  const app = await mountHostedTerminalPopupApp((url, request) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") {
      listCalls += 1
      return json({
        sessions: [activeHostedSession],
        event_cursor: "0",
      })
    }
    if (url.pathname === "/api/hosted-sessions/hs_1/duplicate" && request.method === "POST") {
      duplicateCalls += 1
      return json({
        ...activeHostedSession,
        session_id: "hs_dup",
        session_label: "solve problem A copy",
      }, { status: 201 })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressKey("y", { ctrl: true })
    await wait(async () => {
      await app.setup.renderOnce()
      return listCalls === 2
    })

    expect(duplicateCalls).toBe(1)
    expect(spawns).toEqual([
      {
        cmd: import.meta.env?.AINN_EXECUTABLE || "ainn",
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-id", "hs_dup"],
      },
    ])
    expect(app.setup.renderer.isDestroyed).toBe(false)
    expect(listCalls).toBe(2)
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode locks root shortcuts while a hosted-session child dialog is open", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  let duplicateCalls = 0
  const app = await mountHostedTerminalPopupApp((url, request) => {
    if (url.pathname === "/api/workers") return json({ workers: [defaultWorker] })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") return json({ sessions: [activeHostedSession], event_cursor: "0" })
    if (url.pathname === "/api/hosted-sessions/hs_1/duplicate" && request.method === "POST") {
      duplicateCalls += 1
      return json({
        ...activeHostedSession,
        session_id: "hs_dup",
        session_label: "solve problem A copy",
      }, { status: 201 })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressKey("d", { ctrl: true })
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Delete hosted session") && frame.includes("Delete solve problem A?")
    })

    app.setup.mockInput.pressKey("y", { ctrl: true })
    await Bun.sleep(25)
    await app.setup.renderOnce()

    expect({
      duplicateCalls,
      spawns,
      childDialogOpen: app.setup.captureCharFrame().includes("Delete solve problem A?"),
    }).toEqual({
      duplicateCalls: 0,
      spawns: [],
      childDialogOpen: true,
    })

    app.setup.mockInput.pressEscape()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && !frame.includes("Delete solve problem A?")
    })

    app.setup.mockInput.pressKey("y", { ctrl: true })
    await wait(async () => {
      await app.setup.renderOnce()
      return duplicateCalls === 1 && spawns.length === 1
    })

    expect({ duplicateCalls, spawns: spawns.length }).toEqual({ duplicateCalls: 1, spawns: 1 })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode creates hosted session with setup only and returns to root", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []
  let listCalls = 0

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions") {
      listCalls += 1
      return json({
        sessions: [],
        event_cursor: "0",
      })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Create new session")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Choose worker")
    })
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Launch Worker")
    })
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Create Hosted Session")
    })
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return listCalls >= 2 && frame.includes("Hosted Terminal") && !frame.includes("Create Hosted Session")
    })

    expect(spawns).toEqual([
      {
        cmd: import.meta.env?.AINN_EXECUTABLE || "ainn",
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-label", "test-cli 1", "--cd", directory],
      },
    ])
    expect(app.setup.renderer.isDestroyed).toBe(false)
    expect(listCalls).toBeGreaterThanOrEqual(2)
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode mouse refresh refetches sessions without launching", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []
  let listCalls = 0

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions") {
      listCalls += 1
      return json({
        sessions: [activeHostedSession],
        event_cursor: "0",
      })
    }
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Refresh")
    })
    const lines = app.setup.captureCharFrame().split("\n")
    const row = lines.findIndex((line) => line.includes("Refresh"))
    const column = row >= 0 ? lines[row].indexOf("Refresh") : -1
    if (row < 0 || column < 0) throw new Error("expected visible Refresh row")

    await app.setup.mockMouse.click(column, row)
    await wait(async () => {
      await app.setup.renderOnce()
      return listCalls === 2
    })

    expect(spawns).toEqual([])
    expect(app.setup.renderer.isDestroyed).toBe(false)
    expect(listCalls).toBe(2)
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode renders manager bootstrap error without home route", async () => {
  const originalError = console.error
  console.error = () => {}
  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers") return json({ error: "manager unavailable" }, { status: 503 })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("manager unavailable")
    })

    expect(app.setup.captureCharFrame()).not.toContain("Ask anything")
  } finally {
    console.error = originalError
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})
