import fs from "fs/promises"
import os from "os"
import path from "path"
import { xdgCache, xdgData, xdgState } from "xdg-basedir"
import { Context, Effect, Layer } from "effect"
import { Flag } from "./flag/flag"
import { Flock } from "./util/flock"

const app = "codex-proxy"

export const Path = {
  get home() {
    return process.env.CODEX_PROXY_TEST_HOME ?? os.homedir()
  },
  data: path.join(xdgData ?? path.join(os.homedir(), ".local", "share"), app),
  cache: path.join(xdgCache ?? path.join(os.homedir(), ".cache"), app),
  get config() {
    return Flag.CODEX_PROXY_CONFIG_DIR ?? path.join(this.home, ".codex-proxy")
  },
  state: path.join(xdgState ?? path.join(os.homedir(), ".local", "state"), app),
  tmp: path.join(os.tmpdir(), app),
  get bin() {
    return path.join(this.cache, "bin")
  },
  get log() {
    return path.join(this.data, "log")
  },
  get repos() {
    return path.join(this.data, "repos")
  },
}

Flock.setGlobal({ state: Path.state })

await Promise.all([
  fs.mkdir(Path.data, { recursive: true }),
  fs.mkdir(Path.config, { recursive: true }),
  fs.mkdir(Path.state, { recursive: true }),
  fs.mkdir(Path.tmp, { recursive: true }),
  fs.mkdir(Path.log, { recursive: true }),
  fs.mkdir(Path.bin, { recursive: true }),
  fs.mkdir(Path.repos, { recursive: true }),
])

export interface Interface {
  readonly home: string
  readonly data: string
  readonly cache: string
  readonly config: string
  readonly state: string
  readonly tmp: string
  readonly bin: string
  readonly log: string
  readonly repos: string
}

export class Service extends Context.Service<Service, Interface>()("@codex-proxy/Global") {}

export function make(input: Partial<Interface> = {}): Interface {
  return {
    home: Path.home,
    data: Path.data,
    cache: Path.cache,
    config: Path.config,
    state: Path.state,
    tmp: Path.tmp,
    bin: Path.bin,
    log: Path.log,
    repos: Path.repos,
    ...input,
  }
}

export const layer = Layer.effect(
  Service,
  Effect.sync(() => Service.of(make())),
)
export const defaultLayer = layer
export const layerWith = (input: Partial<Interface>) =>
  Layer.effect(
    Service,
    Effect.sync(() => Service.of(make(input))),
  )

export * as Global from "./global"
