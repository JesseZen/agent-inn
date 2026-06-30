import { createMemo, createSignal } from "solid-js"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { EscHint, useDialog } from "../ui/dialog"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import { useTheme } from "../context/theme"

type UpstreamOption = { type: "create" } | { type: "edit"; name: string } | { type: "test-all" }
type FieldKey = "base_url" | "api_key" | "api_format"

export type Draft = {
  base_url: string
  api_key: string
  api_format: string
  has_api_key: boolean
}

type Field = {
  key: FieldKey
  title: string
  placeholder: string
  hidden?: boolean
}

const FIELDS: Field[] = [
  { key: "base_url", title: "Base URL", placeholder: "https://example.com/v1" },
  { key: "api_key", title: "API Key", placeholder: "sk-...", hidden: true },
  { key: "api_format", title: "API Format", placeholder: "responses or chat_completions" },
]

export function DialogUpstream() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { theme } = useTheme()

  const options = createMemo<DialogSelectOption<UpstreamOption>[]>(() => [
    { title: "Create New Upstream", value: { type: "create" }, description: "Add a relay endpoint", category: "Actions" },
    { title: "Test All Upstreams", value: { type: "test-all" as const }, description: "Probe every configured upstream", category: "Actions" },
    ...sync.data.upstreams.map((upstream) => {
      const probe = sync.data.upstreamProbes[upstream.name]
      return {
        title: upstream.name,
        value: { type: "edit" as const, name: upstream.name },
        description: `${upstream.base_url}${upstream.has_api_key ? "" : " (no key)"}`,
        category: "Configured upstreams",
        footer: !probe ? <span style={{ fg: theme.textMuted }}>—</span>
          : probe.ok ? <span style={{ fg: theme.success }}>●{probe.latency_ms}ms</span>
          : probe.degraded ? <span style={{ fg: theme.warning }}>▲{probe.error || `${probe.latency_ms}ms`}</span>
          : <span style={{ fg: theme.error }}>✕{probe.error || probe.status_code}</span>,
      }
    }),
  ])

  return (
    <DialogSelect
      title="Manage Upstreams"
      options={options()}
      placeholder="Search upstreams..."
      onSelect={async (opt) => {
        if (opt.value.type === "create") {
          const name = await DialogPrompt.show(dialog, "New Upstream Name", { placeholder: "e.g. groq" })
          if (name === null) return
          const upstreamName = name.trim()
          if (!upstreamName || upstreamName.includes("/")) {
            toast.show({ message: "Invalid upstream name", variant: "error" })
            dialog.clear()
            return
          }
          dialog.push(() => <DialogUpstreamEditor name={upstreamName} draft={{ base_url: "", api_key: "", api_format: "chat_completions", has_api_key: false }} mode="created" />)
          return
        }

        if (opt.value.type === "test-all") {
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

        const upstream = sync.data.upstreams.find((item) => item.name === opt.value.name)
        if (!upstream) return
        dialog.push(() => (
          <DialogUpstreamEditor
            name={upstream.name}
            draft={{
              base_url: upstream.base_url,
              api_key: "",
              api_format: upstream.api_format ?? "",
              has_api_key: upstream.has_api_key,
            }}
            mode="saved"
          />
        ))
      }}
    />
  )
}

export function DialogUpstreamEditor(props: { name: string; draft: Draft; mode: "created" | "saved" }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const [draft, setDraft] = createSignal(props.draft)

  const options = createMemo<DialogSelectOption<FieldKey>[]>(() =>
    FIELDS.map((field) => ({
      title: field.title,
      value: field.key,
      description: describe(field, draft()),
      category: "Fields",
      onSelect: async () => {
        const patch = await editField(dialog, field, draft())
        if (!patch) return
        const updated = { ...draft(), ...patch, has_api_key: patch.api_key === undefined ? draft().has_api_key : patch.api_key !== "" }
        setDraft(updated)
        await sdk.client.patchUpstream(props.name, patch)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `${props.mode === "created" ? "Created" : "Saved"} upstream ${props.name}`, variant: "success" })
      },
    })),
  )
  const deleteAction: DialogSelectOption<string> = {
    title: "Delete Upstream",
    value: "delete",
    description: props.name,
    onSelect: async () => {
      const confirmed = await DialogConfirm.show(dialog, "Delete upstream", `Delete ${props.name}? This will remove the provider config.`)
      if (!confirmed) {
        dialog.clear()
        return
      }
      try {
        await sdk.client.deleteUpstream(props.name)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Deleted upstream ${props.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }
  const testAction: DialogSelectOption<string> = {
    title: "Test Upstream",
    value: "test",
    description: "Probe reachability and auth",
    onSelect: async () => {
      try {
        const result = await sdk.client.testUpstream(props.name)
        sync.set("upstreamProbes", result.upstream, result)
        const msg = result.ok
          ? `${props.name}: OK ${result.latency_ms}ms`
          : `${props.name}: FAIL ${result.error || result.status_code}`
        toast.show({ message: msg, variant: result.ok ? "success" : "error" })
      } catch (err) {
        toast.error(err)
      }
    },
  }

  return <DialogSelect title={`Edit Upstream: ${props.name}`} options={[...options(), testAction, deleteAction]} placeholder="Select a field..." footer={<EscHint dialog={dialog} />} />
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

  const result = await DialogPrompt.show(dialog, `${field.title}: ${draft.base_url || "upstream"}`, {
    value: draft[field.key],
    placeholder: field.placeholder,
  })
  if (result === null) return
  return { [field.key]: result } as Partial<Draft>
}
