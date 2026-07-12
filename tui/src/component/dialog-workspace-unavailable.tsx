import { TextAttributes } from "@opentui/core"
import { createStore } from "solid-js/store"
import { For } from "solid-js"
import { useTheme } from "../context/theme"
import { useDialog } from "../ui/dialog"
import { useBindings } from "../keymap"
import { useLanguage } from "../context/language"

export function DialogWorkspaceUnavailable(props: { onRestore?: () => boolean | void | Promise<boolean | void> }) {
  const dialog = useDialog()
  const { theme } = useTheme()
  const language = useLanguage()
  const [store, setStore] = createStore({
    active: "restore" as "cancel" | "restore",
  })

  const options = ["cancel", "restore"] as const

  async function confirm() {
    if (store.active === "cancel") {
      dialog.clear()
      return
    }
    const result = await props.onRestore?.()
    if (result === false) return
  }

  useBindings(() => ({
    bindings: [
      { key: "return", desc: language.t("dialog.workspaceUnavailable.confirm"), group: "Dialog", cmd: () => void confirm() },
      { key: "left", desc: language.t("dialog.workspaceUnavailable.cancel"), group: "Dialog", cmd: () => setStore("active", "cancel") },
      { key: "right", desc: language.t("dialog.workspaceUnavailable.restore"), group: "Dialog", cmd: () => setStore("active", "restore") },
    ],
  }))

  return (
    <box paddingLeft={2} paddingRight={2} gap={1}>
      <box flexDirection="row" justifyContent="space-between">
        <text attributes={TextAttributes.BOLD} fg={theme.text}>
          {language.t("dialog.workspaceUnavailable.title")}
        </text>
        <box onMouseUp={() => dialog.clear()}>
          <text fg={theme.textMuted}>esc</text>
        </box>
      </box>
      <text fg={theme.textMuted} wrapMode="word">
        {language.t("dialog.workspaceUnavailable.sessionAttached")}
      </text>
      <text fg={theme.textMuted} wrapMode="word">
        {language.t("dialog.workspaceUnavailable.restoreQuestion")}
      </text>
      <box flexDirection="row" justifyContent="flex-end" paddingBottom={1} gap={1}>
        <For each={options}>
          {(item) => (
            <box
              paddingLeft={2}
              paddingRight={2}
              backgroundColor={item === store.active ? theme.primary : undefined}
              onMouseUp={() => {
                setStore("active", item)
                void confirm()
              }}
            >
              <text fg={item === store.active ? theme.selectedListItemText : theme.textMuted}>
                {item === "cancel" ? language.t("dialog.workspaceUnavailable.cancelLabel") : language.t("dialog.workspaceUnavailable.restoreLabel")}
              </text>
            </box>
          )}
        </For>
      </box>
    </box>
  )
}
