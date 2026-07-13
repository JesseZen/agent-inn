/** @jsxImportSource @opentui/solid */
import { PasteEvent, TextareaRenderable } from "@opentui/core"
import { createDefaultOpenTuiKeymap } from "@opentui/keymap/opentui"
import { testRender, useRenderer } from "@opentui/solid"
import { expect, test } from "bun:test"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { onCleanup, onMount } from "solid-js"
import { ArgsProvider } from "../../../src/context/args"
import { ClipboardProvider } from "../../../src/context/clipboard"
import { DataProvider } from "../../../src/context/data"
import { EditorContextProvider } from "../../../src/context/editor"
import { ExitProvider } from "../../../src/context/exit"
import { KVProvider } from "../../../src/context/kv"
import { LanguageProvider, type Locale as LanguageLocale, useLanguage } from "../../../src/context/language"
import { LocalProvider } from "../../../src/context/local"
import { ProjectProvider } from "../../../src/context/project"
import { RouteProvider } from "../../../src/context/route"
import { SDKProvider } from "../../../src/context/sdk"
import { SyncProvider } from "../../../src/context/sync"
import { TuiPathsProvider, TuiStartupProvider, TuiTerminalEnvironmentProvider } from "../../../src/context/runtime"
import { createPluginRuntime, PluginRuntimeProvider } from "../../../src/plugin/runtime"
import { TuiConfigProvider } from "../../../src/config"
import { ThemeProvider } from "../../../src/context/theme"
import { DialogProvider } from "../../../src/ui/dialog"
import { ToastProvider } from "../../../src/ui/toast"
import { FrecencyProvider } from "../../../src/prompt/frecency"
import { PromptHistoryProvider } from "../../../src/prompt/history"
import { PromptStashProvider } from "../../../src/prompt/stash"
import { Prompt, type PromptProps, type PromptRef } from "../../../src/component/prompt"
import { AinnKeymapProvider, registerAinnKeymap } from "../../../src/keymap"
import { promptOffsetWidth } from "../../../src/prompt/display"
import { createTuiResolvedConfig } from "../../fixture/tui-runtime"
import { tmpdir } from "../../fixture/fixture"
import { Locale } from "../../../src/util/locale"

async function wait(fn: () => boolean, timeout = 2000) {
  const start = Date.now()
  while (!fn()) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

function json(data: unknown) {
  return new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } })
}

function createFetch(root: string): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const pathname = new URL(input instanceof Request ? input.url : String(input)).pathname
    const method = input instanceof Request ? input.method : (init?.method ?? "GET")
    const location = { directory: root }
    if (pathname === "/session" && method === "POST") return json({ id: "session-1" })
    if (pathname === "/session") return json([])
    if (pathname === "/session/session-1/message") return json({})
    if (pathname === "/experimental/session") return json([])
    if (pathname === "/api/session") return json({ data: [] })
    if (pathname === "/api/location") return json({ directory: root })
    if (
      pathname === "/api/agent" ||
      pathname === "/api/integration" ||
      pathname === "/api/model" ||
      pathname === "/api/provider" ||
      pathname === "/api/reference" ||
      pathname === "/api/command" ||
      pathname === "/api/skill"
    ) {
      return json({ location, data: [] })
    }
    if (pathname === "/api/workers") return json({ workers: [] })
    if (pathname === "/api/upstreams") return json({ upstreams: {} })
    if (pathname === "/api/config") return json({ config: {}, status: {} })
    if (pathname === "/api/events") return new Response(undefined, { status: 204 })
    if (pathname.includes("/path")) {
      return json({ home: root, state: path.join(root, "state"), config: root, worktree: root, directory: root })
    }
    if (pathname.includes("/project/current")) return json({ id: "project", worktree: root })
    if (pathname.includes("/project/directories")) return json([{ directory: root }])
    if (pathname === "/config/providers") {
      return json({
        providers: [
          {
            id: "provider",
            name: "Provider",
            source: "config",
            env: [],
            options: {},
            models: {
              model: { id: "model", name: "Model" },
            },
          },
        ],
        default: {
          provider: "model",
        },
      })
    }
    if (pathname === "/provider") return json({ all: [], default: {}, connected: [] })
    if (pathname === "/agent") return json([{ name: "agent" }])
    if (pathname === "/config") return json({})
    if (pathname === "/experimental/capabilities") return json({})
    if (pathname === "/experimental/console") return json({})
    if (pathname === "/session/status") return json({})
    if (pathname === "/provider/auth") return json({})
    if (pathname === "/vcs") return json(undefined)
    if (pathname === "/experimental/workspace") return json([])
    if (pathname === "/experimental/workspace/status") return json([])
    return json([])
  }) as typeof fetch
}

async function mountPrompt(input: {
  root: string
  editor?: PromptProps["editor"]
  keybinds?: Record<string, unknown>
  locale?: LanguageLocale
  width?: number
  height?: number
}) {
  const state = path.join(input.root, "state")
  await mkdir(state, { recursive: true })
  const pluginRuntime = createPluginRuntime()
  let promptRef!: PromptRef
  let textarea!: TextareaRenderable
  let keymap!: ReturnType<typeof createDefaultOpenTuiKeymap>
  let submitted = 0

  function LocaleSetter() {
    const language = useLanguage()
    onMount(() => {
      if (input.locale) language.setLocale(input.locale)
    })
    return <></>
  }

  function Harness() {
    const renderer = useRenderer()
    keymap = createDefaultOpenTuiKeymap(renderer)
    const resolvedConfig = createTuiResolvedConfig({
      keybinds: input.keybinds,
      leader_timeout: 1000,
    })
    const offKeymap = registerAinnKeymap(keymap, renderer, resolvedConfig)
    onCleanup(() => {
      offKeymap()
    })

    return (
      <TuiPathsProvider value={{ cwd: input.root, home: input.root, state, worktree: input.root }}>
        <TuiTerminalEnvironmentProvider value={{ platform: "linux" }}>
          <TuiStartupProvider value={{ skipInitialLoading: true }}>
            <ClipboardProvider value={{}}>
              <AinnKeymapProvider keymap={keymap}>
                <ArgsProvider>
                  <ExitProvider exit={() => {}}>
                    <KVProvider persist={false}>
                      <LanguageProvider>
                        <LocaleSetter />
                        <ToastProvider>
                          <RouteProvider>
                          <TuiConfigProvider config={resolvedConfig}>
                            <PluginRuntimeProvider value={pluginRuntime}>
                              <SDKProvider url="http://test" directory={input.root} fetch={createFetch(input.root)}>
                                <ProjectProvider>
                                  <SyncProvider>
                                    <DataProvider>
                                      <ThemeProvider mode="dark">
                                        <LocalProvider>
                                          <PromptStashProvider>
                                            <DialogProvider>
                                              <FrecencyProvider>
                                                <PromptHistoryProvider>
                                                  <EditorContextProvider integration={{}}>
                                                  <Prompt
                                                    editor={input.editor}
                                                    ref={(value) => {
                                                      if (value) promptRef = value
                                                    }}
                                                    onSubmit={() => {
                                                      submitted += 1
                                                    }}
                                                  />
                                                  </EditorContextProvider>
                                                </PromptHistoryProvider>
                                              </FrecencyProvider>
                                            </DialogProvider>
                                          </PromptStashProvider>
                                        </LocalProvider>
                                      </ThemeProvider>
                                    </DataProvider>
                                  </SyncProvider>
                                </ProjectProvider>
                              </SDKProvider>
                            </PluginRuntimeProvider>
                          </TuiConfigProvider>
                          </RouteProvider>
                        </ToastProvider>
                      </LanguageProvider>
                    </KVProvider>
                  </ExitProvider>
                </ArgsProvider>
              </AinnKeymapProvider>
            </ClipboardProvider>
          </TuiStartupProvider>
        </TuiTerminalEnvironmentProvider>
      </TuiPathsProvider>
    )
  }

  const app = await testRender(() => <Harness />, {
    kittyKeyboard: true,
    width: input.width,
    height: input.height,
  })
  await wait(() => app.renderer.currentFocusedEditor instanceof TextareaRenderable)
  textarea = app.renderer.currentFocusedEditor as TextareaRenderable

  return {
    app,
    promptRef: () => promptRef,
    textarea: () => textarea,
    keymap: () => keymap,
    submitted: () => submitted,
    cleanup() {
      app.renderer.destroy()
    },
  }
}

test("Chinese attachment markers use display-width extmarks for selection and replacement", async () => {
  await using tmp = await tmpdir()
  const image = path.join(tmp.path, "image.png")
  const pdf = path.join(tmp.path, "document.pdf")
  await Bun.write(image, new Uint8Array([1]))
  await Bun.write(pdf, new Uint8Array([1]))
  const prompt = await mountPrompt({ root: tmp.path, locale: "zh-CN" })

  try {
    prompt.promptRef().set({ input: "前缀 ", parts: [] })
    prompt.textarea().cursorOffset = promptOffsetWidth(prompt.textarea().plainText)
    await prompt.textarea().onPaste?.(new PasteEvent(new TextEncoder().encode(image)))
    await prompt.textarea().onPaste?.(new PasteEvent(new TextEncoder().encode(pdf)))
    await wait(() => prompt.promptRef().current.parts.length === 2)

    const parts = prompt.promptRef().current.parts
    const markers = parts.map((part) => {
      if (part.type !== "file" || !part.source?.text) throw new Error("expected file marker")
      return { value: part.source.text.value, start: part.source.text.start, end: part.source.text.end }
    })
    expect(markers).toEqual([
      { value: "[图片 1]", start: promptOffsetWidth("前缀 "), end: promptOffsetWidth("前缀 [图片 1]") },
      {
        value: "[PDF 1]",
        start: promptOffsetWidth("前缀 [图片 1] "),
        end: promptOffsetWidth("前缀 [图片 1] [PDF 1]"),
      },
    ])

    for (const marker of [...markers].reverse()) {
      prompt.textarea().setSelection(marker.start, marker.end)
      expect(prompt.textarea().getSelectedText()).toBe(marker.value)
      prompt.textarea().deleteSelection()
      prompt.textarea().insertText("替换")
    }
    expect(prompt.textarea().plainText).toBe("前缀 替换 替换 ")
  } finally {
    prompt.cleanup()
  }
})

test("Chinese editor remaps file and agent markers with display-width ranges", async () => {
  await using tmp = await tmpdir()
  const prompt = await mountPrompt({
    root: tmp.path,
    locale: "zh-CN",
    editor: {
      editor: "test-editor",
      runEditor: async (file) => {
        await Bun.write(file, "前缀\n第二行 [图片 1] @代理 后缀")
      },
    },
  })

  try {
    prompt.promptRef().set({
      input: "原始 [图片 1] @代理 ",
      parts: [
        {
          type: "file",
          mime: "image/png",
          filename: "image.png",
          url: "data:image/png;base64,AQ==",
          source: {
            type: "file",
            path: "image.png",
            text: {
              start: promptOffsetWidth("原始 "),
              end: promptOffsetWidth("原始 [图片 1]"),
              value: "[图片 1]",
            },
          },
        },
        {
          type: "agent",
          name: "代理",
          source: {
            start: promptOffsetWidth("原始 [图片 1] "),
            end: promptOffsetWidth("原始 [图片 1] @代理"),
            value: "@代理",
          },
        },
      ],
    })
    const command = prompt.keymap().getCommands().find((item) => item.name === "prompt.editor")
    expect(command).toBeDefined()
    await command!.run({} as never)
    expect(prompt.textarea().plainText).toBe("前缀\n第二行 [图片 1] @代理 后缀")

    expect(prompt.promptRef().current.parts.map((part) => part.type === "file" ? part.source?.text : part.source)).toEqual([
      {
        start: promptOffsetWidth("前缀\n第二行 "),
        end: promptOffsetWidth("前缀\n第二行 [图片 1]"),
        value: "[图片 1]",
      },
      {
        start: promptOffsetWidth("前缀\n第二行 [图片 1] "),
        end: promptOffsetWidth("前缀\n第二行 [图片 1] @代理"),
        value: "@代理",
      },
    ])
  } finally {
    prompt.cleanup()
  }
})

test("main prompt submits with return and inserts newline with shift return by default", async () => {
  await using tmp = await tmpdir()
  const prompt = await mountPrompt({ root: tmp.path })

  try {
    prompt.promptRef().set({ input: "hello", parts: [] })

    prompt.app.mockInput.pressEnter({ shift: true })
    expect({ submitted: prompt.submitted(), text: prompt.textarea().plainText }).toEqual({
      submitted: 0,
      text: "hello\n",
    })

    prompt.app.mockInput.pressEnter()
    await wait(() => prompt.submitted() === 1)

    expect({ submitted: prompt.submitted(), text: prompt.textarea().plainText }).toEqual({
      submitted: 1,
      text: "",
    })
  } finally {
    prompt.cleanup()
  }
})

test("main prompt lets alt return submit when input_submit overrides the default", async () => {
  await using tmp = await tmpdir()
  const prompt = await mountPrompt({
    root: tmp.path,
    keybinds: {
      input_submit: "alt+return",
    },
  })

  try {
    prompt.promptRef().set({ input: "hello", parts: [] })

    prompt.app.mockInput.pressEnter({ meta: true })
    await wait(() => prompt.submitted() === 1)

    expect({ submitted: prompt.submitted(), text: prompt.textarea().plainText }).toEqual({
      submitted: 1,
      text: "",
    })
  } finally {
    prompt.cleanup()
  }
})

test("Chinese prompt editing preserves selection, paste labels, truncation, and narrow wrapping", async () => {
  await using tmp = await tmpdir()
  const prompt = await mountPrompt({ root: tmp.path, locale: "zh-CN", width: 28, height: 12 })

  try {
    prompt.promptRef().set({ input: "你好世界", parts: [] })
    const textarea = prompt.textarea()
    textarea.setSelection(0, Bun.stringWidth("你好"))
    expect(textarea.getSelectedText()).toBe("你好")
    textarea.deleteSelection()
    textarea.insertText("中文")
    expect(textarea.plainText).toBe("中文世界")

    textarea.cursorOffset = Bun.stringWidth(textarea.plainText)
    await textarea.onPaste?.(new PasteEvent(new TextEncoder().encode("第一行\n第二行\n第三行")))
    await wait(() => textarea.plainText.includes("[已粘贴约 3 行]"))
    await prompt.app.renderOnce()

    const frame = prompt.app.captureCharFrame()
    expect({
      text: textarea.plainText,
      wrapped: textarea.virtualLineCount > 1,
      frameContainsPaste: frame.includes("已粘贴约 3 行"),
      truncatedWidth: Bun.stringWidth(Locale.truncateMiddle("非常长的中文文件路径.ts", 12)),
    }).toEqual({
      text: "中文世界[已粘贴约 3 行] ",
      wrapped: true,
      frameContainsPaste: true,
      truncatedWidth: 12,
    })
  } finally {
    prompt.cleanup()
  }
})
