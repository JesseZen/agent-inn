import type { ManagerEvent } from "./hosted-session-contract"

type SubscribeManagerEventStreamInput = {
  url: string
  fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  headers?: RequestInit["headers"]
  lastEventID?: string
  onEvent: (event: ManagerEvent) => void
  onEnd?: () => void
  onError?: (error: unknown) => void
}

export async function subscribeManagerEventStream(input: SubscribeManagerEventStreamInput) {
  const controller = new AbortController()
  const headers = new Headers(input.headers)
  headers.set("Accept", "text/event-stream")
  if (input.lastEventID) headers.set("Last-Event-ID", input.lastEventID)
  const response = await input.fetch(input.url, { signal: controller.signal, headers })
  if (!response.ok || !response.body) throw new Error(`failed to subscribe manager events: ${response.status}`)

  const reader = response.body.getReader()
  void (async () => {
    const decoder = new TextDecoder()
    let buffer = ""
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      while (true) {
        const separator = buffer.match(/\r?\n\r?\n/)
        if (!separator || separator.index === undefined) break
        const frame = buffer.slice(0, separator.index)
        buffer = buffer.slice(separator.index + separator[0].length)
        let id = ""
        let eventType = ""
        const data: string[] = []
        for (const line of frame.split(/\r?\n/)) {
          if (line.startsWith("id:")) id = line.slice(3).trimStart()
          if (line.startsWith("event:")) eventType = line.slice(6).trimStart()
          if (line.startsWith("data:")) data.push(line.slice(5).trimStart())
        }
        if (!eventType) continue
        input.onEvent({
          id,
          type: eventType,
          payload: data.length > 0 ? ((JSON.parse(data.join("\n")) as Record<string, unknown>) ?? {}) : {},
        })
      }
    }
  })().catch((error) => {
    if (!controller.signal.aborted) input.onError?.(error)
  }).finally(() => {
    if (!controller.signal.aborted) input.onEnd?.()
  })

  return () => {
    controller.abort()
    void reader.cancel()
  }
}
