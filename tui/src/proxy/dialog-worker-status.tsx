import { TextAttributes } from "@opentui/core"
import { For, Show, createMemo } from "solid-js"
import { useTheme } from "../context/theme"
import { useDialog } from "../ui/dialog"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import { DialogUpstreamPicker } from "./dialog-upstream-picker"
import { DialogLogs } from "./dialog-logs"
import { DialogModulePicker } from "./dialog-module"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogPoolPicker } from "./dialog-pool-picker"
import { useLanguage } from "../context/language"
import type { Translate } from "../i18n/en"

const LOG_LEVELS = ["simple", "detail"] as const
type LogLevel = (typeof LOG_LEVELS)[number]
const LAUNCHERS = ["codex", "claudecode", "grok", "opencode", "pi"] as const
type Launcher = (typeof LAUNCHERS)[number]
const REDACTED_PROXY_URL_VALUE = "******"

function upstreamLabel(worker: WorkerSummary, t: Translate) {
  return worker.upstream.missing ? t("proxy.worker.missingUpstream", { id: worker.upstream_id }) : worker.upstream.name
}

export function DialogWorkerStatus(props: { worker: WorkerSummary; management?: boolean }) {
  const { theme } = useTheme()
  const dialog = useDialog()
  const sdk = useSDK()
  const sync = useSync()
  const toast = useToast()
  const { t } = useLanguage()
  const currentWorker = createMemo(() => sync.data.workers.find((item) => item.id === props.worker.id) ?? props.worker)
  const modules = createMemo(() => Object.entries(props.worker.modules ?? {}))
  const hooks = createMemo(() => Object.entries(props.worker.hooks ?? {}))
  const hookStatusSummary = createMemo(() =>
    hooks()
      .map(([name]) => {
        const state = props.worker.hook_statuses?.[name]?.state
        return state ? `${name}: ${state}` : ""
      })
      .filter(Boolean)
      .join(" • "),
  )

  const renameAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.rename"),
    value: "rename",
    description: props.worker.id,
    onSelect: async () => {
      const worker = currentWorker()
      const value = await DialogPrompt.show(dialog, t("proxy.worker.renameTitle", { name: worker.name }), {
        placeholder: t("proxy.worker.displayName"),
        value: worker.name,
      })
      if (value === null) return
      const name = value.trim()
      if (!name) {
        toast.show({ message: t("proxy.worker.nameRequired"), variant: "error" })
        return
      }
      if (name === worker.name) return
      try {
        await sdk.client.patchWorker(props.worker.id, { name })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.renamed", { name }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }))

  const logLevelAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.logLevel"),
    value: "log_level",
    description: props.worker.log_level || "—",
    onSelect: async () => {
      const next = await new Promise<LogLevel | null>((resolve) => {
        dialog.push(
          () => (
            <DialogSelect
              title={t("proxy.worker.logLevelTitle", { name: props.worker.name })}
              options={LOG_LEVELS.map((level) => ({
                title: level,
                value: level,
                category: level === props.worker.log_level ? t("common.current") : t("common.options"),
              }))}
              placeholder={t("proxy.worker.logLevelPlaceholder")}
              current={props.worker.log_level}
              onSelect={(opt) => {
                resolve(opt.value as LogLevel)
                dialog.pop()
              }}
            />
          ),
          () => resolve(null),
        )
      })
      if (!next) return
      if (next === props.worker.log_level) return
      try {
        await sdk.client.patchWorker(props.worker.id, { log_level: next })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.savedField", { name: props.worker.name, field: "log_level", value: next }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }))

  const switchAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.switchUpstream"),
    value: "switch",
    description: upstreamLabel(props.worker, t),
    onSelect: () => dialog.push(() => <DialogUpstreamPicker worker={props.worker} />),
  }))

  const poolAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.fallbackPool"),
    value: "pool",
    description: props.worker.upstream_pool || t("common.none"),
    onSelect: () => dialog.push(() => <DialogPoolPicker worker={currentWorker()} />),
  }))

  const logsAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.viewLogs"),
    value: "logs",
    description: `:${props.worker.port}`,
    onSelect: () => dialog.push(() => <DialogLogs worker={props.worker} />),
  }))

  const modulesAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.manageModules"),
    value: "modules",
    description: t("proxy.worker.moduleCount", { modules: modules().length, hooks: hooks().length }),
    onSelect: async () => {
      const worker = await sdk.client.getWorker(props.worker.id)
      dialog.push(() => <DialogModulePicker worker={worker} />)
    },
  }))

  const restartAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.restart"),
    value: "restart",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      try {
        await sdk.client.restartWorker(props.worker.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.restarted", { name: props.worker.name }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }))

  const stopAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.stop"),
    value: "stop",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      try {
        await sdk.client.stopWorker(props.worker.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.stopped", { name: props.worker.name }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }))

  const deleteAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.delete"),
    value: "delete",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      const confirmed = await DialogConfirm.show(
        dialog,
        t("proxy.worker.deleteConfirmTitle"),
        t("proxy.worker.deleteConfirm", { name: props.worker.name }),
      )
      if (!confirmed) {
        dialog.clear()
        return
      }
      try {
        await sdk.client.deleteWorker(props.worker.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.deleted", { name: props.worker.name }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }))

  const launcherAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.launcher"),
    value: "launcher",
    description: props.worker.launcher || "codex",
    onSelect: async () => {
      const current = (props.worker.launcher || "codex") as Launcher
      const next = await new Promise<Launcher | null>((resolve) => {
        dialog.push(
          () => (
            <DialogSelect
              title={t("proxy.worker.launcherTitle", { name: props.worker.name })}
              options={LAUNCHERS.map((launcher) => ({
                title: launcher === "claudecode" ? "Claude Code" : launcher === "grok" ? "Grok Build" : launcher === "opencode" ? "OpenCode" : launcher === "pi" ? "Pi" : "Codex CLI",
                value: launcher,
                category: launcher === current ? t("common.current") : t("common.options"),
              }))}
              placeholder={t("proxy.worker.launcherPlaceholder")}
              current={current}
              onSelect={(opt) => {
                resolve(opt.value as Launcher)
                dialog.pop()
              }}
            />
          ),
          () => resolve(null),
        )
      })
      if (!next) return
      if (next === current) return
      try {
        await sdk.client.patchWorker(props.worker.id, { launcher: next })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.savedField", { name: props.worker.name, field: "launcher", value: next }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }))

  const portAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.port"),
    value: "port",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      const value = await DialogPrompt.show(dialog, t("proxy.worker.portTitle", { name: props.worker.name }), {
        placeholder: t("proxy.worker.portPlaceholder"),
        value: String(props.worker.port),
      })
      if (value === null) return
      const next = parseInt(value, 10)
      if (isNaN(next) || next <= 0) {
        toast.show({ message: t("proxy.worker.invalidPort"), variant: "error" })
        return
      }
      if (next === props.worker.port) return
      try {
        await sdk.client.patchWorker(props.worker.id, { port: next })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.savedField", { name: props.worker.name, field: "port", value: next }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }))

  const proxyAction = createMemo<DialogSelectOption<string>>(() => ({
    title: t("proxy.worker.proxyUrl"),
    value: "proxy_url",
    description: props.worker.proxy_url || t("common.direct"),
    onSelect: async () => {
      const current = props.worker.proxy_url ?? ""
      const redacted = props.worker.proxy_url_redacted === true
      let dirty = false
      const value = await DialogPrompt.show(dialog, t("proxy.worker.proxyUrlTitle", { name: props.worker.name }), {
        placeholder: "http://127.0.0.1:7890",
        value: redacted ? REDACTED_PROXY_URL_VALUE : current,
        onInputChange() {
          dirty = true
        },
      })
      if (value === null) return
      if (redacted && (!dirty || value === REDACTED_PROXY_URL_VALUE)) return
      if (!redacted && value === current) return
      try {
        await sdk.client.patchWorker(props.worker.id, { proxy_url: value })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: t("proxy.worker.savedField", { name: props.worker.name, field: "proxy_url", value: value || t("common.direct") }), variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }))

  const actions = createMemo<DialogSelectOption<string>[]>(() =>
    props.management
      ? [{ ...renameAction(), description: currentWorker().name }, logLevelAction(), switchAction(), modulesAction(), logsAction(), launcherAction(), portAction(), proxyAction(), { ...poolAction(), description: currentWorker().upstream_pool || t("common.none") }, restartAction(), stopAction(), deleteAction()]
      : [switchAction(), logsAction(), modulesAction()],
  )

  return (
    <DialogSelect
      title={`${currentWorker().name} (:${currentWorker().port})`}
      options={actions()}
      placeholder={t("proxy.worker.actionsPlaceholder")}
      footer={
        <box flexDirection="column">
          <text fg={theme.textMuted}>{t("proxy.worker.statusLine", { status: currentWorker().status })}{hookStatusSummary() ? ` • ${hookStatusSummary()}` : ""}</text>
          <text fg={theme.textMuted}>{t("proxy.worker.upstreamLine", { upstream: upstreamLabel(currentWorker(), t), protocol: currentWorker().protocol ?? "responses" })}</text>
          <text fg={theme.textMuted}>{t("proxy.worker.fallbackLine", { pool: currentWorker().upstream_pool || t("common.none") })}</text>
          <text fg={theme.textMuted}>{t("proxy.worker.launcherLine", { launcher: currentWorker().launcher ?? "codex", level: currentWorker().log_level ?? "" })}</text>
          <text fg={theme.textMuted}>{t("proxy.worker.proxyLine", { proxy: currentWorker().proxy_url || t("common.direct"), modules: modules().length, hooks: hooks().length })}</text>
          <Show when={modules().length > 0} fallback={<text fg={theme.textMuted}>{t("proxy.worker.modulesNone")}</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                {t("proxy.worker.requestMiddleware")}
              </text>
              <For each={modules()}>
                {([name, config]) => <text fg={theme.textMuted}>{config.enabled ? "✓" : "○"} {name}</text>}
              </For>
            </box>
          </Show>
          <Show when={hooks().length > 0} fallback={<text fg={theme.textMuted}>{t("proxy.worker.lifecycleHooksNone")}</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                {t("proxy.worker.lifecycleHooks")}
              </text>
              <For each={hooks()}>
                {([name, config]) => {
                  const state = props.worker.hook_statuses?.[name]?.state
                  return <text fg={theme.textMuted}>{config.enabled ? "✓" : "○"} {name}{state ? `: ${state}` : ""}</text>
                }}
              </For>
            </box>
          </Show>
        </box>
      }
    />
  )
}
