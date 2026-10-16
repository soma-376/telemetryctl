import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import ts from "typescript";

test("고정된 인라인 스타일을 다시 추가하지 않는다", () => {
  const root = fileURLToPath(new URL("../src/", import.meta.url));
  const failures = [];

  for (const file of readdirSync(root, { recursive: true }).filter((file) =>
    file.endsWith(".tsx"),
  )) {
    const source = readFileSync(`${root}/${file}`, "utf8");
    const ast = ts.createSourceFile(
      file,
      source,
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );

    function visit(node) {
      if (ts.isJsxAttribute(node) && node.name.text === "style") {
        if (node.initializer && ts.isStringLiteral(node.initializer))
          failures.push(`${file}: 고정 CSS 문자열`);
        const expr =
          node.initializer && ts.isJsxExpression(node.initializer)
            ? node.initializer.expression
            : undefined;

        if (expr && ts.isObjectLiteralExpression(expr)) {
          for (const prop of expr.properties) {
            if (
              ts.isPropertyAssignment(prop) &&
              (ts.isStringLiteral(prop.initializer) ||
                ts.isNumericLiteral(prop.initializer))
            )
              failures.push(`${file}: ${prop.name.getText(ast)}`);
          }
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(ast);
  }
  assert.deepEqual(failures, []);
});
