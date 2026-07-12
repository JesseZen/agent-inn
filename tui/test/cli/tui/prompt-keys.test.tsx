/** @jsxImportSource @opentui/solid */
import { TextareaRenderable } from "@opentui/core"
import { createDefaultOpenTuiKeymap } from "@opentui/keymap/opentui"
import { testRender, useRenderer } from "@opentui/solid"
import { expect, test } from "bun:test"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { onCleanup } from "solid-js"
import { ArgsProvider } from "../../../src/context/args"
import { ClipboardProvider } from "../../../src/context/clipboard"
import { DataProvider } from "../../../src/context/data"
import { EditorContextProvider } from "../../../src/context/editor"
import { ExitProvider } from "../../../src/context/exit"
import { KVProvider } from "../../../src/context/kv"
import { LanguageProvider } from "../../../src/context/language"
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
import { Prompt, type PromptRef } from "../../../src/component/prompt"
import { AinnKeymapProvider, registerAinnKeymap } from "../../../src/keymap"
import { createTuiResolvedConfig } from "../../fixture/tui-runtime"
import { tmpdir } from "../../fixture/fixture"

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

async function mountPrompt(input: { root: string; keybinds?: Record<string, unknown> }) {
  const state = path.join(input.root, "state")
  await mkdir(state, { recursive: true })
  const pluginRuntime = createPluginRuntime()
  let promptRef!: PromptRef
  let textarea!: TextareaRenderable
  let submitted = 0

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
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

  const app = await testRender(() => <Harness />, { kittyKeyboard: true })
  await wait(() => app.renderer.currentFocusedEditor instanceof TextareaRenderable)
  textarea = app.renderer.currentFocusedEditor as TextareaRenderable

  return {
    app,
    promptRef: () => promptRef,
    textarea: () => textarea,
    submitted: () => submitted,
    cleanup() {
      app.renderer.destroy()
    },
  }
}

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
