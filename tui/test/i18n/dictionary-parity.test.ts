import { describe, expect, test } from "bun:test"
import { en, type TranslationKey } from "../../src/i18n/en"
import { zhCN } from "../../src/i18n/zh-CN"

function placeholders(value: string) {
  return [...value.matchAll(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g)].map((match) => match[1]).sort()
}

describe("translation dictionaries", () => {
  test("Chinese has exactly the English keys and named placeholders", () => {
    expect(Object.keys(zhCN).sort()).toEqual(Object.keys(en).sort())

    for (const key of Object.keys(en) as TranslationKey[]) {
      expect(placeholders(zhCN[key])).toEqual(placeholders(en[key]))
    }
  })
})
