import { TextAttributes } from "@opentui/core"
import { useTheme } from "../context/theme"
import { useDialog } from "./dialog"
import { useBindings, useCommandShortcut } from "../keymap"
import { useLanguage } from "../context/language"

export function DialogHelp() {
  const dialog = useDialog()
  const { theme } = useTheme()
  const { t } = useLanguage()
  const commandShortcut = useCommandShortcut("command.palette.show")

  useBindings(() => ({
    bindings: [
      { key: "return", desc: t("dialog.help.close"), group: t("category.dialog"), cmd: () => dialog.pop() },
      { key: "escape", desc: t("dialog.help.close"), group: t("category.dialog"), cmd: () => dialog.pop() },
    ],
  }))

  return (
    <box paddingLeft={2} paddingRight={2} gap={1}>
      <box flexDirection="row" justifyContent="space-between">
        <text attributes={TextAttributes.BOLD} fg={theme.text}>
          {t("app.help")}
        </text>
        <box onMouseUp={() => dialog.pop()}>
          <text fg={theme.textMuted}>esc/enter</text>
        </box>
      </box>
      <box paddingBottom={1}>
        <text fg={theme.textMuted}>
          {t("dialog.help.description", { shortcut: commandShortcut() })}
        </text>
      </box>
      <box flexDirection="row" justifyContent="flex-end" paddingBottom={1}>
        <box paddingLeft={3} paddingRight={3} backgroundColor={theme.primary} onMouseUp={() => dialog.pop()}>
          <text fg={theme.selectedListItemText}>{t("dialog.helpConfirm")}</text>
        </box>
      </box>
    </box>
  )
}
