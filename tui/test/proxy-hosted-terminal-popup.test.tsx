import { afterEach, expect, mock, test } from "bun:test"
import { Global } from "@agent-inn/core/global"
import { activeHostedSession, defaultWorker, json, mountHostedTerminalPopupApp, wait } from "./proxy-hosted-terminal.fixture"

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

test("popup mode ignores normal command palette command", async () => {
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

    app.api().keymap.dispatchCommand("command.palette.show")
    await app.setup.renderOnce()

    const frame = app.setup.captureCharFrame()
    expect(frame).toContain("Hosted Terminal")
    expect(frame).not.toContain("Commands")
  } finally {
    await app.cleanup()
  }
})

test("popup mode does not render provider setup with empty providers", async () => {
  const app = await mountHostedTerminalPopupApp()

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
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.next")
    app.api().keymap.dispatchCommand("dialog.select.submit")
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
