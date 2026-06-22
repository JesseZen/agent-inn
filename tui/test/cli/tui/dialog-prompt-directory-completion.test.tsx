/** @jsxImportSource @opentui/solid */
import { TextareaRenderable } from "@opentui/core"
import { createDefaultOpenTuiKeymap } from "@opentui/keymap/opentui"
import { testRender, useRenderer } from "@opentui/solid"
import { expect, test } from "bun:test"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { onCleanup } from "solid-js"
import { tmpdir } from "../../fixture/fixture"
import { createTuiResolvedConfig } from "../../fixture/tui-runtime"
import { TestTuiContexts } from "../../fixture/tui-environment"

async function wait(fn: () => boolean, timeout = 2000) {
  const start = Date.now()
  while (!fn()) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

test("tab expands the selected directory and enter still submits raw text", async () => {
  await using tmp = await tmpdir()
  await mkdir(path.join(tmp.path, "apps"), { recursive: true })
  await mkdir(path.join(tmp.path, "assets"), { recursive: true })
  const state = path.join(tmp.path, "state")
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), "{}")

  const confirmed: string[] = []
  const [{ DialogProvider }, { DialogPrompt }, { KVProvider }, { ThemeProvider }, { TuiConfigProvider }, { ToastProvider }, { CodexProxyKeymapProvider, registerCodexProxyKeymap }] = await Promise.all([
    import("../../../src/ui/dialog"),
    import("../../../src/ui/dialog-prompt"),
    import("../../../src/context/kv"),
    import("../../../src/context/theme"),
    import("../../../src/config"),
    import("../../../src/ui/toast"),
    import("../../../src/keymap"),
  ])

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const resolvedConfig = createTuiResolvedConfig()
    const off = registerCodexProxyKeymap(keymap, renderer, resolvedConfig)
    onCleanup(off)

    return (
      <TestTuiContexts directory={tmp.path} paths={{ home: tmp.path, state, worktree: tmp.path }}>
        <CodexProxyKeymapProvider keymap={keymap}>
          <TuiConfigProvider config={resolvedConfig}>
            <KVProvider>
              <ThemeProvider mode="dark">
                <ToastProvider>
                  <DialogProvider>
                    <DialogPrompt
                      title="Launch Codex"
                      value="a"
                      directoryCompletion={{ basePath: tmp.path }}
                      onConfirm={(value) => confirmed.push(value)}
                    />
                  </DialogProvider>
                </ToastProvider>
              </ThemeProvider>
            </KVProvider>
          </TuiConfigProvider>
        </CodexProxyKeymapProvider>
      </TestTuiContexts>
    )
  }

  const app = await testRender(() => <Harness />, { kittyKeyboard: true })
  try {
    await wait(() => app.renderer.currentFocusedEditor instanceof TextareaRenderable)
    await wait(() => {
      const frame = app.captureCharFrame()
      return frame.includes("apps/") && frame.includes("assets/")
    })
    const frame = app.captureCharFrame()
    expect(frame).toContain("apps/")
    expect(frame).toContain("assets/")

    app.mockInput.pressTab()
    await wait(() => app.renderer.currentFocusedEditor.plainText === "apps/")
    expect(app.renderer.currentFocusedEditor.plainText).toBe("apps/")

    app.mockInput.pressEnter()
    expect(confirmed).toEqual(["apps/"])
  } finally {
    app.renderer.destroy()
  }
})
