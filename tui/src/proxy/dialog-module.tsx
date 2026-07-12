import { createMemo, createSignal } from "solid-js"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog, EscHint } from "../ui/dialog"
import { useSDK, type WorkerDetail, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import { useLanguage } from "../context/language"
import type { TranslationKey, Translate } from "../i18n/en"

type ModuleKey = "enabled" | "model" | "api_format" | "config_path" | "state_dir" | "blocked_tools"

type ModuleField = {
  key: ModuleKey
  title: TranslationKey
  placeholder: string
}

const MODULE_FIELDS: Record<string, ModuleField[]> = {
  model_override: [{ key: "model", title: "proxy.module.model", placeholder: "gpt-4o" }],
  api_translate: [{ key: "api_format", title: "proxy.module.apiFormat", placeholder: "responses or chat_completions" }],
  config_patch: [
    { key: "config_path", title: "proxy.module.configPath", placeholder: "~/.codex/config.toml" },
    { key: "state_dir", title: "proxy.module.stateDir", placeholder: "~/.ainn" },
  ],
}

const TOOL_FILTER_TOOLS = [
  { value: "image_generation", descriptionKey: "proxy.module.imageGeneration" },
  { value: "web_search_preview", descriptionKey: "proxy.module.webSearch" },
  { value: "file_search", descriptionKey: "proxy.module.fileSearch" },
  { value: "code_interpreter", descriptionKey: "proxy.module.codeInterpreter" },
  { value: "computer_use_preview", descriptionKey: "proxy.module.computerUse" },
  { value: "function", descriptionKey: "proxy.module.functionTools" },
] as const satisfies Array<{ value: string; descriptionKey: TranslationKey }>

export function DialogModulePicker(props: { worker: WorkerSummary }) {
  const sdk = useSDK()
  const sync = useSync()
  const dialog = useDialog()
  const { t } = useLanguage()
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
        description: availabilityDescription(props.worker, name, cfg.params ?? {}, t),
        category,
        onSelect: async () => {
          if (!available && !configured) return
          const worker = await sdk.client.getWorker(props.worker.id)
          dialog.push(() => <DialogModuleEditor worker={worker} moduleName={name} available={available} />)
        },
      }
    }
    return [
      ...configuredRequestModules.map(([name, cfg]) => moduleOption(name, cfg, t("proxy.module.requestMiddleware"), true)),
      ...availableRequestModules.map(([name, cfg]) => moduleOption(name, cfg, t("proxy.module.requestMiddleware"), false)),
      ...hooks.map(([name, cfg]) => moduleOption(name, cfg, t("proxy.module.lifecycleHooks"), props.worker.hooks?.[name] !== undefined)),
    ]
  })

  return (
    <DialogSelect
      title={t("proxy.module.title", { name: props.worker.name, port: props.worker.port })}
      options={options()}
      placeholder={t("proxy.module.search")}
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
  const { t } = useLanguage()

  const options = createMemo<DialogSelectOption<ModuleKey>[]>(() => {
    const cfg = draft()
    if (!props.available) {
      if (cfg.enabled) {
        return [
          {
            title: t("proxy.module.disable"),
            value: "enabled",
            description: t("proxy.module.unavailableDescription"),
            category: t("proxy.module.categoryActions"),
            onSelect: async () => {
              await saveModule({ enabled: false, params: cfg.params })
            },
          },
        ]
      }
      return [
        {
          title: t("proxy.module.unavailable"),
          value: "enabled",
          description: t("proxy.module.disabledDescription"),
          category: t("proxy.module.categoryStatus"),
          onSelect: async () => {},
        },
      ]
    }
    const fields = MODULE_FIELDS[props.moduleName] ?? []
    const toolFields: DialogSelectOption<ModuleKey>[] =
      props.moduleName === "tool_filter"
        ? [
            {
              title: t("proxy.module.blockedTools"),
              value: "blocked_tools",
              description: describeBlockedTools(cfg.params ?? {}),
              category: t("proxy.module.categoryFields"),
              onSelect: async () => {
                dialog.push(() => (
                  <DialogSelect
                    title={t("proxy.module.blockedToolsTitle", { name: props.worker.name })}
                    options={toolFilterToolOptions(draft().params ?? {}, t, async (tool) => {
                      const params = {
                        ...(draft().params ?? {}),
                        blocked_tools: toggledBlockedTools(draft().params ?? {}, tool),
                      }
                      await patchModule({ enabled: draft().enabled, params })
                    })}
                    placeholder={t("proxy.module.selectTool")}
                    footer={<EscHint dialog={dialog} />}
                  />
                ))
              },
            },
          ]
        : []
    return [
      {
        title: cfg.enabled ? t("proxy.module.disable") : t("proxy.module.enable"),
        value: "enabled",
        description: cfg.enabled ? t("common.enabled") : t("common.disabled"),
        category: t("proxy.module.categoryActions"),
        onSelect: async () => {
          await saveModule({ enabled: !cfg.enabled, params: cfg.params })
        },
      },
      ...toolFields,
      ...fields.map((field) => ({
        title: t(field.title),
        value: field.key,
        description: describeField(cfg.params ?? {}, field.key),
        category: t("proxy.module.categoryFields"),
        onSelect: async () => {
          const next = await DialogPrompt.show(dialog, t("proxy.module.fieldTitle", { field: t(field.title), module: props.moduleName }), {
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
      const result = await sdk.client.patchModule(props.worker.id, props.moduleName, next)
      setDraft({
        enabled: result.module.enabled,
        params: result.module.params,
      })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: t("proxy.module.saved", { name: props.moduleName }), variant: "success" })
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
      title={t("proxy.module.editTitle", { name: props.worker.name })}
      options={options()}
      placeholder={t("proxy.module.selectField")}
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

function availabilityDescription(worker: WorkerSummary, name: string, params: Record<string, unknown>, t: Translate) {
  const base = describeModule(name, params)
  return moduleAvailable(worker, name) ? base : t("proxy.module.unavailableSuffix", { description: base })
}

function describeField(params: Record<string, unknown>, key: string) {
  const value = params[key]
  return value === undefined || value === "" ? "—" : String(value)
}

function toolFilterToolOptions(
  params: Record<string, unknown>,
  t: Translate,
  onToggle: (tool: string) => Promise<void>,
): DialogSelectOption<string>[] {
  const blocked = blockedToolList(params)
  const known = new Set<string>(TOOL_FILTER_TOOLS.map((tool) => tool.value))
  const custom: Array<{ value: string; descriptionKey: TranslationKey }> = blocked
    .filter((tool) => !known.has(tool))
    .map((tool) => ({ value: tool, descriptionKey: "proxy.module.customTool" }))
  return [...TOOL_FILTER_TOOLS, ...custom].map((tool) => {
    const selected = blocked.includes(tool.value)
    return {
      title: `${selected ? "✓" : "○"} ${tool.value}`,
      value: tool.value,
      description: selected ? t("proxy.module.filtered", { description: t(tool.descriptionKey) }) : t("proxy.module.allowed", { description: t(tool.descriptionKey) }),
      category: t("proxy.module.categoryTools"),
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
  const knownOrder: string[] = TOOL_FILTER_TOOLS.map((item) => item.value)
  return [...knownOrder.filter((name) => selected.has(name)), ...current.filter((name) => selected.has(name) && !knownOrder.includes(name))]
}

function blockedToolList(params: Record<string, unknown>) {
  const value = params.blocked_tools
  if (!Array.isArray(value)) return []
  return value.filter((tool): tool is string => typeof tool === "string" && tool.trim() !== "").map((tool) => tool.trim())
}
