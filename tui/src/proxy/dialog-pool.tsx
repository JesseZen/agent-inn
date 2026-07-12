import { createMemo } from "solid-js"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import type { UpstreamPool } from "./backend"

type PoolOption = { type: "create" } | { type: "edit"; id: string }
type PoolField = "failure_threshold" | "recovery_success_threshold" | "recovery_wait_seconds"

const POOL_FIELDS: Array<{ key: PoolField; title: string }> = [
  { key: "failure_threshold", title: "Failure Threshold" },
  { key: "recovery_success_threshold", title: "Recovery Successes" },
  { key: "recovery_wait_seconds", title: "Recovery Wait (seconds)" },
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

  async function patchPool(patch: Partial<Pick<UpstreamPool, "upstreams" | "circuit_breaker">>, message: string) {
    try {
      await sdk.client.patchUpstreamPool(props.id, patch)
      await sync.bootstrap({ fatal: false })
      toast.show({ message, variant: "success" })
    } catch (error) {
      toast.error(error)
    }
  }

  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const current = pool()
    if (!current) return []
    const fields: DialogSelectOption<string>[] = POOL_FIELDS.map((field) => ({
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
      description: upstream === current.active_upstream ? "active" : "",
      category: "Members",
      onSelect: () => dialog.push(() => <DialogPoolMember poolID={current.id} upstream={upstream} />),
    }))
    return [
      ...members,
      { title: "Add Upstream", value: "add", description: "Append a fallback member", category: "Members", onSelect: async () => {
        const available = sync.data.upstreams.map((item) => item.id).filter((id) => !current.upstreams.includes(id))
        const upstream = await selectUpstream(dialog, `Add Member: ${current.name}`, available)
        if (upstream) await patchPool({ upstreams: [...current.upstreams, upstream] }, `Added ${upstream}`)
      } },
      ...fields,
      { title: "Test Pool", value: "test", description: "Probe every pool member", category: "Actions", onSelect: async () => {
        try {
          const results = await Promise.all(current.upstreams.map((name) => sdk.client.testUpstream(name)))
          for (const result of results) sync.set("upstreamProbes", result.upstream, result)
          const ready = results.filter((result) => result.ok).length
          toast.show({ message: `${current.name}: ${ready}/${results.length} members ready`, variant: ready === results.length ? "success" : "error" })
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
