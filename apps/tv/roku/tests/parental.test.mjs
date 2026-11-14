// Story 58.14 T04 (the logic half): source/parental.brs on brs-engine, in English and Arabic.
// brs-engine's stdout is kept in build/logs.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, runBrs } from "./brs.mjs";

test("the parental limit, its lines and the account's requests", () => {
  const r = runBrs("parental", [join(ROKU, "tests", "parental_test.brs"), join(ROKU, "tests", "check.brs")]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 47, `${r.pass} checks`);
});
