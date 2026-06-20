import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { DialogPrompt } from "../ui/dialog-prompt"

type UpstreamOption = { type: "create" } | { type: "edit"; name: string }

export function DialogUpstream() {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<UpstreamOption>[]>(() => [
    {
      title: "Create New Upstream",
      value: { type: "create" },
      description: "Add a relay endpoint",
      category: "Actions",
    },
    ...sync.data.providers.map((upstream) => ({
      title: upstream.name,
      value: { type: "edit" as const, name: upstream.name },
      description: `${upstream.base_url}${upstream.has_api_key ? "" : " (no key)"}`,
      category: "Configured upstreams",
    })),
  ])

  async function saveUpstream(input: {
    name: string
    baseURL: string
    apiKeyRef?: string
    apiFormat?: string
    mode: "created" | "saved"
  }) {
    try {
      await sdk.client.putProvider(input.name, {
        base_url: input.baseURL,
        api_key_ref: input.apiKeyRef?.trim() || undefined,
        api_format: input.apiFormat?.trim() || undefined,
      })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: `${input.mode === "created" ? "Created" : "Saved"} upstream ${input.name}`, variant: "success" })
    } catch (err) {
      toast.error(err)
    }
    dialog.clear()
  }

  return (
    <DialogSelect
      title="Manage Upstreams"
      options={options()}
      placeholder="Search upstreams..."
      onSelect={async (opt) => {
        if (opt.value.type === "create") {
          const name = await DialogPrompt.show(dialog, "New Upstream Name", {
            placeholder: "e.g. groq",
          })
          if (name === null) return
          const upstreamName = name.trim()
          if (!upstreamName || upstreamName.includes("/")) {
            toast.show({ message: "Invalid upstream name", variant: "error" })
            dialog.clear()
            return
          }

          const baseURL = await DialogPrompt.show(dialog, `Base URL: ${upstreamName}`, {
            placeholder: "https://example.com/v1",
          })
          if (baseURL === null) return

          const apiKeyRef = await DialogPrompt.show(dialog, `API Key Ref: ${upstreamName}`, {
            placeholder: "${OPENAI_API_KEY}",
          })
          if (apiKeyRef === null) return

          const apiFormat = await DialogPrompt.show(dialog, `API Format: ${upstreamName}`, {
            value: "chat_completions",
            placeholder: "responses or chat_completions",
          })
          if (apiFormat === null) return

          await saveUpstream({
            name: upstreamName,
            baseURL,
            apiKeyRef,
            apiFormat,
            mode: "created",
          })
          return
        }

        const upstream = sync.data.providers.find((item) => item.name === opt.value.name)
        if (!upstream) return

        const baseURL = await DialogPrompt.show(dialog, `Base URL: ${upstream.name}`, {
          value: upstream.base_url,
          placeholder: "https://example.com/v1",
        })
        if (baseURL === null) return

        const apiKeyRef = await DialogPrompt.show(dialog, `API Key Ref: ${upstream.name}`, {
          value: upstream.api_key_ref ?? "",
          placeholder: "${OPENAI_API_KEY}",
        })
        if (apiKeyRef === null) return

        const apiFormat = await DialogPrompt.show(dialog, `API Format: ${upstream.name}`, {
          value: upstream.api_format ?? "",
          placeholder: "responses or chat_completions",
        })
        if (apiFormat === null) return

        await saveUpstream({
          name: upstream.name,
          baseURL,
          apiKeyRef,
          apiFormat,
          mode: "saved",
        })
      }}
    />
  )
}
