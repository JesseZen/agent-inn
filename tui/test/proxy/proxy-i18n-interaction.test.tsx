import { expect, test } from "bun:test"
import { TextareaRenderable } from "@opentui/core"
import { mountProxyApp, runCommand, wait } from "./../proxy-commands.fixture"

test("Proxy dashboard renders translated labels for a Chinese locale", async () => {
  const previousLocale = { LC_ALL: process.env.LC_ALL, LC_MESSAGES: process.env.LC_MESSAGES, LANG: process.env.LANG }
  process.env.LC_ALL = "zh_CN.UTF-8"
  process.env.LC_MESSAGES = "zh_CN.UTF-8"
  process.env.LANG = "zh_CN.UTF-8"
  const app = await mountProxyApp()
  try {
    await runCommand(app, "proxy.dashboard")
    await wait(() => app.frame().includes("仪表板"))

    expect(app.frame()).toContain("仪表板")
    expect(app.frame()).toContain("工作进程")
    expect(app.frame()).not.toContain("Dashboard")
  } finally {
    await app.cleanup()
    process.env.LC_ALL = previousLocale.LC_ALL
    process.env.LC_MESSAGES = previousLocale.LC_MESSAGES
    process.env.LANG = previousLocale.LANG
  }
})

test("Chinese Proxy errors preserve the upstream error body", async () => {
  const previousLocale = { LC_ALL: process.env.LC_ALL, LC_MESSAGES: process.env.LC_MESSAGES, LANG: process.env.LANG }
  process.env.LC_ALL = "zh_CN.UTF-8"
  process.env.LC_MESSAGES = "zh_CN.UTF-8"
  process.env.LANG = "zh_CN.UTF-8"
  const app = await mountProxyApp({ patchUpstreamError: "rename rejected" })
  try {
    await runCommand(app, "proxy.upstreams")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.frame().includes("编辑上游：openai"))
    expect(app.frame()).toContain("编辑上游")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused upstream name prompt")
    editor.selectAll()
    await app.mockInput.typeText("OpenAI Main")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstream.length === 1)
    await wait(async () => {
      await app.render()
      return app.frame().includes("rename rejected")
    })

    expect(app.calls.patchUpstream).toEqual([{ id: "openai", body: { name: "OpenAI Main" } }])
    expect(app.frame()).toContain("rename rejected")
  } finally {
    await app.cleanup()
    process.env.LC_ALL = previousLocale.LC_ALL
    process.env.LC_MESSAGES = previousLocale.LC_MESSAGES
    process.env.LANG = previousLocale.LANG
  }
})
