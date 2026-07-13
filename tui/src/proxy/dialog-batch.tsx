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
import { useLanguage } from "../context/language"

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

export type BatchSessionLauncher = typeof launchProxySession

type DialogBatchProps = {
  launchSession?: BatchSessionLauncher
}

export function DialogBatch(props: DialogBatchProps = {}) {
  const sdk = useSDK()
  const dialog = useDialog()
  const sync = useSync()
  const project = useProject()
  const toast = useToast()
  const { t } = useLanguage()
  const [batches, setBatches] = createSignal<BatchRun[]>([])

  onMount(async () => {
    setBatches(await sdk.client.listBatches())
  })

  const options = createMemo<DialogSelectOption<BatchListOption>[]>(() => [
    {
      title: t("proxy.batch.createRace"),
      value: { type: "create" },
      description: t("proxy.batch.createDescription"),
      category: t("proxy.batch.categoryAction"),
    },
    ...batches().map((batch) => ({
      title: batch.title,
      value: { type: "batch" as const, batch },
      description: t("proxy.batch.summary", { worker: batch.worker_name, count: batch.variants.length }),
      category: t("proxy.batch.category"),
    })),
  ])

  async function launchVariant(batch: BatchRun, variant: BatchVariant, hostedTerminalAttachMode: HostedTerminalAttachMode) {
    const settings = await sdk.client.getSettings()
    await (props.launchSession ?? launchProxySession)({
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
        title={t("proxy.batch.chooseWorker")}
        options={sync.data.workers
          .filter((worker) => (worker.role ?? "cli") === "cli")
          .map((worker) => ({
            title: worker.name,
            value: worker,
            description: `:${worker.port} • ${worker.upstream.name} • ${worker.status}`,
            category: worker.status === "running" ? t("proxy.batch.categoryRunning") : t("proxy.batch.categoryStopped"),
          }))}
        placeholder={t("proxy.worker.search")}
        onSelect={(option) => {
          void createBatchForWorker(option.value)
        }}
      />
    ))
  }

  async function createBatchForWorker(worker: WorkerSummary) {
    const basePath = project.instance.directory() || sync.path.directory
    const sourceDirectory = await DialogPrompt.show(dialog, t("proxy.batch.sourceDirectoryTitle"), {
      placeholder: t("proxy.batch.sourceDirectory"),
      value: basePath,
      directoryCompletion: basePath ? { basePath } : undefined,
    })
    if (sourceDirectory === null) return
    const title = await DialogPrompt.show(dialog, t("proxy.batch.titlePrompt"), {
      placeholder: t("proxy.batch.titlePlaceholder"),
    })
    if (title === null) return
    let count: number | undefined
    for (;;) {
      const countText = await DialogPrompt.show(dialog, t("proxy.batch.countPrompt"), {
        placeholder: t("proxy.batch.countPlaceholder"),
      })
      if (countText === null) return
      const trimmedCount = countText.trim()
      if (trimmedCount === "") break
      const parsedCount = Number(trimmedCount)
      if (Number.isInteger(parsedCount) && parsedCount >= minBatchVariantCount && parsedCount <= maxBatchVariantCount) {
        count = parsedCount
        break
      }
      toast.show({ message: t("proxy.batch.variantCount", { min: minBatchVariantCount, max: maxBatchVariantCount }), variant: "error" })
    }
    const model = await DialogPrompt.show(dialog, t("proxy.module.model"), {
      placeholder: t("proxy.module.model"),
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
    dialog.replace(() => <DialogBatchRun batch={batch} launchSession={props.launchSession} />)
  }

  return (
    <DialogSelect
      title={t("proxy.batch.title")}
      options={options()}
      placeholder={t("proxy.batch.search")}
      onSelect={(option) => {
        const selected = option.value
        if (selected.type === "create") {
          createBatch()
          return
        }
        dialog.replace(() => <DialogBatchRun batch={selected.batch} launchSession={props.launchSession} />)
      }}
    />
  )
}

export function DialogBatchRun(props: { batch: BatchRun; launchSession?: BatchSessionLauncher }) {
  const sdk = useSDK()
  const dialog = useDialog()
  const sync = useSync()
  const { t } = useLanguage()
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
        description: state === "idle" ? t("common.ready") : state,
      }
    }),
  )

  async function openVariant(variant: BatchVariant) {
    const settings = await sdk.client.getSettings()
    await (props.launchSession ?? launchProxySession)({
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
      title={t("proxy.batch.runTitle", { title: props.batch.title })}
      options={options()}
      placeholder={t("proxy.batch.selectVariant")}
      actions={[
        {
          command: "batch.delete",
          title: t("common.delete"),
          onTrigger: async () => {
            const confirmed = await DialogConfirm.show(
              dialog,
              t("proxy.batch.deleteTitle"),
              t("proxy.batch.deleteConfirm"),
            )
            if (!confirmed) return
            try {
              await sdk.client.deleteBatch(props.batch.id)
              dialog.replace(() => <DialogBatch launchSession={props.launchSession} />)
            } catch (err) {
              await DialogAlert.show(dialog, t("proxy.batch.deleteFailed"), String(err instanceof Error ? err.message : err))
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
