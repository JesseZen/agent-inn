import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { run } from "./app"
import { TuiConfig } from "./config"
import { createProxyFetch, emptyEventSource } from "./proxy/backend"
import { registerProxyCommands } from "./proxy/commands"
import type { TuiPluginHost } from "./plugin/runtime"

const url = process.env.CODEX_PROXY_URL || "http://127.0.0.1:9090"
const directory = process.env.CODEX_PROXY_PROJECT_DIR || process.cwd()

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
