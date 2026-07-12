import { expect, test } from "bun:test"
import { en } from "../../src/i18n/en"
import { zhCN } from "../../src/i18n/zh-CN"

// Canonical source inventory for every static dialog label in
// tui/src/component/dialog-*.tsx. Dynamic provider/model/org/session/path,
// skill, upstream, and error values are intentionally excluded.
export const dialogInventory = {
  "dialog.agent.native": "native",
  "dialog.console.loadingOrgs": "Loading orgs...",
  "dialog.console.noOrgs": "No orgs found",
  "dialog.console.switchedOrg": "Switched to {{orgName}}",
  "dialog.console.switchOrg": "Switch org",
  "dialog.mcp.loading": "Loading",
  "dialog.mcp.enabled": "Enabled",
  "dialog.mcp.disabled": "Disabled",
  "dialog.mcp.toggle": "toggle",
  "dialog.model.recent": "Recent",
  "dialog.model.popularProviders": "Popular providers",
  "dialog.model.select": "Select model",
  "dialog.model.connectProvider": "Connect provider",
  "dialog.model.viewProviders": "View all providers",
  "dialog.model.favorite": "Favorite",
  "dialog.move.loadingDirectories": "Loading project directories...",
  "dialog.move.noDirectories": "No project directories found",
  "dialog.move.current": "Current",
  "dialog.move.other": "Other",
  "dialog.move.deleteCopyTitle": "Delete working copy?",
  "dialog.move.deleteCopyMessage": "This working copy has file changes. Do you want to delete it anyway?",
  "dialog.move.deleteCopyFailed": "Failed to delete project copy",
  "dialog.move.new": "new",
  "dialog.move.delete": "delete",
  "dialog.move.refresh": "refresh",
  "dialog.provider.popular": "Popular",
  "dialog.provider.providers": "Providers",
  "dialog.provider.other": "Other",
  "dialog.provider.custom": "Custom provider",
  "dialog.provider.idPlaceholder": "Provider id",
  "dialog.provider.invalidId":
    "Provider ids must start with a lowercase letter or number and only use lowercase letters, numbers, hyphens, and underscores",
  "dialog.provider.apiKey": "API key",
  "dialog.provider.selectAuth": "Select auth method",
  "dialog.provider.connect": "Connect a provider",
  "dialog.provider.copyCode": "Copy provider code",
  "dialog.provider.copied": "Copied to clipboard",
  "dialog.provider.oauthFailed": "OAuth authorization failed. Try /connect again.",
  "dialog.provider.waiting": "Waiting for authorization...",
  "dialog.provider.copy": "copy",
  "dialog.provider.authCode": "Authorization code",
  "dialog.provider.invalidCode": "Invalid code",
  "dialog.provider.zenHelp": "Go to https://opencode.ai/zen to get a key",
  "dialog.provider.goHelp": "Go to https://opencode.ai/go and enable OpenCode Go",
  "dialog.provider.savedCredential": "Saved credential for {{providerID}}. Configure it in ainn.json to use it.",
  "dialog.sessionDelete.deleteWorkspace": "Delete workspace",
  "dialog.sessionDelete.deleteDescription": "Delete the workspace and all sessions attached to it.",
  "dialog.sessionDelete.restoreWorkspace": "Restore to new workspace",
  "dialog.sessionDelete.restoreDescription": "Try to restore this session into a new workspace.",
  "dialog.sessionDelete.confirm": "Confirm recovery option",
  "dialog.sessionDelete.deleteBroken": "Delete broken session",
  "dialog.sessionDelete.restoreBroken": "Restore broken session",
  "dialog.sessionList.createFailed": "Failed to create workspace",
  "dialog.sessionList.deleteWorkspaceFailed": "Failed to delete workspace",
  "dialog.sessionList.switch": "switch",
  "dialog.sessionList.today": "Today",
  "dialog.sessionList.pinned": "Pinned",
  "dialog.sessionList.title": "Sessions",
  "dialog.sessionList.pin": "pin/unpin",
  "dialog.sessionList.delete": "delete",
  "dialog.sessionList.rename": "rename",
  "dialog.sessionList.deleteFailed": "Failed to delete session",
  "dialog.skill.category": "Skills",
  "dialog.skill.placeholder": "Search skills...",
  "dialog.stash.title": "Stash",
  "dialog.stash.delete": "delete",
  "dialog.status.noMcp": "No MCP Servers",
  "dialog.status.connected": "Connected",
  "dialog.status.disabled": "Disabled in configuration",
  "dialog.status.noFormatters": "No Formatters",
  "dialog.status.noPlugins": "No Plugins",
  "dialog.variant.default": "Default",
  "dialog.variant.select": "Select variant",
  "dialog.workspaceCreate.loadFailed": "Failed to load workspace adapters",
  "dialog.workspaceCreate.warpFailed": "Failed to warp session",
  "dialog.workspaceCreate.new": "New workspace",
  "dialog.workspaceCreate.none": "None",
  "dialog.workspaceCreate.local": "Use the local project",
  "dialog.workspaceCreate.choose": "Choose workspace",
  "dialog.workspaceCreate.all": "View all workspaces",
  "dialog.workspaceCreate.allDescription": "Choose from all workspaces",
  "dialog.workspaceCreate.warp": "Warp",
  "dialog.workspaceCreate.existing": "Existing Workspace",
  "dialog.workspaceList.deleting": "Deleting...",
  "dialog.workspaceList.deleteFailed": "Failed to delete workspace",
  "dialog.workspaceList.delete": "delete",
  "dialog.workspaceUnavailable.confirm": "Confirm workspace option",
  "dialog.workspaceUnavailable.cancel": "Cancel workspace restore",
  "dialog.workspaceUnavailable.restore": "Restore workspace",
  "dialog.workspaceChanges.title": "File Changes Found",
  "dialog.workspaceChanges.message": "Do you want to move these changes with the session?",
} as const

function placeholders(value: string) {
  return [...value.matchAll(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g)].map((match) => match[1]).sort()
}

test("dialog source inventory has exact dictionary coverage", () => {
  expect(Object.keys(dialogInventory)).toHaveLength(90)

  for (const [key, value] of Object.entries(dialogInventory)) {
    expect((en as Record<string, string>)[key]).toBe(value)
    expect((zhCN as Record<string, string>)[key]).toBeString()
    expect(placeholders((zhCN as Record<string, string>)[key]!)).toEqual(placeholders(value))
  }
})
