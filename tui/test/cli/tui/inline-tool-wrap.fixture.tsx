/** @jsxImportSource @opentui/solid */
import { createSignal, For, Show } from "solid-js"
import type { ScrollBoxRenderable } from "@opentui/core"
import { testRender, type JSX } from "@opentui/solid"
import { InlineToolRow } from "../../../src/routes/session"

let testSetup: Awaited<ReturnType<typeof testRender>> | undefined

export function destroyInlineToolWrapFixture() {
  testSetup?.renderer.destroy()
  testSetup = undefined
}

type ToolFixture = { icon: string; label: string; error?: string }

const tools: readonly ToolFixture[] = [
  {
    icon: "✱",
    label:
      'Grep "OPENCODE.*DB|database|sqlite|drizzle|dev.*db|data.*dir|xdg|APPDATA" in packages/codex-proxy/src (151 matches)',
  },
  {
    icon: "✱",
    label: 'Glob "**/*db*" in packages/codex-proxy (6 matches)',
  },
  {
    icon: "→",
    label: "Read packages/codex-proxy/src/storage/db.ts [offset=1, limit=130]",
  },
  {
    icon: "→",
    label: "Read packages/codex-proxy/src/index.ts [offset=1, limit=100]",
    error: "No LSP server available for this file type.",
  },
  {
    icon: "✱",
    label:
      'Grep "export const CODEX_PROXY_DB|CODEX_PROXY_DB|CODEX_PROXY_DEV|Global\\.Path\\.data|data =" in packages/codex-proxy/src (115 matches)',
  },
] as const

function ShellOutput() {
  return (
    <box id="tool-block-shell" marginTop={1} paddingTop={1} paddingBottom={1} paddingLeft={2} gap={1}>
      <text paddingLeft={3}># List files</text>
      <box gap={1}>
        <text>$ ls</text>
        <text>file.ts</text>
      </box>
    </box>
  )
}

function UserMessage() {
  return (
    <box id="message-user">
      <box paddingTop={1} paddingBottom={1} paddingLeft={2}>
        <text>Check whether the next tool remains separated.</text>
      </box>
    </box>
  )
}

export function Fixture(props: { errorExpanded?: boolean; before?: "shell" | "user" }) {
  return (
    <box flexDirection="column" width={72}>
      <box flexDirection="column">
        {props.before === "shell" && <ShellOutput />}
        {props.before === "user" && <UserMessage />}
        <For each={tools}>
          {(item) => (
            <InlineToolRow
              icon={item.icon}
              complete={true}
              pending=""
              failed={Boolean(item.error)}
              error={item.error}
              errorExpanded={props.errorExpanded}
              separateAfter={(id) => id === "message-user"}
            >
              {item.label}
            </InlineToolRow>
          )}
        </For>
      </box>
    </box>
  )
}

export function SubagentGroupFixture() {
  return (
    <box flexDirection="column" width={72}>
      <InlineToolRow id="tool-inline-before" icon="✱" complete={true} pending="">
        Grep "Task" (2 matches)
      </InlineToolRow>
      <InlineToolRow id="tool-inline-subagent-one" icon="⠙" complete={true} pending="" subagent={true}>
        Explore Task — Inspect active task spacing
      </InlineToolRow>
      <InlineToolRow id="tool-inline-subagent-two" icon="✓" complete={true} pending="" subagent={true}>
        {"General Task — Confirm completed task spacing\n↳ 1 toolcall · 501ms"}
      </InlineToolRow>
      <InlineToolRow id="tool-inline-after" icon="→" complete={true} pending="">
        Read src/cli/cmd/tui/routes/session/index.tsx
      </InlineToolRow>
    </box>
  )
}

export function LoadedReadBeforeSubagentFixture() {
  return (
    <box flexDirection="column" width={72}>
      <InlineToolRow id="tool-inline-read" icon="→" complete={true} pending="">
        Read src/cli/cmd/tui/routes/session/index.tsx
      </InlineToolRow>
      <box id="tool-inline-loaded-read-child" paddingLeft={3}>
        <text paddingLeft={3}>↳ Loaded src/cli/cmd/tui/routes/session/tools.tsx</text>
      </box>
      <InlineToolRow id="tool-inline-subagent-after-read" icon="✓" complete={true} pending="" subagent={true}>
        {"Explore Task — Inspect active task spacing\n↳ 1 toolcall · 501ms"}
      </InlineToolRow>
    </box>
  )
}

export function AssistantSummaryBeforeSubagentFixture() {
  return (
    <box flexDirection="column" width={72}>
      <box id="assistant-summary-message-one" paddingLeft={3}>
        <text>▣ Build · Little Frank · 53.1s</text>
      </box>
      <InlineToolRow id="tool-inline-subagent-one" icon="✓" complete={true} pending="" subagent={true}>
        {"Build Task — Review changes\n↳ 48 toolcalls · 1m 40s"}
      </InlineToolRow>
    </box>
  )
}

export function AssistantErrorBeforeSubagentFixture() {
  return (
    <box flexDirection="column" width={72}>
      <box id="assistant-error-message-one" border={["left"]} paddingTop={1} paddingBottom={1} paddingLeft={2}>
        <text>Managed inference requires an active Member plan</text>
      </box>
      <InlineToolRow id="tool-inline-subagent-one" icon="✓" complete={true} pending="" subagent={true}>
        {"Build Task — Review changes\n↳ 48 toolcalls · 1m 40s"}
      </InlineToolRow>
    </box>
  )
}

function StickyScrollFixture(props: { separated: boolean; scroll: (scroll: ScrollBoxRenderable) => void }) {
  return (
    <scrollbox ref={props.scroll} stickyScroll={true} stickyStart="bottom" height={3} width={72}>
      <box height={1}>
        <text>First row</text>
      </box>
      <box height={1}>
        <text>Second row</text>
      </box>
      <Show when={props.separated}>
        <box id="text-before-tool">
          <text>Assistant text</text>
        </box>
      </Show>
      <InlineToolRow icon="→" complete={true} pending="">
        Read src/cli/cmd/tui/routes/session/index.tsx
      </InlineToolRow>
    </scrollbox>
  )
}

export function FailedPendingToolFixture() {
  return (
    <InlineToolRow icon="%" complete={false} pending="Preparing patch..." failed={true} failure="Patch failed">
      Patch
    </InlineToolRow>
  )
}

export function FailedCompleteToolFixture() {
  return (
    <InlineToolRow icon="→" complete={true} pending="Reading file..." failed={true} failure="Read failed">
      Read src/index.ts
    </InlineToolRow>
  )
}

export async function renderFrame(component: () => JSX.Element, options: { width: number; height: number }) {
  testSetup = await testRender(component, options)
  await testSetup.renderOnce()

  return testSetup
    .captureCharFrame()
    .split("\n")
    .map((line) => line.trimEnd())
    .join("\n")
    .trimEnd()
}

export async function renderStickyScrollFixture() {
  const [separated, setSeparated] = createSignal(false)
  let scroll: ScrollBoxRenderable | undefined
  testSetup = await testRender(
    () => <StickyScrollFixture separated={separated()} scroll={(value) => (scroll = value)} />,
    {
      width: 72,
      height: 3,
    },
  )

  return { scroll, setSeparated, setup: testSetup }
}
