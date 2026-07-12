import { TextAttributes } from "@opentui/core"
import { useTheme } from "../context/theme"
import { useDialog, type DialogContext } from "./dialog"
import { useBindings } from "../keymap"
import { useLanguage } from "../context/language"

export type DialogAlertProps = {
  title: string
  message: string
  onConfirm?: () => void
}

export function DialogAlert(props: DialogAlertProps) {
  const dialog = useDialog()
  const { theme } = useTheme()
  const { t } = useLanguage()

  useBindings(() => ({
    bindings: [
      {
        key: "return",
        desc: t("dialog.alertConfirm"),
        group: t("category.dialog"),
        cmd: () => {
          props.onConfirm?.()
          dialog.pop()
        },
      },
    ],
  }))
  return (
    <box paddingLeft={2} paddingRight={2} gap={1}>
      <box flexDirection="row" justifyContent="space-between">
        <text attributes={TextAttributes.BOLD} fg={theme.text}>
          {props.title}
        </text>
        <box onMouseUp={() => dialog.pop()}>
          <text fg={theme.textMuted}>esc</text>
        </box>
      </box>
      <box paddingBottom={1}>
        <text fg={theme.textMuted}>{props.message}</text>
      </box>
      <box flexDirection="row" justifyContent="flex-end" paddingBottom={1}>
        <box
          paddingLeft={3}
          paddingRight={3}
          backgroundColor={theme.primary}
          onMouseUp={() => {
            props.onConfirm?.()
            dialog.pop()
          }}
        >
          <text fg={theme.selectedListItemText}>{t("dialog.ok")}</text>
        </box>
      </box>
    </box>
  )
}

DialogAlert.show = (dialog: DialogContext, title: string, message: string) => {
  return new Promise<void>((resolve) => {
    dialog.push(
      () => <DialogAlert title={title} message={message} onConfirm={() => resolve()} />,
      () => resolve(),
    )
  })
}
