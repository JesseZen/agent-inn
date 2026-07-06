export type PluginOptions = Record<string, unknown>

export type Config = {
  $schema?: string
  theme?: string
  plugin?: unknown
  tui?: {
    scroll_speed?: number
    scroll_acceleration?: {
      enabled: boolean
    }
    diff_style?: "auto" | "stacked"
  }
  [key: string]: unknown
}
