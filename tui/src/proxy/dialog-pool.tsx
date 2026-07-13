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

type PoolOption = { type: "create" } | { type: "edit"; id: string }
type PoolField = "failure_threshold" | "recovery_success_threshold" | "recovery_wait_seconds"
type ProbeField = "stable_interval_seconds" | "alert_interval_seconds"
type PoolPatch = Partial<Pick<UpstreamPool, "mode" | "probe" | "upstreams" | "circuit_breaker">>

const MINIMUM_ALERT_INTERVAL_SECONDS = 60

const POOL_FIELDS: Array<{ key: PoolField; title: string }> = [
  { key: "failure_threshold", title: "Failure Threshold" },
  { key: "recovery_success_threshold", title: "Recovery Successes" },
  { key: "recovery_wait_seconds", title: "Recovery Wait (seconds)" },
]

const PROBE_FIELDS: Array<{ key: ProbeField; title: string }> = [
  { key: "stable_interval_seconds", title: "Stable Interval" },
  { key: "alert_interval_seconds", title: "Alert Interval" },
]

export function DialogPool() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const options = createMemo<DialogSelectOption<PoolOption>[]>(() => [
    { title: "Create New Pool", value: { type: "create" }, description: "Add an ordered fallback route", category: "Actions" },
    ...sync.data.upstreamPools.map((pool) => ({
      title: pool.name,
      value: { type: "edit" as const, id: pool.id },
      description: pool.upstreams.join(" -> "),
      details: [`active: ${pool.active_upstream || "none"} • ${pool.workers.length} workers • ${poolStatus(pool)}`],
      category: "Configured pools",
    })),
  ])

  return (
    <DialogSelect
      title="Manage Pools"
      options={options()}
      placeholder="Search pools..."
      onSelect={async (option) => {
        if (option.value.type === "edit") {
          const id = option.value.id
          dialog.push(() => <DialogPoolEditor id={id} />)
          return
        }
        const value = await DialogPrompt.show(dialog, "New Pool Name", { placeholder: "e.g. codex-ha" })
        if (value === null) return
        const name = value.trim()
        if (!name || name.includes("/")) {
          toast.show({ message: "Invalid pool name", variant: "error" })
          return
        }
        const first = await selectUpstream(dialog, "First Pool Member", sync.data.upstreams.map((item) => item.id))
        if (!first) return
        try {
          await sdk.client.createUpstreamPool({ name, upstreams: [first] })
          await sync.bootstrap({ fatal: false })
          toast.show({ message: `Created pool ${name}`, variant: "success" })
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
      { title: "Mode", value: "status-mode", description: current.mode, category: "Status" },
      { title: "Probe State", value: "probe-state", description: current.probe_state, category: "Status" },
      {
        title: "Next Probe",
        value: "next-probe",
        description: current.next_probe_at
          ? Locale.datetime(Date.parse(current.next_probe_at))
          : current.mode === "disabled" ? "paused" : "none",
        category: "Status",
      },
    ]
    const mode: DialogSelectOption<string> = {
      title: "Automatic Failover",
      value: "mode",
      description: current.mode,
      category: "Mode",
      onSelect: () => dialog.push(() => (
        <DialogSelect
          title={`Automatic Failover: ${current.name}`}
          options={[
            { title: "Active", value: "active" as const },
            { title: "Disabled", value: "disabled" as const },
          ].map((option) => ({
            ...option,
            onSelect: async () => {
              const saved = await patchPool({ mode: option.value }, `Saved ${current.name}`)
              if (saved) dialog.pop()
            },
          }))}
          current={current.mode}
          placeholder="Select automatic failover mode..."
        />
      )),
    }
    const probeFields: DialogSelectOption<string>[] = PROBE_FIELDS.map((field) => ({
      title: field.title,
      value: `probe:${field.key}`,
      description: `${current.probe[field.key]} seconds`,
      category: "Probe Policy",
      onSelect: async () => {
        const value = await DialogPrompt.show(dialog, `${field.title}: ${current.name}`, {
          value: String(current.probe[field.key]),
          selectAll: true,
        })
        if (value === null) return
        const seconds = Number(value)
        if (!Number.isInteger(seconds) || seconds < 1) {
          toast.show({ message: `${field.title} must be a positive integer`, variant: "error" })
          return
        }
        const probe = { ...current.probe, [field.key]: seconds }
        if (probe.alert_interval_seconds < MINIMUM_ALERT_INTERVAL_SECONDS) {
          toast.show({
            message: `upstream pool "${current.name}" alert_interval_seconds must be at least ${MINIMUM_ALERT_INTERVAL_SECONDS}`,
            variant: "error",
          })
          return
        }
        if (probe.stable_interval_seconds < probe.alert_interval_seconds) {
          toast.show({
            message: `upstream pool "${current.name}" stable_interval_seconds must be greater than or equal to alert_interval_seconds`,
            variant: "error",
          })
          return
        }
        await patchPool({ probe }, `Saved ${current.name}`)
      },
    }))
    const circuitFields: DialogSelectOption<string>[] = POOL_FIELDS.map((field) => ({
      title: field.title,
      value: `field:${field.key}`,
      description: String(current.circuit_breaker[field.key]),
      category: "Circuit Breaker",
      onSelect: async () => {
        const value = await DialogPrompt.show(dialog, `${field.title}: ${current.name}`, {
          value: String(current.circuit_breaker[field.key]),
          selectAll: true,
        })
        if (value === null) return
        const number = Number(value)
        if (!Number.isInteger(number) || number < 1) {
          toast.show({ message: `${field.title} must be a positive integer`, variant: "error" })
          return
        }
        await patchPool({ circuit_breaker: { ...current.circuit_breaker, [field.key]: number } }, `Saved ${current.name}`)
      },
    }))
    const members = current.upstreams.map((upstream, index) => ({
      title: `${index + 1}. ${upstream}`,
      value: `member:${upstream}`,
      description: [
        upstream === current.active_upstream ? "active" : "",
        current.readiness.find((item) => item.upstream === upstream)?.readiness ?? "unknown",
      ].filter(Boolean).join(" • "),
      category: "Members",
      onSelect: () => dialog.push(() => <DialogPoolMember poolID={current.id} upstream={upstream} />),
    }))
    const switchAction: DialogSelectOption<string> = {
      title: "Switch Active Upstream",
      value: "switch",
      description: current.active_upstream ?? "none",
      category: "Actions",
      onSelect: () => dialog.push(() => (
        <DialogSelect
          title={`Switch Active Upstream: ${current.name}`}
          options={current.upstreams.map((upstream) => {
            const readiness = current.readiness.find((item) => item.upstream === upstream)
            return {
              title: upstream,
              value: upstream,
              description: `${readiness?.readiness ?? "unknown"} • ${readiness?.eligible ? "eligible" : "ineligible"}`,
              onSelect: async () => {
                const mode = readiness?.eligible ? "normal" : "force"
                if (mode === "force") {
                  const confirmed = await DialogConfirm.show(
                    dialog,
                    "Force switch",
                    `${upstream} is not eligible. Force ${current.name} to this upstream?`,
                  )
                  if (!confirmed) return
                }
                try {
                  await sdk.client.switchUpstreamPool(current.id, { upstream, mode })
                  await sync.bootstrap({ fatal: false })
                  toast.show({ message: `Switched ${current.name} to ${upstream}`, variant: "success" })
                  dialog.pop()
                } catch (error) {
                  toast.error(error)
                }
              },
            }
          })}
          current={current.active_upstream}
          placeholder="Select a pool member..."
        />
      )),
    }
    return [
      ...status,
      mode,
      ...members,
      { title: "Add Upstream", value: "add", description: "Append a fallback member", category: "Members", onSelect: async () => {
        const available = sync.data.upstreams.map((item) => item.id).filter((id) => !current.upstreams.includes(id))
        const upstream = await selectUpstream(dialog, `Add Member: ${current.name}`, available)
        if (upstream) await patchPool({ upstreams: [...current.upstreams, upstream] }, `Added ${upstream}`)
      } },
      ...probeFields,
      ...circuitFields,
      ...(current.workers.length > 0 ? [switchAction] : []),
      { title: "Refresh Readiness", value: "refresh", description: current.probe_state, category: "Actions", onSelect: async () => {
        try {
          await sdk.client.probeUpstreamPool(current.id)
          await sync.bootstrap({ fatal: false })
          toast.show({ message: `Refreshed ${current.name} readiness`, variant: "success" })
        } catch (error) {
          toast.error(error)
        }
      } },
      { title: "Delete Pool", value: "delete", description: current.name, category: "Actions", onSelect: async () => {
        const confirmed = await DialogConfirm.show(dialog, "Delete pool", `Delete ${current.name}?`)
        if (!confirmed) return
        try {
          await sdk.client.deleteUpstreamPool(current.id)
          await sync.bootstrap({ fatal: false })
          toast.show({ message: `Deleted pool ${current.name}`, variant: "success" })
          dialog.pop()
        } catch (error) {
          toast.error(error)
        }
      } },
    ]
  })

  return <DialogSelect title={`Edit Pool: ${pool()?.name ?? props.id}`} options={options()} placeholder="Select a pool setting..." />
}

function DialogPoolMember(props: { poolID: string; upstream: string }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
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
    if (position > 0) result.push({ title: "Move Up", value: "up", description: `Priority ${position}`, onSelect: () => {
      const next = [...members]
      ;[next[position - 1], next[position]] = [next[position]!, next[position - 1]!]
      void save(next, `Moved ${props.upstream} up`)
    } })
    if (position >= 0 && position < members.length - 1) result.push({ title: "Move Down", value: "down", description: `Priority ${position + 2}`, onSelect: () => {
      const next = [...members]
      ;[next[position], next[position + 1]] = [next[position + 1]!, next[position]!]
      void save(next, `Moved ${props.upstream} down`)
    } })
    result.push({ title: "Remove", value: "remove", description: props.upstream, onSelect: () => void save(members.filter((item) => item !== props.upstream), `Removed ${props.upstream}`) })
    return result
  })
  return <DialogSelect title={`Pool Member: ${props.upstream}`} options={options()} placeholder="Select an action..." />
}

function selectUpstream(dialog: ReturnType<typeof useDialog>, title: string, upstreams: string[]) {
  return new Promise<string | null>((resolve) => {
    dialog.push(
      () => <DialogSelect title={title} options={upstreams.map((id) => ({ title: id, value: id }))} placeholder="Search upstreams..." onSelect={(option) => {
        resolve(option.value)
        dialog.pop()
      }} />,
      () => resolve(null),
    )
  })
}

function poolStatus(pool: UpstreamPool) {
  if (pool.readiness.some((item) => item.readiness === "not_ready" || (item.readiness === "ready" && !item.eligible))) return "degraded"
  if (pool.readiness.length === pool.upstreams.length && pool.readiness.every((item) => item.readiness === "ready" && item.eligible)) return "healthy"
  return "unknown"
}
