import { createMemo, createSignal } from "solid-js"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog, EscHint } from "../ui/dialog"
import { useSDK, type WorkerDetail, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"

type ModuleKey = "enabled" | "model" | "api_format" | "config_path" | "state_dir" | "blocked_tools"

type ModuleField = {
  key: ModuleKey
  title: string
  placeholder: string
}

const MODULE_FIELDS: Record<string, ModuleField[]> = {
  model_override: [{ key: "model", title: "Model", placeholder: "gpt-4o" }],
  api_translate: [{ key: "api_format", title: "API Format", placeholder: "responses or chat_completions" }],
  config_patch: [
    { key: "config_path", title: "Config Path", placeholder: "~/.codex/config.toml" },
    { key: "state_dir", title: "State Dir", placeholder: "~/.ainn" },
  ],
}

const TOOL_FILTER_TOOLS = [
  { value: "image_generation", description: "OpenAI image generation" },
  { value: "web_search_preview", description: "OpenAI web search" },
  { value: "file_search", description: "OpenAI file search" },
  { value: "code_interpreter", description: "OpenAI code interpreter" },
  { value: "computer_use_preview", description: "OpenAI computer use" },
  { value: "function", description: "Function tools" },
]

export function DialogModulePicker(props: { worker: WorkerSummary }) {
  const sdk = useSDK()
  const sync = useSync()
  const dialog = useDialog()
  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const plugins = sync.data.manager_config.plugins ?? {}
    const configuredRequestModules = Object.entries(props.worker.modules ?? {}).filter(
      ([name]) => plugins[name]?.kind === "request_middleware",
    )
    const configuredRequestNames = new Set(configuredRequestModules.map(([name]) => name))
    const availableRequestModules = Object.entries(plugins)
      .filter(([name, definition]) => definition.kind === "request_middleware" && !configuredRequestNames.has(name))
      .map(([name]) => [name, { enabled: false }] as const)
    const hooks = [
      ...Object.entries(props.worker.hooks ?? {}).filter(([name]) => plugins[name]?.kind === "lifecycle_hook"),
      ...Object.entries(plugins)
        .filter(([name, definition]) => definition.kind === "lifecycle_hook" && props.worker.hooks?.[name] === undefined)
        .map(([name]) => [name, { enabled: false }] as const),
    ]
    const moduleOption = (
      name: string,
      cfg: { enabled: boolean; params?: Record<string, unknown> },
      category: string,
      configured: boolean,
    ) => {
      const available = moduleAvailable(props.worker, name)
      return {
        title: `${cfg.enabled ? "✓" : "○"} ${name}`,
        value: name,
        description: availabilityDescription(props.worker, name, cfg.params ?? {}),
        category,
        onSelect: async () => {
          if (!available && !configured) return
          const worker = await sdk.client.getWorker(props.worker.port)
          dialog.push(() => <DialogModuleEditor worker={worker} moduleName={name} available={available} />)
        },
      }
    }
    return [
      ...configuredRequestModules.map(([name, cfg]) => moduleOption(name, cfg, "Request Middleware", true)),
      ...availableRequestModules.map(([name, cfg]) => moduleOption(name, cfg, "Request Middleware", false)),
      ...hooks.map(([name, cfg]) => moduleOption(name, cfg, "Lifecycle Hooks", props.worker.hooks?.[name] !== undefined)),
    ]
  })

  return (
    <DialogSelect
      title={`Modules & Hooks: ${props.worker.name} (:${props.worker.port})`}
      options={options()}
      placeholder="Search modules..."
      footer={<EscHint dialog={dialog} />}
    />
  )
}

function DialogModuleEditor(props: { worker: WorkerDetail; moduleName: string; available: boolean }) {
  const dialog = useDialog()
  const sdk = useSDK()
  const sync = useSync()
  const toast = useToast()
  const [draft, setDraft] = createSignal(props.worker.modules?.[props.moduleName] ?? props.worker.hooks?.[props.moduleName] ?? { enabled: false })

  const options = createMemo<DialogSelectOption<ModuleKey>[]>(() => {
    const cfg = draft()
    if (!props.available) {
      if (cfg.enabled) {
        return [
          {
            title: "Disable",
            value: "enabled",
            description: "unavailable for current protocol",
            category: "Actions",
            onSelect: async () => {
              await saveModule({ enabled: false, params: cfg.params })
            },
          },
        ]
      }
      return [
        {
          title: "Unavailable",
          value: "enabled",
          description: "disabled for current protocol",
          category: "Status",
          onSelect: async () => {},
        },
      ]
    }
    const fields = MODULE_FIELDS[props.moduleName] ?? []
    const toolFields: DialogSelectOption<ModuleKey>[] =
      props.moduleName === "tool_filter"
        ? [
            {
              title: "Blocked Tools",
              value: "blocked_tools",
              description: describeBlockedTools(cfg.params ?? {}),
              category: "Fields",
              onSelect: async () => {
                dialog.push(() => (
                  <DialogSelect
                    title={`Blocked Tools: ${props.worker.name}`}
                    options={toolFilterToolOptions(draft().params ?? {}, async (tool) => {
                      const params = {
                        ...(draft().params ?? {}),
                        blocked_tools: toggledBlockedTools(draft().params ?? {}, tool),
                      }
                      await patchModule({ enabled: draft().enabled, params })
                    })}
                    placeholder="Select a tool..."
                    footer={<EscHint dialog={dialog} />}
                  />
                ))
              },
            },
          ]
        : []
    return [
      {
        title: cfg.enabled ? "Disable" : "Enable",
        value: "enabled",
        description: cfg.enabled ? "enabled" : "disabled",
        category: "Actions",
        onSelect: async () => {
          await saveModule({ enabled: !cfg.enabled, params: cfg.params })
        },
      },
      ...toolFields,
      ...fields.map((field) => ({
        title: field.title,
        value: field.key,
        description: describeField(cfg.params ?? {}, field.key),
        category: "Fields",
        onSelect: async () => {
          const next = await DialogPrompt.show(dialog, `${field.title}: ${props.moduleName}`, {
            placeholder: field.placeholder,
          })
          if (next === null) return
          const params = { ...(cfg.params ?? {}), [field.key]: next }
          await saveModule({ enabled: cfg.enabled, params })
        },
      })),
    ]
  })

  async function patchModule(next: { enabled: boolean; params?: Record<string, unknown> }) {
    try {
      const result = await sdk.client.patchModule(props.worker.port, props.moduleName, next)
      setDraft({
        enabled: result.module.enabled,
        params: result.module.params,
      })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: `Saved ${props.moduleName}`, variant: "success" })
      return true
    } catch (err) {
      toast.error(err)
      return false
    }
  }

  async function saveModule(next: { enabled: boolean; params?: Record<string, unknown> }) {
    if (!(await patchModule(next))) return
    dialog.pop()
    dialog.pop()
  }

  return (
    <DialogSelect
      title={`Edit Module: ${props.worker.name}`}
      options={options()}
      placeholder="Select a field..."
      footer={<EscHint dialog={dialog} />}
    />
  )
}

function describeModule(name: string, params: Record<string, unknown>) {
  if (name === "model_override") return String(params.model ?? "—")
  if (name === "api_translate") return String(params.api_format ?? "—")
  if (name === "config_patch") return [String(params.config_path ?? "—"), String(params.state_dir ?? "—")].join(" • ")
  if (name === "tool_filter") return describeBlockedTools(params)
  return "—"
}

function moduleAvailable(worker: WorkerSummary, name: string) {
  const protocol = worker.protocol
  const support = worker.module_support?.[name]
  if (!protocol || !support?.protocols) return false
  return support.protocols.includes(protocol)
}

function availabilityDescription(worker: WorkerSummary, name: string, params: Record<string, unknown>) {
  const base = describeModule(name, params)
  return moduleAvailable(worker, name) ? base : `${base} • unavailable`
}

function describeField(params: Record<string, unknown>, key: string) {
  const value = params[key]
  return value === undefined || value === "" ? "—" : String(value)
}

function toolFilterToolOptions(
  params: Record<string, unknown>,
  onToggle: (tool: string) => Promise<void>,
): DialogSelectOption<string>[] {
  const blocked = blockedToolList(params)
  const known = new Set(TOOL_FILTER_TOOLS.map((tool) => tool.value))
  const custom = blocked.filter((tool) => !known.has(tool)).map((tool) => ({ value: tool, description: "Custom tool" }))
  return [...TOOL_FILTER_TOOLS, ...custom].map((tool) => {
    const selected = blocked.includes(tool.value)
    return {
      title: `${selected ? "✓" : "○"} ${tool.value}`,
      value: tool.value,
      description: selected ? `filtered • ${tool.description}` : `allowed • ${tool.description}`,
      category: "Tools",
      onSelect: async () => {
        await onToggle(tool.value)
      },
    }
  })
}

function describeBlockedTools(params: Record<string, unknown>) {
  const blocked = blockedToolList(params)
  return blocked.length === 0 ? "—" : blocked.join(", ")
}

function toggledBlockedTools(params: Record<string, unknown>, tool: string) {
  const current = blockedToolList(params)
  const selected = new Set(current)
  if (selected.has(tool)) {
    selected.delete(tool)
  } else {
    selected.add(tool)
  }
  const knownOrder = TOOL_FILTER_TOOLS.map((item) => item.value)
  return [...knownOrder.filter((name) => selected.has(name)), ...current.filter((name) => selected.has(name) && !knownOrder.includes(name))]
}

function blockedToolList(params: Record<string, unknown>) {
  const value = params.blocked_tools
  if (!Array.isArray(value)) return []
  return value.filter((tool): tool is string => typeof tool === "string" && tool.trim() !== "").map((tool) => tool.trim())
}
