import { createMemo } from "solid-js"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { useLanguage } from "../context/language"

export function DialogPoolPicker(props: { worker: WorkerSummary }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { t } = useLanguage()
  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const upstreamNames = new Map(sync.data.upstreams.map((upstream) => [upstream.id, upstream.name]))
    return [
      { title: t("common.none"), value: "", description: t("proxy.worker.fallbackPoolDisabled"), category: props.worker.upstream_pool ? t("common.options") : t("common.current") },
      ...sync.data.upstreamPools.map((pool) => ({
        title: pool.name,
        value: pool.id,
        description: pool.upstreams.map((upstream) => upstreamNames.get(upstream)!).join(" -> "),
        details: pool.active_upstream ? [t("proxy.pool.activeUpstream", { upstream: upstreamNames.get(pool.active_upstream)! })] : undefined,
        category: pool.id === props.worker.upstream_pool ? t("common.current") : t("proxy.dashboard.pools"),
      })),
    ]
  })

  return (
    <DialogSelect
      title={`${t("proxy.worker.fallbackPool")}: ${props.worker.name}`}
      options={options()}
      placeholder={t("proxy.pool.select")}
      current={props.worker.upstream_pool ?? ""}
      onSelect={async (option) => {
        if (option.value === (props.worker.upstream_pool ?? "")) {
          dialog.pop()
          return
        }
        const pool = option.value ? sync.data.upstreamPools.find((item) => item.id === option.value)! : undefined
        try {
          if (!pool) {
            await sdk.client.patchWorker(props.worker.id, { upstream_pool: "" })
          } else {
            await sdk.client.patchWorker(props.worker.id, {
              upstream_pool: pool.id,
              upstream_id: pool.active_upstream || pool.upstreams[0]!,
            })
          }
          await sync.bootstrap({ fatal: false })
          toast.show({ message: t("proxy.worker.fallbackPoolSaved", { name: props.worker.name, pool: pool?.name ?? t("common.none") }), variant: "success" })
          dialog.pop()
        } catch (error) {
          toast.error(error)
        }
      }}
    />
  )
}
