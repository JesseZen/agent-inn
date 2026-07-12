import { createSignal } from "solid-js"
import { createSimpleContext } from "./helper"
import { useKV } from "./kv"
import { en, type TranslationKey, type TranslationParams } from "../i18n/en"
import { zhCN } from "../i18n/zh-CN"

export const locales = ["en", "zh-CN"] as const
export type Locale = (typeof locales)[number]

export const localeLabels: Record<Locale, string> = {
  en: "English",
  "zh-CN": "简体中文",
}

const LOCALE_KV_KEY = "locale"

export function detectLocale(value: string | undefined): Locale | undefined {
  if (!value) return undefined
  if (value.startsWith("zh")) return "zh-CN"
  if (value.startsWith("en")) return "en"
  return undefined
}

export function selectInitialLocale(
  persisted: string | undefined,
  environment: Record<string, string | undefined> = process.env,
): Locale {
  const persistedLocale = detectLocale(persisted)
  if (persistedLocale) return persistedLocale

  for (const name of ["LC_ALL", "LC_MESSAGES", "LANG"]) {
    const detected = detectLocale(environment[name])
    if (detected) return detected
  }
  return "en"
}

export function interpolate(template: string, params?: TranslationParams): string {
  if (!params) return template
  return template.replace(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g, (placeholder, name: string) => {
    const value = params[name]
    return value === undefined ? placeholder : String(value)
  })
}

function hasKey(dictionary: Record<string, string>, key: string): key is TranslationKey {
  return Object.hasOwn(dictionary, key)
}

export function translate(locale: Locale, key: string, params?: TranslationParams): string {
  const template = locale === "zh-CN" && hasKey(zhCN, key) ? zhCN[key] : hasKey(en, key) ? en[key] : key
  return interpolate(template, params)
}

export const { use: useLanguage, provider: LanguageProvider } = createSimpleContext({
  name: "Language",
  init: () => {
    const kv = useKV()
    const persisted = kv.get(LOCALE_KV_KEY)
    const [locale, setLocale] = createSignal(selectInitialLocale(typeof persisted === "string" ? persisted : undefined))

    return {
      get locale() {
        return locale()
      },
      locales,
      labels: localeLabels,
      t(key: TranslationKey, params?: TranslationParams) {
        return translate(locale(), key, params)
      },
      setLocale(next: Locale) {
        setLocale(next)
        kv.set(LOCALE_KV_KEY, next)
      },
    }
  },
})
