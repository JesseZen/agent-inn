/** @jsxImportSource @opentui/solid */
import { describe, expect, test } from "bun:test"
import { tmpdir } from "../../../fixture/fixture"
import { json, mount, wait } from "./sync-fixture"
import type { GlobalEvent } from "@agent-inn/sdk/v2"

function branchEvent(branch: string, workspace?: string): GlobalEvent {
  return {
    directory: "/tmp/other",
    project: "proj_test",
    workspace,
    payload: {
      id: `evt_vcs_${branch}`,
      type: "vcs.branch.updated",
      properties: { branch },
    },
  }
}

describe("tui sync", () => {
  test("refresh scopes sessions by default and lists project sessions when disabled", async () => {
    await using tmp = await tmpdir()
    await Bun.write(`${tmp.path}/kv.json`, "{}")
    const { app, kv, sync, session } = await mount(undefined, tmp.path)

    try {
      expect(kv.get("session_directory_filter_enabled", true)).toBe(true)
      expect(session.at(-1)?.searchParams.get("scope")).toBeNull()
      expect(session.at(-1)?.searchParams.get("path")).toBe("packages/tui")

      kv.set("session_directory_filter_enabled", false)
      await sync.session.refresh()

      expect(session.at(-1)?.searchParams.get("scope")).toBe("project")
      expect(session.at(-1)?.searchParams.get("path")).toBeNull()
    } finally {
      app.renderer.destroy()
    }
  })

  test("vcs branch updates only apply for the active workspace", async () => {
    await using tmp = await tmpdir()
    await Bun.write(`${tmp.path}/kv.json`, "{}")
    const { app, emit, project, sync } = await mount(undefined, tmp.path)

    try {
      expect(sync.data.vcs?.branch).toBe("main")

      project.workspace.set("ws_a")
      emit(branchEvent("other", "ws_b"))
      await Bun.sleep(30)

      expect(sync.data.vcs?.branch).toBe("main")

      emit(branchEvent("feature", "ws_a"))
      await wait(() => sync.data.vcs?.branch === "feature")

      expect(sync.data.vcs?.branch).toBe("feature")
    } finally {
      app.renderer.destroy()
    }
  })

  test("manager events refresh manager data and surface manager errors", async () => {
    await using tmp = await tmpdir()
    await Bun.write(`${tmp.path}/kv.json`, "{}")
    const encoder = new TextEncoder()
    const oldUpstream = { name: "openai", base_url: "https://old.example/v1", has_api_key: true }
    const newUpstream = { name: "openai", base_url: "https://new.example/v1", has_api_key: true, api_format: "responses" }
    const oldWorker = {
      name: "app",
      port: 6767,
      role: "app",
      upstream: oldUpstream,
      status: "running",
      snapshot_generation: 1,
      log_level: "simple",
    }
    const newWorker = {
      ...oldWorker,
      upstream: newUpstream,
      status: "failed",
      snapshot_generation: 2,
      log_level: "detail",
    }
    let manager = {
      workers: [oldWorker],
      upstreams: { openai: oldUpstream },
      config: { plugins: { model_override: { kind: "request_middleware", source: "external" } } },
      status: { generation: 1, dirty: false, last_save_error: "" },
    }
    let events!: ReadableStreamDefaultController<Uint8Array>

    const { app, sync } = await mount((url) => {
      if (url.pathname === "/api/workers") return json({ workers: manager.workers })
      if (url.pathname === "/api/upstreams") return json({ upstreams: manager.upstreams })
      if (url.pathname === "/api/config")
        return json({
          config: manager.config,
          status: manager.status,
        })
      if (url.pathname === "/api/events")
        return new Response(
          new ReadableStream({
            start(controller) {
              events = controller
            },
          }),
          { headers: { "content-type": "text/event-stream" } },
        )
      return undefined
    }, tmp.path)

    try {
      manager = {
        workers: [newWorker],
        upstreams: { openai: newUpstream },
        config: { plugins: { model_override: { kind: "request_middleware", source: "builtin" } } },
        status: { generation: 2, dirty: true, last_save_error: "save failed" },
      }
      events.enqueue(
        encoder.encode('id: 1\nevent: worker.health.changed\ndata: {"worker":"app","status":"failed","error":"worker failed"}\n\n'),
      )
      await wait(() => sync.data.workers[0]?.snapshot_generation === 2)

      expect(sync.data.workers).toEqual([newWorker])
      expect(sync.data.upstreams).toEqual([newUpstream])
      expect(sync.data.manager_config).toEqual({
        plugins: { model_override: { kind: "request_middleware", source: "builtin" } },
      })
      expect(sync.data.config_status).toEqual({ generation: 2, dirty: true, last_save_error: "save failed" })
      expect(sync.data.error).toBe("worker failed")
    } finally {
      app.renderer.destroy()
    }
  })

  test("replayed worker error events do not stale after healthy manager refresh", async () => {
    await using tmp = await tmpdir()
    await Bun.write(`${tmp.path}/kv.json`, "{}")
    const encoder = new TextEncoder()
    const upstream = { name: "openai", base_url: "https://api.example/v1", has_api_key: true }
    const worker = {
      name: "app",
      port: 6767,
      role: "app",
      upstream,
      status: "running",
      snapshot_generation: 1,
      log_level: "simple",
    }
    let workersRequests = 0
    let events!: ReadableStreamDefaultController<Uint8Array>

    const { app, sync } = await mount((url) => {
      if (url.pathname === "/api/workers") {
        workersRequests++
        return json({ workers: [{ ...worker, snapshot_generation: workersRequests }] })
      }
      if (url.pathname === "/api/upstreams") return json({ upstreams: { openai: upstream } })
      if (url.pathname === "/api/config")
        return json({
          config: {},
          status: { generation: 1, dirty: false, last_save_error: "" },
        })
      if (url.pathname === "/api/events")
        return new Response(
          new ReadableStream({
            start(controller) {
              events = controller
            },
          }),
          { headers: { "content-type": "text/event-stream" } },
        )
      return undefined
    }, tmp.path)

    try {
      const before = workersRequests
      events.enqueue(
        encoder.encode('id: 1\nevent: worker.health.changed\ndata: {"worker":"app","status":"failed","error":"worker failed"}\n\n'),
      )
      await wait(() => workersRequests > before && sync.data.workers[0]?.snapshot_generation === workersRequests)
      await Bun.sleep(30)

      expect(sync.data.workers).toEqual([{ ...worker, snapshot_generation: workersRequests }])
      expect(sync.data.error).toBeUndefined()
    } finally {
      app.renderer.destroy()
    }
  })

  test("worker health errors surface when refreshed worker remains in event status", async () => {
    await using tmp = await tmpdir()
    await Bun.write(`${tmp.path}/kv.json`, "{}")
    const encoder = new TextEncoder()
    const upstream = { name: "openai", base_url: "https://api.example/v1", has_api_key: true }
    const worker = {
      name: "app",
      port: 6767,
      role: "app",
      upstream,
      status: "running",
      snapshot_generation: 1,
      log_level: "simple",
    }
    let managerWorker = worker
    let events!: ReadableStreamDefaultController<Uint8Array>

    const { app, sync } = await mount((url) => {
      if (url.pathname === "/api/workers") return json({ workers: [managerWorker] })
      if (url.pathname === "/api/upstreams") return json({ upstreams: { openai: upstream } })
      if (url.pathname === "/api/config")
        return json({
          config: {},
          status: { generation: 1, dirty: false, last_save_error: "" },
        })
      if (url.pathname === "/api/events")
        return new Response(
          new ReadableStream({
            start(controller) {
              events = controller
            },
          }),
          { headers: { "content-type": "text/event-stream" } },
        )
      return undefined
    }, tmp.path)

    try {
      managerWorker = { ...worker, status: "out_of_sync", snapshot_generation: 2 }
      events.enqueue(
        encoder.encode('id: 1\nevent: worker.health.changed\ndata: {"worker":"app","status":"out_of_sync","error":"runtime apply failed"}\n\n'),
      )
      await wait(() => sync.data.workers[0]?.snapshot_generation === 2)

      expect(sync.data.workers).toEqual([managerWorker])
      expect(sync.data.error).toBe("runtime apply failed")
    } finally {
      app.renderer.destroy()
    }
  })

  test("non-health manager refresh preserves current worker health error", async () => {
    await using tmp = await tmpdir()
    await Bun.write(`${tmp.path}/kv.json`, "{}")
    const encoder = new TextEncoder()
    const upstream = { name: "openai", base_url: "https://api.example/v1", has_api_key: true }
    const worker = {
      name: "app",
      port: 6767,
      role: "app",
      upstream,
      status: "running",
      snapshot_generation: 1,
      log_level: "simple",
    }
    let managerWorker = worker
    let events!: ReadableStreamDefaultController<Uint8Array>

    const { app, sync } = await mount((url) => {
      if (url.pathname === "/api/workers") return json({ workers: [managerWorker] })
      if (url.pathname === "/api/upstreams") return json({ upstreams: { openai: upstream } })
      if (url.pathname === "/api/config")
        return json({
          config: {},
          status: { generation: 1, dirty: false, last_save_error: "" },
        })
      if (url.pathname === "/api/events")
        return new Response(
          new ReadableStream({
            start(controller) {
              events = controller
            },
          }),
          { headers: { "content-type": "text/event-stream" } },
        )
      return undefined
    }, tmp.path)

    try {
      managerWorker = { ...worker, status: "out_of_sync", snapshot_generation: 2 }
      events.enqueue(
        encoder.encode('id: 1\nevent: worker.health.changed\ndata: {"worker":"app","status":"out_of_sync","error":"runtime apply failed"}\n\n'),
      )
      await wait(() => sync.data.error === "runtime apply failed")

      managerWorker = { ...managerWorker, snapshot_generation: 3 }
      events.enqueue(encoder.encode('id: 2\nevent: upstream.updated\ndata: {"upstream":"openai"}\n\n'))
      await wait(() => sync.data.workers[0]?.snapshot_generation === 3)

      expect(sync.data.workers).toEqual([managerWorker])
      expect(sync.data.error).toBe("runtime apply failed")
    } finally {
      app.renderer.destroy()
    }
  })
})
