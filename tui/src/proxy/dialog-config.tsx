import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"

export function DialogConfig() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const status = () => sync.data.config_status

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const c = status()
    const items: DialogSelectOption<string>[] = [
      {
        title: "Generation",
        value: "generation",
        description: String(c?.generation ?? "—"),
        category: "Config Status",
      },
      {
        title: "Dirty",
        value: "dirty",
        description: c?.dirty ? "yes" : "no",
        category: "Config Status",
      },
      {
        title: "Last Save Error",
        value: "last_save_error",
        description: c?.last_save_error || "none",
        category: "Config Status",
      },
    ]
    if (c?.dirty) {
      items.push({
        title: "Save Config to Disk",
        value: "save",
        category: "Actions",
        onSelect: async () => {
          try {
            await sdk.client.saveConfig()
            await sync.bootstrap({ fatal: false })
            toast.show({ message: "Config saved", variant: "success" })
          } catch (err) {
            toast.error(err)
          }
          dialog.clear()
        },
      })
    }
    return items
  })

  return (
    <DialogSelect
      title="Config"
      options={options()}
      placeholder=""
      renderFilter={false}
    />
  )
}
