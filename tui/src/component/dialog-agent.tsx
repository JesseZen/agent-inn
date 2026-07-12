import { createMemo } from "solid-js"
import { useLocal } from "../context/local"
import { DialogSelect } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useLanguage } from "../context/language"

export function DialogAgent() {
  const local = useLocal()
  const dialog = useDialog()
  const language = useLanguage()

  const options = createMemo(() =>
    local.agent.list().map((item) => {
      return {
        value: item.name,
        title: item.name,
        description: item.native ? language.t("dialog.agent.native") : item.description,
      }
    }),
  )

  return (
    <DialogSelect
      title={language.t("dialog.agent.select")}
      current={local.agent.current()?.name}
      options={options()}
      onSelect={(option) => {
        local.agent.set(option.value)
        dialog.clear()
      }}
    />
  )
}
