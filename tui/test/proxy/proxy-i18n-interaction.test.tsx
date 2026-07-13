import { expect, test } from "bun:test"
import { TextareaRenderable } from "@opentui/core"
import { mountProxyApp, openWorkerDetail, runCommand, wait, type ProxyApp } from "./../proxy-commands.fixture"
import { useLanguage, type Locale } from "../../src/context/language"
import { DialogUpstreamEditor } from "../../src/proxy/dialog-upstream"

function setLocaleEnvironment(locale: string) {
  const previous = { LC_ALL: process.env.LC_ALL, LC_MESSAGES: process.env.LC_MESSAGES, LANG: process.env.LANG }
  process.env.LC_ALL = locale
  process.env.LC_MESSAGES = locale
  process.env.LANG = locale
  return () => {
    process.env.LC_ALL = previous.LC_ALL
    process.env.LC_MESSAGES = previous.LC_MESSAGES
    process.env.LANG = previous.LANG
  }
}

function proxyCommandLabel(app: ProxyApp, name: string) {
  const command = app.api.keymap.getCommands().find((item) => item.name === name)
  return { title: command?.title, category: command?.category }
}

function lineValueForeground(app: ProxyApp, label: string, value: string) {
  const line = app.setup.captureSpans().lines.find((item) =>
    item.spans
      .map((span) => span.text)
      .join("")
      .includes(label),
  )
  const span = line?.spans.find((item) => item.text.trim() === value)
  if (!span) throw new Error(`missing rendered value ${value} beside ${label}`)
  return span.fg
}

async function waitForFrame(app: ProxyApp, value: string) {
  try {
    await wait(() => app.frame().includes(value))
  } catch {
    throw new Error(`timed out waiting for ${value}\n${app.frame()}`)
  }
}

test("Proxy dashboard renders translated labels for a Chinese locale", async () => {
  const restoreLocale = setLocaleEnvironment("zh_CN.UTF-8")
  const app = await mountProxyApp()
  try {
    await runCommand(app, "proxy.dashboard")
    await wait(() => app.frame().includes("仪表板"))

    expect(app.frame()).toContain("仪表板")
    expect(app.frame()).toContain("工作进程")
    expect(app.frame()).not.toContain("Dashboard")
  } finally {
    await app.cleanup()
    restoreLocale()
  }
})

test("Proxy command labels react to a runtime locale switch", async () => {
  const restoreLocale = setLocaleEnvironment("en_US.UTF-8")
  const app = await mountProxyApp()
  try {
    expect(proxyCommandLabel(app, "proxy.workers")).toEqual({ title: "Manage workers", category: "Proxy" })

    await runCommand(app, "language.switch")
    await wait(() => proxyCommandLabel(app, "proxy.workers").title === "管理工作进程")

    expect(proxyCommandLabel(app, "proxy.workers")).toEqual({ title: "管理工作进程", category: "代理" })
    await Bun.sleep(500)
  } finally {
    await app.cleanup()
    restoreLocale()
  }
})

test("mounted upstream actions react to locale and draft name changes", async () => {
  const restoreLocale = setLocaleEnvironment("en_US.UTF-8")
  const localeSetters: Array<(locale: Locale) => void> = []

  function UpstreamEditorProbe() {
    const language = useLanguage()
    localeSetters[0] = language.setLocale
    return (
      <DialogUpstreamEditor
        id="openai"
        draft={{
          name: "openai",
          base_url: "https://api.openai.com/v1",
          api_key: "",
          api_format: "chat_completions",
          has_api_key: true,
          protocol_probe_model: "",
        }}
        mode="saved"
      />
    )
  }

  const app = await mountProxyApp({ height: 40 })
  try {
    app.api.ui.dialog.replace(() => <UpstreamEditorProbe />)
    await app.render()
    await waitForFrame(app, "Delete Upstream")

    await runCommand(app, "dialog.select.submit")
    await wait(() => app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable)
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused upstream name prompt")
    editor.selectAll()
    await app.mockInput.typeText("OpenAI Main")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstream.length === 1)
    await wait(() => app.lines().some((line) => line.includes("Delete Upstream") && line.includes("OpenAI Main")))

    const setLocale = localeSetters[0]
    if (!setLocale) throw new Error("expected mounted language context")
    setLocale("zh-CN")
    await app.render()
    await wait(() => app.lines().some((line) => line.includes("删除上游") && line.includes("OpenAI Main")))

    expect({
      testAction: app.frame().includes("测试上游"),
      deleteAction: app.frame().includes("删除上游"),
      probeDescription: app.frame().includes("探测可达性和认证"),
      renamedDescription: app.lines().some((line) => line.includes("删除上游") && line.includes("OpenAI Main")),
      englishAction: app.frame().includes("Delete Upstream"),
    }).toEqual({
      testAction: true,
      deleteAction: true,
      probeDescription: true,
      renamedDescription: true,
      englishAction: false,
    })
    await Bun.sleep(500)
  } finally {
    await app.cleanup()
    restoreLocale()
  }
})

test("Chinese Proxy dialogs cover each mounted action family", async () => {
  const restoreLocale = setLocaleEnvironment("zh_CN.UTF-8")
  const app = await mountProxyApp({
    hostedSessions: [
      {
        session_id: "hs_active",
        session_label: "Active build",
        worker_id: "app",
        worker_name: "app",
        worker_port: 6767,
        turn_state: "idle",
        created_at: "2026-07-11T00:00:00Z",
        last_opened_at: "2026-07-11T00:00:00Z",
        status: "active",
      },
    ],
  })
  try {
    for (const [command, label] of [
      ["proxy.pools", "管理池"],
      ["proxy.status", "工作进程指标"],
      ["proxy.batch", "批处理运行"],
    ] as const) {
      await runCommand(app, command)
      await wait(() => app.frame().includes(label))
    }

    await runCommand(app, "proxy.settings")
    await waitForFrame(app, "状态目录")
    for (let index = 0; index < 7; index++) await runCommand(app, "dialog.select.next")
    await waitForFrame(app, "已禁用")

    await runCommand(app, "proxy.logs")
    await waitForFrame(app, "工作进程日志")
    await runCommand(app, "dialog.select.submit")
    await waitForFrame(app, "日志：app")

    await runCommand(app, "proxy.launch")
    await waitForFrame(app, "托管终端")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await waitForFrame(app, "重命名")

    await openWorkerDetail(app)
    await waitForFrame(app, "管理模块")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await waitForFrame(app, "切换上游：app")

    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await waitForFrame(app, "模块和钩子：app")

    await runCommand(app, "proxy.workers")
    await runCommand(app, "dialog.select.submit")
    await wait(() => app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable)
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused worker name prompt")
    await app.mockInput.typeText("new-worker")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await waitForFrame(app, "codex 启动器")

    expect({
      pools: app.api.keymap.getCommands().some((item) => item.title === "管理池"),
      logs: app.api.keymap.getCommands().some((item) => item.title === "查看工作进程日志"),
      metrics: app.api.keymap.getCommands().some((item) => item.title === "查看工作进程指标"),
      batch: app.api.keymap.getCommands().some((item) => item.title === "运行批处理"),
      launch: app.api.keymap.getCommands().some((item) => item.title === "启动工作进程"),
      launcher: app.frame().includes("codex 启动器"),
    }).toEqual({ pools: true, logs: true, metrics: true, batch: true, launch: true, launcher: true })
  } finally {
    await app.cleanup()
    restoreLocale()
  }
})

test("translated dashboard warnings keep their semantic warning color", async () => {
  const restoreLocale = setLocaleEnvironment("zh_CN.UTF-8")
  const app = await mountProxyApp({
    hostedSessions: [
      {
        session_id: "hs_unbound",
        session_label: "Unbound build",
        worker_id: "missing",
        worker_name: "missing",
        worker_port: 6999,
        turn_state: "idle",
        created_at: "2026-07-11T00:00:00Z",
        last_opened_at: "2026-07-11T00:00:00Z",
        status: "active",
      },
    ],
  })
  try {
    await runCommand(app, "proxy.dashboard")
    await wait(() => app.frame().includes("未绑定 1"))

    expect(lineValueForeground(app, "未绑定", "1")).not.toEqual(lineValueForeground(app, "工作进程", "2"))
  } finally {
    await app.cleanup()
    restoreLocale()
  }
})

test("Chinese Proxy errors preserve the upstream error body", async () => {
  const restoreLocale = setLocaleEnvironment("zh_CN.UTF-8")
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
    restoreLocale()
  }
})
