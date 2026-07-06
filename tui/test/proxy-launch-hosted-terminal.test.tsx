import { afterEach, expect, mock, test } from "bun:test"
import { Global } from "@agent-inn/core/global"
import { TextareaRenderable } from "@opentui/core"
import { homedir, tmpdir } from "node:os"
import path from "node:path"
import { chmod, mkdtemp } from "node:fs/promises"
import { createProxyLaunchCommand, renderProxyLaunchCommand } from "../src/proxy/launch"
import { activeHostedSession, defaultWorker, directory, json, mountHostedTerminalApp, wait } from "./proxy-hosted-terminal.fixture"

afterEach(() => {
  mock.restore()
})

test("Global.Path.config defaults to ~/.ainn", () => {
  expect(Global.Path.config).toBe(path.join(homedir(), ".ainn"))
})

test("createProxyLaunchCommand omits --mode for external-window", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "external-window" })
  expect(cmd).toEqual(["ainn", "launch", "--worker", "1234", "--profile", "cli"])
})

test("createProxyLaunchCommand includes --mode hosted-terminal when selected", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  expect(cmd).toEqual(["ainn", "launch", "--worker", "1234", "--profile", "cli", "--mode", "hosted-terminal"])
})

test("createProxyLaunchCommand includes --config-dir for hosted terminal launches", () => {
  const cmd = createProxyLaunchCommand({
    workerPort: 1234,
    profile: "cli",
    mode: "hosted-terminal",
    configDir: "/tmp/codex-config",
  })
  expect(cmd).toEqual([
    "ainn",
    "launch",
    "--worker",
    "1234",
    "--profile",
    "cli",
    "--config-dir",
    "/tmp/codex-config",
    "--mode",
    "hosted-terminal",
  ])
})

test("createProxyLaunchCommand omits --mode by default", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli" })
  expect(cmd).toEqual(["ainn", "launch", "--worker", "1234", "--profile", "cli"])
})

test("renderProxyLaunchCommand quotes hosted-terminal mode", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  const rendered = renderProxyLaunchCommand(cmd)
  expect(rendered).toContain("'--mode' 'hosted-terminal'")
})

test("launchHostedTerminal reuses existing macOS terminal window when tmux already has a client", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:os", () => ({
    platform: () => "darwin",
  }))
  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      let onStdoutData: ((chunk: Buffer) => void) | undefined
      const child = {
        stdout: {
          on(event: string, handler: (data: Buffer) => void) {
            if (event === "data") onStdoutData = handler
          },
        },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") {
            queueMicrotask(() => {
              if (cmd === "tmux" && args[2] === "list-clients") onStdoutData?.(Buffer.from("/dev/ttys001: ainn-host\n"))
              handler(0)
            })
          }
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const launchModule = await import(`../src/proxy/launch?reuse-existing-client=${Date.now()}`)
  const launched = await launchModule.launchProxySession({
    executable: "ainn",
    workerPort: 1234,
    profile: "cli",
    configDir: "/tmp/codex-config",
    mode: "hosted-terminal",
    sessionID: "hs_1",
    opener: "default",
    tmuxSocketName: "ainn",
    tmuxHostSession: "ainn-host",
  })

  expect(launched).toBe(true)
  expect(spawns).toEqual([
    {
      cmd: "ainn",
      args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "cli", "--config-dir", "/tmp/codex-config", "--session-id", "hs_1"],
    },
    {
      cmd: "tmux",
      args: ["-L", "ainn", "list-clients", "-t", "ainn-host"],
    },
    {
      cmd: "osascript",
      args: ["-e", 'tell application "Terminal" to activate'],
    },
  ])
})

test("launch dialog uses hosted terminal default mode", async () => {
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    return undefined
  })

  try {
    await app.openLaunchDialog()
    const frame = app.setup.captureCharFrame()
    expect(frame.includes("Hosted Terminal")).toBe(true)
    expect(frame.includes("Create new session")).toBe(true)
    expect(frame.includes("External window")).toBe(false)
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("launch dialog opens hosted terminal session menu", async () => {
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            session_id: "hs_1",
            session_label: "solve problem A",
            worker_name: "test-cli",
            worker_port: 1234,
            status: "active",
            created_at: "2026-06-23T00:00:00Z",
            last_opened_at: "2026-06-23T00:00:00Z",
          },
        ],
      })
    return undefined
  })

  try {
    await app.openHostedTerminalPicker()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("solve problem A")
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("hosted terminal create returns to refreshed session list", async () => {
  const originalPath = process.env.PATH
  const originalExecutable = process.env.AINN_EXECUTABLE
  const bin = await mkdtemp(path.join(tmpdir(), "ainn-hosted-terminal."))
  const fakeAinn = path.join(bin, "ainn")
  const fakeTmux = path.join(bin, "tmux")
  const fakeOsa = path.join(bin, "osascript")
  await Bun.write(fakeAinn, "#!/bin/sh\nexit 0\n")
  await Bun.write(fakeTmux, "#!/bin/sh\nif [ \"$3\" = \"list-clients\" ]; then echo '/dev/ttys001: ainn-host'; fi\nexit 0\n")
  await Bun.write(fakeOsa, "#!/bin/sh\nexit 0\n")
  await chmod(fakeAinn, 0o755)
  await chmod(fakeTmux, 0o755)
  await chmod(fakeOsa, 0o755)
  process.env.PATH = `${bin}:${originalPath ?? ""}`
  process.env.AINN_EXECUTABLE = fakeAinn
  let hostedSessionCalls = 0
  const app = await mountHostedTerminalApp((url, request) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/settings")
      return json({
        settings: {
          state_dir: "~/.ainn",
          log_dir: "~/.ainn/logs",
          launch: { default_mode: "hosted-terminal" },
          terminal: {
            host: "tmux",
            opener: "default",
            tmux: {
              socket_name: "ainn",
              host_session: "ainn-host",
              host_start_mode: "new-window",
              turn_status_hooks: false,
            },
          },
        },
      })
    if (url.pathname === "/api/hosted-sessions" && request.method === "GET") {
      hostedSessionCalls += 1
      return json({
        sessions: hostedSessionCalls > 1
          ? [
              {
                session_id: "hs_created",
                session_label: "test-cli 1",
                worker_name: "test-cli",
                worker_port: 1234,
                status: "active",
                created_at: "2026-06-23T00:00:00Z",
                last_opened_at: "2026-06-23T00:00:00Z",
              },
            ]
          : [],
      })
    }
    return undefined
  })

  try {
    await app.openHostedTerminalPicker()
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Choose worker")
    })
    app.api().keymap.dispatchCommand("dialog.select.submit")
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
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("test-cli 1")
    }, 5000)

    expect(hostedSessionCalls >= 2).toBe(true)
    expect(app.setup.captureCharFrame()).toContain("test-cli 1")
  } finally {
    process.env.PATH = originalPath
    if (originalExecutable === undefined) {
      delete process.env.AINN_EXECUTABLE
    } else {
      process.env.AINN_EXECUTABLE = originalExecutable
    }
    await app.cleanup()
  }
})

test("hosted terminal duplicate label alert returns to worker picker", async () => {
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            ...activeHostedSession,
            session_label: "test-cli 1",
          },
        ],
      })
    return undefined
  })

  try {
    await app.openHostedTerminalPicker()
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Choose worker")
    })
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Launch Worker")
    })
    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Create Hosted Session")
    })
    await wait(() => app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable)
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused hosted session label prompt")
    editor.selectAll()
    await app.setup.mockInput.typeText("test-cli 1")
    app.api().keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.setup.renderOnce()
      return app.setup.captureCharFrame().includes("Create hosted session failed")
    })

    app.setup.mockInput.pressEnter()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Choose worker") && frame.includes("test-cli")
    })
  } finally {
    await app.cleanup()
  }
})

test("stale hosted session launches reopen through CLI", async () => {
  const originalPath = process.env.PATH
  const originalExecutable = process.env.AINN_EXECUTABLE
  const bin = await mkdtemp(path.join(tmpdir(), "ainn-hosted-terminal."))
  const callsPath = path.join(bin, "ainn-calls.log")
  const fakeAinn = path.join(bin, "ainn")
  const fakeTmux = path.join(bin, "tmux")
  const fakeOsa = path.join(bin, "osascript")
  await Bun.write(fakeAinn, `#!/bin/sh\nprintf '%s\\n' "$*" >> '${callsPath}'\nexit 0\n`)
  await Bun.write(fakeTmux, "#!/bin/sh\nif [ \"$3\" = \"list-clients\" ]; then echo '/dev/ttys001: ainn-host'; fi\nexit 0\n")
  await Bun.write(fakeOsa, "#!/bin/sh\nexit 0\n")
  await chmod(fakeAinn, 0o755)
  await chmod(fakeTmux, 0o755)
  await chmod(fakeOsa, 0o755)
  process.env.PATH = `${bin}:${originalPath ?? ""}`
  process.env.AINN_EXECUTABLE = fakeAinn
  const spawns: Array<{ cmd: string; args: string[] }> = []
  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      let onStdoutData: ((chunk: Buffer) => void) | undefined
      const child = {
        stdout: {
          on(event: string, handler: (data: Buffer) => void) {
            if (event === "data") onStdoutData = handler
          },
        },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") {
            queueMicrotask(() => {
              if (cmd === "tmux" && args[2] === "list-clients") onStdoutData?.(Buffer.from("/dev/ttys001: ainn-host\n"))
              handler(0)
            })
          }
          return child
        },
        unref() {},
      }
      return child
    },
  }))
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/settings")
      return json({
        settings: {
          state_dir: "~/.ainn",
          log_dir: "~/.ainn/logs",
          launch: { default_mode: "hosted-terminal" },
          terminal: {
            host: "tmux",
            opener: "default",
            tmux: {
              socket_name: "ainn",
              host_session: "ainn-host",
              host_start_mode: "new-window",
              turn_status_hooks: false,
            },
          },
        },
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            session_id: "hs_1",
            session_label: "solve problem A",
            worker_name: "test-cli",
            worker_port: 1234,
            created_at: "2026-06-23T00:00:00Z",
            last_opened_at: "2026-06-23T00:00:00Z",
            status: "stale",
          },
        ],
      })
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
    app.api().keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      const text = await Bun.file(callsPath).text().catch(() => "")
      return text.includes("launch --worker 1234 --mode hosted-terminal --no-attach --profile test-cli") ||
        spawns.some((spawned) =>
          spawned.cmd.endsWith("ainn") &&
          spawned.args.join(" ") === "launch --worker 1234 --mode hosted-terminal --no-attach --profile test-cli --config-dir " + Global.Path.config + " --session-id hs_1"
        )
    })
  } finally {
    process.env.PATH = originalPath
    if (originalExecutable === undefined) {
      delete process.env.AINN_EXECUTABLE
    } else {
      process.env.AINN_EXECUTABLE = originalExecutable
    }
    await app.cleanup()
  }
})
