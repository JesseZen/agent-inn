import { createMemo } from "solid-js"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { EscHint, useDialog } from "../ui/dialog"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import { useTheme } from "../context/theme"
import type { RedactedUpstream, UpstreamProbeResult } from "./backend"
import { DialogPool } from "./dialog-pool"

type UpstreamOption = { type: "create" } | { type: "edit"; id: string } | { type: "test-all" } | { type: "pools" }
type FieldKey = "name" | "base_url" | "api_key" | "api_format" | "protocol_probe_model"

export type Draft = {
  name: string
  base_url: string
  api_key: string
  api_format: string
  has_api_key: boolean
  protocol_probe_model: string
}

type Field = {
  key: FieldKey
  title: string
  placeholder: string
  hidden?: boolean
}

const API_FORMAT_OPTIONS = [
  { title: "responses", value: "responses", description: "OpenAI Responses API" },
  { title: "chat_completions", value: "chat_completions", description: "OpenAI-compatible Chat Completions API" },
  { title: "anthropic", value: "anthropic", description: "Anthropic Messages API" },
  { title: "unset", value: "", description: "Native Responses passthrough" },
]

const FIELDS: Field[] = [
  { key: "name", title: "Name", placeholder: "Display name" },
  { key: "base_url", title: "Base URL", placeholder: "https://example.com/v1" },
  { key: "api_key", title: "API Key", placeholder: "sk-...", hidden: true },
  { key: "api_format", title: "API Format", placeholder: "responses, chat_completions, or anthropic" },
  { key: "protocol_probe_model", title: "Probe model", placeholder: "Model used for protocol readiness" },
]

export function DialogUpstream() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<UpstreamOption>[]>(() => [
    { title: "Create New Upstream", value: { type: "create" }, description: "Add a relay endpoint", category: "Actions" },
    { title: "Test All Upstreams", value: { type: "test-all" as const }, description: "Probe every configured upstream", category: "Actions" },
    { title: "Manage Pools", value: { type: "pools" as const }, description: "Configure ordered fallback routes", category: "Actions" },
    ...sync.data.upstreams.map((upstream) => {
      const probe = sync.data.upstreamProbes[upstream.id]
      return {
        title: upstream.name,
        value: { type: "edit" as const, id: upstream.id },
        description: `${upstream.base_url ?? ""}${upstream.has_api_key ? "" : " (no key)"}`,
        category: "Configured upstreams",
        footer: <UpstreamStatusFooter upstream={upstream} probe={probe} />,
      }
    }),
  ])

  return (
    <DialogSelect
      title="Manage Upstreams"
      options={options()}
      placeholder="Search upstreams..."
      onSelect={async (opt) => {
        const value = opt.value
        if (value.type === "create") {
          const name = await DialogPrompt.show(dialog, "New Upstream Name", { placeholder: "e.g. groq" })
          if (name === null) return
          const upstreamName = name.trim()
          if (!upstreamName || upstreamName.includes("/")) {
            toast.show({ message: "Invalid upstream name", variant: "error" })
            return
          }
          dialog.push(() => <DialogUpstreamEditor id={upstreamName} draft={{ name: upstreamName, base_url: "", api_key: "", api_format: "chat_completions", has_api_key: false, protocol_probe_model: "" }} mode="created" />)
          return
        }

        if (value.type === "test-all") {
          try {
            const results = await sdk.client.testAllUpstreams()
            for (const result of results) {
              sync.set("upstreamProbes", result.upstream, result)
            }
            toast.show({ message: `Tested ${results.length} upstreams`, variant: "success" })
          } catch (err) {
            toast.error(err)
          }
          return
        }

        if (value.type === "pools") {
          dialog.replace(() => <DialogPool />)
          return
        }

        const upstream = sync.data.upstreams.find((item) => item.id === value.id)
        if (!upstream) return
        dialog.push(() => (
          <DialogUpstreamEditor
            id={upstream.id}
            draft={{
              name: upstream.name,
              base_url: upstream.base_url ?? "",
              api_key: "",
              api_format: upstream.api_format ?? "",
              has_api_key: upstream.has_api_key,
              protocol_probe_model: upstream.protocol_probe?.model ?? "",
            }}
            mode="saved"
          />
        ))
      }}
    />
  )
}

export function DialogUpstreamEditor(props: { id: string; draft: Draft; mode: "created" | "saved" }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const draft = createMemo<Draft>(() => {
    const upstream = sync.data.upstreams.find((item) => item.id === props.id)
    if (!upstream) return props.draft
    return {
      name: upstream.name,
      base_url: upstream.base_url ?? "",
      api_key: "",
      api_format: upstream.api_format ?? "",
      has_api_key: upstream.has_api_key,
      protocol_probe_model: upstream.protocol_probe?.model ?? "",
    }
  })

  const options = createMemo<DialogSelectOption<FieldKey>[]>(() =>
    FIELDS.map((field) => ({
      title: field.title,
      value: field.key,
      description: describe(field, draft()),
      category: "Fields",
      onSelect: async () => {
        const patch = await editField(dialog, field, draft())
        if (!patch) return
        await sdk.client.patchUpstream(props.id, patch)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `${props.mode === "created" ? "Created" : "Saved"} upstream ${"name" in patch ? patch.name ?? draft().name : draft().name}`, variant: "success" })
      },
    })),
  )
  const deleteAction: DialogSelectOption<string> = {
    title: "Delete Upstream",
    value: "delete",
    description: draft().name,
    onSelect: async () => {
      const confirmed = await DialogConfirm.show(dialog, "Delete upstream", `Delete ${draft().name}? This will remove the provider config.`)
      if (!confirmed) return
      try {
        await sdk.client.deleteUpstream(props.id)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Deleted upstream ${draft().name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.pop()
    },
  }
  const testAction: DialogSelectOption<string> = {
    title: "Test Upstream",
    value: "test",
    description: "Probe reachability and auth",
    onSelect: async () => {
      try {
        const result = await sdk.client.testUpstream(props.id)
        sync.set("upstreamProbes", result.upstream, result)
        const msg = result.ok
          ? `${draft().name}: OK ${result.latency_ms}ms`
          : `${draft().name}: FAIL ${result.error || result.status_code}`
        toast.show({ message: msg, variant: result.ok ? "success" : "error" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  return <DialogSelect title={`Edit Upstream: ${draft().name}`} options={[...options(), testAction, deleteAction]} placeholder="Select a field..." footer={<EscHint dialog={dialog} />} />
}

function describe(field: Field, draft: Draft) {
  if (field.hidden) return draft.has_api_key ? "******" : "none"
  return draft[field.key] || "—"
}

async function editField(dialog: ReturnType<typeof useDialog>, field: Field, draft: Draft) {
  if (field.hidden) {
    let dirty = false
    let value = draft.api_key
    const result = await DialogPrompt.show(dialog, `${field.title}: ${draft.base_url || "upstream"}`, {
      value: draft.has_api_key ? "******" : "",
      placeholder: field.placeholder,
      onInputChange(next) {
        value = next
        dirty = true
      },
    })
    if (result === null) {
      if (!dirty) return
      const save = await DialogConfirm.show(dialog, "Save API Key", "Save the edited API key?")
      if (save !== true) return
    }
    if (!dirty) return
    return { api_key: value === "******" ? "" : value }
  }

  if (field.key === "api_format") {
    const result = await new Promise<string | null>((resolve) => {
      dialog.push(
        () => (
          <DialogSelect
            title={`${field.title}: ${draft.base_url || "upstream"}`}
            options={API_FORMAT_OPTIONS.map((option) => ({
              ...option,
              category: option.value === draft.api_format ? "Current" : "Options",
            }))}
            placeholder="Select API format..."
            current={draft.api_format}
            onSelect={(opt) => {
              resolve(opt.value)
              dialog.pop()
            }}
          />
        ),
        () => resolve(null),
      )
    })
    if (result === null) return
    return { api_format: result }
  }

  if (field.key === "protocol_probe_model") {
    const result = await DialogPrompt.show(dialog, `${field.title}: ${draft.name || "upstream"}`, {
      value: draft.protocol_probe_model,
      placeholder: field.placeholder,
    })
    if (result === null) return
    return { protocol_probe: { model: result } }
  }

  const promptTarget = field.key === "name" ? draft.name : draft.base_url || "upstream"
  const result = await DialogPrompt.show(dialog, `${field.title}: ${promptTarget}`, {
    value: draft[field.key],
    placeholder: field.placeholder,
  })
  if (result === null) return
  return { [field.key]: result } as Partial<Draft>
}

type StatusKind = "protocol_ok" | "protocol_error" | "reachable" | "unreachable" | "unknown"

function statusForUpstream(upstream: RedactedUpstream, probe?: UpstreamProbeResult, pool?: string): { kind: StatusKind; label: string } {
  const bindings = pool
    ? (upstream.pool_readiness ?? []).filter((item) => item.pool === pool)
    : (upstream.pool_readiness ?? [])
  if (bindings.length > 0) {
    const ready = bindings.filter((item) => item.readiness === "ready" && !item.stale).length
    const count = `${ready}/${bindings.length} pools`
    const failed = bindings.find((item) => item.readiness === "not_ready")
    if (failed) return { kind: "protocol_error", label: `${failed.error || "protocol_error"} ${count}` }
    if (bindings.some((item) => item.readiness !== "ready" || item.stale)) return { kind: "unknown", label: `unknown ${count}` }
    return { kind: "protocol_ok", label: `${bindings[0]?.latency_ms ?? 0}ms ${count}` }
  }
  if (!probe) return { kind: "unknown", label: "" }
  if (probe.mode === "protocol") {
    return probe.ok
      ? { kind: "protocol_ok", label: `${probe.latency_ms}ms` }
      : { kind: "protocol_error", label: probe.error || String(probe.status_code) }
  }
  return probe.status_code > 0
    ? { kind: "reachable", label: `reachable ${probe.latency_ms}ms` }
    : { kind: "unreachable", label: probe.error || "unreachable" }
}

export function UpstreamStatusFooter(props: { upstream: RedactedUpstream; probe?: UpstreamProbeResult; pool?: string }) {
  const { theme } = useTheme()
  const status = statusForUpstream(props.upstream, props.probe, props.pool)
  if (status.kind === "protocol_ok") return <span style={{ fg: theme.success }}>●{status.label}</span>
  if (status.kind === "protocol_error" || status.kind === "unreachable") return <span style={{ fg: theme.error }}>✕{status.label}</span>
  if (status.kind === "reachable") return <span style={{ fg: theme.warning }}>▲{status.label}</span>
  return <span style={{ fg: theme.textMuted }}>—{status.label}</span>
}
