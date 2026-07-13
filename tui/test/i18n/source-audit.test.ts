import { expect, test } from "bun:test"
import path from "node:path"
import ts from "typescript"
import { en } from "../../src/i18n/en"

const sourceRoot = path.resolve(import.meta.dir, "../../src")
const sourcePatterns = [
  "app.tsx",
  "routes/home/**/*.{ts,tsx}",
  "routes/session/**/*.{ts,tsx}",
  "component/dialog-*.tsx",
  "component/prompt/**/*.{ts,tsx}",
  "ui/**/*.{ts,tsx}",
  "proxy/**/*.{ts,tsx}",
]
const visibleProperties = new Set([
  "title",
  "description",
  "placeholder",
  "footer",
  "message",
  "label",
  "content",
  "pending",
  "complete",
  "failure",
  "category",
  "group",
  "desc",
  "text",
  "empty",
  "hint",
])
const englishValues = new Set<string>(Object.values(en))

test("scoped TUI surfaces contain no untranslated dictionary values", async () => {
  const files = new Set<string>()
  for (const pattern of sourcePatterns) {
    for await (const file of new Bun.Glob(pattern).scan({ cwd: sourceRoot })) files.add(path.join(sourceRoot, file))
  }

  const untranslated: string[] = []
  for (const file of [...files].sort()) {
    const source = ts.createSourceFile(
      file,
      await Bun.file(file).text(),
      ts.ScriptTarget.Latest,
      true,
      file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    )

    function record(node: ts.Node, value: string) {
      const normalized = value.replace(/\s+/g, " ").trim()
      if (!normalized || !englishValues.has(normalized)) return
      const line = source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1
      untranslated.push(`${path.relative(sourceRoot, file)}:${line}: ${normalized}`)
    }

    function visit(node: ts.Node) {
      if (ts.isJsxText(node)) record(node, node.text)

      if (
        ts.isJsxAttribute(node) &&
        ts.isIdentifier(node.name) &&
        visibleProperties.has(node.name.text) &&
        node.initializer
      ) {
        if (ts.isStringLiteral(node.initializer)) record(node, node.initializer.text)
        if (ts.isJsxExpression(node.initializer) && node.initializer.expression) {
          const expression = node.initializer.expression
          if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
            record(node, expression.text)
          }
        }
      }

      if (ts.isPropertyAssignment(node)) {
        const name = ts.isIdentifier(node.name) || ts.isStringLiteral(node.name) ? node.name.text : ""
        if (visibleProperties.has(name)) {
          const value = node.initializer
          if (ts.isStringLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value)) record(node, value.text)
        }
      }

      ts.forEachChild(node, visit)
    }

    visit(source)
  }

  expect(files.size).toBeGreaterThan(0)
  expect(untranslated).toEqual([])
})
