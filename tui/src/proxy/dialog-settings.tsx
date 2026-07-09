import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { DialogPrompt } from "../ui/dialog-prompt"
import { createMemo, createSignal, onMount } from "solid-js"
import type { ProxySettings } from "./backend"

type SettingsChoice = {
  title: string
  value: string
  description?: string
}

type SettingsField = {
  key: string
  title: string
  category: string
  value: (settings: ProxySettings) => string
  patch: (value: string) => Partial<ProxySettings>
  choices?: SettingsChoice[]
}

const LAUNCH_MODE_CHOICES: SettingsChoice[] = [
  { title: "External window", value: "external-window", description: "Open launchers in a new terminal window" },
  { title: "Hosted terminal", value: "hosted-terminal", description: "Use AINN-managed tmux sessions" },
]

const TERMINAL_OPENER_CHOICES: SettingsChoice[] = [
  { title: "Default", value: "default" },
  { title: "Terminal.app", value: "terminal_app" },
  { title: "iTerm2", value: "iterm2" },
  { title: "WezTerm", value: "wezterm" },
  { title: "Kitty", value: "kitty" },
]

const TMUX_HOST_START_MODE_CHOICES: SettingsChoice[] = [
  { title: "New window", value: "new-window", description: "Create hosted sessions in new tmux windows" },
  { title: "Reuse first window", value: "reuse-first-window", description: "Use window 0 for the first hosted session on a new host" },
  { title: "Main TUI window", value: "main-tui-window", description: "Run the main TUI in tmux window 0" },
]

const TMUX_TURN_STATUS_HOOK_CHOICES: SettingsChoice[] = [
  { title: "Enabled", value: "enabled", description: "Install AINN-managed Codex and Claude turn hooks" },
  { title: "Disabled", value: "disabled", description: "Remove AINN-managed turn hooks" },
]

const METRICS_PERSIST_CHOICES: SettingsChoice[] = [
  { title: "Enabled", value: "enabled" },
  { title: "Disabled", value: "disabled" },
]

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
    choices: LAUNCH_MODE_CHOICES,
  },
  {
    key: "terminal.opener",
    title: "Terminal Opener",
    category: "Terminal",
    value: (settings) => settings.terminal.opener,
    patch: (value) => ({ terminal: { opener: value } } as Partial<ProxySettings>),
    choices: TERMINAL_OPENER_CHOICES,
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
    choices: TMUX_HOST_START_MODE_CHOICES,
  },
  {
    key: "terminal.tmux.turn_status_hooks",
    title: "Tmux Turn Status Hooks",
    category: "Terminal",
    value: (settings) => (settings.terminal.tmux.turn_status_hooks ? "enabled" : "disabled"),
    patch: (value) => ({ terminal: { tmux: { turn_status_hooks: value === "enabled" } } } as Partial<ProxySettings>),
    choices: TMUX_TURN_STATUS_HOOK_CHOICES,
  },
  {
    key: "metrics.persist_enabled",
    title: "Metrics Persist",
    category: "Metrics",
    value: (settings) => (settings.metrics.persist_enabled ? "enabled" : "disabled"),
    patch: (value) => ({ metrics: { persist_enabled: value === "enabled" } } as Partial<ProxySettings>),
    choices: METRICS_PERSIST_CHOICES,
  },
  {
    key: "metrics.retention_days",
    title: "Metrics Retention",
    category: "Metrics",
    value: (settings) => String(settings.metrics.retention_days),
    patch: (value) => ({ metrics: { retention_days: Number(value) } } as Partial<ProxySettings>),
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

  async function saveField(field: SettingsField, value: string) {
    const response = await sdk.client.patchSettings(field.patch(value))
    setSettings(response.settings)
    await sync.bootstrap({ fatal: false })
    toast.show({ message: `Saved ${field.title}`, variant: "success" })
    if (field.key === "terminal.tmux.host_start_mode" && value === "reuse-first-window") {
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
  }

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
          if (field.choices) {
            const currentValue = field.value(current)
            const choices = field.choices
            const result = await new Promise<string | null>((resolve) => {
              dialog.push(
                () => (
                  <DialogSelect
                    title={field.title}
                    options={choices.map((choice) => ({
                      title: choice.title,
                      value: choice.value,
                      description: choice.description,
                      category: choice.value === currentValue ? "Current" : "Options",
                    }))}
                    placeholder={`Select ${field.title}...`}
                    current={currentValue}
                    onSelect={(opt) => {
                      resolve(opt.value)
                      dialog.pop()
                    }}
                  />
                ),
                () => resolve(null),
              )
            })
            if (result === null) return
            if (result === currentValue) return
            try {
              await saveField(field, result)
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
            await saveField(field, result)
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
