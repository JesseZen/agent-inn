import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"

type Launcher = "codex" | "claudecode" | "grok"

export async function showNewWorkerDialog(dialog: ReturnType<typeof import("../ui/dialog").useDialog>, sdk: ReturnType<typeof import("../context/sdk").useSDK>["client"], toast: ReturnType<typeof import("../ui/toast").useToast>) {
  const name = await DialogPrompt.show(dialog, "Worker Name", { placeholder: "e.g. worker-main" })
  if (!name) return

  dialog.replace(() => <LauncherStep name={name} />)
}

function LauncherStep(props: { name: string }) {
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
    {
      title: "Grok Build",
      value: "grok",
      description: "grok coding agent",
    },
  ]

  return (
    <DialogSelect
      title={`Select Launcher for ${props.name}`}
      options={options}
      placeholder="Search launchers..."
      onSelect={async (opt) => {
        dialog.replace(() => <UpstreamStep name={props.name} launcher={opt.value} />)
      }}
    />
  )
}

function UpstreamStep(props: { name: string; launcher: Launcher }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.upstreams.map((p) => ({
      title: p.name,
      value: p.id,
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
          await sdk.client.createWorker({ name: props.name, upstream: opt.value, launcher: props.launcher })
          toast.show({ message: `Created worker ${props.name}`, variant: "success" })
        } catch (err) {
          toast.error(err)
        }
        dialog.clear()
      }}
    />
  )
}
