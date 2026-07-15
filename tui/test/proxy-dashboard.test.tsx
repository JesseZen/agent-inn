import { expect, test } from "bun:test"
import { InputRenderable, ScrollBoxRenderable, type Renderable } from "@opentui/core"
import { resolveSlashCommand } from "../src/keymap"
import type { HostedSessionSnapshot } from "../src/proxy/hosted-session-contract"
import { mountProxyApp, wait } from "./proxy-commands.fixture"
import { DEFAULT_THEMES, resolveTheme } from "../src/theme"

const activeSession: HostedSessionSnapshot = {
  session_id: "hs_active",
  session_label: "Active build",
  worker: { id: "app", name: "app", port: 6767, missing: false },
  workspace: "",
  model: "",
  add_dirs: [],
  user_marker: "",
  turn: { state: "running", reason: "", unread: false, needs_input: false },
  created_at: "2026-07-11T00:00:00Z",
  last_opened_at: "2026-07-11T00:00:00Z",
  status: "active",
}

const staleSession: HostedSessionSnapshot = {
  ...activeSession,
  session_id: "hs_stale",
  session_label: "Stale review",
  turn: { state: "idle", reason: "", unread: false, needs_input: false },
  status: "stale",
}

function findDashboardScrollBox(root: Renderable): ScrollBoxRenderable | undefined {
  if (root instanceof ScrollBoxRenderable && root.scrollHeight > 0) return root
  return root.getChildren().map(findDashboardScrollBox).find(Boolean)
}

function framePoint(frame: string, value: string) {
  const lines = frame.split("\n")
  const y = lines.findIndex((line) => line.includes(value))
  return { x: lines[y].indexOf(value) + 1, y }
}

function selectedLine(frame: string) {
  return (
    frame
      .split("\n")
      .find((line) => line.includes("›"))
      ?.trim() ?? ""
  )
}

function lineForeground(app: Awaited<ReturnType<typeof mountProxyApp>>, value: string) {
  const line = app.setup.captureSpans().lines.find((item) =>
    item.spans
      .map((span) => span.text)
      .join("")
      .includes(value),
  )
  const span = line?.spans.find((item) => item.text.includes(value)) ?? line?.spans.find((item) => item.text.trim() !== "")
  if (!span) throw new Error(`missing rendered span for ${value}`)
  return span.fg
}

test("proxy dashboard is canonical and topology resolves as its alias", async () => {
  const app = await mountProxyApp()
  try {
    expect(resolveSlashCommand(app.api.keymap, "/dashboard")).toBe("proxy.dashboard")
    expect(resolveSlashCommand(app.api.keymap, "/topology")).toBe("proxy.dashboard")

    app.api.keymap.dispatchCommand("proxy.dashboard")
    await app.render()
    expect(app.frame()).toContain("Dashboard")
  } finally {
    await app.cleanup()
  }
})

test("dashboard renders summary and relationship-first hierarchy", async () => {
  const app = await mountProxyApp({
    hostedSessions: [activeSession, staleSession],
    upstreams: [
      {
        id: "openai",
        name: "openai",
        base_url: "https://api.openai.com/v1",
        has_api_key: true,
      },
      {
        id: "missing-key",
        name: "missing key upstream",
        base_url: "https://example.com/v1",
        has_api_key: false,
      },
    ],
    workers: [
      {
        id: "app",
        name: "app",
        upstream_id: "openai",
        port: 6767,
        upstream: { id: "openai", name: "openai", has_api_key: true },
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
      {
        id: "failed",
        name: "failed worker",
        upstream_id: "missing-key",
        port: 6768,
        upstream: {
          id: "missing-key",
          name: "missing key upstream",
          has_api_key: false,
        },
        status: "failed",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Active build")
    })
    const frame = app.frame()
    expect({
      title: frame.includes("Dashboard"),
      summary: ["UPSTREAMS", "WORKERS", "SESSIONS", "UNBOUND"].every((label) => frame.includes(label)),
      hierarchy: ["◆ openai", "└─ app", "Active build", "Stale review"].every((label) => frame.includes(label)),
      warnings: ["missing key", "failed"].every((label) => frame.includes(label)),
      oldLegend: frame.includes("■ upstream"),
    }).toEqual({
      title: true,
      summary: true,
      hierarchy: true,
      warnings: true,
      oldLegend: false,
    })
  } finally {
    await app.cleanup()
  }
})

test("dashboard renders hosted marker priority without consuming todo", async () => {
  const sessions: HostedSessionSnapshot[] = [
    {
      ...activeSession,
      session_id: "hs_waiting",
      session_label: "Waiting todo",
      user_marker: "todo",
      turn: { state: "running", reason: "", unread: false, needs_input: true },
    },
    {
      ...activeSession,
      session_id: "hs_running",
      session_label: "Running todo",
      user_marker: "todo",
    },
    {
      ...activeSession,
      session_id: "hs_unread",
      session_label: "Unread todo",
      user_marker: "todo",
      turn: { state: "done", reason: "", unread: true, needs_input: false },
    },
    {
      ...activeSession,
      session_id: "hs_todo",
      session_label: "Acknowledged todo",
      worker: { id: "cli-openrouter", name: "cli-openrouter", port: 11199, missing: false },
      user_marker: "todo",
      turn: { state: "done", reason: "", unread: false, needs_input: false },
    },
  ]
  const app = await mountProxyApp({ hostedSessions: sessions })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Acknowledged todo")
    })
    const frame = app.frame()
    expect({
      waiting: frame.split("\n").find((line) => line.includes("Waiting todo"))?.includes("?"),
      running: frame.split("\n").find((line) => line.includes("Running todo"))?.includes("*"),
      unread: frame.split("\n").find((line) => line.includes("Unread todo"))?.includes("+"),
      todo: frame.split("\n").find((line) => line.includes("Acknowledged todo"))?.includes("~"),
      waitingColor: lineForeground(app, "?"),
    }).toEqual({
      waiting: true,
      running: true,
      unread: true,
      todo: true,
      waitingColor: resolveTheme(DEFAULT_THEMES.ainn, "dark").warning,
    })
  } finally {
    await app.cleanup()
  }
})

test("dashboard renders pool hierarchy with inactive members and opens the pool editor", async () => {
  const openai = {
    id: "openai",
    name: "openai",
    base_url: "https://api.openai.com/v1",
    has_api_key: true,
  }
  const fallback = {
    id: "fallback",
    name: "fallback",
    base_url: "https://fallback.example/v1",
    has_api_key: true,
  }
  const app = await mountProxyApp({
    upstreams: [openai, fallback],
    upstreamPools: [
      {
        id: "primary",
        name: "primary",
        mode: "active",
        probe: { stable_interval_seconds: 900, alert_interval_seconds: 60 },
        probe_state: "stable",
        upstreams: [openai.id, fallback.id],
        circuit_breaker: {
          failure_threshold: 3,
          recovery_success_threshold: 2,
          recovery_wait_seconds: 60,
        },
        active_upstream: openai.id,
        workers: ["app"],
        readiness: [],
      },
    ],
    workers: [
      {
        id: "app",
        name: "app",
        upstream_id: openai.id,
        upstream_pool: "primary",
        port: 6767,
        upstream: openai,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
    hostedSessions: [activeSession],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("▣ primary") && app.frame().includes("◆ fallback")
    })
    const frame = app.frame()
    expect({
      summary: frame.includes("POOLS") && frame.includes("UPSTREAMS"),
      hierarchy: ["▣ primary", "◆ openai", "└─ app", "Active build", "◆ fallback"].every((value) => frame.includes(value)),
      active: frame.includes("active"),
      depths: [framePoint(frame, "▣ primary").x, framePoint(frame, "◆ openai").x, framePoint(frame, "└─ app").x, framePoint(frame, "Active build").x].every(
        (depth, index, depths) => index === 0 || depth > depths[index - 1]!,
      ),
      selected: selectedLine(frame),
    }).toEqual({
      summary: true,
      hierarchy: true,
      active: true,
      depths: true,
      selected: expect.stringContaining("▣ primary"),
    })

    app.api.keymap.dispatchCommand("dashboard.submit")
    await app.render()
    expect(app.frame()).toContain("Edit Pool: primary")
  } finally {
    await app.cleanup()
  }
})

test("dashboard keeps sibling workers aligned when only one has sessions", async () => {
  const app = await mountProxyApp({
    upstreams: [
      {
        id: "fastapi",
        name: "fastapi",
        base_url: "https://example.com/v1",
        has_api_key: true,
      },
    ],
    workers: [
      {
        id: "worker-0p02",
        name: "0p02",
        upstream_id: "fastapi",
        port: 57380,
        upstream: { id: "fastapi", name: "fastapi", has_api_key: true },
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
      {
        id: "worker-10dolloars",
        name: "10dolloars",
        upstream_id: "fastapi",
        port: 50137,
        upstream: { id: "fastapi", name: "fastapi", has_api_key: true },
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
    hostedSessions: [
      {
        ...activeSession,
        session_id: "hs-10dolloars",
        session_label: "0p02 1",
        worker: { id: "worker-10dolloars", name: "10dolloars", port: 50137, missing: false },
      },
    ],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("10dolloars")
    })
    const frame = app.frame()
    expect(framePoint(frame, "0p02").x).toBe(framePoint(frame, "10dolloars").x)
  } finally {
    await app.cleanup()
  }
})

test("dashboard shows fixed keys and contextual actions for selected rows", async () => {
  const app = await mountProxyApp({
    hostedSessions: [activeSession, staleSession],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Stale review")
    })
    expect({
      fixed: ["↑↓ select", "←→ collapse/expand", "enter open", "type to filter", "esc close"].every((value) => app.frame().includes(value)),
      context: app.frame().includes("enter edit upstream"),
    }).toEqual({ fixed: true, context: true })

    app.api.keymap.dispatchCommand("dashboard.next")
    await app.render()
    expect(app.frame()).toContain("enter manage worker")

    app.api.keymap.dispatchCommand("dashboard.next")
    await app.render()
    expect({
      open: app.frame().includes("enter open session"),
      drag: app.frame().includes("drag to rebind"),
    }).toEqual({ open: true, drag: false })

    app.api.keymap.dispatchCommand("dashboard.next")
    await app.render()
    expect(app.frame()).toContain("enter open session · drag to rebind")

    app.api.keymap.dispatchCommand("dashboard.end")
    await app.render()
    expect(app.frame()).toContain("click or ←→ expand/collapse")
  } finally {
    await app.cleanup()
  }
})

test("dashboard renders a precise empty state", async () => {
  const app = await mountProxyApp({
    workers: [],
    upstreams: [],
    hostedSessions: [],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await app.render()
    expect(app.frame()).toContain("No pools, workers, upstreams, or sessions configured")
  } finally {
    await app.cleanup()
  }
})

test("dashboard mouse toggles the unbound relationship group", async () => {
  const app = await mountProxyApp({
    workers: [],
    hostedSessions: [],
    upstreams: [
      {
        id: "alpha",
        name: "Alpha API",
        base_url: "https://alpha.example/v1",
        has_api_key: true,
      },
      {
        id: "beta",
        name: "Beta API",
        base_url: "https://beta.example/v1",
        has_api_key: true,
      },
    ],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("UNBOUND 2")
    })
    const before = app.frame()

    await app.setup.mockMouse.click(...(Object.values(framePoint(before, "⚠ UNBOUND 2")) as [number, number]))
    await app.render()
    const expanded = app.frame()

    await app.setup.mockMouse.click(...(Object.values(framePoint(expanded, "⚠ UNBOUND 2")) as [number, number]))
    await app.render()
    const collapsed = app.frame()

    expect({
      before: before.includes("◆ Alpha API"),
      expanded: expanded.includes("◆ Alpha API") && expanded.includes("◆ Beta API"),
      collapsed: collapsed.includes("◆ Alpha API"),
    }).toEqual({ before: false, expanded: true, collapsed: false })
  } finally {
    await app.cleanup()
  }
})

test("dashboard disclosure toggles hierarchy while labels open details", async () => {
  const app = await mountProxyApp({ hostedSessions: [activeSession] })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Active build")
    })

    expect(app.frame()).toContain("▾ ◆ openai")
    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "▾ ◆ openai")) as [number, number]))
    await app.render()
    expect({
      collapsed: app.frame().includes("▸ ◆ openai") && !app.frame().includes("Active build"),
      upstreamDetail: app.frame().includes("Edit Upstream"),
    }).toEqual({ collapsed: true, upstreamDetail: false })

    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "▸ ◆ openai")) as [number, number]))
    await app.render()
    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "◆ openai")) as [number, number]))
    await app.render()
    expect(app.frame()).toContain("Edit Upstream")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("▾ └─ app")
    })
    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "▾ └─ app")) as [number, number]))
    await app.render()
    expect({
      collapsed: app.frame().includes("▸ └─ app") && !app.frame().includes("Active build"),
      workerDetail: app.frame().includes("Worker actions"),
    }).toEqual({ collapsed: true, workerDetail: false })

    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "▸ └─ app")) as [number, number]))
    await app.render()
    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "└─ app")) as [number, number]))
    await app.render()
    expect(app.frame()).toContain("Worker actions")
  } finally {
    await app.cleanup()
  }
})

test("dashboard restores the same view after closing a detail", async () => {
  const app = await mountProxyApp({
    hostedSessions: [activeSession],
    width: 140,
    height: 18,
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("◆ openai") && app.setup.renderer.currentFocusedRenderable instanceof InputRenderable
    }, 5000)
    await app.mockInput.typeText("openai")
    await app.render()

    const dashboardInput = app.setup.renderer.currentFocusedRenderable as InputRenderable
    const dashboardScroll = findDashboardScrollBox(app.setup.renderer.root)!
    dashboardScroll.scrollTop = 1
    await app.render()
    const before = {
      filter: dashboardInput.value,
      scrollTop: dashboardScroll.scrollTop,
      selected: selectedLine(app.frame()),
      titleColumn: app
        .frame()
        .split("\n")
        .find((line) => line.includes("Dashboard"))!
        .indexOf("Dashboard"),
    }
    app.api.keymap.dispatchCommand("dashboard.submit")
    await app.render()
    expect(app.frame()).toContain("Edit Upstream")

    app.mockInput.pressArrow("down")
    await app.render()
    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      const restoredInput = app.setup.renderer.currentFocusedRenderable
      return (
        app.frame().includes("Dashboard") &&
        restoredInput instanceof InputRenderable &&
        restoredInput.value === before.filter &&
        findDashboardScrollBox(app.setup.renderer.root)?.scrollTop === before.scrollTop
      )
    })

    const restoredInput = app.setup.renderer.currentFocusedRenderable
    const restoredScroll = findDashboardScrollBox(app.setup.renderer.root)!
    expect({
      filter: restoredInput instanceof InputRenderable ? restoredInput.value : null,
      scrollTop: restoredScroll.scrollTop,
      selected: selectedLine(app.frame()),
      titleColumn: app
        .frame()
        .split("\n")
        .find((line) => line.includes("Dashboard"))!
        .indexOf("Dashboard"),
    }).toEqual({
      filter: before.filter,
      scrollTop: before.scrollTop,
      selected: before.selected,
      titleColumn: before.titleColumn,
    })
  } finally {
    await app.cleanup()
  }
})

test("dashboard keeps configured relationships visible when hosted sessions fail to load", async () => {
  const app = await mountProxyApp({
    hostedSessionsError: "session refresh failed",
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("◆ openai") && app.setup.renderer.currentFocusedRenderable instanceof InputRenderable
    })
    await wait(async () => {
      await app.render()
      return app.frame().includes("session refresh failed")
    })
    expect(app.frame()).toContain("◆ openai")
  } finally {
    await app.cleanup()
  }
})

test("dashboard selection scrolls inside the native scrollbox", async () => {
  const upstreams = Array.from({ length: 12 }, (_, index) => ({
    id: `upstream-${index}`,
    name: `domain-${String(index).padStart(2, "0")}`,
    base_url: "https://example.com/v1",
    has_api_key: true,
  }))
  const workers = upstreams.map((upstream, index) => ({
    id: `worker-${index}`,
    name: `worker-${String(index).padStart(2, "0")}`,
    upstream_id: upstream.id,
    port: 6800 + index,
    upstream,
    status: "running",
    snapshot_generation: 1,
    log_level: "simple",
  }))
  const app = await mountProxyApp({ upstreams, workers, height: 18 })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("domain-00")
    })
    const scroll = findDashboardScrollBox(app.setup.renderer.root)!
    for (let index = 0; index < 11; index += 1) {
      app.api.keymap.dispatchCommand("dashboard.next")
      await app.render()
    }
    expect(scroll.scrollTop).toBeGreaterThan(0)
  } finally {
    await app.cleanup()
  }
})

test("dashboard rows open existing upstream, worker, and session details", async () => {
  const app = await mountProxyApp({ hostedSessions: [activeSession] })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Active build")
    })

    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "◆ openai")) as [number, number]))
    await app.render()
    expect(app.frame()).toContain("Edit Upstream")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Active build")
    })
    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "└─ app")) as [number, number]))
    await app.render()
    expect(app.frame()).toContain("Worker actions")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Active build")
    })
    await app.setup.mockMouse.click(...(Object.values(framePoint(app.frame(), "Active build")) as [number, number]))
    await app.render()
    expect(app.frame()).toContain("Hosted Terminal")
  } finally {
    await app.cleanup()
  }
})

test("dashboard keyboard navigation expands, collapses, wraps, and submits stable rows", async () => {
  const app = await mountProxyApp({ hostedSessions: [activeSession] })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Active build")
    })

    expect(selectedLine(app.frame())).toContain("◆ openai")
    app.api.keymap.dispatchCommand("dashboard.next")
    await app.render()
    expect(selectedLine(app.frame())).toContain("└─ app")

    app.api.keymap.dispatchCommand("dashboard.collapse")
    await app.render()
    expect({
      selected: selectedLine(app.frame()),
      sessionsVisible: app.frame().includes("Active build"),
    }).toEqual({
      selected: expect.stringContaining("└─ app"),
      sessionsVisible: false,
    })
    app.api.keymap.dispatchCommand("dashboard.collapse")
    await app.render()
    expect(selectedLine(app.frame())).toContain("◆ openai")
    app.api.keymap.dispatchCommand("dashboard.collapse")
    await app.render()
    expect(app.frame()).not.toContain("└─ app")

    app.api.keymap.dispatchCommand("dashboard.expand")
    await app.render()
    expect({
      workerVisible: app.frame().includes("└─ app"),
      sessionsVisible: app.frame().includes("Active build"),
    }).toEqual({ workerVisible: true, sessionsVisible: false })
    app.api.keymap.dispatchCommand("dashboard.expand")
    await app.render()
    expect(selectedLine(app.frame())).toContain("└─ app")
    app.api.keymap.dispatchCommand("dashboard.expand")
    await app.render()
    expect({
      selected: selectedLine(app.frame()),
      sessionsVisible: app.frame().includes("Active build"),
    }).toEqual({
      selected: expect.stringContaining("└─ app"),
      sessionsVisible: true,
    })
    app.api.keymap.dispatchCommand("dashboard.expand")
    await app.render()
    expect(selectedLine(app.frame())).toContain("Active build")

    app.api.keymap.dispatchCommand("dashboard.end")
    await app.render()
    expect(selectedLine(app.frame())).toContain("UNBOUND")
    app.api.keymap.dispatchCommand("dashboard.home")
    await app.render()
    expect(selectedLine(app.frame())).toContain("◆ openai")

    app.api.keymap.dispatchCommand("dashboard.submit")
    await app.render()
    expect(app.frame()).toContain("Edit Upstream")
  } finally {
    await app.cleanup()
  }
})

test("dashboard filtering expands only the matching relationship path", async () => {
  const app = await mountProxyApp({ hostedSessions: [staleSession] })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("◆ openai") && app.setup.renderer.currentFocusedRenderable instanceof InputRenderable
    })
    await app.mockInput.typeText("Stale review")
    await app.render()
    const frame = app.frame()
    expect({
      selected: selectedLine(frame),
      path: ["◆ openai", "└─ app", "Stale review"].every((value) => frame.includes(value)),
      unrelated: frame.includes("anthropic"),
    }).toEqual({
      selected: expect.stringContaining("Stale review"),
      path: true,
      unrelated: false,
    })
  } finally {
    await app.cleanup()
  }
})

test("dashboard expands the session preview from the more row", async () => {
  const sessions = Array.from({ length: 5 }, (_, index): HostedSessionSnapshot => ({
    ...staleSession,
    session_id: `hs_preview_${index}`,
    session_label: `Preview ${index}`,
    status: "active",
  }))
  const app = await mountProxyApp({ hostedSessions: sessions })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("+2 sessions")
    })
    for (let index = 0; index < 5; index += 1) app.api.keymap.dispatchCommand("dashboard.next")
    await app.render()
    expect(app.frame()).toContain("enter show all sessions")
    app.api.keymap.dispatchCommand("dashboard.submit")
    await app.render()
    expect({
      finalSession: app.frame().includes("Preview 4"),
      more: app.frame().includes("+2 sessions"),
    }).toEqual({ finalSession: true, more: false })
  } finally {
    await app.cleanup()
  }
})

test("dashboard page down changes selection and scroll position", async () => {
  const upstreams = Array.from({ length: 12 }, (_, index) => ({
    id: `page-${index}`,
    name: `page-domain-${String(index).padStart(2, "0")}`,
    base_url: "https://example.com/v1",
    has_api_key: true,
  }))
  const workers = upstreams.map((upstream, index) => ({
    id: `page-worker-${index}`,
    name: `page-worker-${index}`,
    upstream_id: upstream.id,
    port: 6900 + index,
    upstream,
    status: "running",
    snapshot_generation: 1,
    log_level: "simple",
  }))
  const app = await mountProxyApp({ upstreams, workers, height: 18 })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("page-domain-00")
    })
    const before = selectedLine(app.frame())
    const scroll = findDashboardScrollBox(app.setup.renderer.root)!
    app.api.keymap.dispatchCommand("dashboard.page_down")
    app.api.keymap.dispatchCommand("dashboard.page_down")
    await wait(async () => {
      await app.render()
      return scroll.scrollTop > 0
    })
    expect({
      selectionChanged: selectedLine(app.frame()) !== before,
      scrollTop: scroll.scrollTop > 0,
    }).toEqual({
      selectionChanged: true,
      scrollTop: true,
    })
  } finally {
    await app.cleanup()
  }
})

test("dashboard reconciles reactive data by stable id and opens new warnings", async () => {
  const openai = {
    id: "openai",
    name: "openai",
    base_url: "https://api.openai.com/v1",
    has_api_key: true,
  }
  const anthropic = {
    id: "anthropic",
    name: "anthropic",
    base_url: "https://api.anthropic.com/v1",
    has_api_key: true,
  }
  const appWorker = {
    id: "app",
    name: "app",
    upstream_id: "openai",
    port: 6767,
    upstream: openai,
    status: "running",
    snapshot_generation: 1,
    log_level: "simple",
  }
  const cliWorker = {
    ...appWorker,
    id: "cli",
    name: "cli",
    upstream_id: "anthropic",
    port: 6768,
    upstream: anthropic,
  }
  const app = await mountProxyApp({
    upstreams: [openai, anthropic],
    workers: [appWorker, cliWorker],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("◆ openai") && app.setup.renderer.currentFocusedRenderable instanceof InputRenderable
    })
    app.api.keymap.dispatchCommand("dashboard.end")
    await app.render()
    expect(selectedLine(app.frame())).toContain("◆ openai")

    app.replaceDashboardData({
      upstreams: [openai, anthropic],
      workers: [appWorker, { ...cliWorker, status: "failed" }],
      hostedSessions: [],
    })
    await wait(async () => {
      await app.render()
      return app.frame().includes("└─ cli")
    })
    expect({
      selected: selectedLine(app.frame()),
      warningExpanded: app.frame().includes("└─ cli"),
    }).toEqual({
      selected: expect.stringContaining("◆ openai"),
      warningExpanded: true,
    })

    app.replaceDashboardData({
      upstreams: [anthropic],
      workers: [{ ...cliWorker, status: "failed" }],
      hostedSessions: [],
    })
    await wait(async () => {
      await app.render()
      return !app.frame().includes("◆ openai")
    })
    expect(selectedLine(app.frame())).toContain("◆ anthropic")
  } finally {
    await app.cleanup()
  }
})

test("dashboard drags workers and upstreams through stable upstream ids", async () => {
  const openai = {
    id: "openai",
    name: "openai",
    base_url: "https://api.openai.com/v1",
    has_api_key: true,
  }
  const anthropic = {
    id: "anthropic-id",
    name: "anthropic",
    base_url: "https://api.anthropic.com/v1",
    has_api_key: true,
  }
  const app = await mountProxyApp({
    upstreams: [openai, anthropic],
    workers: [
      {
        id: "app",
        name: "app",
        upstream_id: "openai",
        port: 6767,
        upstream: openai,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
      {
        id: "other",
        name: "other",
        upstream_id: "anthropic-id",
        port: 6768,
        upstream: anthropic,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("◆ anthropic") && app.setup.renderer.currentFocusedRenderable instanceof InputRenderable
    })
    app.api.keymap.dispatchCommand("dashboard.end")
    app.api.keymap.dispatchCommand("dashboard.expand")
    await app.render()

    const worker = framePoint(app.frame(), "└─ app")
    const target = framePoint(app.frame(), "◆ anthropic")
    await app.setup.mockMouse.pressDown(worker.x, worker.y)
    await app.setup.mockMouse.moveTo(target.x, target.y)
    await app.render()
    expect({
      inspector: app.frame().includes("Move From app To anthropic"),
      expandedTarget: app.frame().includes("└─ other"),
    }).toEqual({ inspector: true, expandedTarget: true })
    await app.setup.mockMouse.release(target.x, target.y)
    await app.render()
    await wait(() => app.calls.patchWorker.length === 1)
    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "anthropic-id", log_level: "simple" }])
  } finally {
    await app.cleanup()
  }
})

test("dashboard drags an unbound upstream onto a worker", async () => {
  const openai = {
    id: "openai",
    name: "openai",
    base_url: "https://api.openai.com/v1",
    has_api_key: true,
  }
  const anthropic = {
    id: "anthropic-id",
    name: "anthropic",
    base_url: "https://api.anthropic.com/v1",
    has_api_key: true,
  }
  const app = await mountProxyApp({
    upstreams: [openai, anthropic],
    workers: [
      {
        id: "app",
        name: "app",
        upstream_id: "anthropic-id",
        port: 6767,
        upstream: anthropic,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("UNBOUND 1") && app.setup.renderer.currentFocusedRenderable instanceof InputRenderable
    })
    app.api.keymap.dispatchCommand("dashboard.expand")
    app.api.keymap.dispatchCommand("dashboard.end")
    app.api.keymap.dispatchCommand("dashboard.expand")
    await app.render()
    const source = framePoint(app.frame(), "◆ openai")
    const target = framePoint(app.frame(), "└─ app")
    await app.setup.mockMouse.pressDown(source.x, source.y)
    await app.setup.mockMouse.moveTo(target.x, target.y)
    await app.render()
    await app.setup.mockMouse.release(target.x, target.y)
    await app.render()
    await wait(() => app.calls.patchWorker.length === 1)
    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "simple" }])
  } finally {
    await app.cleanup()
  }
})

test("dashboard rebinds stale sessions and explains why running session drags do not apply", async () => {
  const app = await mountProxyApp({
    hostedSessions: [staleSession, activeSession],
  })
  try {
    app.api.keymap.dispatchCommand("proxy.dashboard")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Stale review")
    })
    const stale = framePoint(app.frame(), "Stale review")
    const cli = framePoint(app.frame(), "└─ cli-openrouter")
    await app.setup.mockMouse.pressDown(stale.x, stale.y)
    await app.setup.mockMouse.moveTo(cli.x, cli.y)
    await app.render()
    expect(app.frame()).toContain("Move From Stale review To cli-openrouter")
    expect(lineForeground(app, "◆ openai")).not.toEqual(lineForeground(app, "└─ cli-openrouter"))
    await app.setup.mockMouse.release(cli.x, cli.y)
    await wait(() => app.calls.patchHostedSession.length === 1)
    expect(app.calls.patchHostedSession).toEqual([{ session_id: "hs_stale", worker_id: "cli-openrouter" }])

    const running = framePoint(app.frame(), "Active build")
    await app.setup.mockMouse.pressDown(running.x, running.y)
    await app.setup.mockMouse.moveTo(cli.x, cli.y)
    await app.render()
    expect(app.frame()).toContain("Move From Active build To cli-openrouter")
    await app.setup.mockMouse.release(cli.x, cli.y)
    await app.render()
    expect({
      patches: app.calls.patchHostedSession,
      workerDialog: app.frame().includes("Worker actions"),
      warning: app.frame().includes("Running sessions can be rebound"),
    }).toEqual({
      patches: [{ session_id: "hs_stale", worker_id: "cli-openrouter" }],
      workerDialog: false,
      warning: true,
    })
  } finally {
    await app.cleanup()
  }
})
