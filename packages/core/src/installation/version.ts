declare global {
  const AINN_VERSION: string
  const AINN_CHANNEL: string
}

export const InstallationVersion = typeof AINN_VERSION === "string" ? AINN_VERSION : "local"
export const InstallationChannel = typeof AINN_CHANNEL === "string" ? AINN_CHANNEL : "local"
export const InstallationLocal = InstallationChannel === "local"
