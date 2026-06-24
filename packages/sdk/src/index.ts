export * from "./client.js"
export * from "./server.js"

import { createAinnClient } from "./client.js"
import { createAinnServer } from "./server.js"
import type { ServerOptions } from "./server.js"

export async function createAinn(options?: ServerOptions) {
  const server = await createAinnServer({
    ...options,
  })

  const client = createAinnClient({
    baseUrl: server.url,
  })

  return {
    client,
    server,
  }
}
