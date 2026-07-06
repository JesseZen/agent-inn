import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"

type Launcher = "codex" | "claudecode"

export async function showNewWorkerDialog(dialog: ReturnType<typeof import("../ui/dialog").useDialog>, sdk: ReturnType<typeof import("../context/sdk").useSDK>["client"], toast: ReturnType<typeof import("../ui/toast").useToast>) {
  const portStr = await DialogPrompt.show(dialog, "New Worker", { placeholder: "Port number (e.g. 9094)" })
  if (!portStr) return
  const port = parseInt(portStr, 10)
  if (isNaN(port) || port <= 0) {
    toast.show({ message: "Invalid port number", variant: "error" })
    return
  }

  const name = await DialogPrompt.show(dialog, "Worker Name", { placeholder: "e.g. worker-default", value: `worker-${port}` })
  if (!name) return

  dialog.replace(() => <LauncherStep name={name} port={port} />)
}

function LauncherStep(props: { name: string; port: number }) {
  const dialog = useDialog()
  const options: DialogSelectOption<Launcher>[] = [
    {
      title: "Codex CLI",
      value: "codex",
      description: "codex launcher",
    },
    {
      title: "Claude Code",
      value: "claudecode",
      description: "claude launcher",
    },
  ]

  return (
    <DialogSelect
      title={`Select Launcher for ${props.name}`}
      options={options}
      placeholder="Search launchers..."
      onSelect={async (opt) => {
        dialog.replace(() => <UpstreamStep name={props.name} port={props.port} launcher={opt.value} />)
      }}
    />
  )
}

function UpstreamStep(props: { name: string; port: number; launcher: Launcher }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.upstreams.map((p) => ({
      title: p.name,
      value: p.name,
      description: `${p.base_url}${p.has_api_key ? "" : " (no key)"}`,
    })),
  )

  return (
    <DialogSelect
      title={`Select Upstream for ${props.name}`}
      options={options()}
      placeholder="Search upstreams..."
      onSelect={async (opt) => {
        try {
          await sdk.client.createWorker({ name: props.name, port: props.port, upstream: opt.value, launcher: props.launcher })
          toast.show({ message: `Created worker ${props.name}`, variant: "success" })
        } catch (err) {
          toast.error(err)
        }
        dialog.clear()
      }}
    />
  )
}
