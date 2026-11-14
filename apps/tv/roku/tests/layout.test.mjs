// Story 58.15: the layout direction helper on brs-engine (x positions, row order, start focus,
// keys and focus maps mirrored in Arabic). brs-engine's stdout is kept in build/logs.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, runBrs } from "./brs.mjs";

test("the layout mirrors x positions, row order and the focus map in Arabic", () => {
  const r = runBrs("layout", [join(ROKU, "tests", "layout_test.brs")]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 37, `${r.pass} checks`);
});
