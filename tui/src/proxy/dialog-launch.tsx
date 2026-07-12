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
import { useLanguage } from "../context/language"

export function resolveExternalLaunchTarget(workers: WorkerSummary[], workerID: string) {
  const worker = workers.find((item) => item.id === workerID)
  if (!worker) return
  return { worker, workerPort: worker.port, profile: worker.id }
}

export function DialogLaunch() {
  const dialog = useDialog()
  const sdk = useSDK()
  const { t } = useLanguage()

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
      title={t("proxy.command.launchWorker")}
      options={[] as DialogSelectOption<LaunchMode>[]}
      placeholder={t("proxy.launch.loading")}
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
  const { t } = useLanguage()

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const sections = workerFrecency.sections(sync.data.workers.filter((worker) => worker.role === "cli"))
    const toOption = (worker: (typeof sections.recent)[number], category: string) => ({
      title: worker.name,
      value: worker.id,
      description: `:${worker.port} • ${worker.upstream.name}`,
      category,
    })

    return [
      ...sections.recent.map((worker) => toOption(worker, t("proxy.launch.categoryRecent"))),
      ...sections.rest.map((worker) =>
        toOption(worker, worker.status === "running" ? t("proxy.launch.categoryRunning") : t("proxy.launch.categoryStopped")),
      ),
    ]
  })

  async function launch(workerID: string) {
    const target = resolveExternalLaunchTarget(sync.data.workers, workerID)
    if (!target) return
    const { worker, workerPort, profile } = target
    const basePath = project.instance.directory() || sync.path.directory
    const workspace = await DialogPrompt.show(dialog, t("proxy.command.launchWorker"), {
      placeholder: t("proxy.hosted.workspace"),
      description: () => <text>{t("proxy.hosted.launchDescription")}</text>,
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
        await DialogAlert.show(dialog, t("proxy.launch.commandTitle"), rendered)
        return
      }
      workerFrecency.record(profile)
      await DialogAlert.show(dialog, t("category.launch"), t("proxy.launch.opened"))
    } catch (err) {
      await DialogAlert.show(dialog, t("proxy.launch.failed"), String(err instanceof Error ? err.message : err))
    }
  }

  return (
    <DialogSelect
      title={t("proxy.command.launchWorker")}
      options={options()}
      placeholder={t("proxy.launch.searchCliWorkers")}
      onSelect={(option) => {
        void launch(option.value)
      }}
    />
  )
}
