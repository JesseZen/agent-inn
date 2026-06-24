import { Effect } from "effect"
import { Global } from "@agent-inn/core/global"
import { run } from "./app"
import { TuiConfig } from "./config"
import { createProxyFetch, emptyEventSource } from "./proxy/backend"
import { registerProxyCommands } from "./proxy/commands"
import type { TuiPluginHost } from "./plugin/runtime"

const url = process.env.AINN_URL || "http://127.0.0.1:9090"
const directory = process.env.AINN_PROJECT_DIR || process.cwd()

const proxyFetch = createProxyFetch({ baseUrl: url, directory })

const host: TuiPluginHost = {
  async start(input) {
    registerProxyCommands(input.api)
  },
  async dispose() {},
}

await Effect.runPromise(
  run({
    url,
    directory,
    fetch: proxyFetch as typeof fetch,
    events: emptyEventSource(),
    args: {},
    config: TuiConfig.resolve({}, { terminalSuspend: false }),
    pluginHost: host,
  }).pipe(Effect.provide(Global.defaultLayer)),
)
