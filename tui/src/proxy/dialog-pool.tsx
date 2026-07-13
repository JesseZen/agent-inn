import { createMemo } from "solid-js"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { Locale } from "../util/locale"
import type { UpstreamPool } from "./backend"
import { useLanguage } from "../context/language"

type PoolOption = { type: "create" } | { type: "edit"; id: string }
type PoolField = "failure_threshold" | "recovery_success_threshold" | "recovery_wait_seconds"
type ProbeField = "stable_interval_seconds" | "alert_interval_seconds"
type PoolPatch = Partial<Pick<UpstreamPool, "mode" | "probe" | "upstreams" | "circuit_breaker">>

const MINIMUM_ALERT_INTERVAL_SECONDS = 60

const POOL_FIELDS: Array<{ key: PoolField; title: "proxy.pool.failureThreshold" | "proxy.pool.recoverySuccesses" | "proxy.pool.recoveryWait" }> = [
  { key: "failure_threshold", title: "proxy.pool.failureThreshold" },
  { key: "recovery_success_threshold", title: "proxy.pool.recoverySuccesses" },
  { key: "recovery_wait_seconds", title: "proxy.pool.recoveryWait" },
]

const PROBE_FIELDS: Array<{ key: ProbeField; title: "proxy.pool.stableInterval" | "proxy.pool.alertInterval" }> = [
  { key: "stable_interval_seconds", title: "proxy.pool.stableInterval" },
  { key: "alert_interval_seconds", title: "proxy.pool.alertInterval" },
]

export function DialogPool() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { t } = useLanguage()
  const options = createMemo<DialogSelectOption<PoolOption>[]>(() => [
    { title: t("proxy.pool.create"), value: { type: "create" }, description: t("proxy.pool.createDescription"), category: t("common.actions") },
    ...sync.data.upstreamPools.map((pool) => ({
      title: pool.name,
      value: { type: "edit" as const, id: pool.id },
      description: pool.upstreams.join(" -> "),
      details: [t("proxy.pool.summary", { active: pool.active_upstream || t("common.none"), count: pool.workers.length, status: t(poolStatus(pool)) })],
      category: t("proxy.pool.configured"),
    })),
  ])

  return (
    <DialogSelect
      title={t("proxy.pool.manage")}
      options={options()}
      placeholder={t("proxy.pool.search")}
      onSelect={async (option) => {
        if (option.value.type === "edit") {
          const id = option.value.id
          dialog.push(() => <DialogPoolEditor id={id} />)
          return
        }
        const value = await DialogPrompt.show(dialog, t("proxy.pool.newName"), { placeholder: t("proxy.pool.namePlaceholder") })
        if (value === null) return
        const name = value.trim()
        if (!name || name.includes("/")) {
          toast.show({ message: t("proxy.pool.invalidName"), variant: "error" })
          return
        }
        const first = await selectUpstream(dialog, t("proxy.pool.firstMember"), t("proxy.upstream.search"), sync.data.upstreams.map((item) => item.id))
        if (!first) return
        try {
          await sdk.client.createUpstreamPool({ name, upstreams: [first] })
          await sync.bootstrap({ fatal: false })
          toast.show({ message: t("proxy.pool.created", { name }), variant: "success" })
          dialog.push(() => <DialogPoolEditor id={name} />)
        } catch (error) {
          toast.error(error)
        }
      }}
    />
  )
}

export function DialogPoolEditor(props: { id: string }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const language = useLanguage()
  const { t } = language
  const pool = createMemo(() => sync.data.upstreamPools.find((item) => item.id === props.id))

  async function patchPool(patch: PoolPatch, message: string) {
    try {
      await sdk.client.patchUpstreamPool(props.id, patch)
      await sync.bootstrap({ fatal: false })
      toast.show({ message, variant: "success" })
      return true
    } catch (error) {
      toast.error(error)
      return false
    }
  }

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const current = pool()
    if (!current) return []
    const status: DialogSelectOption<string>[] = [
      { title: t("proxy.pool.mode"), value: "status-mode", description: current.mode === "active" ? t("proxy.pool.active") : t("common.disabled"), category: t("proxy.pool.status") },
      { title: t("proxy.pool.probeState"), value: "probe-state", description: current.probe_state, category: t("proxy.pool.status") },
      {
        title: t("proxy.pool.nextProbe"),
        value: "next-probe",
        description: current.next_probe_at
          ? Locale.datetime(Date.parse(current.next_probe_at), language.locale)
          : current.mode === "disabled" ? t("proxy.pool.paused") : t("proxy.pool.none"),
        category: t("proxy.pool.status"),
      },
    ]
    const mode: DialogSelectOption<string> = {
      title: t("proxy.pool.automaticFailover"),
      value: "mode",
      description: current.mode === "active" ? t("proxy.pool.active") : t("common.disabled"),
      category: t("proxy.pool.mode"),
      onSelect: () => dialog.push(() => (
        <DialogSelect
          title={t("proxy.pool.automaticFailoverTitle", { name: current.name })}
          options={[
            { title: t("proxy.pool.modeActive"), value: "active" as const },
            { title: t("common.disabled"), value: "disabled" as const },
          ].map((option) => ({
            ...option,
            onSelect: async () => {
              const saved = await patchPool({ mode: option.value }, t("proxy.pool.saved", { name: current.name }))
              if (saved) dialog.pop()
            },
          }))}
          current={current.mode}
          placeholder={t("proxy.pool.selectFailoverMode")}
        />
      )),
    }
    const probeFields: DialogSelectOption<string>[] = PROBE_FIELDS.map((field) => ({
      title: t(field.title),
      value: `probe:${field.key}`,
      description: t("proxy.pool.seconds", { seconds: current.probe[field.key] }),
      category: t("proxy.pool.probePolicy"),
      onSelect: async () => {
        const value = await DialogPrompt.show(dialog, `${t(field.title)}: ${current.name}`, {
          value: String(current.probe[field.key]),
          selectAll: true,
        })
        if (value === null) return
        const seconds = Number(value)
        if (!Number.isInteger(seconds) || seconds < 1) {
          toast.show({ message: t("proxy.pool.positiveInteger", { field: t(field.title) }), variant: "error" })
          return
        }
        const probe = { ...current.probe, [field.key]: seconds }
        if (probe.alert_interval_seconds < MINIMUM_ALERT_INTERVAL_SECONDS) {
          toast.show({
            message: t("proxy.pool.alertMinimum", { name: current.name, minimum: MINIMUM_ALERT_INTERVAL_SECONDS }),
            variant: "error",
          })
          return
        }
        if (probe.stable_interval_seconds < probe.alert_interval_seconds) {
          toast.show({
            message: t("proxy.pool.stableMinimum", { name: current.name }),
            variant: "error",
          })
          return
        }
        await patchPool({ probe }, t("proxy.pool.saved", { name: current.name }))
      },
    }))
    const circuitFields: DialogSelectOption<string>[] = POOL_FIELDS.map((field) => ({
      title: t(field.title),
      value: `field:${field.key}`,
      description: String(current.circuit_breaker[field.key]),
      category: t("proxy.pool.circuitBreaker"),
      onSelect: async () => {
        const value = await DialogPrompt.show(dialog, `${t(field.title)}: ${current.name}`, {
          value: String(current.circuit_breaker[field.key]),
          selectAll: true,
        })
        if (value === null) return
        const number = Number(value)
        if (!Number.isInteger(number) || number < 1) {
          toast.show({ message: t("proxy.pool.positiveInteger", { field: t(field.title) }), variant: "error" })
          return
        }
        await patchPool({ circuit_breaker: { ...current.circuit_breaker, [field.key]: number } }, t("proxy.pool.saved", { name: current.name }))
      },
    }))
    const members = current.upstreams.map((upstream, index) => ({
      title: `${index + 1}. ${upstream}`,
      value: `member:${upstream}`,
      description: [
        upstream === current.active_upstream ? t("proxy.pool.active") : "",
        current.readiness.find((item) => item.upstream === upstream)?.readiness ?? "unknown",
      ].filter(Boolean).join(" • "),
      category: t("proxy.pool.members"),
      onSelect: () => dialog.push(() => <DialogPoolMember poolID={current.id} upstream={upstream} />),
    }))
    const switchAction: DialogSelectOption<string> = {
      title: t("proxy.pool.switchActive"),
      value: "switch",
      description: current.active_upstream ?? t("common.none"),
      category: t("common.actions"),
      onSelect: () => dialog.push(() => (
        <DialogSelect
          title={t("proxy.pool.switchActiveTitle", { name: current.name })}
          options={current.upstreams.map((upstream) => {
            const readiness = current.readiness.find((item) => item.upstream === upstream)
            return {
              title: upstream,
              value: upstream,
              description: `${readiness?.readiness ?? "unknown"} • ${readiness?.eligible ? t("proxy.pool.eligible") : t("proxy.pool.ineligible")}`,
              onSelect: async () => {
                const mode = readiness?.eligible ? "normal" : "force"
                if (mode === "force") {
                  const confirmed = await DialogConfirm.show(
                    dialog,
                    t("proxy.pool.forceSwitch"),
                    t("proxy.pool.forceSwitchConfirm", { upstream, name: current.name }),
                  )
                  if (!confirmed) return
                }
                try {
                  await sdk.client.switchUpstreamPool(current.id, { upstream, mode })
                  await sync.bootstrap({ fatal: false })
                  toast.show({ message: t("proxy.pool.switched", { name: current.name, upstream }), variant: "success" })
                  dialog.pop()
                } catch (error) {
                  toast.error(error)
                }
              },
            }
          })}
          current={current.active_upstream}
          placeholder={t("proxy.pool.selectMember")}
        />
      )),
    }
    return [
      ...status,
      mode,
      ...members,
      { title: t("proxy.pool.addUpstream"), value: "add", description: t("proxy.pool.addUpstreamDescription"), category: t("proxy.pool.members"), onSelect: async () => {
        const available = sync.data.upstreams.map((item) => item.id).filter((id) => !current.upstreams.includes(id))
        const upstream = await selectUpstream(dialog, t("proxy.pool.addMemberTitle", { name: current.name }), t("proxy.upstream.search"), available)
        if (upstream) await patchPool({ upstreams: [...current.upstreams, upstream] }, t("proxy.pool.memberAdded", { upstream }))
      } },
      ...probeFields,
      ...circuitFields,
      ...(current.workers.length > 0 ? [switchAction] : []),
      { title: t("proxy.pool.refreshReadiness"), value: "refresh", description: current.probe_state, category: t("common.actions"), onSelect: async () => {
        try {
          await sdk.client.probeUpstreamPool(current.id)
          await sync.bootstrap({ fatal: false })
          toast.show({ message: t("proxy.pool.refreshed", { name: current.name }), variant: "success" })
        } catch (error) {
          toast.error(error)
        }
      } },
      { title: t("proxy.pool.delete"), value: "delete", description: current.name, category: t("common.actions"), onSelect: async () => {
        const confirmed = await DialogConfirm.show(dialog, t("proxy.pool.deleteConfirmTitle"), t("proxy.pool.deleteConfirm", { name: current.name }))
        if (!confirmed) return
        try {
          await sdk.client.deleteUpstreamPool(current.id)
          await sync.bootstrap({ fatal: false })
          toast.show({ message: t("proxy.pool.deleted", { name: current.name }), variant: "success" })
          dialog.pop()
        } catch (error) {
          toast.error(error)
        }
      } },
    ]
  })

  return <DialogSelect title={t("proxy.pool.editTitle", { name: pool()?.name ?? props.id })} options={options()} placeholder={t("proxy.pool.selectSetting")} />
}

function DialogPoolMember(props: { poolID: string; upstream: string }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { t } = useLanguage()
  const pool = createMemo(() => sync.data.upstreamPools.find((item) => item.id === props.poolID))
  const index = createMemo(() => pool()?.upstreams.indexOf(props.upstream) ?? -1)

  async function save(upstreams: string[], message: string) {
    try {
      await sdk.client.patchUpstreamPool(props.poolID, { upstreams })
      await sync.bootstrap({ fatal: false })
      toast.show({ message, variant: "success" })
      dialog.pop()
    } catch (error) {
      toast.error(error)
    }
  }

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const members = pool()?.upstreams ?? []
    const position = index()
    const result: DialogSelectOption<string>[] = []
    if (position > 0) result.push({ title: t("proxy.pool.moveUp"), value: "up", description: t("proxy.pool.priority", { priority: position }), onSelect: () => {
      const next = [...members]
      ;[next[position - 1], next[position]] = [next[position]!, next[position - 1]!]
      void save(next, t("proxy.pool.memberMovedUp", { upstream: props.upstream }))
    } })
    if (position >= 0 && position < members.length - 1) result.push({ title: t("proxy.pool.moveDown"), value: "down", description: t("proxy.pool.priority", { priority: position + 2 }), onSelect: () => {
      const next = [...members]
      ;[next[position], next[position + 1]] = [next[position + 1]!, next[position]!]
      void save(next, t("proxy.pool.memberMovedDown", { upstream: props.upstream }))
    } })
    result.push({ title: t("common.remove"), value: "remove", description: props.upstream, onSelect: () => void save(members.filter((item) => item !== props.upstream), t("proxy.pool.removed", { upstream: props.upstream })) })
    return result
  })
  return <DialogSelect title={t("proxy.pool.memberTitle", { upstream: props.upstream })} options={options()} placeholder={t("proxy.pool.selectAction")} />
}

function selectUpstream(dialog: ReturnType<typeof useDialog>, title: string, placeholder: string, upstreams: string[]) {
  return new Promise<string | null>((resolve) => {
    dialog.push(
      () => <DialogSelect title={title} options={upstreams.map((id) => ({ title: id, value: id }))} placeholder={placeholder} onSelect={(option) => {
        resolve(option.value)
        dialog.pop()
      }} />,
      () => resolve(null),
    )
  })
}

function poolStatus(pool: UpstreamPool) {
  if (pool.readiness.some((item) => item.readiness === "not_ready" || (item.readiness === "ready" && !item.eligible))) return "proxy.pool.degraded"
  if (pool.readiness.length === pool.upstreams.length && pool.readiness.every((item) => item.readiness === "ready" && item.eligible)) return "proxy.pool.healthy"
  return "common.unknown"
}
