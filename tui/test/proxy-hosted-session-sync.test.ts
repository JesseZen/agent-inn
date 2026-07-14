import { expect, test } from "bun:test"
import path from "node:path"
import type { HostedSessionListResponse, HostedSessionSnapshot, ManagerEvent } from "../src/proxy/hosted-session-contract"
import { createHostedSessionSync } from "../src/proxy/hosted-session-sync"

const baseSnapshot: HostedSessionSnapshot = {
  session_id: "hs_1",
  session_label: "solve problem A",
  worker: { id: "cli", name: "CLI", port: 11199, missing: false },
  workspace: "/tmp/work",
  model: "gpt-5.5",
  add_dirs: [],
  status: "stale",
  user_marker: "",
  turn: { state: "running", reason: "", unread: false, needs_input: false },
  created_at: "2026-07-13T01:02:03Z",
  last_opened_at: "2026-07-13T01:02:03Z",
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

async function waitFor(check: () => boolean) {
  const deadline = Date.now() + 1000
  while (Date.now() < deadline) {
    if (check()) return
    await new Promise((resolve) => setTimeout(resolve, 1))
  }
  throw new Error("condition not reached")
}

test("hosted sync buffers the list race and orders large decimal event IDs", async () => {
  const baseline = deferred<HostedSessionListResponse>()
  const order: string[] = []
  let emit!: (event: ManagerEvent) => void
  let end!: () => void
  let stopped = 0
  let listCalls = 0
  const subscriptionCursors: Array<string | undefined> = []
  const states: Array<{ sessions: Record<string, HostedSessionSnapshot>; cursor: string }> = []
  const sync = createHostedSessionSync({
    list: () => {
      order.push("list")
      listCalls++
      if (listCalls === 1) return baseline.promise
      return Promise.resolve({ sessions: [], event_cursor: "9007199254740996" })
    },
    subscribe: async (handler, options) => {
      order.push("subscribe")
      subscriptionCursors.push(options.lastEventID)
      emit = handler
      end = options.onEnd
      return () => {
        stopped++
      }
    },
    commit: (sessions, cursor) => states.push({ sessions, cursor }),
  })

  const starting = sync.start()
  expect(order).toEqual(["subscribe", "list"])
  const older = { ...baseSnapshot, session_label: "older" }
  const newest = { ...baseSnapshot, session_label: "newest" }
  emit({ id: "9007199254740995", type: "hosted.session.snapshot.changed", payload: { snapshot: newest } })
  emit({ id: "9007199254740993", type: "hosted.session.snapshot.changed", payload: { snapshot: older } })
  emit({ id: "9007199254740995", type: "hosted.session.snapshot.changed", payload: { snapshot: newest } })
  baseline.resolve({ sessions: [baseSnapshot], event_cursor: "9007199254740992" })
  await starting

  expect(states.at(-1)).toEqual({ sessions: { hs_1: newest }, cursor: "9007199254740995" })
  emit({ id: "9007199254740994", type: "hosted.session.deleted", payload: { session_id: "hs_1" } })
  expect(states.at(-1)).toEqual({ sessions: { hs_1: newest }, cursor: "9007199254740995" })
  emit({ id: "9007199254740996", type: "hosted.session.deleted", payload: { session_id: "hs_1" } })
  expect(states.at(-1)).toEqual({ sessions: {}, cursor: "9007199254740996" })

  end()
  expect(states.at(-1)).toEqual({ sessions: {}, cursor: "9007199254740996" })
  await sync.refresh()
  expect(subscriptionCursors).toEqual([undefined, "9007199254740996"])
  const recovered = { ...baseSnapshot, session_label: "recovered" }
  emit({ id: "9007199254740997", type: "hosted.session.snapshot.changed", payload: { snapshot: recovered } })
  expect(states.at(-1)).toEqual({ sessions: { hs_1: recovered }, cursor: "9007199254740997" })
  sync.stop()
  expect(stopped).toBe(2)
})

test("hosted sync reloads on active-stream resync and never infers unsupported-provider waiting", async () => {
  const claudeSnapshot: HostedSessionSnapshot = {
    ...baseSnapshot,
    worker: { id: "claude", name: "Claude", port: 11200, missing: false },
    turn: { state: "running", reason: "", unread: false, needs_input: false },
  }
  const reloaded = { ...claudeSnapshot, user_marker: "todo" as const }
  const lists: HostedSessionListResponse[] = [
    { sessions: [claudeSnapshot], event_cursor: "4" },
    { sessions: [reloaded], event_cursor: "8" },
  ]
  let emit!: (event: ManagerEvent) => void
  let listCalls = 0
  let state = { sessions: {} as Record<string, HostedSessionSnapshot>, cursor: "0" }
  const sync = createHostedSessionSync({
    list: async () => lists[listCalls++]!,
    subscribe: async (handler) => {
      emit = handler
      return () => {}
    },
    commit: (sessions, cursor) => {
      state = { sessions, cursor }
    },
  })

  await sync.start()
  expect(state).toEqual({ sessions: { hs_1: claudeSnapshot }, cursor: "4" })
  expect(state.sessions.hs_1?.turn.needs_input).toBe(false)
  emit({ id: "7", type: "manager.resync-required", payload: { reason: "event_cursor_expired" } })
  await waitFor(() => listCalls === 2 && state.cursor === "8")
  expect(state).toEqual({ sessions: { hs_1: reloaded }, cursor: "8" })
})

test("hosted sync stops a stream whose baseline fails before Refresh recovery", async () => {
  let listCalls = 0
  let stopped = 0
  const handlers: Array<(event: ManagerEvent) => void> = []
  let state = { sessions: {} as Record<string, HostedSessionSnapshot>, cursor: "0" }
  const sync = createHostedSessionSync({
    list: async () => {
      listCalls++
      if (listCalls === 1) throw new Error("baseline failed")
      return { sessions: [baseSnapshot], event_cursor: "2" }
    },
    subscribe: async (handler) => {
      let live = true
      handlers.push((event) => {
        if (live) handler(event)
      })
      return () => {
        live = false
        stopped++
      }
    },
    commit: (sessions, cursor) => {
      state = { sessions, cursor }
    },
  })

  await expect(sync.start()).rejects.toThrow("baseline failed")
  await waitFor(() => listCalls === 1)
  await sync.refresh()
  expect({ subscriptions: handlers.length, stopped }).toEqual({ subscriptions: 2, stopped: 1 })
  const stale = { ...baseSnapshot, session_label: "stale stream" }
  handlers[0]!({ id: "3", type: "hosted.session.snapshot.changed", payload: { snapshot: stale } })
  expect(state).toEqual({ sessions: { hs_1: baseSnapshot }, cursor: "2" })
  const recovered = { ...baseSnapshot, session_label: "recovered stream" }
  handlers[1]!({ id: "3", type: "hosted.session.snapshot.changed", payload: { snapshot: recovered } })
  expect(state).toEqual({ sessions: { hs_1: recovered }, cursor: "3" })
  sync.stop()
  expect(stopped).toBe(2)
})

test("hosted sync observes the baseline when subscription startup also fails", async () => {
  const subscriptionError = new Error("subscription failed")
  const sync = createHostedSessionSync({
    list: async () => {
      throw new Error("baseline failed")
    },
    subscribe: async () => {
      throw subscriptionError
    },
    commit: () => {},
  })

  try {
    await sync.start()
    throw new Error("expected startup failure")
  } catch (error) {
    expect(error).toBe(subscriptionError)
  }
  await Bun.sleep(0)
})

test("hosted sync ignores an older baseline that resolves after a newer Refresh", async () => {
  const olderBaseline = deferred<HostedSessionListResponse>()
  const newerBaseline = deferred<HostedSessionListResponse>()
  let listCalls = 0
  let emit!: (event: ManagerEvent) => void
  let state = { sessions: {} as Record<string, HostedSessionSnapshot>, cursor: "0" }
  const sync = createHostedSessionSync({
    list: async () => {
      listCalls++
      if (listCalls === 1) return { sessions: [baseSnapshot], event_cursor: "7" }
      if (listCalls === 2) return olderBaseline.promise
      return newerBaseline.promise
    },
    subscribe: async (handler) => {
      emit = handler
      return () => {}
    },
    commit: (sessions, cursor) => {
      state = { sessions, cursor }
    },
  })

  await sync.start()
  const olderRefresh = sync.refresh()
  const newerRefresh = sync.refresh()
  await waitFor(() => listCalls === 3)
  const eventTen = { ...baseSnapshot, session_label: "event 10" }
  emit({ id: "10", type: "hosted.session.snapshot.changed", payload: { snapshot: eventTen } })
  const newer = { ...baseSnapshot, session_label: "newer baseline" }
  newerBaseline.resolve({ sessions: [newer], event_cursor: "9" })
  await newerRefresh
  const eventEleven = { ...baseSnapshot, session_label: "event 11" }
  emit({ id: "11", type: "hosted.session.snapshot.changed", payload: { snapshot: eventEleven } })
  const older = { ...baseSnapshot, session_label: "older baseline" }
  olderBaseline.resolve({ sessions: [older], event_cursor: "8" })
  await olderRefresh

  expect(state).toEqual({ sessions: { hs_1: eventEleven }, cursor: "11" })
  sync.stop()
})

test("popup bootstrap owns one stream and sends its existing decimal cursor", async () => {
  let subscriptions = 0
  let lastEventID: string | undefined
  const sync = createHostedSessionSync({
    initialCursor: "9007199254740999",
    list: async () => ({ sessions: [baseSnapshot], event_cursor: "9007199254741000" }),
    subscribe: async (_handler, options) => {
      subscriptions++
      lastEventID = options.lastEventID
      return () => {}
    },
    commit: () => {},
  })

  await sync.start()
  expect({ subscriptions, lastEventID }).toEqual({ subscriptions: 1, lastEventID: "9007199254740999" })
  sync.stop()
})

test("hosted TypeScript contract matches the canonical Schema properties", async () => {
  const candidates = [
    path.resolve(import.meta.dir, "../../docs/superpowers/specs/hosted-session-snapshot.schema.json"),
    path.resolve(import.meta.dir, "../../../../docs/superpowers/specs/hosted-session-snapshot.schema.json"),
  ]
  const schemaPath = candidates.find((candidate) => Bun.file(candidate).size > 0)
  if (!schemaPath) throw new Error("canonical hosted-session Schema not found")
  const schema = (await Bun.file(schemaPath).json()) as {
    $defs: Record<string, { properties: Record<string, unknown>; required: string[] }>
  }
  const snapshotKeys = Object.keys(baseSnapshot).sort()
  const turnKeys = Object.keys(baseSnapshot.turn).sort()
  const workerKeys = Object.keys(baseSnapshot.worker).sort()

  expect(snapshotKeys).toEqual(Object.keys(schema.$defs.hostedSessionSnapshot!.properties).sort())
  expect(snapshotKeys).toEqual([...schema.$defs.hostedSessionSnapshot!.required].sort())
  expect(turnKeys).toEqual(Object.keys(schema.$defs.turnSnapshot!.properties).sort())
  expect(turnKeys).toEqual([...schema.$defs.turnSnapshot!.required].sort())
  expect(workerKeys).toEqual(Object.keys(schema.$defs.workerSnapshot!.properties).sort())
  expect(workerKeys).toEqual([...schema.$defs.workerSnapshot!.required].sort())
})
