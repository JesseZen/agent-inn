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

export function registerProxyCommands(api: TuiPluginApi, dependencies: { batchSessionLauncher?: BatchSessionLauncher } = {}) {
  return api.keymap.registerLayer({
    commands: [
      {
        namespace: "palette",
        name: "proxy.upstreams",
        title: "Manage upstreams",
        category: "Proxy",
        slashName: "upstreams",
        run() {
          api.ui.dialog.replace(() => <DialogUpstream />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.workers",
        title: "Manage workers",
        category: "Proxy",
        slashName: "workers",
        run() {
          api.ui.dialog.replace(() => <DialogWorkers />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.logs",
        title: "View worker logs",
        category: "Proxy",
        slashName: "logs",
        async run() {
          api.ui.dialog.replace(() => (
            <DialogWorkerPicker
              title="Worker Logs"
              placeholder="Search workers..."
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
        title: "View worker metrics",
        category: "Proxy",
        slashName: "status",
        run() {
          api.ui.dialog.replace(() => <DialogStatus />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.settings",
        title: "View proxy settings",
        category: "Proxy",
        slashName: "settings",
        slashAliases: ["config"],
        run() {
          api.ui.dialog.replace(() => <DialogSettings />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.launch",
        title: "Launch Worker",
        category: "Proxy",
        slashName: "launch",
        run() {
          api.ui.dialog.replace(() => <DialogLaunch />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.batch",
        title: "Run batch",
        category: "Proxy",
        slashName: "batch",
        run() {
          api.ui.dialog.replace(() => <DialogBatch launchSession={dependencies.batchSessionLauncher} />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.dashboard",
        title: "View relationship dashboard",
        category: "Proxy",
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
