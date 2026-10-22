import { normalizeFencedCodeInLists } from "./markdown-normalize.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("pads an unindented body inside a list item's fenced block", () => {
  const input = [
    "2. 构造请求并发送：",
    "   ```bash",
    "   TS=$(date +%s)",
    '   SIGN=$(python3 -c "',
    "import hashlib",
    "print(hashlib.md5(s).hexdigest())",
    '")"',
    "   ```",
    "3. 期望结果：HTTP 200",
  ].join("\n");

  const out = normalizeFencedCodeInLists(input).split("\n");

  assert.equal(out[0], "2. 构造请求并发送：");
  assert.equal(out[1], "   ```bash");
  // Body lines are padded to the fence's indent so the list item doesn't end.
  assert.equal(out[4], "   import hashlib");
  assert.equal(out[5], "   print(hashlib.md5(s).hexdigest())");
  assert.equal(out[6], '   ")"');
  assert.equal(out[7], "   ```");
  assert.equal(out[8], "3. 期望结果：HTTP 200");
});

test("leaves a correctly indented block unchanged", () => {
  const input = ["1. 步骤", "   ```bash", "   echo ok", "   ```"].join("\n");
  assert.equal(normalizeFencedCodeInLists(input), input);
});

test("leaves column-0 fences unchanged", () => {
  const input = ["```bash", "echo ok", "```"].join("\n");
  assert.equal(normalizeFencedCodeInLists(input), input);
});

test("does not pad lines that are already deeper than the fence", () => {
  const input = ["1. 步骤", "   ```js", "     if (x) {", "   ```"].join("\n");
  assert.equal(normalizeFencedCodeInLists(input), input);
});

test("handles tilde fences and a shallower closing fence", () => {
  const input = ["1. 步骤", "   ~~~", "code at column 0", "~~~", "after"].join("\n");
  const out = normalizeFencedCodeInLists(input).split("\n");
  assert.equal(out[2], "   code at column 0");
  assert.equal(out[3], "   ~~~");
  assert.equal(out[4], "after");
});

test("pads an unclosed fence through the end of the document", () => {
  const input = ["1. 步骤", "   ```bash", "line one", "line two"].join("\n");
  const out = normalizeFencedCodeInLists(input).split("\n");
  assert.equal(out[2], "   line one");
  assert.equal(out[3], "   line two");
});

test("returns text without fences untouched", () => {
  const input = "# 标题\n\n普通段落。";
  assert.equal(normalizeFencedCodeInLists(input), input);
});
