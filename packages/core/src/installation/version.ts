declare global {
  const CODEX_PROXY_VERSION: string
  const CODEX_PROXY_CHANNEL: string
}

export const InstallationVersion = typeof CODEX_PROXY_VERSION === "string" ? CODEX_PROXY_VERSION : "local"
export const InstallationChannel = typeof CODEX_PROXY_CHANNEL === "string" ? CODEX_PROXY_CHANNEL : "local"
export const InstallationLocal = InstallationChannel === "local"
