/** @jsxImportSource @opentui/solid */
import { TextareaRenderable } from "@opentui/core"
import { createDefaultOpenTuiKeymap } from "@opentui/keymap/opentui"
import { createBindingLookup } from "@opentui/keymap/extras"
import { testRender, useRenderer } from "@opentui/solid"
import { expect, test } from "bun:test"
import { onCleanup } from "solid-js"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { TuiKeybind } from "../src/config/keybind"
import {
  getAinnModeStack,
  AINN_BASE_MODE,
  AinnKeymapProvider,
  registerAinnKeymap,
  resolvePromptSubmitKind,
  useCommandSlashes,
} from "../src/keymap"

function createResolvedKeymapConfig(input: TuiKeybind.KeybindOverrides = {}) {
  const keybinds = TuiKeybind.parse(input)
  return {
    keybinds: createBindingLookup(TuiKeybind.toBindingConfig(keybinds), {
      commandMap: TuiKeybind.CommandMap,
      bindingDefaults: TuiKeybind.bindingDefaults(),
    }),
    leader_timeout: 2000,
  }
}

test("default prompt input keys submit with return and keep shift return for newline", () => {
  const config = createResolvedKeymapConfig()

  expect({
    submit: config.keybinds.get("input.submit").map((binding) => binding.key),
    newline: config.keybinds.get("input.newline").map((binding) => binding.key),
  }).toEqual({
    submit: ["return"],
    newline: ["shift+return,ctrl+return,alt+return,ctrl+j"],
  })
})

test("legacy page key aliases compile as page keys", async () => {
  const sequences: Record<string, string[][]> = {}

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig({
      messages_page_up: "pgup",
      messages_page_down: "pgdown",
    })
    const offKeymap = registerAinnKeymap(keymap, renderer, config)
    const offLayer = keymap.registerLayer({
      bindings: config.keybinds.gather("session", ["session.page.up", "session.page.down"]),
    })
    const bindings = keymap.getCommandBindings({
      visibility: "registered",
      commands: ["session.page.up", "session.page.down"],
    })
    sequences.up =
      bindings.get("session.page.up")?.map((binding) => binding.sequence.map((part) => part.stroke.name)) ?? []
    sequences.down =
      bindings.get("session.page.down")?.map((binding) => binding.sequence.map((part) => part.stroke.name)) ?? []
    onCleanup(() => {
      offLayer()
      offKeymap()
    })

    return (
      <AinnKeymapProvider keymap={keymap}>
        <box />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />)
  try {
    expect(sequences).toEqual({
      up: [["pageup"]],
      down: [["pagedown"]],
    })
  } finally {
    app.renderer.destroy()
  }
})

test("mode-less bindings stay active when ainn mode changes", async () => {
  const counts: Record<string, Record<string, number>> = {}

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig()
    const offKeymap = registerAinnKeymap(keymap, renderer, config)
    const offGlobal = keymap.registerLayer({
      commands: [
        { name: "session.list", run() {} },
        { name: "session.new", run() {} },
        { name: "session.page.up", run() {} },
        { name: "session.first", run() {} },
      ],
      bindings: config.keybinds.gather("test.global", [
        "session.list",
        "session.new",
        "session.page.up",
        "session.first",
      ]),
    })
    const offBase = keymap.registerLayer({
      mode: AINN_BASE_MODE,
      commands: [{ name: "model.list", run() {} }],
      bindings: config.keybinds.gather("test.base", ["model.list"]),
    })
    const activeCounts = () =>
      Object.fromEntries(
        Array.from(
          keymap.getCommandBindings({
            visibility: "active",
            commands: ["session.list", "session.new", "session.page.up", "session.first", "model.list"],
          }),
          ([command, bindings]) => [command, bindings.length],
        ),
      )

    counts.base = activeCounts()
    const popQuestion = getAinnModeStack(keymap).push("question")
    counts.question = activeCounts()
    popQuestion()
    const popAutocomplete = getAinnModeStack(keymap).push("autocomplete")
    counts.autocomplete = activeCounts()
    popAutocomplete()

    onCleanup(() => {
      offBase()
      offGlobal()
      offKeymap()
    })

    return (
      <AinnKeymapProvider keymap={keymap}>
        <box />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />)
  try {
    expect(counts).toEqual({
      base: { "session.list": 1, "session.new": 1, "session.page.up": 2, "session.first": 2, "model.list": 1 },
      question: { "session.list": 1, "session.new": 1, "session.page.up": 2, "session.first": 2, "model.list": 0 },
      autocomplete: {
        "session.list": 1,
        "session.new": 1,
        "session.page.up": 2,
        "session.first": 2,
        "model.list": 0,
      },
    })
  } finally {
    app.renderer.destroy()
  }
})

test("prompt submit prefers a reachable local slash command over a remote command", async () => {
  let result!: ReturnType<typeof resolvePromptSubmitKind>

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig()
    const offKeymap = registerAinnKeymap(keymap, renderer, config)
    const offLayer = keymap.registerLayer({
      commands: [
        {
          namespace: "palette",
          name: "proxy.status",
          title: "Proxy status",
          slashName: "status",
          run() {},
        },
      ],
    })

    result = resolvePromptSubmitKind(keymap, "/status", [{ name: "status" }])

    onCleanup(() => {
      offLayer()
      offKeymap()
    })

    return (
      <AinnKeymapProvider keymap={keymap}>
        <box />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />)
  try {
    expect(result).toEqual({ type: "local", commandName: "proxy.status" })
  } finally {
    app.renderer.destroy()
  }
})

test("prompt submit falls back to remote slash commands when no local slash exists", async () => {
  let result!: ReturnType<typeof resolvePromptSubmitKind>

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig()
    const offKeymap = registerAinnKeymap(keymap, renderer, config)

    result = resolvePromptSubmitKind(keymap, "/deploy now", [{ name: "deploy" }])

    onCleanup(() => {
      offKeymap()
    })

    return (
      <AinnKeymapProvider keymap={keymap}>
        <box />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />)
  try {
    expect(result).toEqual({ type: "remote", commandName: "deploy" })
  } finally {
    app.renderer.destroy()
  }
})

test("slash command descriptions react to command metadata changes", async () => {
  let updateTitle!: () => void

  function Commands() {
    const slashes = useCommandSlashes()
    return <text>{slashes().find((item) => item.commandName === "language.switch")?.description}</text>
  }

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig()
    const offKeymap = registerAinnKeymap(keymap, renderer, config)
    let offLayer = keymap.registerLayer({
      commands: [
        {
          namespace: "palette",
          name: "language.switch",
          title: "Switch language",
          slashName: "language",
          run() {},
        },
      ],
    })
    updateTitle = () => {
      offLayer()
      offLayer = keymap.registerLayer({
        commands: [
          {
            namespace: "palette",
            name: "language.switch",
            title: "切换语言",
            slashName: "language",
            run() {},
          },
        ],
      })
    }
    onCleanup(() => {
      offLayer()
      offKeymap()
    })

    return (
      <AinnKeymapProvider keymap={keymap}>
        <Commands />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />)
  try {
    await app.renderOnce()
    expect(app.captureCharFrame()).toContain("Switch language")
    updateTitle()
    await app.renderOnce()
    expect(app.captureCharFrame()).toContain("切换语言")
  } finally {
    app.renderer.destroy()
  }
})

test("managed prompt input submits with return and inserts newline with shift return by default", async () => {
  let textarea!: TextareaRenderable
  let submitted = 0

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig()
    const offKeymap = registerAinnKeymap(keymap, renderer, config)
    onCleanup(offKeymap)

    return (
      <AinnKeymapProvider keymap={keymap}>
        <textarea
          ref={(value: TextareaRenderable) => {
            textarea = value
            textarea.focus()
          }}
          onSubmit={() => {
            submitted += 1
          }}
        />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />, { kittyKeyboard: true })
  try {
    textarea.focus()

    app.mockInput.pressEnter({ shift: true })
    app.mockInput.pressEnter()

    expect({ submitted, text: textarea.plainText }).toEqual({
      submitted: 1,
      text: "\n",
    })
  } finally {
    app.renderer.destroy()
  }
})

test("managed prompt input lets users bind alt return to submit", async () => {
  let textarea!: TextareaRenderable
  let submitted = 0

  function Harness() {
    const renderer = useRenderer()
    const keymap = createDefaultOpenTuiKeymap(renderer)
    const config = createResolvedKeymapConfig({
      input_submit: "alt+return",
      input_newline: "shift+return,ctrl+return,ctrl+j",
    })
    const offKeymap = registerAinnKeymap(keymap, renderer, config)
    onCleanup(offKeymap)

    return (
      <AinnKeymapProvider keymap={keymap}>
        <textarea
          ref={(value: TextareaRenderable) => {
            textarea = value
            textarea.focus()
          }}
          onSubmit={() => {
            submitted += 1
          }}
        />
      </AinnKeymapProvider>
    )
  }

  const app = await testRender(() => <Harness />, { kittyKeyboard: true })
  try {
    textarea.focus()

    app.mockInput.pressEnter({ shift: true })
    app.mockInput.pressEnter({ meta: true })

    expect({ submitted, text: textarea.plainText }).toEqual({
      submitted: 1,
      text: "\n",
    })
  } finally {
    app.renderer.destroy()
  }
})

test("resolved keybinds keep return submit out of default newline keys", () => {
  const config = createTuiResolvedConfig({
    keybinds: {},
  })

  expect({
    submit: config.keybinds.get("input.submit").map((binding) => binding.key),
    newline: config.keybinds.get("input.newline").map((binding) => binding.key),
  }).toEqual({
    submit: ["return"],
    newline: ["shift+return,ctrl+return,alt+return,ctrl+j"],
  })
})

test("resolved keybinds remove default newline keys claimed by submit overrides", () => {
  const stringConfig = createTuiResolvedConfig({
    keybinds: {
      input_submit: "ctrl+return",
    },
  })
  const strokeConfig = createTuiResolvedConfig({
    keybinds: {
      input_submit: { name: "return", shift: true },
    },
  })
  const objectConfig = createTuiResolvedConfig({
    keybinds: {
      input_submit: { key: { name: "return", ctrl: true } },
    },
  })

  expect({
    stringNewline: stringConfig.keybinds.get("input.newline").map((binding) => binding.key),
    strokeNewline: strokeConfig.keybinds.get("input.newline").map((binding) => binding.key),
    objectNewline: objectConfig.keybinds.get("input.newline").map((binding) => binding.key),
  }).toEqual({
    stringNewline: ["shift+return,alt+return,ctrl+j"],
    strokeNewline: ["ctrl+return,alt+return,ctrl+j"],
    objectNewline: ["shift+return,alt+return,ctrl+j"],
  })
})

test("resolved keybinds normalize enter aliases when removing submit collisions", () => {
  const enterConfig = createTuiResolvedConfig({
    keybinds: {
      input_submit: "enter",
    },
  })
  const ctrlEnterConfig = createTuiResolvedConfig({
    keybinds: {
      input_submit: "ctrl+enter",
    },
  })
  const strokeConfig = createTuiResolvedConfig({
    keybinds: {
      input_submit: { name: "enter", shift: true },
    },
  })

  expect({
    enterNewline: enterConfig.keybinds.get("input.newline").map((binding) => binding.key),
    ctrlEnterNewline: ctrlEnterConfig.keybinds.get("input.newline").map((binding) => binding.key),
    strokeNewline: strokeConfig.keybinds.get("input.newline").map((binding) => binding.key),
  }).toEqual({
    enterNewline: ["shift+return,ctrl+return,alt+return,ctrl+j"],
    ctrlEnterNewline: ["shift+return,alt+return,ctrl+j"],
    strokeNewline: ["ctrl+return,alt+return,ctrl+j"],
  })
})
