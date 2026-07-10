import { createMemo, createSignal, onMount } from "solid-js"
import { Global } from "@agent-inn/core/global"
import { useDialog } from "../ui/dialog"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { DialogAlert } from "../ui/dialog-alert"
import { DialogConfirm } from "../ui/dialog-confirm"
import { useProject } from "../context/project"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import type { BatchRun, BatchVariant, HostedSessionSummary, WorkerSummary } from "./backend"
import { launchProxySession, type HostedTerminalAttachMode } from "./launch"

const minBatchVariantCount = 1
const maxBatchVariantCount = 8

type BatchListOption =
  | {
      type: "create"
    }
  | {
      type: "batch"
      batch: BatchRun
    }

export function DialogBatch() {
  const sdk = useSDK()
  const dialog = useDialog()
  const sync = useSync()
  const project = useProject()
  const toast = useToast()
  const [batches, setBatches] = createSignal<BatchRun[]>([])

  onMount(async () => {
    setBatches(await sdk.client.listBatches())
  })

  const options = createMemo<DialogSelectOption<BatchListOption>[]>(() => [
    {
      title: "Create a worktree race",
      value: { type: "create" },
      description: "Create isolated worktrees",
      category: "Action",
    },
    ...batches().map((batch) => ({
      title: batch.title,
      value: { type: "batch" as const, batch },
      description: `${batch.worker_name} • ${batch.variants.length} variants`,
      category: "Batches",
    })),
  ])

  async function launchVariant(batch: BatchRun, variant: BatchVariant, hostedTerminalAttachMode: HostedTerminalAttachMode) {
    const settings = await sdk.client.getSettings()
    await launchProxySession({
      executable: import.meta.env?.AINN_EXECUTABLE || undefined,
      workerPort: batch.worker_port,
      profile: batch.worker_name,
      configDir: Global.Path.config,
      workspace: variant.worktree_dir,
      model: batch.model,
      mode: "hosted-terminal",
      sessionID: variant.hosted_session_id,
      opener: settings.settings.terminal.opener,
      tmuxSocketName: settings.settings.terminal.tmux.socket_name,
      tmuxHostSession: settings.settings.terminal.tmux.host_session,
      hostedTerminalAttachMode,
    })

  }

  function createBatch() {
    dialog.push(() => (
      <DialogSelect
        title="Choose worker"
        options={sync.data.workers
          .filter((worker) => (worker.role ?? "cli") === "cli")
          .map((worker) => ({
            title: worker.name,
            value: worker,
            description: `:${worker.port} • ${worker.upstream.name} • ${worker.status}`,
            category: worker.status === "running" ? "Running" : "Stopped",
          }))}
        placeholder="Search workers..."
        onSelect={(option) => {
          void createBatchForWorker(option.value)
        }}
      />
    ))
  }

  async function createBatchForWorker(worker: WorkerSummary) {
    const basePath = project.instance.directory() || sync.path.directory
    const sourceDirectory = await DialogPrompt.show(dialog, "Source Directory", {
      placeholder: "Source directory",
      value: basePath,
      directoryCompletion: basePath ? { basePath } : undefined,
    })
    if (sourceDirectory === null) return
    const title = await DialogPrompt.show(dialog, "Batch Title", {
      placeholder: "Title",
    })
    if (title === null) return
    let count: number | undefined
    for (;;) {
      const countText = await DialogPrompt.show(dialog, "Variant Count", {
        placeholder: "Count",
      })
      if (countText === null) return
      const trimmedCount = countText.trim()
      if (trimmedCount === "") break
      const parsedCount = Number(trimmedCount)
      if (Number.isInteger(parsedCount) && parsedCount >= minBatchVariantCount && parsedCount <= maxBatchVariantCount) {
        count = parsedCount
        break
      }
      toast.show({ message: `Variant count must be between ${minBatchVariantCount} and ${maxBatchVariantCount}`, variant: "error" })
    }
    const model = await DialogPrompt.show(dialog, "Model", {
      placeholder: "Model",
    })
    if (model === null) return

    const batch = await sdk.client.createBatch({
      title,
      worker_name: worker.name,
      ...(count !== undefined ? { count } : {}),
      source_directory: sourceDirectory,
      ...(model ? { model } : {}),
    })
    for (const variant of batch.variants) {
      await launchVariant(batch, variant, "setup-only")
    }
    if (batch.variants.length > 0) await launchVariant(batch, batch.variants[0], "open")
    dialog.replace(() => <DialogBatchRun batch={batch} />)
  }

  return (
    <DialogSelect
      title="Batch Runs"
      options={options()}
      placeholder="Search batches..."
      onSelect={(option) => {
        const selected = option.value
        if (selected.type === "create") {
          createBatch()
          return
        }
        dialog.replace(() => <DialogBatchRun batch={selected.batch} />)
      }}
    />
  )
}

export function DialogBatchRun(props: { batch: BatchRun }) {
  const sdk = useSDK()
  const dialog = useDialog()
  const sync = useSync()
  const [sessions, setSessions] = createSignal<HostedSessionSummary[]>([])
  const sessionStates = createMemo(() => new Map(sessions().map((session) => [session.session_id, session.turn_state ?? "idle"])))

  onMount(async () => {
    setSessions(await sdk.client.listHostedSessions())
  })

  const options = createMemo<DialogSelectOption<BatchVariant>[]>(() =>
    props.batch.variants.map((variant) => {
      const state = sync.data.hosted_session_turn_states[variant.hosted_session_id] ?? sessionStates().get(variant.hosted_session_id) ?? "interrupted"
      return {
        title: variant.session_label,
        value: variant,
        description: state === "idle" ? "ready" : state,
      }
    }),
  )

  async function openVariant(variant: BatchVariant) {
    const settings = await sdk.client.getSettings()
    await launchProxySession({
      executable: import.meta.env?.AINN_EXECUTABLE || undefined,
      workerPort: props.batch.worker_port,
      profile: props.batch.worker_name,
      configDir: Global.Path.config,
      workspace: variant.worktree_dir,
      model: props.batch.model,
      mode: "hosted-terminal",
      sessionID: variant.hosted_session_id,
      opener: settings.settings.terminal.opener,
      tmuxSocketName: settings.settings.terminal.tmux.socket_name,
      tmuxHostSession: settings.settings.terminal.tmux.host_session,
      hostedTerminalAttachMode: "open",
    })
  }

  return (
    <DialogSelect
      title={`Batch: ${props.batch.title}`}
      options={options()}
      placeholder="Select variant..."
      actions={[
        {
          command: "batch.delete",
          title: "delete",
          onTrigger: async () => {
            const confirmed = await DialogConfirm.show(
              dialog,
              "Delete batch",
              "Remove all variants, hosted sessions, and worktrees?",
            )
            if (!confirmed) return
            try {
              await sdk.client.deleteBatch(props.batch.id)
              dialog.replace(() => <DialogBatch />)
            } catch (err) {
              await DialogAlert.show(dialog, "Delete batch failed", String(err instanceof Error ? err.message : err))
            }
          },
        },
      ]}
      onSelect={(option) => {
        void openVariant(option.value)
      }}
    />
  )
}
