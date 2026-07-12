import { onCleanup } from "solid-js"
import { defaultLogoStyleID, logoStyleIDs, logoStyles, resolveLogoStyle, type LogoStyleID } from "../logo"
import { useKV } from "../context/kv"
import { useDialog } from "../ui/dialog"
import { DialogSelect } from "../ui/dialog-select"
import { useLanguage } from "../context/language"

export function DialogLogoList() {
  const kv = useKV()
  const dialog = useDialog()
  const language = useLanguage()
  const [selected] = kv.signal<LogoStyleID>("logo_style", defaultLogoStyleID)
  const initial = resolveLogoStyle(selected()).id
  const options = logoStyleIDs.map((id) => ({
    title: logoStyles[id].title,
    value: id,
    description: logoStyles[id].description,
  }))
  let confirmed = false

  onCleanup(() => {
    if (!confirmed) kv.set("logo_style", initial)
  })

  return (
    <DialogSelect
      title={language.t("dialog.logo.title")}
      options={options}
      current={initial}
      skipFilter
      onMove={(opt) => {
        kv.set("logo_style", opt.value)
      }}
      onSelect={(opt) => {
        kv.set("logo_style", opt.value)
        confirmed = true
        dialog.clear()
      }}
    />
  )
}
