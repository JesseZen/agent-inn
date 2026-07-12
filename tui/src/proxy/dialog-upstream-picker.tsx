import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"
import { UpstreamStatusFooter } from "./dialog-upstream"
import { useLanguage } from "../context/language"

export function DialogUpstreamPicker(props: { worker: WorkerSummary }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { t } = useLanguage()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.upstreams.filter((upstream) => {
      if (!props.worker.upstream_pool) return true
      return upstream.id === props.worker.upstream_id || upstream.pool_readiness?.some((item) => item.pool === props.worker.upstream_pool)
    }).map((p) => {
      const probe = sync.data.upstreamProbes[p.id]
      const readiness = p.pool_readiness?.find((item) => item.pool === props.worker.upstream_pool)
      return {
        title: p.name,
        value: p.id,
        description: `${p.base_url ?? ""}${p.has_api_key ? "" : ` ${t("proxy.upstream.noKey")}`}`,
        category: p.id === props.worker.upstream_id ? t("common.current") : props.worker.upstream_pool && !readiness?.eligible ? t("common.unavailable") : t("proxy.upstream.available"),
        footer: <UpstreamStatusFooter upstream={p} probe={probe} pool={props.worker.upstream_pool} />,
      }
    }),
  )

  return (
    <DialogSelect
      title={t("proxy.worker.switchUpstreamTitle", { name: props.worker.name })}
      options={options()}
      placeholder={t("proxy.upstream.search")}
      current={props.worker.upstream_id}
      onSelect={async (opt) => {
        if (opt.value === props.worker.upstream_id) {
          dialog.pop()
          return
        }
        if (props.worker.upstream_pool) {
          const target = sync.data.upstreams.find((item) => item.id === opt.value)
          const readiness = target?.pool_readiness?.find((item) => item.pool === props.worker.upstream_pool)
          if (!readiness?.eligible) {
            toast.show({ message: t("proxy.upstream.targetIneligible"), variant: "error" })
            return
          }
        }
        try {
          await sdk.client.patchWorker(props.worker.id, { upstream_id: opt.value })
          await sync.bootstrap({ fatal: false })
          toast.show({ message: t("proxy.worker.switchedUpstream", { name: props.worker.name, upstream: opt.value }), variant: "success" })
          dialog.pop()
        } catch (err) {
          toast.error(err)
        }
      }}
    />
  )
}
