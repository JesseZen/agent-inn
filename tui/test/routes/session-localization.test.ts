import { expect, test } from "bun:test"
import path from "node:path"
import { translate } from "../../src/context/language"

const root = path.resolve(import.meta.dir, "../../src")

const routeKeys = {
  "routes/session/index.tsx": [
    "session.share",
    "session.confirmRedo",
    "session.thinking",
    "tool.readingFile",
  ],
  "routes/session/permission.tsx": ["permission.required", "permission.path", "permission.allowOnce"],
  "routes/session/question.tsx": ["question.confirm", "dialog.question.custom", "question.dismiss"],
  "routes/session/dialog-timeline.tsx": ["session.timeline"],
  "routes/session/dialog-message.tsx": ["session.messageActions", "session.copyDescription"],
  "routes/session/dialog-fork-from-timeline.tsx": ["session.full", "session.fork"],
  "routes/session/dialog-subagent.tsx": ["session.subagentActions", "session.subagentDescription"],
  "routes/session/footer.tsx": ["session.getStarted", "session.permissionCount"],
  "routes/session/subagent-footer.tsx": ["session.subagent", "session.parent", "session.next"],
  "component/prompt/index.tsx": ["prompt.clear", "prompt.placeholderExample", "prompt.commands"],
  "component/prompt/autocomplete.tsx": ["dialog.noMatchingItems", "prompt.autocomplete.select"],
  "component/prompt/move.tsx": ["workspace.createFailed"],
  "component/prompt/workspace.tsx": ["workspace.createFailed"],
  "ui/dialog-alert.tsx": ["dialog.alertConfirm", "dialog.ok"],
  "ui/dialog-confirm.tsx": ["dialog.confirmSelection", "dialog.confirmLabel", "dialog.cancelLabel"],
  "ui/dialog-help.tsx": ["dialog.helpConfirm"],
  "ui/dialog-prompt.tsx": ["dialog.submitPrompt", "dialog.enterText", "dialog.noMatchingDirectories"],
  "ui/dialog-select.tsx": ["dialog.search", "dialog.noResults", "dialog.selectItem"],
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
