import type { TuiPluginApi } from "@agent-inn/plugin/tui"
import { DialogSettings } from "./dialog-settings"
import { DialogLogs } from "./dialog-logs"
import { DialogDashboard, type DashboardSnapshot } from "./dialog-dashboard"
import { DialogUpstream } from "./dialog-upstream"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { DialogWorkers } from "./dialog-workers"
import { DialogLaunch } from "./dialog-launch"
import { DialogBatch, type BatchSessionLauncher } from "./dialog-batch"
import { DialogStatus } from "./dialog-status"
import { DialogPool } from "./dialog-pool"
import { useLanguage } from "../context/language"

export function registerProxyCommands(api: TuiPluginApi, dependencies: { batchSessionLauncher?: BatchSessionLauncher } = {}) {
  const { t } = useLanguage()
  return api.keymap.registerLayer({
    commands: [
      {
        namespace: "palette",
        name: "proxy.upstreams",
        title: t("proxy.command.manageUpstreams"),
        category: t("proxy.command.category"),
        slashName: "upstreams",
        run() {
          api.ui.dialog.replace(() => <DialogUpstream />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.workers",
        title: t("proxy.command.manageWorkers"),
        category: t("proxy.command.category"),
        slashName: "workers",
        run() {
          api.ui.dialog.replace(() => <DialogWorkers />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.pools",
        title: t("proxy.command.managePools"),
        category: t("proxy.command.category"),
        slashName: "pools",
        run() {
          api.ui.dialog.replace(() => <DialogPool />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.logs",
        title: t("proxy.command.viewWorkerLogs"),
        category: t("proxy.command.category"),
        slashName: "logs",
        async run() {
          api.ui.dialog.replace(() => (
            <DialogWorkerPicker
              title={t("proxy.logs.workerTitle")}
              placeholder={t("proxy.worker.search")}
              onSelect={async (worker) => {
                const initialLines = await (api.client as unknown as { getLogs(port: number): Promise<string[]> }).getLogs(
                  worker.port,
                )
                api.ui.dialog.push(() => <DialogLogs worker={worker} initialLines={initialLines} />)
              }}
            />
          ))
        },
      },
      {
        namespace: "palette",
        name: "proxy.status",
        title: t("proxy.command.viewWorkerMetrics"),
        category: t("proxy.command.category"),
        slashName: "status",
        run() {
          api.ui.dialog.replace(() => <DialogStatus />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.settings",
        title: t("proxy.settings.view"),
        category: t("proxy.command.category"),
        slashName: "settings",
        slashAliases: ["config"],
        run() {
          api.ui.dialog.replace(() => <DialogSettings />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.launch",
        title: t("proxy.command.launchWorker"),
        category: t("proxy.command.category"),
        slashName: "launch",
        run() {
          api.ui.dialog.replace(() => <DialogLaunch />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.batch",
        title: t("proxy.command.runBatch"),
        category: t("proxy.command.category"),
        slashName: "batch",
        run() {
          api.ui.dialog.replace(() => <DialogBatch launchSession={dependencies.batchSessionLauncher} />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.dashboard",
        title: t("proxy.command.viewDashboard"),
        category: t("proxy.command.category"),
        slashName: "dashboard",
        slashAliases: ["topology"],
        run() {
          const snapshot: DashboardSnapshot = { state: null, scrollTop: 0 }
          api.ui.dialog.replace(() => <DialogDashboard snapshot={snapshot} />)
        },
      },
    ],
    bindings: [],
  })
}
