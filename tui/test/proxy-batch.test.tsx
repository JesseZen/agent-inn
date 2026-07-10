/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import { InputRenderable, TextareaRenderable } from "@opentui/core"
import { afterEach, expect, mock, test } from "bun:test"
import { onMount } from "solid-js"
import { SDKProvider, useSDK } from "../src/context/sdk"
import { resolveSlashCommand } from "../src/keymap"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"

const launchCalls: unknown[] = []

afterEach(() => {
  launchCalls.length = 0
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
    async setupHostedTerminalSession(opts: unknown) {
      launchCalls.push(opts)
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

test("proxy batch create flow sets up variants before opening one hosted terminal", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create a worktree race")
    })

    expect({
      hasBatchRunsTitle: app.frame().includes("Batch Runs"),
      hasRaceDescription: app.frame().includes("Create isolated worktrees"),
      hasPromptClaim: app.frame().includes("Race variants from one prompt"),
    }).toEqual({
      hasBatchRunsTitle: true,
      hasRaceDescription: true,
      hasPromptClaim: false,
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
    await submitPrompt(app)

    await wait(() => app.calls.createBatch.length === 1 && launchCalls.length === 4)

    expect(app.calls.createBatch).toEqual([
      {
        title: "fix scroll",
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
        hostedTerminalAttachMode: "setup-only",
      }),
      expect.objectContaining({
        workerPort: 11199,
        profile: "cli-openrouter",
        workspace: `${directory}/.worktrees/fix-scroll-2`,
        model: undefined,
        mode: "hosted-terminal",
        sessionID: "batch_1_session_2",
        hostedTerminalAttachMode: "setup-only",
      }),
      expect.objectContaining({
        workerPort: 11199,
        profile: "cli-openrouter",
        workspace: `${directory}/.worktrees/fix-scroll-3`,
        model: undefined,
        mode: "hosted-terminal",
        sessionID: "batch_1_session_3",
        hostedTerminalAttachMode: "setup-only",
      }),
      expect.objectContaining({
        workerPort: 11199,
        profile: "cli-openrouter",
        workspace: `${directory}/.worktrees/fix-scroll-1`,
        model: undefined,
        mode: "hosted-terminal",
        sessionID: "batch_1_session_1",
        hostedTerminalAttachMode: "open",
      }),
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy batch create flow does not inspect tmux windows", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const app = await mountProxyApp({ batchHostedSessionWindowMode: "missing" })

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create a worktree race")
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
    await submitPrompt(app)

    await wait(async () => {
      await app.render()
      return app.frame().includes("Batch: no window")
    })

    expect(launchCalls).toEqual([
      expect.objectContaining({
        workspace: `${directory}/.worktrees/no-window-1`,
        sessionID: "batch_1_session_1",
        hostedTerminalAttachMode: "setup-only",
      }),
      expect.objectContaining({
        workspace: `${directory}/.worktrees/no-window-1`,
        sessionID: "batch_1_session_1",
        hostedTerminalAttachMode: "open",
      }),
    ])
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
      return app.frame().includes("Create a worktree race")
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
    await submitPrompt(app)

    await wait(() => app.calls.createBatch.length === 1 && launchCalls.length === 4)

    expect(app.calls.createBatch).toEqual([
      {
        title: "default count",
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
      return app.frame().includes("Create a worktree race")
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
    await submitPrompt(app)

    await wait(() => app.calls.createBatch.length === 1 && launchCalls.length === 3)
    expect(app.calls.createBatch).toEqual([
      {
        title: "invalid count",
        worker_name: "cli-openrouter",
        count: 2,
        source_directory: directory,
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("batch detail shows hosted session states", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const batch = {
    id: "batch_1",
    title: "fix scroll",
    worker_name: "cli-openrouter",
    worker_port: 11199,
    source_directory: directory,
    created_at: "2026-07-09T00:00:00Z",
    variants: [
      {
        id: "variant_1",
        index: 1,
        hosted_session_id: "session_1",
        session_label: "fix scroll batch_1 #1",
        worktree_dir: `${directory}/.worktrees/fix-scroll-1`,
      },
      {
        id: "variant_2",
        index: 2,
        hosted_session_id: "session_2",
        session_label: "fix scroll batch_1 #2",
        worktree_dir: `${directory}/.worktrees/fix-scroll-2`,
      },
    ],
  }
  const app = await mountProxyApp({ batches: [batch] })
  app.hostedSessions.push(
    {
      session_id: "session_1",
      session_label: "fix scroll batch_1 #1",
      worker_name: "cli-openrouter",
      worker_port: 11199,
      status: "active",
      turn_state: "idle",
      created_at: "2026-07-09T00:00:00Z",
      last_opened_at: "2026-07-09T00:00:00Z",
    },
    {
      session_id: "session_2",
      session_label: "fix scroll batch_1 #2",
      worker_name: "cli-openrouter",
      worker_port: 11199,
      status: "active",
      turn_state: "running",
      created_at: "2026-07-09T00:00:00Z",
      last_opened_at: "2026-07-09T00:00:00Z",
    },
  )

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create a worktree race") && app.frame().includes("fix scroll")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return app.calls.listHostedSessions === 1 && (frame.includes("active") || frame.includes("ready"))
    })

    expect(app.frame()).toContain("ready")
    expect(app.frame()).toContain("running")
    expect(app.frame()).not.toContain("Hosted session")
    expect(app.frame()).not.toContain("Worktree")
    expect(app.frame()).not.toContain("Winner")
  } finally {
    await app.cleanup()
  }
})

test("batch detail refreshes when a hosted session state changes", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const batch = {
    id: "batch_1",
    title: "fix scroll",
    worker_name: "cli-openrouter",
    worker_port: 11199,
    source_directory: directory,
    created_at: "2026-07-09T00:00:00Z",
    variants: [
      {
        id: "variant_1",
        index: 1,
        hosted_session_id: "session_1",
        session_label: "fix scroll batch_1 #1",
        worktree_dir: `${directory}/.worktrees/fix-scroll-1`,
      },
    ],
  }
  const app = await mountProxyApp({ batches: [batch] })
  app.hostedSessions.push({
    session_id: "session_1",
    session_label: "fix scroll batch_1 #1",
    worker_name: "cli-openrouter",
    worker_port: 11199,
    status: "active",
    turn_state: "idle",
    created_at: "2026-07-09T00:00:00Z",
    last_opened_at: "2026-07-09T00:00:00Z",
  })

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create a worktree race") && app.frame().includes("fix scroll")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("ready")
    })

    app.hostedSessions[0].turn_state = "running"

    await wait(async () => {
      await app.render()
      return app.frame().includes("running")
    })
  } finally {
    await app.cleanup()
  }
})

test("batch detail deletes all variants after confirmation", async () => {
  installLaunchMock()
  const { mountProxyApp, runCommand, wait } = await loadProxyFixture()
  const batch = {
    id: "batch_1",
    title: "fix scroll",
    worker_name: "cli-openrouter",
    worker_port: 11199,
    source_directory: directory,
    created_at: "2026-07-09T00:00:00Z",
    variants: [
      {
        id: "variant_1",
        index: 1,
        hosted_session_id: "session_1",
        session_label: "fix scroll batch_1 #1",
        worktree_dir: `${directory}/.worktrees/fix-scroll-1`,
      },
    ],
  }
  const app = await mountProxyApp({ batches: [batch] })

  try {
    app.api.keymap.dispatchCommand("proxy.batch")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Create a worktree race") && app.frame().includes("fix scroll")
    })

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Batch: fix scroll")
    })

    app.api.keymap.dispatchCommand("batch.delete")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Delete batch")
    })

    app.mockInput.pressEnter()
    await wait(() => app.calls.deleteBatch.length === 1)
    await wait(async () => {
      await app.render()
      return app.frame().includes("Batch Runs") && !app.frame().includes("fix scroll")
    })

    expect(app.calls.deleteBatch).toEqual(["batch_1"])
  } finally {
    await app.cleanup()
  }
})

type BatchClient = ReturnType<typeof useSDK>["client"] & {
  createBatch(input: {
    title: string
    worker_name: string
    count?: number
    source_directory: string
    model?: string
  }): Promise<unknown>
}

async function withBatchClient(fn: (client: BatchClient) => Promise<void>) {
  let done: Promise<void> | undefined
  const calls = {
    createBatch: [] as unknown[],
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
      worker_name: "coder",
      count: 3,
      source_directory: "/repo",
      model: "gpt-5",
    })
  })

  expect(calls.createBatch).toEqual([
    {
      title: "Race parser fix",
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

test("batch client does not expose winner selection", async () => {
  let client!: BatchClient

  await withBatchClient(async (nextClient) => {
    client = nextClient
  })

  expect(client).not.toHaveProperty("selectBatchWinner")
})
