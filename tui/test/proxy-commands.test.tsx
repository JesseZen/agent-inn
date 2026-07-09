import { expect, test } from "bun:test"
import { InputRenderable, TextareaRenderable } from "@opentui/core"
import { resolveSlashCommand } from "../src/keymap"
import { mountProxyApp, openWorkerDetail, runCommand, wait } from "./proxy-commands.fixture"

test("proxy logs opens worker logs dialog with initial log lines", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.logs")
    await app.render()
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.getLogs === 1)
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Logs: app (:6767)") && frame.includes("booted")
    })

    expect(app.frame()).toContain("Logs: app (:6767)")
    expect(app.frame()).toContain("booted")
  } finally {
    await app.cleanup()
  }
})

test("command palette opens help and returns to it from nested commands", async () => {
  const app = await mountProxyApp()

  try {
    await wait(async () => {
      await app.render()
      return app.frame().includes("Ask anything")
    })
    app.mockInput.pressKey("p", { ctrl: true })
    await wait(async () => {
      await app.render()
      return app.frame().includes("Commands")
    })
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof InputRenderable
    })

    await app.mockInput.typeText("help")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Help")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Help") && frame.includes("Press")
    })

    app.mockInput.pressKey("p", { ctrl: true })
    await wait(async () => {
      await app.render()
      return app.frame().includes("Commands")
    })

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Help") && frame.includes("Press")
    })

    app.mockInput.pressKey("p", { ctrl: true })
    await wait(async () => {
      await app.render()
      return app.frame().includes("Commands")
    })
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof InputRenderable
    })

    await app.mockInput.typeText("help")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Help")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Help") && frame.includes("Press") && !frame.includes("Commands")
    })
    expect({ depth: app.api.ui.dialog.depth }).toEqual({ depth: 2 })

    app.mockInput.pressEnter()
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Help") && frame.includes("Press")
    })
    expect({ depth: app.api.ui.dialog.depth }).toEqual({ depth: 1 })
  } finally {
    await app.cleanup()
  }
})

test("proxy config save clears dirty state on reopen", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.end")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.saveConfig === 1)
    await app.render()

    app.api.keymap.dispatchCommand("proxy.settings")
    await app.render()
    expect(app.frame().includes("Save Config to Disk")).toBe(false)
  } finally {
    await app.cleanup()
  }
})

test("proxy settings editor patches settings through manager API", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Settings") && frame.includes("State Dir") && frame.includes("~/.ainn")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused settings prompt")
    editor.selectAll()
    await app.mockInput.typeText("/tmp/ainn-state")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      return app.calls.patchSettings.length === 1
    })

    expect(app.calls.patchSettings).toEqual([{ state_dir: "/tmp/ainn-state" }])
  } finally {
    await app.cleanup()
  }
})

test("proxy settings field save keeps settings dialog open with updated value", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Settings") && app.frame().includes("State Dir") && app.frame().includes("~/.ainn")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused settings prompt")
    editor.selectAll()
    await app.mockInput.typeText("/tmp/ainn-state")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Settings") && frame.includes("State Dir") && frame.includes("/tmp/ainn-state")
    })

    expect(app.calls.patchSettings).toEqual([{ state_dir: "/tmp/ainn-state" }])
    expect(app.frame()).toContain("Settings")
    expect(app.frame()).toContain("/tmp/ainn-state")
  } finally {
    await app.cleanup()
  }
})

test("proxy settings default launch mode uses a select list", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Settings") && app.frame().includes("Default Launch Mode hosted-terminal")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Default Launch Mode") && app.frame().includes("External window")
    })

    await runCommand(app, "dialog.select.prev")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Default Launch Mode external-window")
    })

    expect(app.calls.patchSettings).toContainEqual({
      launch: { default_mode: "external-window" },
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy settings terminal opener uses a select list", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Settings") && app.frame().includes("State Dir")
    })

    for (let i = 0; i < 3; i++) {
      await runCommand(app, "dialog.select.next")
    }
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Terminal Opener") && app.frame().includes("iTerm2")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.calls.patchSettings.some((entry) => entry.terminal?.opener === "iterm2") && app.frame().includes("Settings")
    })

    expect(app.calls.patchSettings).toContainEqual({
      terminal: { opener: "iterm2" },
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy settings host start mode save shows note when hosted sessions already exist", async () => {
  const app = await mountProxyApp()

  try {
    app.hostedSessions.splice(0, app.hostedSessions.length, {
      session_id: "hs_1",
      session_label: "solve problem A",
      worker_name: "cli-openrouter",
      worker_port: 11199,
      created_at: "2026-07-03T00:00:00Z",
      last_opened_at: "2026-07-03T00:00:00Z",
      status: "active",
    })

    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Settings") && app.frame().includes("State Dir ~/.ainn")
    })

    for (let i = 0; i < 6; i++) {
      app.api.keymap.dispatchCommand("dialog.select.next")
      await app.render()
    }
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Tmux Host Start Mode") && app.frame().includes("Reuse first window")
    })
    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.calls.listHostedSessions === 1
    }, 5000)
    await wait(async () => {
      await Bun.sleep(10)
      await app.render()
      const frame = app.frame().replace(/\s+/g, " ")
      return frame.includes("Host start mode applies only to newly created tmux")
    }, 5000)

    expect(app.calls.patchSettings).toContainEqual({
      terminal: { tmux: { host_start_mode: "reuse-first-window" } },
    })
    expect(app.calls.listHostedSessions).toBe(1)
    const frame = app.frame().replace(/\s+/g, " ")
    expect(frame).toContain("Host start mode applies only to newly created tmux")
    expect(frame).toContain("hosts. Remove existing hosted terminal sessions to")
    expect(frame).toContain("recreate the host.")
  } finally {
    await app.cleanup()
  }
})

test("proxy settings host start mode accepts main-tui-window", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Settings") && app.frame().includes("State Dir ~/.ainn")
    })

    for (let i = 0; i < 6; i++) {
      app.api.keymap.dispatchCommand("dialog.select.next")
      await app.render()
    }
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Tmux Host Start Mode") && app.frame().includes("Main TUI window")
    })
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.calls.patchSettings.some((entry) => entry.terminal?.tmux?.host_start_mode === "main-tui-window")
    })

    expect(app.calls.patchSettings).toContainEqual({
      terminal: { tmux: { host_start_mode: "main-tui-window" } },
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy settings turn status hooks uses a select list", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Settings") && app.frame().includes("State Dir ~/.ainn")
    })

    for (let i = 0; i < 7; i++) {
      app.api.keymap.dispatchCommand("dialog.select.next")
      await app.render()
    }
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Tmux Turn Status Hooks") && frame.includes("Enabled")
    })
    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.calls.patchSettings.some((entry) => entry.terminal?.tmux?.turn_status_hooks === true)
    })

    expect(app.calls.patchSettings).toContainEqual({
      terminal: { tmux: { turn_status_hooks: true } },
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy settings command is registered and config command is removed", async () => {
  const app = await mountProxyApp()

  try {
    const commands = app.api.keymap.getCommandEntries({
      namespace: "palette",
      visibility: "registered",
    })
    const names = commands.map((entry) => entry.command.name)
    expect(names.includes("proxy.settings")).toBe(true)

    expect(resolveSlashCommand(app.api.keymap, "/settings")).toBe("proxy.settings")
    expect(resolveSlashCommand(app.api.keymap, "/config")).toBe("proxy.settings")
  } finally {
    await app.cleanup()
  }
})

test("proxy worker status commands are folded into workers", async () => {
  const app = await mountProxyApp()

  try {
    const commands = app.api.keymap.getCommandEntries({
      namespace: "palette",
      visibility: "registered",
    })
    expect(commands.map((entry) => entry.command.name).includes("proxy.status")).toBe(false)
    expect(commands.map((entry) => entry.command.name).includes("proxy.modules")).toBe(false)

    await openWorkerDetail(app)

    expect(app.frame()).toContain("Switch Upstream")
    expect(app.frame()).toContain("View Logs")
    expect(app.frame()).toContain("Manage Modules")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail exposes worker status and scoped actions", async () => {
  const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    expect(app.frame()).toContain("Manage Workers")
    expect(app.frame()).toContain("Create New Worker")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("app (:6767)")
    expect(app.frame()).toContain("status: running")
    expect(app.frame()).toContain("upstream: openai")
    expect(app.frame()).toContain("protocol: chat_completions")
    expect(app.frame()).toContain("log level: simple")
    expect(app.frame()).toContain("modules")
    expect(app.frame()).toContain("config_patch: active")
    expect(app.frame()).toContain("Log Level")
    expect(app.frame()).toContain("Switch Upstream")
    expect(app.frame()).toContain("Manage Modules")
    expect(app.frame()).toContain("View Logs")
    expect(app.frame()).toContain("Launcher")
    expect(app.frame()).toContain("Port")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail escape returns to manage workers", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("app (:6767)")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Manage Workers")
    })

    expect(app.frame()).toContain("Manage Workers")
    expect(app.frame()).toContain("Create New Worker")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers nested select esc click returns to worker detail", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Log Level: app")

    const lines = app.frame().split("\n")
    const row = lines.findIndex((line) => line.includes("esc"))
    const column = row >= 0 ? lines[row].indexOf("esc") : -1
    if (row < 0 || column < 0) throw new Error("expected visible esc affordance")

    await app.setup.mockMouse.click(column, row)
    await app.render()
    await wait(async () => {
      await app.render()
      return app.frame().includes("app (:6767)")
    })

    expect(app.frame()).toContain("Log Level")
    expect(app.frame()).toContain("Switch Upstream")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Manage Workers")
    })
    expect(app.frame()).toContain("Manage Workers")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers launcher subselect escape returns to worker detail", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    for (let i = 0; i < 4; i++) {
      await runCommand(app, "dialog.select.next")
    }
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Launcher: app")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("app (:6767)")
    })

    expect(app.frame()).toContain("Log Level")
    expect(app.frame()).toContain("Launcher")
    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Manage Workers")
    })
    expect(app.frame()).toContain("Manage Workers")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers log level selection returns to worker detail before patch finishes", async () => {
  const app = await mountProxyApp({ patchWorkerDelayMs: 500 })

  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Log Level: app")

    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()

    expect(app.frame()).toContain("app (:6767)")
    expect(app.frame()).toContain("Log Level")
    expect(app.frame()).not.toContain("Log Level: app")

    await wait(async () => {
      await app.render()
      return app.frame().includes("log level: detail")
    })

    expect(app.frame()).toContain("app (:6767)")
    expect(app.frame()).toContain("Log Level")
    expect(app.frame()).toContain("log level: detail")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers launcher selection returns to worker detail before patch finishes", async () => {
  const app = await mountProxyApp({ patchWorkerDelayMs: 500 })

  try {
    await openWorkerDetail(app)
    for (let i = 0; i < 4; i++) {
      await runCommand(app, "dialog.select.next")
    }
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Launcher: app")

    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()

    expect(app.frame()).toContain("app (:6767)")
    expect(app.frame()).toContain("Launcher")
    expect(app.frame()).not.toContain("Launcher: app")

    await wait(async () => {
      await app.render()
      return app.frame().includes("launcher: claudecode")
    })

    expect(app.frame()).toContain("app (:6767)")
    expect(app.frame()).toContain("Launcher")
    expect(app.frame()).toContain("launcher: claudecode")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers proxy URL prompt patches worker proxy_url", async () => {
	const app = await mountProxyApp()

	try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("proxy: direct")

    for (let i = 0; i < 6; i++) {
      await runCommand(app, "dialog.select.next")
    }
    expect(app.frame()).toContain("Proxy URL")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused proxy URL prompt")
    await app.mockInput.typeText("http://127.0.0.1:7890")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      return app.calls.patchWorker.some((call) => call.proxy_url === "http://127.0.0.1:7890")
    })

    expect(app.calls.patchWorker).toContainEqual({
      port: 6767,
      upstream: "openai",
      log_level: "simple",
      proxy_url: "http://127.0.0.1:7890",
    })
    expect(app.frame()).toContain("proxy: http://127.0.0.1:7890")
  } finally {
    await app.cleanup()
	}
})

test("proxy workers proxy URL prompt does not prefill redacted credential URL", async () => {
	const app = await mountProxyApp({
		workers: [
			{
				name: "app",
				port: 6767,
				role: "app",
				protocol: "chat_completions",
				upstream: { name: "openai", base_url: "https://api.openai.com/v1", has_api_key: true },
				status: "running",
				snapshot_generation: 3,
				log_level: "simple",
				proxy_url: "http://127.0.0.1:7890",
				proxy_url_redacted: true,
			},
		],
	})

	try {
		await openWorkerDetail(app)
		expect(app.frame()).toContain("proxy: http://127.0.0.1:7890")

		for (let i = 0; i < 6; i++) {
			await runCommand(app, "dialog.select.next")
		}
		expect(app.frame()).toContain("Proxy URL")
		await runCommand(app, "dialog.select.submit")
		await wait(async () => {
			await app.render()
			return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
		})
		const editor = app.setup.renderer.currentFocusedEditor
		if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused proxy URL prompt")

		expect(editor.plainText).toBe("******")
		app.api.keymap.dispatchCommand("dialog.prompt.submit")
		await app.render()
		expect(app.calls.patchWorker).toEqual([])
	} finally {
		await app.cleanup()
	}
})

test("proxy workers create claudecode worker payload", async () => {
	const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Worker Name")
    })
    await app.mockInput.typeText("claude-main")
    app.mockInput.pressEnter()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Select Launcher")
    })
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Select Upstream")
    })
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.createWorker.length === 1)
    app.mockInput.pressEscape()
    await app.render()

    expect(app.calls.createWorker).toEqual([
      { name: "claude-main", upstream: "anthropic", launcher: "claudecode" },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail shows claudecode launcher and anthropic protocol", async () => {
  const app = await mountProxyApp({
    workers: [
      {
        name: "worker-11201",
        port: 11201,
        role: "cli",
        launcher: "claudecode",
        protocol: "anthropic",
        upstream: { name: "anthropic", base_url: "https://api.anthropic.com/v1", has_api_key: true, api_format: "anthropic" },
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
        modules: {},
        hooks: {},
        module_support: {
          api_translate: { protocols: ["responses", "chat_completions"], capabilities: ["input_text", "tool_calls", "stream_events"] },
          tool_filter: { protocols: ["responses"], capabilities: ["tool_calls"] },
          request_log: { protocols: ["responses", "chat_completions", "anthropic"] },
        },
      },
    ],
  })

  try {
    await runCommand(app, "proxy.workers")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")

    expect(app.frame()).toContain("worker-11201 (:11201)")
    expect(app.frame()).toContain("launcher: claudecode")
    expect(app.frame()).toContain("worker-11201")
    expect(app.frame()).toContain("protocol: anthropic")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers module action patches module through module API", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("Manage Modules")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Module: app")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchModule.length === 1)

    expect(app.calls.patchModule).toEqual([
      {
        port: 6767,
        module: "model_override",
        body: {
          enabled: true,
          params: { model: "gpt-old" },
        },
      },
    ])
    expect(app.calls.patchWorker).toEqual([])

    expect(app.frame()).not.toContain("Modules & Hooks: app")

    app.mockInput.pressEscape()
    await app.render()
    expect(app.frame()).not.toContain("Saved model_override")
  } finally {
    await app.cleanup()
  }
})

test("proxy tool filter module picks blocked tools", async () => {
  const app = await mountProxyApp({
    workers: [
      {
        name: "app",
        port: 6767,
        role: "app",
        protocol: "responses",
        upstream: { name: "openai", base_url: "https://api.openai.com/v1", has_api_key: true },
        status: "running",
        snapshot_generation: 3,
        log_level: "simple",
        modules: {
          tool_filter: { enabled: true, params: { blocked_tools: ["image_generation"] } },
        },
        hooks: {},
        module_support: {
          tool_filter: { protocols: ["responses"], capabilities: ["tool_calls"] },
        },
      },
    ],
  })

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("Manage Modules")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Module: app")
    })

    expect(app.frame()).toContain("Blocked Tools")
    expect(app.frame()).toContain("image_generation")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Blocked Tools: app")
    })

    expect(app.frame()).toContain("✓ image_generation")
    expect(app.frame()).toContain("○ web_search_preview")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchModule.length === 1)
    await wait(async () => {
      await app.render()
      return app.frame().includes("✓ web_search_preview")
    })

    expect(app.calls.patchModule).toEqual([
      {
        port: 6767,
        module: "tool_filter",
        body: {
          enabled: true,
          params: { blocked_tools: ["image_generation", "web_search_preview"] },
        },
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers module view shows lifecycle hooks separately", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("Manage Modules")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    expect(app.frame()).toContain("Request Middleware")
    expect(app.frame()).toContain("request_log")
    expect(app.frame()).toContain("tool_filter")
    expect(app.frame()).toContain("unavailable")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    expect(app.frame()).toContain("Lifecycle Hooks")
    expect(app.frame()).toContain("config_patch")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers module view does not open unavailable candidate module editor", async () => {
  const app = await mountProxyApp()
  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    await wait(async () => {
      await app.render()
      return app.frame().includes("request_log") && app.frame().includes("unavailable")
    })
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await app.render()

    expect(app.frame()).toContain("Modules & Hooks: app")
    expect(app.frame()).not.toContain("Edit Module: app")
    expect(app.calls.patchModule).toEqual([])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers module view opens configured unavailable module editor and allows disable", async () => {
  const app = await mountProxyApp()
  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    await wait(async () => {
      await app.render()
      return app.frame().includes("tool_filter") && app.frame().includes("unavailable")
    })
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Module: app")
    })

    expect(app.frame()).toContain("Disable")
    expect(app.frame()).toContain("unavailable for current protocol")
    expect(app.frame()).not.toContain("Enable")
    expect(app.frame()).not.toContain("API Format")

    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchModule.length === 1)

    expect(app.calls.patchModule).toEqual([
      {
        port: 6767,
        module: "tool_filter",
        body: {
          enabled: false,
          params: undefined,
        },
      },
    ])
    await wait(async () => {
      await app.render()
      return !app.frame().includes("Edit Module: app") && !app.frame().includes("Modules & Hooks: app")
    })
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail controls worker lifecycle", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.prev")
    await runCommand(app, "dialog.select.prev")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.restartWorker.length === 1)
    expect(app.calls.restartWorker).toEqual([6767])

    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.end")
    await runCommand(app, "dialog.select.prev")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.stopWorker.length === 1)
    expect(app.calls.stopWorker).toEqual([6767])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail deletes worker config after confirmation", async () => {
  const app = await mountProxyApp()

	try {
		await openWorkerDetail(app)
		await runCommand(app, "dialog.select.end")
		app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Delete worker")
    })

    app.api.keymap.dispatchCommand("worker.delete")
    app.mockInput.pressEnter()
    await wait(() => app.calls.deleteWorker.length === 1)
    await app.render()

    expect(app.calls.deleteWorker).toEqual([6767])
    await runCommand(app, "proxy.workers")
    expect(app.frame()).not.toContain("app")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail view logs action opens worker logs", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.getLogs === 1)
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Logs: app (:6767)") && frame.includes("booted")
    })

    expect(app.frame()).toContain("Logs: app (:6767)")
    expect(app.frame()).toContain("booted")
  } finally {
    await app.cleanup()
  }
})

test("proxy launch registers a launch command", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Hosted Terminal")
    })
    expect(app.frame()).toContain("Hosted Terminal")
    expect(app.frame()).not.toContain("External window")
  } finally {
    await app.cleanup()
  }
})

test("proxy topology command is registered and opens topology dialog", async () => {
  const app = await mountProxyApp()

  try {
    const commands = app.api.keymap.getCommandEntries({
      namespace: "palette",
      visibility: "registered",
    })
    expect(commands.map((entry) => entry.command.name).includes("proxy.topology")).toBe(true)

    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    const frame = app.frame()
    expect(frame).toContain("Topology")
    expect(frame).toContain("■ upstream")
    expect(frame).toContain("■ running")
    expect(frame).toContain("orphan upstreams")
    expect(frame).toContain("app")
    expect(frame).toContain("anthropic")
  } finally {
    await app.cleanup()
  }
})

test("topology dialog frame is vertically centered", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    const titleRow = app.frame().split("\n").findIndex((line) => line.includes("Topology"))
    expect(titleRow).toBeLessThan(7)
  } finally {
    await app.cleanup()
  }
})

test("topology dialog click on worker navigates to worker status", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.click(5, 13)
    await app.render()
    await app.render()

    const frame = app.frame()
    expect(frame).toContain("app (:6767)")
    expect(frame).toContain("Worker actions")
  } finally {
    await app.cleanup()
  }
})

test("topology dialog click on upstream navigates to upstream editor", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.click(22, 9)
    await app.render()
    await app.render()

    const frame = app.frame()
    expect(frame).toContain("Edit Upstream")
    expect(frame).toContain("openai")
  } finally {
    await app.cleanup()
  }
})

test("topology dialog drag worker to upstream calls patchWorker", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.drag(5, 13, 5, 19)
    await app.render()
    await app.render()

    expect(app.calls.patchWorker).toContainEqual({ port: 6767, upstream: "anthropic", log_level: "simple" })
  } finally {
    await app.cleanup()
  }
})

test("topology dialog does not start a text selection", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    const titleRow = app.frame().split("\n").findIndex((line) => line.includes("Topology"))
    await app.setup.mockMouse.pressDown(1, titleRow)
    await app.setup.mockMouse.moveTo(8, titleRow)
    await app.render()
    const selection = app.setup.renderer.getSelection()
    await app.setup.mockMouse.release(8, titleRow)
    expect(selection === null).toBe(true)
  } finally {
    await app.cleanup()
  }
})

test("topology dialog drag upstream to worker calls patchWorker", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.drag(5, 19, 5, 13)
    await app.render()
    await app.render()

    expect(app.calls.patchWorker).toContainEqual({ port: 6767, upstream: "anthropic", log_level: "simple" })
  } finally {
    await app.cleanup()
  }
})

test("proxy workers editor patches log_level field", async () => {
  const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    expect(app.frame()).toContain("Manage Workers")
    expect(app.frame()).toContain("Create New Worker")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("app (:6767)")

    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Log Level: app")

    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchWorker.some((c) => c.log_level === "detail"))
    await app.render()

    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "detail" }])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers editor preserves proxy URL when patching log_level", async () => {
  const app = await mountProxyApp({
    workers: [
      {
        name: "app",
        port: 6767,
        role: "app",
        protocol: "chat_completions",
        upstream: { name: "openai", base_url: "https://api.openai.com/v1", has_api_key: true },
        status: "running",
        snapshot_generation: 3,
        log_level: "simple",
        proxy_url: "http://proxy.local:7890",
      },
    ],
  })

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("proxy: http://proxy.local:7890")

    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchWorker.some((c) => c.log_level === "detail"))
    await app.render()

    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "detail" }])
    expect(app.frame()).toContain("proxy: http://proxy.local:7890")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers editor patches launcher field", async () => {
  const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("app (:6767)")

    for (let i = 0; i < 4; i++) {
      await runCommand(app, "dialog.select.next")
    }
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Launcher: app")

    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchWorker.some((c) => c.launcher === "claudecode"))
    await app.render()

    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "simple", launcher: "claudecode" }])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers editor patches port field", async () => {
  const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("app (:6767)")

    for (let i = 0; i < 5; i++) {
      await runCommand(app, "dialog.select.next")
    }
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Port: app")
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused worker port prompt")
    editor.selectAll()
    await app.mockInput.typeText("11200")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchWorker.some((c) => c.next_port === 11200))
    await app.render()

    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "simple", next_port: 11200 }])
  } finally {
    await app.cleanup()
  }
})
