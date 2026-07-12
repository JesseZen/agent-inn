/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import { expect, test } from "bun:test"
import { onMount } from "solid-js"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { translate, LanguageProvider, useLanguage } from "../../src/context/language"
import { KVProvider } from "../../src/context/kv"
import { TestTuiContexts } from "../fixture/tui-environment"
import { tmpdir } from "../fixture/fixture"

const root = path.resolve(import.meta.dir, "../../src")

const routeKeys = {
  "routes/session/index.tsx": [
    "session.share",
    "session.confirmRedo",
    "session.thinking",
    "tool.readingFile",
    "tool.grep",
    "tool.webFetch",
    "tool.webSearch",
    "tool.webSearchResults",
    "tool.editHeader",
    "tool.patchDeleted",
    "tool.patchCreated",
    "tool.patchMoved",
    "tool.patchPatched",
    "tool.todosHeading",
    "tool.questionsHeading",
    "tool.diagnosticsError",
  ],
  "routes/session/permission.tsx": ["permission.required", "permission.path", "permission.allowOnce", "category.permission"],
  "routes/session/question.tsx": ["question.confirm", "dialog.question.custom", "question.dismiss", "question.selectAnswerNumber"],
  "routes/session/dialog-timeline.tsx": ["session.timeline"],
  "routes/session/dialog-message.tsx": ["session.messageActions", "session.copyDescription"],
  "routes/session/dialog-fork-from-timeline.tsx": ["session.full", "session.fork"],
  "routes/session/dialog-subagent.tsx": ["session.subagentActions", "session.subagentDescription"],
  "routes/session/footer.tsx": ["session.getStarted", "session.permissionCount"],
  "routes/session/subagent-footer.tsx": ["session.subagent", "session.parent", "session.next", "session.subagentPosition"],
  "component/prompt/index.tsx": [
    "prompt.clear",
    "prompt.placeholderExample",
    "prompt.commands",
    "prompt.createSessionFailed",
    "prompt.pasteSvg",
    "prompt.pasteSummary",
    "prompt.pastePdf",
    "prompt.pasteImage",
    "prompt.quotaHot",
    "prompt.clickExpandHint",
    "prompt.retryStatusWithDelay",
    "prompt.retryStatus",
    "prompt.interruptHint",
    "prompt.interruptAgain",
  ],
  "component/prompt/autocomplete.tsx": ["dialog.noMatchingItems", "prompt.autocomplete.select", "category.autocomplete"],
  "component/prompt/move.tsx": ["workspace.createFailed", "workspace.noProjectCopyDirectory"],
  "component/prompt/workspace.tsx": ["workspace.createFailed", "workspace.noResponse", "workspace.localProject"],
  "ui/dialog-alert.tsx": ["dialog.alertConfirm", "dialog.ok"],
  "ui/dialog-confirm.tsx": ["dialog.confirmSelection", "dialog.confirmLabel", "dialog.cancelLabel"],
  "ui/dialog-help.tsx": ["dialog.helpConfirm"],
  "ui/dialog-prompt.tsx": ["dialog.submitPrompt", "dialog.enterText", "dialog.noMatchingDirectories", "dialog.working"],
  "ui/dialog-select.tsx": ["dialog.search", "dialog.noResults", "dialog.selectItem"],
  "ui/dialog.tsx": ["dialog.backHint", "dialog.closeHint"],
  "ui/dialog-export-options.tsx": ["export.filename", "export.includeThinking", "export.openWithoutSaving"],
} as const

test("core session surfaces consume their typed translation keys", async () => {
  const missing: string[] = []
  for (const [file, keys] of Object.entries(routeKeys)) {
    const source = await Bun.file(path.join(root, file)).text()
    for (const key of keys) {
      if (!source.includes(`t(\"${key}\"`)) missing.push(`${file}: ${key}`)
    }
  }
  expect(missing).toEqual([])
})

test("core session source has no remaining owned display literals", async () => {
  const forbidden: Record<string, string[]> = {
    "routes/session/index.tsx": [
      'Grep "',
      "WebFetch {stringValue",
      "results)",
      "← Edit ",
      "# Deleted ",
      "# Created ",
      "# Moved ",
      "← Patched ",
      'line${file.deletions !== 1 ? "s" : ""}',
      'title="# Todos"',
      'title="# Questions"',
      "Error [",
      '"Shell"',
    ],
    "routes/session/permission.tsx": ['category: "Permission"', 'group: "Permission"'],
    "routes/session/question.tsx": ["Select answer ${index + 1}"],
    "component/prompt/index.tsx": [
      "Creating a session failed. Open console for more details.",
      "[SVG: ${filename",
      "[Pasted ~${lineCount}",
      "[PDF ${count + 1}]",
      "[Image ${count + 1}]",
      "gemini is way too hot right now",
      " (click to expand)",
      "again to interrupt",
      ': "interrupt"',
    ],
    "component/prompt/autocomplete.tsx": ['category: "Autocomplete"'],
    "component/prompt/move.tsx": ['"No project copy directory returned"'],
    "component/prompt/workspace.tsx": ['"no response"', 'name: "local project"'],
    "routes/session/subagent-footer.tsx": [" of "],
    "ui/dialog-prompt.tsx": ['"Working..."'],
    "ui/dialog.tsx": ['? "back" : "close"'],
  }
  const remaining: string[] = []
  for (const [file, literals] of Object.entries(forbidden)) {
    const source = await Bun.file(path.join(root, file)).text()
    for (const literal of literals) {
      if (source.includes(literal)) remaining.push(`${file}: ${literal}`)
    }
  }
  expect(remaining).toEqual([])
})

test("core session route labels resolve in English and Chinese", () => {
  expect({
    en: {
      session: translate("en", "session.timeline"),
      prompt: translate("en", "prompt.placeholderExample", { example: '"status"' }),
      permission: translate("en", "permission.required"),
      dialog: translate("en", "dialog.noResults"),
    },
    zhCN: {
      session: translate("zh-CN", "session.timeline"),
      prompt: translate("zh-CN", "prompt.placeholderExample", { example: '"status"' }),
      permission: translate("zh-CN", "permission.required"),
      dialog: translate("zh-CN", "dialog.noResults"),
    },
  }).toEqual({
    en: {
      session: "Timeline",
      prompt: 'Ask anything... "status"',
      permission: "Permission required",
      dialog: "No results found",
    },
    zhCN: {
      session: "时间线",
      prompt: '请输入内容... "status"',
      permission: "需要权限",
      dialog: "未找到结果",
    },
  })
})

test("Chinese route labels keep terminal-cell width at a narrow boundary", () => {
  const labels = [
    translate("zh-CN", "permission.allowOnce"),
    translate("zh-CN", "permission.allowAlways"),
    translate("zh-CN", "dialog.permission.deny"),
  ]
  expect(labels.map((label) => ({ label, width: Bun.stringWidth(label) }))).toEqual([
    { label: "允许一次", width: 8 },
    { label: "始终允许", width: 8 },
    { label: "拒绝", width: 4 },
  ])
})

test("mounted core labels react to a runtime locale switch", async () => {
  await using tmp = await tmpdir()
  const state = path.join(tmp.path, "state")
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), JSON.stringify({ locale: "en" }))

  let snapshot: { before: string; after: string } | undefined
  let ready = false
  function Probe() {
    const language = useLanguage()
    onMount(() => {
      snapshot = {
        before: language.t("session.timeline"),
        after: language.t("session.timeline"),
      }
      language.setLocale("zh-CN")
      snapshot = { before: snapshot.before, after: language.t("session.timeline") }
      ready = true
    })
    return <box />
  }

  const app = await testRender(() => (
    <TestTuiContexts directory={tmp.path} paths={{ home: tmp.path, state, worktree: tmp.path }}>
      <KVProvider persist={false}>
        <LanguageProvider>
          <Probe />
        </LanguageProvider>
      </KVProvider>
    </TestTuiContexts>
  ))

  try {
    while (!ready) await Bun.sleep(10)
    expect(snapshot).toEqual({ before: "Timeline", after: "时间线" })
  } finally {
    app.renderer.destroy()
  }
})
