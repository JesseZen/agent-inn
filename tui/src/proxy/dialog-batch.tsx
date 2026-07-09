import { createMemo, createSignal, onMount } from "solid-js"
import { Global } from "@agent-inn/core/global"
import { useDialog } from "../ui/dialog"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useProject } from "../context/project"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import type { BatchRun, BatchVariant, HostedSessionSummary, WorkerSummary } from "./backend"
import { launchProxySession, pasteHostedPrompt } from "./launch"

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
  const [batches, setBatches] = createSignal<BatchRun[]>([])

  onMount(async () => {
    setBatches(await sdk.client.listBatches())
  })

  const options = createMemo<DialogSelectOption<BatchListOption>[]>(() => [
    {
      title: "Create new batch",
      value: { type: "create" },
      description: "Race variants from one prompt",
      category: "Action",
    },
    ...batches().map((batch) => ({
      title: batch.title,
      value: { type: "batch" as const, batch },
      description: `${batch.worker_name} • ${batch.variants.length} variants`,
      category: "Batches",
    })),
  ])

  async function launchVariant(batch: BatchRun, variant: BatchVariant, prompt?: string) {
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
    })

    if (!prompt) return
    const session = await sdk.client.getHostedSession(variant.hosted_session_id)
    const tmuxWindowID = (session as { tmux_window_id?: string }).tmux_window_id
    if (!tmuxWindowID) return
    await pasteHostedPrompt({
      prompt,
      tmuxSocketName: settings.settings.terminal.tmux.socket_name,
      tmuxWindowID,
    })
  }

  function createBatch() {
    dialog.push(() => (
      <DialogSelect
        title="Choose worker"
        options={sync.data.workers.map((worker) => ({
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
    const countText = await DialogPrompt.show(dialog, "Variant Count", {
      placeholder: "Count",
    })
    if (countText === null) return
    const prompt = await DialogPrompt.show(dialog, "Prompt", {
      placeholder: "Prompt",
    })
    if (prompt === null) return
    const model = await DialogPrompt.show(dialog, "Model", {
      placeholder: "Model",
    })
    if (model === null) return

    const batch = await sdk.client.createBatch({
      title,
      ...(prompt ? { prompt } : {}),
      worker_name: worker.name,
      count: Number(countText),
      source_directory: sourceDirectory,
      ...(model ? { model } : {}),
    })
    for (const variant of batch.variants) {
      await launchVariant(batch, variant, batch.prompt)
    }
    dialog.replace(() => <DialogBatchRun batch={batch} />)
  }

  return (
    <DialogSelect
      title="Batch Runs"
      options={options()}
      placeholder="Search batches..."
      onSelect={(option) => {
        if (option.value.type === "create") {
          createBatch()
          return
        }
        dialog.replace(() => <DialogBatchRun batch={option.value.batch} />)
      }}
    />
  )
}

export function DialogBatchRun(props: { batch: BatchRun }) {
  const sdk = useSDK()
  const [batch, setBatch] = createSignal(props.batch)
  const [sessions, setSessions] = createSignal<HostedSessionSummary[]>([])
  const sessionStatus = createMemo(() => new Map(sessions().map((session) => [session.session_id, session.status])))

  onMount(async () => {
    setSessions(await sdk.client.listHostedSessions())
  })

  const options = createMemo<DialogSelectOption<BatchVariant>[]>(() =>
    batch().variants.map((variant) => {
      const winner = batch().winner_variant_id === variant.id
      const status = sessionStatus().get(variant.hosted_session_id) ?? "stale"
      return {
        title: `${winner ? "[winner] " : ""}${variant.session_label}`,
        value: variant,
        description: `${status} • ${variant.hosted_session_id}`,
        details: [
          `Hosted session ${variant.hosted_session_id}`,
          `Worktree ${variant.worktree_dir}`,
          `Status ${status}`,
          `Winner ${winner ? "yes" : "no"}`,
        ],
      }
    }),
  )

  async function openVariant(variant: BatchVariant) {
    const settings = await sdk.client.getSettings()
    await launchProxySession({
      executable: import.meta.env?.AINN_EXECUTABLE || undefined,
      workerPort: batch().worker_port,
      profile: batch().worker_name,
      configDir: Global.Path.config,
      workspace: variant.worktree_dir,
      model: batch().model,
      mode: "hosted-terminal",
      sessionID: variant.hosted_session_id,
      opener: settings.settings.terminal.opener,
      tmuxSocketName: settings.settings.terminal.tmux.socket_name,
      tmuxHostSession: settings.settings.terminal.tmux.host_session,
    })
  }

  return (
    <DialogSelect
      title={`Batch: ${batch().title}`}
      options={options()}
      placeholder="Select variant..."
      actions={[
        {
          command: "batch.winner",
          title: "winner",
          onTrigger: (option) => {
            void sdk.client.selectBatchWinner(batch().id, option.value.id).then((nextBatch) => {
              setBatch(nextBatch)
            })
          },
        },
      ]}
      onSelect={(option) => {
        void openVariant(option.value)
      }}
    />
  )
}
