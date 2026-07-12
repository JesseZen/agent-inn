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

const LOG_LEVELS = ["simple", "detail"] as const
type LogLevel = (typeof LOG_LEVELS)[number]
const LAUNCHERS = ["codex", "claudecode"] as const
type Launcher = (typeof LAUNCHERS)[number]
const REDACTED_PROXY_URL_VALUE = "******"

function upstreamLabel(worker: WorkerSummary) {
  return worker.upstream.missing ? `missing upstream: ${worker.upstream_id}` : worker.upstream.name
}

export function DialogWorkerStatus(props: { worker: WorkerSummary; management?: boolean }) {
  const { theme } = useTheme()
  const dialog = useDialog()
  const sdk = useSDK()
  const sync = useSync()
  const toast = useToast()
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

  const renameAction: DialogSelectOption<string> = {
    title: "Rename Worker",
    value: "rename",
    description: props.worker.id,
    onSelect: async () => {
      const value = await DialogPrompt.show(dialog, `Rename: ${props.worker.name}`, {
        placeholder: "Worker display name",
        value: props.worker.name,
      })
      if (value === null) return
      const name = value.trim()
      if (!name) {
        toast.show({ message: "Worker name is required", variant: "error" })
        return
      }
      if (name === props.worker.name) return
      try {
        await sdk.client.patchWorker(props.worker.id, { name })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Renamed worker ${name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  const logLevelAction: DialogSelectOption<string> = {
    title: "Log Level",
    value: "log_level",
    description: props.worker.log_level || "—",
    onSelect: async () => {
      const next = await new Promise<LogLevel | null>((resolve) => {
        dialog.push(
          () => (
            <DialogSelect
              title={`Log Level: ${props.worker.name}`}
              options={LOG_LEVELS.map((level) => ({
                title: level,
                value: level,
                category: level === props.worker.log_level ? "Current" : "Options",
              }))}
              placeholder="Select log level..."
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
        toast.show({ message: `Saved ${props.worker.name} log_level: ${next}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  const switchAction: DialogSelectOption<string> = {
    title: "Switch Upstream",
    value: "switch",
    description: upstreamLabel(props.worker),
    onSelect: () => dialog.push(() => <DialogUpstreamPicker worker={props.worker} />),
  }

  const poolAction: DialogSelectOption<string> = {
    title: "Fallback Pool",
    value: "pool",
    description: props.worker.upstream_pool || "none",
    onSelect: () => dialog.push(() => <DialogPoolPicker worker={currentWorker()} />),
  }

  const logsAction: DialogSelectOption<string> = {
    title: "View Logs",
    value: "logs",
    description: `:${props.worker.port}`,
    onSelect: () => dialog.push(() => <DialogLogs worker={props.worker} />),
  }

  const modulesAction: DialogSelectOption<string> = {
    title: "Manage Modules",
    value: "modules",
    description: `${modules().length} req • ${hooks().length} hook`,
    onSelect: async () => {
      const worker = await sdk.client.getWorker(props.worker.id)
      dialog.push(() => <DialogModulePicker worker={worker} />)
    },
  }

  const restartAction: DialogSelectOption<string> = {
    title: "Restart Worker",
    value: "restart",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      try {
        await sdk.client.restartWorker(props.worker.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Restarted ${props.worker.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const stopAction: DialogSelectOption<string> = {
    title: "Stop Worker",
    value: "stop",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      try {
        await sdk.client.stopWorker(props.worker.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Stopped ${props.worker.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const deleteAction: DialogSelectOption<string> = {
    title: "Delete Worker",
    value: "delete",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      const confirmed = await DialogConfirm.show(
        dialog,
        "Delete worker",
        `Delete ${props.worker.name}? This will remove the worker config and stop the process.`,
      )
      if (!confirmed) {
        dialog.clear()
        return
      }
      try {
        await sdk.client.deleteWorker(props.worker.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Deleted ${props.worker.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const launcherAction: DialogSelectOption<string> = {
    title: "Launcher",
    value: "launcher",
    description: props.worker.launcher || "codex",
    onSelect: async () => {
      const current = (props.worker.launcher || "codex") as Launcher
      const next = await new Promise<Launcher | null>((resolve) => {
        dialog.push(
          () => (
            <DialogSelect
              title={`Launcher: ${props.worker.name}`}
              options={LAUNCHERS.map((launcher) => ({
                title: launcher === "claudecode" ? "Claude Code" : "Codex CLI",
                value: launcher,
                category: launcher === current ? "Current" : "Options",
              }))}
              placeholder="Select launcher..."
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
        toast.show({ message: `Saved ${props.worker.name} launcher: ${next}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  const portAction: DialogSelectOption<string> = {
    title: "Port",
    value: "port",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      const value = await DialogPrompt.show(dialog, `Port: ${props.worker.name}`, {
        placeholder: "Port number",
        value: String(props.worker.port),
      })
      if (value === null) return
      const next = parseInt(value, 10)
      if (isNaN(next) || next <= 0) {
        toast.show({ message: "Invalid port number", variant: "error" })
        return
      }
      if (next === props.worker.port) return
      try {
        await sdk.client.patchWorker(props.worker.id, { port: next })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Saved ${props.worker.name} port: ${next}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  const proxyAction: DialogSelectOption<string> = {
    title: "Proxy URL",
    value: "proxy_url",
    description: props.worker.proxy_url || "direct",
    onSelect: async () => {
      const current = props.worker.proxy_url ?? ""
      const redacted = props.worker.proxy_url_redacted === true
      let dirty = false
      const value = await DialogPrompt.show(dialog, `Proxy URL: ${props.worker.name}`, {
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
        toast.show({ message: `Saved ${props.worker.name} proxy_url: ${value || "direct"}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  const actions = createMemo<DialogSelectOption<string>[]>(() =>
    props.management
      ? [renameAction, logLevelAction, switchAction, modulesAction, logsAction, launcherAction, portAction, proxyAction, { ...poolAction, description: currentWorker().upstream_pool || "none" }, restartAction, stopAction, deleteAction]
      : [switchAction, logsAction, modulesAction],
  )

  return (
    <DialogSelect
      title={`${currentWorker().name} (:${currentWorker().port})`}
      options={actions()}
      placeholder="Worker actions..."
      footer={
        <box flexDirection="column">
          <text fg={theme.textMuted}>status: {currentWorker().status}{hookStatusSummary() ? ` • ${hookStatusSummary()}` : ""}</text>
          <text fg={theme.textMuted}>upstream: {upstreamLabel(currentWorker())} • protocol: {currentWorker().protocol ?? "responses"}</text>
          <text fg={theme.textMuted}>fallback pool: {currentWorker().upstream_pool || "none"}</text>
          <text fg={theme.textMuted}>launcher: {currentWorker().launcher ?? "codex"} • log level: {currentWorker().log_level}</text>
          <text fg={theme.textMuted}>proxy: {currentWorker().proxy_url || "direct"} • modules: {modules().length} req / {hooks().length} hook</text>
          <Show when={modules().length > 0} fallback={<text fg={theme.textMuted}>modules: none</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                request middleware
              </text>
              <For each={modules()}>
                {([name, config]) => <text fg={theme.textMuted}>{config.enabled ? "✓" : "○"} {name}</text>}
              </For>
            </box>
          </Show>
          <Show when={hooks().length > 0} fallback={<text fg={theme.textMuted}>lifecycle hooks: none</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                lifecycle hooks
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
