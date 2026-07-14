import { expect, test } from "bun:test"
import { subscribeManagerEventStream } from "../src/proxy/manager-event-stream"

const encoder = new TextEncoder()

function createStreamFetch() {
  let controller: ReadableStreamDefaultController<Uint8Array> | undefined
  let signal: AbortSignal | undefined
  let headers = new Headers()
  return {
    fetch: async (_input: RequestInfo | URL, init?: RequestInit) => {
      signal = init?.signal ?? undefined
      headers = new Headers(init?.headers)
      return new Response(
        new ReadableStream<Uint8Array>({
          start(value) {
            controller = value
          },
        }),
        { status: 200, headers: { "content-type": "text/event-stream" } },
      )
    },
    push(value: string) {
      controller!.enqueue(encoder.encode(value))
    },
    close() {
      controller!.close()
    },
    get signal() {
      return signal
    },
    get headers() {
      return headers
    },
  }
}

async function nextTick() {
  await new Promise((resolve) => setTimeout(resolve, 0))
}

test("manager event stream parses split chunks, multiple frames, and decimal IDs", async () => {
  const stream = createStreamFetch()
  const events: Array<{ id: string; type: string; payload: Record<string, unknown> }> = []
  let ended = false
  const unsubscribe = await subscribeManagerEventStream({
    url: "http://manager.local/api/events",
    fetch: stream.fetch,
    lastEventID: "9007199254740992",
    onEvent: (event) => events.push(event),
    onEnd: () => {
      ended = true
    },
  })

  stream.push("id: 9007199254740993\nevent: hosted.session.snapshot.changed\nda")
  stream.push('ta: {"snapshot":{"session_id":"hs_1"}}\n\nid: 9007199254740994\nevent: hosted.session.deleted\n')
  stream.push('data: {"session_id":"hs_1"}\n\n')
  await nextTick()

  expect(events).toEqual([
    {
      id: "9007199254740993",
      type: "hosted.session.snapshot.changed",
      payload: { snapshot: { session_id: "hs_1" } },
    },
    {
      id: "9007199254740994",
      type: "hosted.session.deleted",
      payload: { session_id: "hs_1" },
    },
  ])
  expect(stream.headers.get("accept")).toBe("text/event-stream")
  expect(stream.headers.get("last-event-id")).toBe("9007199254740992")

  stream.close()
  await nextTick()
  expect(ended).toBe(true)
  unsubscribe()
})

test("manager event stream aborts its fetch lifecycle", async () => {
  const stream = createStreamFetch()
  const unsubscribe = await subscribeManagerEventStream({
    url: "http://manager.local/api/events",
    fetch: stream.fetch,
    onEvent: () => {},
  })

  expect(stream.signal?.aborted).toBe(false)
  unsubscribe()
  expect(stream.signal?.aborted).toBe(true)
})

test("manager event stream signals end after a non-abort reader failure", async () => {
  const stream = createStreamFetch()
  let ended = 0
  let failure: unknown
  await subscribeManagerEventStream({
    url: "http://manager.local/api/events",
    fetch: stream.fetch,
    onEvent: () => {},
    onEnd: () => {
      ended++
    },
    onError: (error) => {
      failure = error
    },
  })

  stream.push("id: 1\nevent: hosted.session.snapshot.changed\ndata: not-json\n\n")
  await nextTick()
  expect({ ended, failed: failure instanceof SyntaxError }).toEqual({ ended: 1, failed: true })
})
