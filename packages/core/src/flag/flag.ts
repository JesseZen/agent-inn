import { Config } from "effect"

export function truthy(key: string) {
  const value = process.env[key]?.toLowerCase()
  return value === "true" || value === "1"
}

const copy = process.env["AINN_EXPERIMENTAL_DISABLE_COPY_ON_SELECT"]
const fff = process.env["AINN_DISABLE_FFF"]

function enabledByExperimental(key: string) {
  return process.env[key] === undefined ? truthy("AINN_EXPERIMENTAL") : truthy(key)
}

export const Flag = {
  OTEL_EXPORTER_OTLP_ENDPOINT: process.env["OTEL_EXPORTER_OTLP_ENDPOINT"],
  OTEL_EXPORTER_OTLP_HEADERS: process.env["OTEL_EXPORTER_OTLP_HEADERS"],
  AINN_AUTO_HEAP_SNAPSHOT: truthy("AINN_AUTO_HEAP_SNAPSHOT"),
  AINN_GIT_BASH_PATH: process.env["AINN_GIT_BASH_PATH"],
  AINN_CONFIG: process.env["AINN_CONFIG"],
  AINN_CONFIG_CONTENT: process.env["AINN_CONFIG_CONTENT"],
  AINN_DISABLE_AUTOUPDATE: truthy("AINN_DISABLE_AUTOUPDATE"),
  AINN_ALWAYS_NOTIFY_UPDATE: truthy("AINN_ALWAYS_NOTIFY_UPDATE"),
  AINN_DISABLE_PRUNE: truthy("AINN_DISABLE_PRUNE"),
  AINN_DISABLE_TERMINAL_TITLE: truthy("AINN_DISABLE_TERMINAL_TITLE"),
  AINN_SHOW_TTFD: truthy("AINN_SHOW_TTFD"),
  AINN_DISABLE_AUTOCOMPACT: truthy("AINN_DISABLE_AUTOCOMPACT"),
  AINN_DISABLE_MODELS_FETCH: truthy("AINN_DISABLE_MODELS_FETCH"),
  AINN_DISABLE_MOUSE: truthy("AINN_DISABLE_MOUSE"),
  AINN_FAKE_VCS: process.env["AINN_FAKE_VCS"],
  AINN_SERVER_PASSWORD: process.env["AINN_SERVER_PASSWORD"],
  AINN_SERVER_USERNAME: process.env["AINN_SERVER_USERNAME"],
  AINN_DISABLE_FFF: fff === undefined ? process.platform === "win32" : truthy("AINN_DISABLE_FFF"),
  AINN_EXPERIMENTAL_FILEWATCHER: Config.boolean("AINN_EXPERIMENTAL_FILEWATCHER").pipe(
    Config.withDefault(false),
  ),
  AINN_EXPERIMENTAL_DISABLE_FILEWATCHER: Config.boolean("AINN_EXPERIMENTAL_DISABLE_FILEWATCHER").pipe(
    Config.withDefault(false),
  ),
  AINN_EXPERIMENTAL_DISABLE_COPY_ON_SELECT:
    copy === undefined ? process.platform === "win32" : truthy("AINN_EXPERIMENTAL_DISABLE_COPY_ON_SELECT"),
  AINN_MODELS_URL: process.env["AINN_MODELS_URL"],
  AINN_MODELS_PATH: process.env["AINN_MODELS_PATH"],
  AINN_DB: process.env["AINN_DB"],
  AINN_WORKSPACE_ID: process.env["AINN_WORKSPACE_ID"],
  AINN_EXPERIMENTAL_WORKSPACES: enabledByExperimental("AINN_EXPERIMENTAL_WORKSPACES"),
  get AINN_DISABLE_PROJECT_CONFIG() {
    return truthy("AINN_DISABLE_PROJECT_CONFIG")
  },
  get AINN_EXPERIMENTAL_REFERENCES() {
    return enabledByExperimental("AINN_EXPERIMENTAL_REFERENCES")
  },
  get AINN_TUI_CONFIG() {
    return process.env["AINN_TUI_CONFIG"]
  },
  get AINN_CONFIG_DIR() {
    return process.env["AINN_CONFIG_DIR"]
  },
  get AINN_PURE() {
    return truthy("AINN_PURE")
  },
  get AINN_PERMISSION() {
    return process.env["AINN_PERMISSION"]
  },
  get AINN_PLUGIN_META_FILE() {
    return process.env["AINN_PLUGIN_META_FILE"]
  },
  get AINN_CLIENT() {
    return process.env["AINN_CLIENT"] ?? "cli"
  },
}
