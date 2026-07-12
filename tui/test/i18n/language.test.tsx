/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import { expect, test } from "bun:test"
import { mkdir, readFile } from "node:fs/promises"
import path from "node:path"
import { onMount } from "solid-js"
import { KVProvider } from "../../src/context/kv"
import {
  LanguageProvider,
  detectLocale,
  interpolate,
  selectInitialLocale,
  translate,
  useLanguage,
} from "../../src/context/language"
import { TestTuiContexts } from "../fixture/tui-environment"
import { tmpdir } from "../fixture/fixture"

test("locale detection prefers persisted value, then environment precedence", () => {
  expect(selectInitialLocale("zh-CN", { LC_ALL: "en_US.UTF-8", LC_MESSAGES: "en", LANG: "en" })).toBe("zh-CN")
  expect(selectInitialLocale(undefined, { LC_ALL: "C", LC_MESSAGES: "zh_TW.UTF-8", LANG: "en" })).toBe("zh-CN")
  expect(selectInitialLocale(undefined, { LC_ALL: "C", LC_MESSAGES: "C", LANG: "en_GB.UTF-8" })).toBe("en")
  expect(selectInitialLocale("fr", { LC_ALL: "C", LC_MESSAGES: "C", LANG: "fr_FR.UTF-8" })).toBe("en")
  expect(detectLocale("zh-Hans")).toBe("zh-CN")
  expect(detectLocale("malformed")).toBeUndefined()
})

test("translation interpolates named values and falls back to English", () => {
  expect(interpolate("Hello {{name}}", { name: "Ada" })).toBe("Hello Ada")
  expect(interpolate("Count: {{count}}", { count: 3 })).toBe("Count: 3")
  expect(translate("zh-CN", "common.copied")).toBe("已复制到剪贴板")
  expect(translate("zh-CN", "missing.key")).toBe("missing.key")
})

test("LanguageProvider switches reactively and persists the locale identifier", async () => {
  await using tmp = await tmpdir()
  const state = path.join(tmp.path, "state")
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), JSON.stringify({ locale: "en" }))

  let snapshot: { before: string; after: string } | undefined
  let ready = false

  function Probe() {
    const language = useLanguage()
    onMount(async () => {
      const before = language.t("language.name")
      language.setLocale("zh-CN")
      snapshot = { before, after: language.t("language.name") }
      ready = true
    })
    return <box />
  }

  const app = await testRender(() => (
    <TestTuiContexts directory={tmp.path} paths={{ home: tmp.path, state, worktree: tmp.path }}>
      <KVProvider>
        <LanguageProvider>
          <Probe />
        </LanguageProvider>
      </KVProvider>
    </TestTuiContexts>
  ))

  try {
    while (!ready) await Bun.sleep(10)
    expect(snapshot).toEqual({ before: "Language", after: "语言" })
  } finally {
    app.renderer.destroy()
  }

  while (JSON.parse(await Bun.file(path.join(state, "kv.json")).text()).locale !== "zh-CN") await Bun.sleep(10)
  expect(JSON.parse(await Bun.file(path.join(state, "kv.json")).text())).toEqual({ locale: "zh-CN" })
})
