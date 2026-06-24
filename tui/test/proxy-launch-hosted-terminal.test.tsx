import { expect, test } from "bun:test"
import { Global } from "@codex-proxy/core/global"
import { homedir } from "node:os"
import path from "node:path"
import { createProxyLaunchCommand, renderProxyLaunchCommand } from "../src/proxy/launch"
import { defaultWorker, directory, json, mountHostedTerminalApp, wait } from "./proxy-hosted-terminal.fixture"

test("Global.Path.config defaults to ~/.codex-proxy", () => {
  expect(Global.Path.config).toBe(path.join(homedir(), ".codex-proxy"))
})

test("createProxyLaunchCommand omits --mode for external-window", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "external-window" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli"])
})

test("createProxyLaunchCommand includes --mode hosted-terminal when selected", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli", "--mode", "hosted-terminal"])
})

test("createProxyLaunchCommand includes --config-dir for hosted terminal launches", () => {
  const cmd = createProxyLaunchCommand({
    workerPort: 1234,
    profile: "cli",
    mode: "hosted-terminal",
    configDir: "/tmp/codex-config",
  })
  expect(cmd).toEqual([
    "codex-proxy",
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
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli"])
})

test("renderProxyLaunchCommand quotes hosted-terminal mode", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  const rendered = renderProxyLaunchCommand(cmd)
  expect(rendered).toContain("'--mode' 'hosted-terminal'")
})

test("launch dialog prompts for mode before worker selection", async () => {
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
    expect(frame.includes("Launch Codex CLI")).toBe(true)
    expect(frame.includes("External window")).toBe(true)
    expect(frame.includes("Hosted terminal")).toBe(true)
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

test("stale hosted session cannot be opened", async () => {
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
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})
