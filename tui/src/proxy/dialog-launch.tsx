import { createMemo, onMount } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useDialog } from "../ui/dialog"
import { DialogPrompt } from "../ui/dialog-prompt"
import { useClipboard } from "../context/clipboard"
import { DialogAlert } from "../ui/dialog-alert"
import { useProject } from "../context/project"
import { createProxyLaunchCommand, launchProxySession, renderProxyLaunchCommand, type LaunchMode } from "./launch"
import { DialogHostedTerminal } from "./dialog-hosted-terminal"
import { Global } from "@agent-inn/core/global"
import { useSDK } from "../context/sdk"
import { useWorkerFrecency } from "./worker-frecency-context"
import type { WorkerSummary } from "./backend"

export function resolveExternalLaunchTarget(workers: WorkerSummary[], workerID: string) {
  const worker = workers.find((item) => item.id === workerID)
  if (!worker) return
  return { worker, workerPort: worker.port, profile: worker.id }
}

export function DialogLaunch() {
  const dialog = useDialog()
  const sdk = useSDK()

  onMount(async () => {
    const settings = await sdk.client.getSettings()
    if (settings.settings.launch.default_mode === "hosted-terminal") {
      dialog.replace(() => <DialogHostedTerminal />)
      return
    }
    dialog.replace(() => <DialogExternalWindowLaunch />)
  })

  return (
    <DialogSelect
      title="Launch Worker"
      options={[] as DialogSelectOption<LaunchMode>[]}
      placeholder="Loading launch settings..."
      renderFilter={false}
    />
  )
}

function DialogExternalWindowLaunch() {
  const sync = useSync()
  const sdk = useSDK()
  const project = useProject()
  const dialog = useDialog()
  const clipboard = useClipboard()
  const workerFrecency = useWorkerFrecency()

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const sections = workerFrecency.sections(sync.data.workers.filter((worker) => worker.role === "cli"))
    const toOption = (worker: (typeof sections.recent)[number], category: string) => ({
      title: worker.name,
      value: worker.id,
      description: `:${worker.port} • ${worker.upstream.name}`,
      category,
    })

    return [
      ...sections.recent.map((worker) => toOption(worker, "Recent")),
      ...sections.rest.map((worker) =>
        toOption(worker, worker.status === "running" ? "Running cli workers" : "Stopped cli workers"),
      ),
    ]
  })

  async function launch(workerID: string) {
    const target = resolveExternalLaunchTarget(sync.data.workers, workerID)
    if (!target) return
    const { worker, workerPort, profile } = target
    const basePath = project.instance.directory() || sync.path.directory
    const workspace = await DialogPrompt.show(dialog, "Launch Worker", {
      placeholder: "Workspace directory",
      description: () => <text>Launch this worker in the workspace.</text>,
      value: basePath,
      directoryCompletion: basePath
        ? {
            basePath,
          }
        : undefined,
    })
    if (workspace === null) return

    dialog.clear()
    const settings = await sdk.client.getSettings()
    const command = createProxyLaunchCommand({
      workerPort,
      profile,
      configDir: Global.Path.config,
      workspace: workspace || undefined,
    })
    const rendered = renderProxyLaunchCommand(command)
    await clipboard.write?.(rendered).catch(() => undefined)
    try {
      const launched = await launchProxySession({
        executable: import.meta.env?.AINN_EXECUTABLE || undefined,
        workerPort,
        profile,
        configDir: Global.Path.config,
        workspace: workspace || undefined,
        opener: settings.settings.terminal.opener,
      })
      if (!launched) {
        await DialogAlert.show(dialog, "Launch Command", rendered)
        return
      }
      workerFrecency.record(profile)
      await DialogAlert.show(dialog, "Launch", "Opened a new worker session.")
    } catch (err) {
      await DialogAlert.show(dialog, "Launch failed", String(err instanceof Error ? err.message : err))
    }
  }

  return (
    <DialogSelect
      title="Launch Worker"
      options={options()}
      placeholder="Search cli workers..."
      onSelect={(option) => {
        void launch(option.value)
      }}
    />
  )
}
