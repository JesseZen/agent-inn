import type { Locale as LanguageLocale } from "../context/language"

const THOUSAND = 1_000
const MILLION = 1_000_000
const ELLIPSIS = "…"
const ELLIPSIS_WIDTH = Bun.stringWidth(ELLIPSIS)
const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" })

export function titlecase(str: string) {
  return str.replace(/\b\w/g, (c) => c.toUpperCase())
}

export function time(input: number, locale?: LanguageLocale): string {
  const date = new Date(input)
  return date.toLocaleTimeString(locale, { timeStyle: "short" })
}

export function datetime(input: number, locale?: LanguageLocale): string {
  const date = new Date(input)
  const localTime = time(input, locale)
  const localDate = date.toLocaleDateString(locale)
  return `${localTime} · ${localDate}`
}

export function todayTimeOrDateTime(input: number, locale?: LanguageLocale): string {
  const date = new Date(input)
  const now = new Date()
  const isToday =
    date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate()

  if (isToday) {
    return time(input, locale)
  } else {
    return datetime(input, locale)
  }
}

export function number(num: number, locale?: LanguageLocale): string {
  if (!locale) {
    if (num >= MILLION) return (num / MILLION).toFixed(1) + "M"
    if (num >= THOUSAND) return (num / THOUSAND).toFixed(1) + "K"
    return num.toString()
  }

  const formatter = new Intl.NumberFormat(locale, {
    useGrouping: false,
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  })
  if (num >= MILLION) return formatter.format(num / MILLION) + "M"
  if (num >= THOUSAND) return formatter.format(num / THOUSAND) + "K"
  return new Intl.NumberFormat(locale, { useGrouping: false, maximumFractionDigits: 20 }).format(num)
}

export function currency(input: number, locale?: LanguageLocale): string {
  return new Intl.NumberFormat(locale, { style: "currency", currency: "USD" }).format(input)
}

export function duration(input: number) {
  if (input < 1000) {
    return `${input}ms`
  }
  if (input < 60000) {
    return `${(input / 1000).toFixed(1)}s`
  }
  if (input < 3600000) {
    const minutes = Math.floor(input / 60000)
    const seconds = Math.floor((input % 60000) / 1000)
    return `${minutes}m ${seconds}s`
  }
  if (input < 86400000) {
    const hours = Math.floor(input / 3600000)
    const minutes = Math.floor((input % 3600000) / 60000)
    return `${hours}h ${minutes}m`
  }
  const hours = Math.floor(input / 3600000)
  const days = Math.floor((input % 3600000) / 86400000)
  return `${days}d ${hours}h`
}

export function truncate(str: string, len: number): string {
  if (Bun.stringWidth(str) <= len) return str
  if (len < ELLIPSIS_WIDTH) return ""

  const widthLimit = len - ELLIPSIS_WIDTH
  let width = 0
  let result = ""
  for (const part of graphemes.segment(str)) {
    const nextWidth = width + Bun.stringWidth(part.segment)
    if (nextWidth > widthLimit) break
    result += part.segment
    width = nextWidth
  }
  return result + ELLIPSIS
}

export function truncateLeft(str: string, len: number): string {
  if (Bun.stringWidth(str) <= len) return str
  if (len < ELLIPSIS_WIDTH) return ""

  const widthLimit = len - ELLIPSIS_WIDTH
  const parts = Array.from(graphemes.segment(str), (part) => part.segment)
  let width = 0
  let result = ""
  for (let index = parts.length - 1; index >= 0; index--) {
    const nextWidth = width + Bun.stringWidth(parts[index])
    if (nextWidth > widthLimit) break
    result = parts[index] + result
    width = nextWidth
  }
  return ELLIPSIS + result
}

export function truncateMiddle(str: string, maxLength: number = 35): string {
  if (Bun.stringWidth(str) <= maxLength) return str
  if (maxLength < ELLIPSIS_WIDTH) return ""

  const availableWidth = maxLength - ELLIPSIS_WIDTH
  const startWidthLimit = Math.ceil(availableWidth / 2)
  const endWidthLimit = Math.floor(availableWidth / 2)
  const parts = Array.from(graphemes.segment(str), (part) => part.segment)
  let startWidth = 0
  let start = ""
  for (const part of parts) {
    const nextWidth = startWidth + Bun.stringWidth(part)
    if (nextWidth > startWidthLimit) break
    start += part
    startWidth = nextWidth
  }

  let endWidth = 0
  let end = ""
  for (let index = parts.length - 1; index >= 0; index--) {
    const nextWidth = endWidth + Bun.stringWidth(parts[index])
    if (nextWidth > endWidthLimit) break
    end = parts[index] + end
    endWidth = nextWidth
  }
  return start + ELLIPSIS + end
}

export function pluralize(count: number, singular: string, plural: string): string {
  const template = count === 1 ? singular : plural
  return template.replace("{}", count.toString())
}

export * as Locale from "./locale"
