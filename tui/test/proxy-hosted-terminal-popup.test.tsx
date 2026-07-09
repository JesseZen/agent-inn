import { afterEach, expect, mock, test } from "bun:test"
import { Global } from "@agent-inn/core/global"
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
      })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("solve problem A")
    })

    expect(app.setup.captureCharFrame()).not.toContain("Ask anything")
  } finally {
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

test("popup mode opens existing hosted session with setup only then exits", async () => {
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

  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [activeHostedSession],
      })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("solve problem A")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressEnter()
    await wait(() => app.setup.renderer.isDestroyed)

    expect(spawns).toEqual([
      {
        cmd: import.meta.env?.AINN_EXECUTABLE || "ainn",
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-id", "hs_1"],
      },
    ])
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode duplicates hosted session with setup only then exits", async () => {
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
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET")
      return json({
        sessions: [activeHostedSession],
      })
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
      return app.setup.captureCharFrame().includes("solve problem A")
    })
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressArrow("down")
    app.setup.mockInput.pressKey("y", { ctrl: true })
    await wait(() => app.setup.renderer.isDestroyed)

    expect(duplicateCalls).toBe(1)
    expect(spawns).toEqual([
      {
        cmd: import.meta.env?.AINN_EXECUTABLE || "ainn",
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-id", "hs_dup"],
      },
    ])
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("popup mode creates hosted session with setup only then exits", async () => {
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

  const app = await mountHostedTerminalPopupApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [],
      })
    return undefined
  })

  try {
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Create new session")
    })
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
    await wait(() => app.setup.renderer.isDestroyed)

    expect(spawns).toEqual([
      {
        cmd: import.meta.env?.AINN_EXECUTABLE || "ainn",
        args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "test-cli", "--config-dir", Global.Path.config, "--session-label", "test-cli 1", "--cd", directory],
      },
    ])
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
