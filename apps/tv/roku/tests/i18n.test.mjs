// Story 58.15: the channel reads its generated catalogs (locale/*/strings.json) through
// source/i18n.brs: static Arabic already visual, templates filled in and shaped on the device.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, runBrs } from "./brs.mjs";

test("I18n reads the catalogs, fills templates and shapes Arabic", () => {
  const r = runBrs("i18n", [join(ROKU, "tests", "i18n_test.brs")]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 15, `${r.pass} checks`);
});
