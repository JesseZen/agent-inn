import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useDialog } from "../ui/dialog"
import { useToast } from "../ui/toast"
import { createMemo } from "solid-js"
import { useLanguage } from "../context/language"
import type { Translate } from "../i18n/en"

type Launcher = "codex" | "claudecode" | "grok" | "opencode" | "pi"

export async function showNewWorkerDialog(dialog: ReturnType<typeof import("../ui/dialog").useDialog>, sdk: ReturnType<typeof import("../context/sdk").useSDK>["client"], toast: ReturnType<typeof import("../ui/toast").useToast>, t: Translate) {
  const name = await DialogPrompt.show(dialog, t("proxy.worker.name"), { placeholder: t("proxy.worker.namePlaceholder") })
  if (!name) return

  dialog.replace(() => <LauncherStep name={name} />)
}

function LauncherStep(props: { name: string }) {
  const dialog = useDialog()
  const { t } = useLanguage()
  const options: DialogSelectOption<Launcher>[] = [
    {
      title: "Codex CLI",
      value: "codex",
      description: t("proxy.worker.codexDescription"),
    },
    {
      title: "Claude Code",
      value: "claudecode",
      description: t("proxy.worker.claudeDescription"),
    },
    {
      title: "Grok Build",
      value: "grok",
      description: t("proxy.worker.grokDescription"),
    },
    {
      title: "OpenCode",
      value: "opencode",
      description: t("proxy.worker.opencodeDescription"),
    },
    {
      title: "Pi",
      value: "pi",
      description: t("proxy.worker.piDescription"),
    },
  ]

  return (
    <DialogSelect
      title={t("proxy.worker.selectLauncher", { name: props.name })}
      options={options}
      placeholder={t("proxy.worker.searchLaunchers")}
      onSelect={async (opt) => {
        dialog.replace(() => <UpstreamStep name={props.name} launcher={opt.value} />)
      }}
    />
  )
}

function UpstreamStep(props: { name: string; launcher: Launcher }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const { t } = useLanguage()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.upstreams.map((p) => ({
      title: p.name,
      value: p.id,
      description: `${p.base_url}${p.has_api_key ? "" : ` ${t("proxy.upstream.noKey")}`}`,
    })),
  )

  return (
    <DialogSelect
      title={t("proxy.worker.selectUpstream", { name: props.name })}
      options={options()}
      placeholder={t("proxy.upstream.search")}
      onSelect={async (opt) => {
        try {
          await sdk.client.createWorker({ name: props.name, upstream: opt.value, launcher: props.launcher })
          toast.show({ message: t("proxy.worker.created", { name: props.name }), variant: "success" })
        } catch (err) {
          toast.error(err)
        }
        dialog.clear()
      }}
    />
  )
}
