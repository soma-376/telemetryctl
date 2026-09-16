import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";

test("JSX 라벨에 변환 흔적의 개행과 불필요한 문자열 표현을 남기지 않는다", () => {
  const root = new URL("../src/", import.meta.url);
  const failures = [];

  for (const file of readdirSync(root, { recursive: true }).filter((file) =>
    file.endsWith(".tsx"),
  )) {
    const source = readFileSync(
      new URL(file.replaceAll("\\", "/"), root),
      "utf8",
    );
    const ast = ts.createSourceFile(
      file,
      source,
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );

    function visit(node) {
      if (
        ts.isJsxExpression(node) &&
        node.expression &&
        ts.isStringLiteral(node.expression) &&
        (ts.isJsxElement(node.parent) || ts.isJsxFragment(node.parent))
      ) {
        const text = node.expression.text;

        if (text.trim() && !/[\r\n]/.test(text.trim()))
          failures.push(`${file}: 불필요한 문자열 라벨`);
        if (!text.trim() && ts.isJsxFragment(node.parent))
          failures.push(`${file}: 분기에 삽입된 공백`);
      }
      ts.forEachChild(node, visit);
    }
    visit(ast);
  }
  assert.deepEqual(failures, []);
});
