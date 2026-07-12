import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { DialogPrompt } from "../ui/dialog-prompt"
import { createMemo, createSignal, onMount } from "solid-js"
import type { ProxySettings } from "./backend"
import { useLanguage } from "../context/language"

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

export function DialogSettings() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { t } = useLanguage()
  const fields = createMemo<SettingsField[]>(() => [
    { key: "state_dir", title: t("proxy.settings.stateDir"), category: t("category.paths"), value: (settings) => settings.state_dir, patch: (value) => ({ state_dir: value }) },
    { key: "log_dir", title: t("proxy.settings.logDir"), category: t("category.paths"), value: (settings) => settings.log_dir, patch: (value) => ({ log_dir: value }) },
    { key: "launch.default_mode", title: t("proxy.settings.defaultLaunchMode"), category: t("category.launch"), value: (settings) => settings.launch.default_mode, patch: (value) => ({ launch: { default_mode: value } }), choices: [
      { title: t("proxy.settings.externalWindow"), value: "external-window", description: t("proxy.settings.externalWindowDescription") },
      { title: t("proxy.settings.hostedTerminal"), value: "hosted-terminal", description: t("proxy.settings.hostedTerminalDescription") },
    ] },
    { key: "terminal.opener", title: t("proxy.settings.terminalOpener"), category: t("category.terminal"), value: (settings) => settings.terminal.opener, patch: (value) => ({ terminal: { opener: value } } as Partial<ProxySettings>), choices: [
      { title: t("proxy.settings.default"), value: "default" },
      { title: "Terminal.app", value: "terminal_app" }, { title: "iTerm2", value: "iterm2" }, { title: "WezTerm", value: "wezterm" }, { title: "Kitty", value: "kitty" },
    ] },
    { key: "terminal.tmux.socket_name", title: t("proxy.settings.tmuxSocket"), category: t("category.terminal"), value: (settings) => settings.terminal.tmux.socket_name, patch: (value) => ({ terminal: { tmux: { socket_name: value } } } as Partial<ProxySettings>) },
    { key: "terminal.tmux.host_session", title: t("proxy.settings.tmuxHostSession"), category: t("category.terminal"), value: (settings) => settings.terminal.tmux.host_session, patch: (value) => ({ terminal: { tmux: { host_session: value } } } as Partial<ProxySettings>) },
    { key: "terminal.tmux.host_start_mode", title: t("proxy.settings.tmuxHostStartMode"), category: t("category.terminal"), value: (settings) => settings.terminal.tmux.host_start_mode, patch: (value) => ({ terminal: { tmux: { host_start_mode: value } } } as Partial<ProxySettings>), choices: [
      { title: t("proxy.settings.newWindow"), value: "new-window", description: t("proxy.settings.newWindowDescription") },
      { title: t("proxy.settings.reuseFirstWindow"), value: "reuse-first-window", description: t("proxy.settings.reuseFirstWindowDescription") },
      { title: t("proxy.settings.mainTuiWindow"), value: "main-tui-window", description: t("proxy.settings.mainTuiWindowDescription") },
    ] },
    { key: "terminal.tmux.turn_status_hooks", title: t("proxy.settings.tmuxTurnHooks"), category: t("category.terminal"), value: (settings) => settings.terminal.tmux.turn_status_hooks ? "enabled" : "disabled", patch: (value) => ({ terminal: { tmux: { turn_status_hooks: value === "enabled" } } } as Partial<ProxySettings>), choices: [
      { title: t("common.enabled"), value: "enabled", description: t("proxy.settings.enableHooksDescription") },
      { title: t("common.disabled"), value: "disabled", description: t("proxy.settings.disableHooksDescription") },
    ] },
    { key: "metrics.retention_days", title: t("proxy.settings.metricsRetention"), category: t("category.metrics"), value: (settings) => String(settings.metrics.retention_days), patch: (value) => ({ metrics: { retention_days: Number(value) } } as Partial<ProxySettings>) },
  ])
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
    toast.show({ message: t("proxy.settings.fieldSaved", { field: field.title }), variant: "success" })
    if (field.key === "terminal.tmux.host_start_mode" && value === "reuse-first-window") {
      const hostedSessions = await sdk.client.listHostedSessions()
      if (hostedSessions.length > 0) {
        setTimeout(() => {
          toast.show({
            message: t("proxy.settings.hostStartWarning"),
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
      ...fields().map((field) => ({
        title: field.title,
        value: field.key,
        description: current ? field.value(current) || "—" : t("common.loading"),
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
                      category: choice.value === currentValue ? t("common.current") : t("common.options"),
                    }))}
                    placeholder={t("proxy.settings.selectField", { field: field.title })}
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
      { title: t("proxy.settings.generation"), value: "generation", description: String(c?.generation ?? "—"), category: t("category.configStatus") },
      { title: t("proxy.settings.dirty"), value: "dirty", description: c?.dirty ? t("common.yes") : t("common.no"), category: t("category.configStatus") },
      { title: t("proxy.settings.lastSaveError"), value: "last_save_error", description: c?.last_save_error || t("common.none"), category: t("category.configStatus") },
    ]
    if (c?.dirty) {
      items.push({
        title: t("proxy.settings.saveToDisk"),
        value: "save",
        category: t("common.actions"),
        onSelect: async () => {
          try {
            await sdk.client.saveConfig()
            await sync.bootstrap({ fatal: false })
            toast.show({ message: t("proxy.settings.saved"), variant: "success" })
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
      title={t("proxy.settings")}
      options={options()}
      placeholder=""
      renderFilter={false}
    />
  )
}
