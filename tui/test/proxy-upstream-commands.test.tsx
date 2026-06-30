import { expect, test } from "bun:test"
import { resolveSlashCommand } from "../src/keymap"
import { mountProxyApp, openUpstreamEditor, openUpstreamManager, openWorkerDetail, runCommand, wait } from "./proxy-commands.fixture"

test("proxy workers switch upstream action updates worker provider and reflects the change", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("Switch Upstream")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Switch Upstream: app")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.patchWorker.length === 1)
    await app.render()

    await openWorkerDetail(app)
    expect(app.frame()).toContain("upstream: anthropic")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream registers an upstream command", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    expect(app.frame()).toContain("Manage Upstreams")
    expect(app.frame()).toContain("Create New Upstream")
    expect(resolveSlashCommand(app.api.keymap, "/upstreams")).toBe("proxy.upstreams")
    expect(resolveSlashCommand(app.api.keymap, "/upstream")).toBeUndefined()
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream selection opens field list and saves provider", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    expect(app.frame()).toContain("Manage Upstreams")

    await openUpstreamEditor(app, "openai")

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Base URL: https://api.openai.com/v1")
    })
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstream.length === 1)

    expect(app.calls.patchUpstream).toEqual([
      {
        name: "openai",
        body: { base_url: "https://api.openai.com/v1" },
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream creates a new upstream", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    expect(app.frame()).toContain("Create New Upstream")

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("New Upstream Name")
    })

    await app.mockInput.typeText("groq")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Upstream: groq")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Base URL: upstream")
    })
    await app.mockInput.typeText("https://api.groq.com/openai/v1")
    await app.render()
    await wait(async () => {
      await app.render()
      return app.frame().includes("https://api.groq.com/openai/v1")
    })
    app.mockInput.pressEnter()
    await wait(() => app.calls.patchUpstream.length === 1)

    expect(app.calls.patchUpstream).toEqual([
      {
        name: "groq",
        body: { base_url: "https://api.groq.com/openai/v1" },
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor shows empty api_format as dash and persists edits", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamEditor(app, "openai")

    await app.render()
    const frame = app.frame()
    expect(frame).toContain("API Format")
    expect(frame).not.toContain("chat_completions")

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()
    await app.mockInput.typeText("responses")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstream.length === 1)

    expect(app.calls.patchUpstream).toEqual([
      { name: "openai", body: { api_format: "responses" } },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor deletes upstream after confirmation", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await runCommand(app, "dialog.select.end")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Delete upstream")
    })

    app.mockInput.pressEnter()
    await wait(() => app.calls.deleteUpstream.length === 1)
    await app.render()

    expect(app.calls.deleteUpstream).toEqual(["openai"])
    await openUpstreamManager(app)
    expect(app.frame()).not.toContain("openai")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor ESC returns to upstream list when stack depth > 1", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    expect(app.frame()).toContain("Manage Upstreams")

    await openUpstreamEditor(app, "openai")
    expect(app.frame()).toContain("esc back")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Manage Upstreams") && !app.frame().includes("Edit Upstream: openai")
    })
    expect(app.frame()).toContain("Manage Upstreams")
    expect(app.frame()).not.toContain("Edit Upstream: openai")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor ESC closes dialog when stack depth is 1", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    expect(app.frame()).toContain("Manage Upstreams")
    expect(app.frame()).not.toContain("esc back")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return !app.frame().includes("Manage Upstreams")
    })
    expect(app.frame()).not.toContain("Manage Upstreams")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor tests upstream reachability and shows toast", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamEditor(app, "openai")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.testUpstream.length === 1)
    await app.render()

    expect(app.calls.testUpstream).toEqual(["openai"])
    expect(app.frame()).toContain("openai: OK 120ms")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream manager tests all upstreams and shows toast", async () => {
  const app = await mountProxyApp()

  try {
    await openUpstreamManager(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.calls.testAllUpstreams === 1)
    await app.render()

    expect(app.calls.testAllUpstreams).toBe(1)
    expect(app.frame()).toContain("Tested 2 upstreams")
  } finally {
    await app.cleanup()
  }
})
