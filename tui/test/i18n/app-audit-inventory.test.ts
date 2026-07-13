import { expect, test } from "bun:test"
import { en } from "../../src/i18n/en"
import { zhCN } from "../../src/i18n/zh-CN"
import path from "node:path"

export const appAuditInventory = {
  "app.aiChat.sessionsTodo": "TODO: future AI chat sessions",
  "app.aiChat.sessionsPlaceholder": "Placeholder for future AI chat sessions.",
  "app.aiChat.newSessionTodo": "TODO: future AI chat new session",
  "app.aiChat.newSessionPlaceholder": "Placeholder for future AI chat new session.",
  "app.aiChat.modelPickerTodo": "TODO: future AI chat model picker",
  "app.aiChat.modelPickerPlaceholder": "Placeholder for future AI chat model picker.",
  "app.aiChat.mcpsTodo": "TODO: future AI chat MCPs",
  "app.aiChat.mcpsPlaceholder": "Placeholder for future AI chat MCPs.",
  "app.aiChat.providerTodo": "TODO: future AI chat placeholder",
  "app.aiChat.providerPlaceholder": "Placeholder for future AI chat implementation.",
  "model.cycle": "Model cycle",
  "model.cycleReverse": "Model cycle reverse",
  "model.favoriteCycle": "Favorite cycle",
  "model.favoriteCycleReverse": "Favorite cycle reverse",
  "model.variant.cycle": "Variant cycle",
  "agent.cycle": "Agent cycle",
  "agent.cycleReverse": "Agent cycle reverse",
  "session.currentDeleted": "The current session was deleted",
  "app.update.availableTitle": "Update Available",
  "app.update.availableMessage": "A new release v{{version}} is available. Would you like to update now?",
  "app.update.updating": "Updating to v{{version}}...",
  "app.update.failedTitle": "Update Failed",
  "app.update.failedMessage": "Update failed",
  "app.update.completeTitle": "Update Complete",
  "app.update.completeMessage": "Successfully updated to Ainn v{{version}}. Please restart the application.",
  "common.exit": "Exit",
  "category.popup": "Popup",
  "common.unknownError": "An unknown error has occurred",
} as const

function placeholders(value: string) {
  return [...value.matchAll(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g)].map((match) => match[1]).sort()
}

test("app audit inventory has exact dictionary and placeholder coverage", () => {
  expect(Object.keys(appAuditInventory)).toHaveLength(28)
  for (const [key, value] of Object.entries(appAuditInventory)) {
    expect((en as Record<string, string>)[key]).toBe(value)
    expect((zhCN as Record<string, string>)[key]).toBeString()
    expect(placeholders((zhCN as Record<string, string>)[key]!)).toEqual(placeholders(value))
  }
})

const appSourceKeys = [
  "app.commandPalette.show",
  "app.aiChat.sessionsTodo",
  "app.aiChat.sessionsPlaceholder",
  "workspace.copyPath",
  "workspace.pathCopied",
  "workspace.manage",
  "session.quickSwitch",
  "model.cycle",
  "model.cycleReverse",
  "model.favoriteCycle",
  "model.favoriteCycleReverse",
  "agent.switch",
  "agent.cycle",
  "agent.cycleReverse",
  "model.variant.cycle",
  "model.variant.switch",
  "provider.switchOrg",
  "app.status.view",
  "app.theme.switch",
  "app.logo.switch",
  "app.help",
  "app.docs.open",
  "app.exit",
  "app.debug.toggle",
  "app.console.toggle",
  "app.heapSnapshot.write",
  "app.terminal.suspend",
  "app.terminalTitle.disable",
  "app.animations.disable",
  "category.system",
  "category.workspace",
  "category.session",
  "category.agent",
  "category.provider",
  "category.aiChat",
  "session.currentDeleted",
  "app.update.availableTitle",
  "proxy.hosted.title",
  "common.exit",
  "category.popup",
] as const

test("app audit callsites consume typed keys without owned English literals", async () => {
  const source = await Bun.file(path.resolve(import.meta.dir, "../../src/app.tsx")).text()
  const sourceKeys = [...source.matchAll(/language\.t\("([^"]+)"/g)].map((match) => match[1]!)
  expect(sourceKeys.length).toBeGreaterThan(60)
  for (const key of new Set(sourceKeys)) expect((en as Record<string, string>)[key]).toBeString()
  for (const key of appSourceKeys) expect(source).toContain(`language.t("${key}"`)

  for (const literal of [
    'title: "Show command palette"',
    'category: "System"',
    'title: "Copy worktree path"',
    'title: "Model cycle"',
    'title: "Switch agent"',
    'message: "The current session was deleted"',
    '`Update Available`',
    'desc: "Exit"',
    'group: "Popup"',
  ]) {
    expect(source).not.toContain(literal)
  }
})
