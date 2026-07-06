import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { DialogPrompt } from "../ui/dialog-prompt"
import { createMemo, createSignal, onMount } from "solid-js"
import type { ProxySettings } from "./backend"
import { DialogAlert } from "../ui/dialog-alert"

type SettingsField = {
  key: string
  title: string
  category: string
  value: (settings: ProxySettings) => string
  patch: (value: string) => Partial<ProxySettings>
  presets?: string[]
}

const FIELDS: SettingsField[] = [
  {
    key: "state_dir",
    title: "State Dir",
    category: "Paths",
    value: (settings) => settings.state_dir,
    patch: (value) => ({ state_dir: value }),
  },
  {
    key: "log_dir",
    title: "Log Dir",
    category: "Paths",
    value: (settings) => settings.log_dir,
    patch: (value) => ({ log_dir: value }),
  },
  {
    key: "launch.default_mode",
    title: "Default Launch Mode",
    category: "Launch",
    value: (settings) => settings.launch.default_mode,
    patch: (value) => ({ launch: { default_mode: value } }),
  },
  {
    key: "terminal.opener",
    title: "Terminal Opener",
    category: "Terminal",
    value: (settings) => settings.terminal.opener,
    patch: (value) => ({ terminal: { opener: value } } as Partial<ProxySettings>),
    presets: ["default", "terminal_app", "iterm2", "wezterm", "kitty"],
  },
  {
    key: "terminal.tmux.socket_name",
    title: "Tmux Socket",
    category: "Terminal",
    value: (settings) => settings.terminal.tmux.socket_name,
    patch: (value) => ({ terminal: { tmux: { socket_name: value } } } as Partial<ProxySettings>),
  },
  {
    key: "terminal.tmux.host_session",
    title: "Tmux Host Session",
    category: "Terminal",
    value: (settings) => settings.terminal.tmux.host_session,
    patch: (value) => ({ terminal: { tmux: { host_session: value } } } as Partial<ProxySettings>),
  },
  {
    key: "terminal.tmux.host_start_mode",
    title: "Tmux Host Start Mode",
    category: "Terminal",
    value: (settings) => settings.terminal.tmux.host_start_mode,
    patch: (value) => ({ terminal: { tmux: { host_start_mode: value } } } as Partial<ProxySettings>),
    presets: ["new-window", "reuse-first-window", "main-tui-window"],
  },
]

export function DialogSettings() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const status = () => sync.data.config_status
  const [settings, setSettings] = createSignal<ProxySettings>()

  onMount(async () => {
    try {
      const response = await sdk.client.getSettings()
      setSettings(response.settings)
    } catch (err) {
      toast.error(err)
    }
  })

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const c = status()
    const current = settings()
    const items: DialogSelectOption<string>[] = [
      ...FIELDS.map((field) => ({
        title: field.title,
        value: field.key,
        description: current ? field.value(current) || "—" : "loading",
        category: field.category,
        onSelect: async () => {
          if (!current) return
          if (field.presets) {
            const result = await DialogPrompt.show(dialog, field.title, {
              value: field.value(current),
              selectAll: true,
              placeholder: field.presets.join(", "),
            })
            if (result === null) return
            if (!field.presets.includes(result)) {
              await DialogAlert.show(dialog, `Invalid ${field.title.toLowerCase()}`, `Choose one of: ${field.presets.join(", ")}`)
              return
            }
            try {
              const response = await sdk.client.patchSettings(field.patch(result))
              setSettings(response.settings)
              await sync.bootstrap({ fatal: false })
              toast.show({ message: `Saved ${field.title}`, variant: "success" })
              if (field.key === "terminal.tmux.host_start_mode" && result === "reuse-first-window") {
                const hostedSessions = await sdk.client.listHostedSessions()
                if (hostedSessions.length > 0) {
                  setTimeout(() => {
                    toast.show({
                      message: "Host start mode applies only to newly created tmux hosts. Remove existing hosted terminal sessions to recreate the host.",
                      variant: "info",
                    })
                  }, 0)
                }
              }
              dialog.replace(() => <DialogSettings />)
            } catch (err) {
              toast.error(err)
            }
            return
          }
          const result = await DialogPrompt.show(dialog, field.title, {
            value: field.value(current),
            selectAll: true,
            placeholder: field.title,
          })
          if (result === null) return
          try {
            const response = await sdk.client.patchSettings(field.patch(result))
            setSettings(response.settings)
            await sync.bootstrap({ fatal: false })
            toast.show({ message: `Saved ${field.title}`, variant: "success" })
            dialog.replace(() => <DialogSettings />)
          } catch (err) {
            toast.error(err)
          }
        },
      })),
      { title: "Generation", value: "generation", description: String(c?.generation ?? "—"), category: "Config Status" },
      { title: "Dirty", value: "dirty", description: c?.dirty ? "yes" : "no", category: "Config Status" },
      { title: "Last Save Error", value: "last_save_error", description: c?.last_save_error || "none", category: "Config Status" },
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
      title="Settings"
      options={options()}
      placeholder=""
      renderFilter={false}
    />
  )
}
