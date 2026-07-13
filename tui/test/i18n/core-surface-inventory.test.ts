import { expect, test } from "bun:test"
import { en } from "../../src/i18n/en"
import { zhCN } from "../../src/i18n/zh-CN"
import path from "node:path"
import { mountProxyApp } from "../proxy-commands.fixture"

const inventory = {
  "common.enabled": "Enabled",
  "common.disabled": "Disabled",
  "common.ready": "ready",
  "commandPalette.suggested": "Suggested",
  "commandPalette.commands": "Commands",
  "home.placeholder.fixTodo": "Fix a TODO in the codebase",
  "home.placeholder.techStack": "What is the tech stack of this project?",
  "home.placeholder.fixTests": "Fix broken tests",
  "startup.finishing": "Finishing startup...",
  "startup.loadingPlugins": "Loading plugins...",
  "plugin.routeMissing": "Unknown plugin route: {{id}}",
  "plugin.goHome": "Go home",
} as const

test("core gap inventory has exact dictionary and placeholder coverage", () => {
  expect(Object.keys(inventory)).toHaveLength(12)
  const placeholders = (value: string) => [...value.matchAll(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g)].map((match) => match[1]).sort()
  for (const [key, value] of Object.entries(inventory)) {
    expect((en as Record<string, string>)[key]).toBe(value)
    expect((zhCN as Record<string, string>)[key]).toBeString()
    expect(placeholders((zhCN as Record<string, string>)[key]!)).toEqual(placeholders(value))
  }
})

test("core gap callsites use typed translations", async () => {
  const files = {
    palette: "component/command-palette.tsx",
    home: "routes/home.tsx",
    startup: "component/startup-loading.tsx",
    plugin: "component/plugin-route-missing.tsx",
    settings: "proxy/dialog-settings.tsx",
    batch: "proxy/dialog-batch.tsx",
  }
  const sources = Object.fromEntries(
    await Promise.all(Object.entries(files).map(async ([name, relative]) => [name, await Bun.file(path.resolve(import.meta.dir, `../../src/${relative}`)).text()])),
  ) as Record<keyof typeof files, string>

  for (const key of ["commandPalette.suggested", "commandPalette.commands"] as const) expect(sources.palette).toContain(`t("${key}")`)
  for (const key of ["home.placeholder.fixTodo", "home.placeholder.techStack", "home.placeholder.fixTests"] as const) expect(sources.home).toContain(`language.t("${key}")`)
  for (const key of ["startup.finishing", "startup.loadingPlugins"] as const) expect(sources.startup).toContain(`language.t("${key}")`)
  expect(sources.plugin).toContain('language.t("plugin.routeMissing", { id: props.id })')
  expect(sources.plugin).toContain('language.t("plugin.goHome")')
  expect(sources.settings).toContain('t("common.enabled")')
  expect(sources.settings).toContain('t("common.disabled")')
  expect(sources.batch).toContain('t("common.ready")')
})

test("home placeholder examples react to a locale switch", async () => {
  const app = await mountProxyApp({ stateFiles: { "kv.json": JSON.stringify({ locale: "en" }) } })
  const english = Object.values(inventory).slice(5, 8)
  const chinese = ["修复代码库中的一个待办事项", "这个项目使用了哪些技术栈？", "修复失败的测试"]
  try {
    expect(english.some((value) => app.frame().includes(value))).toBeTrue()

    app.api.keymap.dispatchCommand("language.switch")
    const start = Date.now()
    while (!chinese.some((value) => app.frame().includes(value))) {
      if (Date.now() - start > 2000) throw new Error("timed out waiting for translated home placeholder")
      await app.render()
      await Bun.sleep(10)
    }

    expect(chinese.some((value) => app.frame().includes(value))).toBeTrue()
  } finally {
    await app.cleanup()
  }
})
