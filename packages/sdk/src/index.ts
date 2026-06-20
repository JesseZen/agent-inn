export * from "./client.js"
export * from "./server.js"

import { createCodexProxyClient } from "./client.js"
import { createCodexProxyServer } from "./server.js"
import type { ServerOptions } from "./server.js"

export async function createCodexProxy(options?: ServerOptions) {
  const server = await createCodexProxyServer({
    ...options,
  })

  const client = createCodexProxyClient({
    baseUrl: server.url,
  })

  return {
    client,
    server,
  }
}
