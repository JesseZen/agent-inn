/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import { InputRenderable, TextareaRenderable } from "@opentui/core"
import { afterEach, expect, mock, test } from "bun:test"
import { onMount } from "solid-js"
import { SDKProvider, useSDK } from "../src/context/sdk"
import { resolveSlashCommand } from "../src/keymap"
import type { BatchRun } from "../src/proxy/backend"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"

const launchCalls: unknown[] = []
const pasteCalls: unknown[] = []

afterEach(() => {
  launchCalls.length = 0
  pasteCalls.length = 0
  mock.restore()
})

function installLaunchMock() {
  mock.module("../src/proxy/launch", () => ({
    createProxyLaunchCommand() {
      return ["ainn", "launch"]
    },
    renderProxyLaunchCommand(command: string[]) {
      return command.join(" ")
    },
    async launchProxySession(opts: unknown) {
      launchCalls.push(opts)
      return true
    },
    async pasteHostedPrompt(opts: unknown) {
      pasteCalls.push(opts)
      return true
    },
  }))
}

async function loadProxyFixture() {
  return import("./proxy-commands.fixture")
}

async function submitPrompt(app: Awaited<ReturnType<(typeof import("./proxy-commands.fixture"))["mountProxyApp"]>>, value?: string) {
  await app.render()
  const editor = app.setup.renderer.currentFocusedEditor
  if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused prompt")
  if (value !== undefined) {
    editor.selectAll()
    await app.mockInput.typeText(value)
  }
  app.api.keymap.dispatchCommand("dialog.prompt.submit")
  await app.render()
}

test("proxy batch command is registered", async () => {
  installLaunchMock()
  const { mountProxyApp } = await loadProxyFixture()
  const app = await mountProxyApp()

  try {
    await app.render()
    expect(resolveSlashCommand(app.api.keymap, "/batch")).toBe("proxy.batch")
  } finally {
    await app.cleanup()
  }
})

test("proxy batch create flow launches each hosted variant", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create new batch")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Choose worker") && app.setup.renderer.currentFocusedEditor instanceof InputRenderable
    })
    expect(app.frame()).not.toContain("app")
    await app.mockInput.typeText("cli-openrouter")
    await runCommand(app, "dialog.select.submit")

    await submitPrompt(app)
    await submitPrompt(app, "fix scroll")
    await submitPrompt(app, "3")
    await submitPrompt(app, "Fix scroll")
    await submitPrompt(app)

    await wait(() => app.calls.createBatch.length === 1 && launchCalls.length === 3 && pasteCalls.length === 3)

    expect(app.calls.createBatch).toEqual([
      {
        title: "fix scroll",
        prompt: "Fix scroll",
        worker_name: "cli-openrouter",
        count: 3,
        source_directory: directory,
      },
    ])
    expect(launchCalls).toEqual([
      expect.objectContaining({
        workerPort: 11199,
        profile: "cli-openrouter",
        workspace: `${directory}/.worktrees/fix-scroll-1`,
        model: undefined,
        mode: "hosted-terminal",
        sessionID: "batch_1_session_1",
      }),
      expect.objectContaining({
        workerPort: 11199,
        profile: "cli-openrouter",
        workspace: `${directory}/.worktrees/fix-scroll-2`,
        model: undefined,
        mode: "hosted-terminal",
        sessionID: "batch_1_session_2",
      }),
      expect.objectContaining({
        workerPort: 11199,
        profile: "cli-openrouter",
        workspace: `${directory}/.worktrees/fix-scroll-3`,
        model: undefined,
        mode: "hosted-terminal",
        sessionID: "batch_1_session_3",
      }),
    ])
    expect(app.calls.getHostedSession).toEqual(["batch_1_session_1", "batch_1_session_2", "batch_1_session_3"])
    expect(pasteCalls).toEqual([
      { prompt: "Fix scroll", submit: true, tmuxSocketName: "ainn", tmuxWindowID: "@1" },
      { prompt: "Fix scroll", submit: true, tmuxSocketName: "ainn", tmuxWindowID: "@2" },
      { prompt: "Fix scroll", submit: true, tmuxSocketName: "ainn", tmuxWindowID: "@3" },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy batch create flow waits for tmux window before pasting prompt", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const app = await mountProxyApp({ batchHostedSessionWindowMode: "missing" })

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create new batch")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Choose worker") && app.setup.renderer.currentFocusedEditor instanceof InputRenderable
    })
    await app.mockInput.typeText("cli-openrouter")
    await runCommand(app, "dialog.select.submit")

    await submitPrompt(app)
    await submitPrompt(app, "no window")
    await submitPrompt(app, "1")
    await submitPrompt(app, "Fix scroll")
    await submitPrompt(app)

    await wait(async () => {
      await app.render()
      return app.calls.getHostedSession.length === 1 && app.frame().includes("Batch: no window")
    })

    expect(launchCalls).toEqual([
      expect.objectContaining({
        workspace: `${directory}/.worktrees/no-window-1`,
        sessionID: "batch_1_session_1",
      }),
    ])
    expect(app.calls.getHostedSession).toEqual(["batch_1_session_1"])
    expect(pasteCalls).toEqual([])
  } finally {
    await app.cleanup()
  }
})

test("proxy batch create flow omits blank count so the backend default applies", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create new batch")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Choose worker") && app.setup.renderer.currentFocusedEditor instanceof InputRenderable
    })
    await app.mockInput.typeText("cli-openrouter")
    await runCommand(app, "dialog.select.submit")

    await submitPrompt(app)
    await submitPrompt(app, "default count")
    await submitPrompt(app)
    await submitPrompt(app, "Fix scroll")
    await submitPrompt(app)

    await wait(() => app.calls.createBatch.length === 1 && launchCalls.length === 3)

    expect(app.calls.createBatch).toEqual([
      {
        title: "default count",
        prompt: "Fix scroll",
        worker_name: "cli-openrouter",
        source_directory: directory,
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy batch create flow re-prompts invalid variant count before creating", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create new batch")
    })

    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Choose worker") && app.setup.renderer.currentFocusedEditor instanceof InputRenderable
    })
    await app.mockInput.typeText("cli-openrouter")
    await runCommand(app, "dialog.select.submit")

    await submitPrompt(app)
    await submitPrompt(app, "invalid count")
    await submitPrompt(app, "abc")

    await wait(async () => {
      await app.render()
      return app.frame().includes("Variant Count")
    })
    expect(app.calls.createBatch).toEqual([])

    await submitPrompt(app, "2")
    await submitPrompt(app, "Fix scroll")
    await submitPrompt(app)

    await wait(() => app.calls.createBatch.length === 1 && launchCalls.length === 2)
    expect(app.calls.createBatch).toEqual([
      {
        title: "invalid count",
        prompt: "Fix scroll",
        worker_name: "cli-openrouter",
        count: 2,
        source_directory: directory,
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("batch winner action selects the highlighted variant", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const batch: BatchRun = {
    id: "batch_1",
    title: "fix scroll",
    prompt: "Fix scroll",
    worker_name: "cli-openrouter",
    worker_port: 11199,
    source_directory: directory,
    created_at: "2026-07-09T00:00:00Z",
    variants: [
      {
        id: "variant_1",
        index: 1,
        hosted_session_id: "session_1",
        session_label: "fix scroll 1",
        worktree_dir: `${directory}/.worktrees/fix-scroll-1`,
      },
      {
        id: "variant_2",
        index: 2,
        hosted_session_id: "session_2",
        session_label: "fix scroll 2",
        worktree_dir: `${directory}/.worktrees/fix-scroll-2`,
      },
    ],
  }
  const app = await mountProxyApp({ batches: [batch] })

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create new batch") && app.frame().includes("fix scroll")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("fix scroll 1") && app.frame().includes("fix scroll 2")
    })

    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("batch.winner")
    await wait(() => app.calls.selectBatchWinner.length === 1)

    expect(app.calls.selectBatchWinner).toEqual([{ batchID: "batch_1", variantID: "variant_2" }])
  } finally {
    await app.cleanup()
  }
})

type BatchClient = ReturnType<typeof useSDK>["client"] & {
  createBatch(input: {
    title: string
    prompt?: string
    worker_name: string
    count?: number
    source_directory: string
    model?: string
  }): Promise<unknown>
  selectBatchWinner(batchID: string, variantID: string): Promise<unknown>
}

async function withBatchClient(fn: (client: BatchClient) => Promise<void>) {
  let done: Promise<void> | undefined
  const calls = {
    createBatch: [] as unknown[],
    selectBatchWinner: [] as string[],
  }

  function Probe() {
    const client = useSDK().client as BatchClient
    onMount(() => {
      done = fn(client)
    })
    return <box />
  }

  const events = createEventSource()
  const fetch = createFetch(async (url, request) => {
    if (url.pathname === "/api/batches" && request.method === "POST") {
      calls.createBatch.push(await request.json())
      return json({
        id: "batch_1",
        title: "Race parser fix",
        worker_name: "coder",
        worker_port: 19091,
        source_directory: "/repo",
        created_at: "2026-07-09T00:00:00Z",
        variants: [],
      })
    }
    if (url.pathname === "/api/batches/batch_1/variants/variant_2/select" && request.method === "POST") {
      calls.selectBatchWinner.push(url.pathname)
      return json({
        id: "batch_1",
        title: "Race parser fix",
        worker_name: "coder",
        worker_port: 19091,
        source_directory: "/repo",
        created_at: "2026-07-09T00:00:00Z",
        winner_variant_id: "variant_2",
        variants: [],
      })
    }
    return undefined
  })

  const app = await testRender(() => (
    <SDKProvider url="http://test" directory={directory} events={events.source} fetch={fetch.fetch}>
      <Probe />
    </SDKProvider>
  ))

  try {
    await done
  } finally {
    app.renderer.destroy()
  }

  return calls
}

test("createBatch POSTs the exact request body to /api/batches", async () => {
  let created: unknown

  const calls = await withBatchClient(async (client) => {
    created = await client.createBatch({
      title: "Race parser fix",
      prompt: "fix it",
      worker_name: "coder",
      count: 3,
      source_directory: "/repo",
      model: "gpt-5",
    })
  })

  expect(calls.createBatch).toEqual([
    {
      title: "Race parser fix",
      prompt: "fix it",
      worker_name: "coder",
      count: 3,
      source_directory: "/repo",
      model: "gpt-5",
    },
  ])
  expect(created).toEqual({
    id: "batch_1",
    title: "Race parser fix",
    worker_name: "coder",
    worker_port: 19091,
    source_directory: "/repo",
    created_at: "2026-07-09T00:00:00Z",
    variants: [],
  })
})

test("selectBatchWinner POSTs to the batch variant select endpoint", async () => {
  let selected: unknown

  const calls = await withBatchClient(async (client) => {
    selected = await client.selectBatchWinner("batch_1", "variant_2")
  })

  expect(calls.selectBatchWinner).toEqual(["/api/batches/batch_1/variants/variant_2/select"])
  expect(selected).toEqual({
    id: "batch_1",
    title: "Race parser fix",
    worker_name: "coder",
    worker_port: 19091,
    source_directory: "/repo",
    created_at: "2026-07-09T00:00:00Z",
    winner_variant_id: "variant_2",
    variants: [],
  })
})

test("pasteHostedPrompt sends literal prompt text without Enter", async () => {
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
      }
      return child
    },
  }))

  const launchModule = await import(`../src/proxy/launch?paste-hosted-prompt=${Date.now()}`)
  const pasted = await launchModule.pasteHostedPrompt({
    prompt: "prompt",
    tmuxSocketName: "ainn",
    tmuxWindowID: "@12",
  })

  expect(pasted).toBe(true)
  expect(spawns).toEqual([
    {
      cmd: "tmux",
      args: ["-L", "ainn", "send-keys", "-l", "-t", "@12", "prompt"],
    },
  ])
})

test("pasteHostedPrompt can submit after literal prompt", async () => {
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
      }
      return child
    },
  }))

  const launchModule = await import(`../src/proxy/launch?paste-hosted-submit=${Date.now()}`)
  const pasted = await launchModule.pasteHostedPrompt({
    prompt: "prompt",
    submit: true,
    tmuxSocketName: "ainn",
    tmuxWindowID: "@12",
  })

  expect(pasted).toBe(true)
  expect(spawns).toEqual([
    {
      cmd: "tmux",
      args: ["-L", "ainn", "send-keys", "-l", "-t", "@12", "prompt"],
    },
    {
      cmd: "tmux",
      args: ["-L", "ainn", "send-keys", "-t", "@12", "Enter"],
    },
  ])
})

test("pasteHostedPrompt returns false for an empty prompt", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      throw new Error("empty prompt should not spawn")
    },
  }))

  const launchModule = await import(`../src/proxy/launch?paste-empty-prompt=${Date.now()}`)
  const pasted = await launchModule.pasteHostedPrompt({
    prompt: "",
    tmuxWindowID: "@12",
  })

  expect(pasted).toBe(false)
  expect(spawns).toEqual([])
})
