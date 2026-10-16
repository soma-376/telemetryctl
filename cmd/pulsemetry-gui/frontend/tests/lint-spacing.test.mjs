import assert from "node:assert/strict";
import test from "node:test";
import { ESLint } from "eslint";
import { format } from "prettier";

test("빈 줄 누락은 오류이며 자동 수정 결과를 Prettier도 유지한다", async () => {
  const code =
    "export function example() {\nconst value = 1;\nreturn value;\n}\n";
  const eslint = new ESLint();
  const [result] = await eslint.lintText(code, {
    filePath: "src/spacing-example.ts",
  });
  assert.ok(
    result.messages.some(
      (message) =>
        message.ruleId === "@stylistic/padding-line-between-statements",
    ),
  );

  const fixer = new ESLint({ fix: true });
  const [fixed] = await fixer.lintText(code, {
    filePath: "src/spacing-example.ts",
  });
  assert.ok(fixed.output);
  const formatted = await format(fixed.output, { parser: "typescript" });
  const [checked] = await eslint.lintText(formatted, {
    filePath: "src/spacing-example.ts",
  });
  assert.equal(checked.errorCount, 0);
});
