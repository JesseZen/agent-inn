import { TextAttributes } from "@opentui/core"
import { useTheme } from "../context/theme"
import { useDialog } from "../ui/dialog"
import { createStore } from "solid-js/store"
import { For } from "solid-js"
import { useBindings } from "../keymap"
import { useLanguage } from "../context/language"

export function DialogSessionDeleteFailed(props: {
  session: string
  workspace: string
  onDelete?: () => boolean | void | Promise<boolean | void>
  onRestore?: () => boolean | void | Promise<boolean | void>
  onDone?: () => void
}) {
  const dialog = useDialog()
  const { theme } = useTheme()
  const language = useLanguage()
  const [store, setStore] = createStore({
    active: "delete" as "delete" | "restore",
  })

  const options = [
    {
      id: "delete" as const,
      title: language.t("dialog.sessionDelete.deleteWorkspace"),
      description: language.t("dialog.sessionDelete.deleteDescription"),
      run: props.onDelete,
    },
    {
      id: "restore" as const,
      title: language.t("dialog.sessionDelete.restoreWorkspace"),
      description: language.t("dialog.sessionDelete.restoreDescription"),
      run: props.onRestore,
    },
  ]

  async function confirm() {
    const result = await options.find((item) => item.id === store.active)?.run?.()
    if (result === false) return
    props.onDone?.()
    if (!props.onDone) dialog.clear()
  }

  useBindings(() => ({
    bindings: [
      { key: "return", desc: language.t("dialog.sessionDelete.confirm"), group: "Dialog", cmd: () => void confirm() },
      { key: "left", desc: language.t("dialog.sessionDelete.deleteBroken"), group: "Dialog", cmd: () => setStore("active", "delete") },
      { key: "up", desc: language.t("dialog.sessionDelete.deleteBroken"), group: "Dialog", cmd: () => setStore("active", "delete") },
      { key: "right", desc: language.t("dialog.sessionDelete.restoreBroken"), group: "Dialog", cmd: () => setStore("active", "restore") },
      { key: "down", desc: language.t("dialog.sessionDelete.restoreBroken"), group: "Dialog", cmd: () => setStore("active", "restore") },
    ],
  }))

  return (
    <box paddingLeft={2} paddingRight={2} gap={1}>
      <box flexDirection="row" justifyContent="space-between">
        <text attributes={TextAttributes.BOLD} fg={theme.text}>
          {language.t("dialog.sessionDelete.title")}
        </text>
        <box onMouseUp={() => dialog.clear()}>
          <text fg={theme.textMuted}>esc</text>
        </box>
      </box>
      <text fg={theme.textMuted} wrapMode="word">
        {language.t("dialog.sessionDelete.unavailable", { session: props.session, workspace: props.workspace })}
      </text>
      <text fg={theme.textMuted} wrapMode="word">
        {language.t("dialog.sessionDelete.chooseRecovery")}
      </text>
      <box flexDirection="column" paddingBottom={1} gap={1}>
        <For each={options}>
          {(item) => (
            <box
              flexDirection="column"
              paddingLeft={1}
              paddingRight={1}
              paddingTop={1}
              paddingBottom={1}
              backgroundColor={item.id === store.active ? theme.primary : undefined}
              onMouseUp={() => {
                setStore("active", item.id)
                void confirm()
              }}
            >
              <text
                attributes={TextAttributes.BOLD}
                fg={item.id === store.active ? theme.selectedListItemText : theme.text}
              >
                {item.title}
              </text>
              <text fg={item.id === store.active ? theme.selectedListItemText : theme.textMuted} wrapMode="word">
                {item.description}
              </text>
            </box>
          )}
        </For>
      </box>
    </box>
  )
}
