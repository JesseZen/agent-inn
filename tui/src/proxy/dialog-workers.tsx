import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { showNewWorkerDialog } from "./dialog-new-worker"

const LOG_LEVELS = ["simple", "detail"] as const
type LogLevel = (typeof LOG_LEVELS)[number]

type FieldKey = "log_level"
type Field = { key: FieldKey; title: string }

const FIELDS: Field[] = [{ key: "log_level", title: "Log Level" }]

export function DialogWorkers() {
  const sync = useSync()
  const dialog = useDialog()
  const sdk = useSDK()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<string>[]>(() => [
    { title: "Create New Worker", value: "create", description: "Add a worker", category: "Actions" },
    ...sync.data.workers.map((w) => ({
      title: w.name,
      value: `edit:${w.port}`,
      description: `:${w.port} • ${w.upstream.name} • ${w.status}`,
      category: "Workers",
    })),
  ])

  return (
    <DialogSelect
      title="Manage Workers"
      options={options()}
      placeholder="Search workers..."
      onSelect={async (opt) => {
        if (opt.value === "create") {
          void showNewWorkerDialog(dialog as never, sdk.client as never, toast as never)
          return
        }
        const port = Number(opt.value.slice("edit:".length))
        const worker = sync.data.workers.find((w) => w.port === port)
        if (!worker) return
        dialog.replace(() => <DialogWorkerEditor worker={worker} />)
      }}
    />
  )
}

function DialogWorkerEditor(props: { worker: WorkerSummary }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<FieldKey>[]>(() =>
    FIELDS.map((field) => ({
      title: field.title,
      value: field.key,
      description: props.worker.log_level || "—",
      category: "Fields",
      onSelect: async () => {
        const next = await new Promise<LogLevel | null>((resolve) => {
          dialog.replace(
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
                onSelect={(opt) => resolve(opt.value as LogLevel)}
              />
            ),
            () => resolve(null),
          )
        })
        if (!next || next === props.worker.log_level) {
          dialog.replace(() => <DialogWorkerEditor worker={props.worker} />)
          return
        }
        try {
          await sdk.client.patchWorker(props.worker.port, { log_level: next })
          await sync.bootstrap({ fatal: false })
          toast.show({ message: `Saved ${props.worker.name} log_level: ${next}`, variant: "success" })
        } catch (err) {
          toast.error(err)
        }
        dialog.clear()
      },
    })),
  )

  return <DialogSelect title={`Edit Worker: ${props.worker.name}`} options={options()} placeholder="Select a field..." />
}
