// Epic 49 story 49.11: Search's help on brs-engine (source/search.brs): request bodies from the
// order and facet choices, suggestion kinds, the answer's new fields and an older home without
// them, and the lines the screen shows in English and Arabic. brs-engine's stdout is kept in
// build/logs.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, runBrs } from "./brs.mjs";

test("Search's help: bodies, suggestions, did-you-mean, facets, Arabic lines", () => {
  const r = runBrs("search", [join(ROKU, "tests", "search_test.brs"), join(ROKU, "tests", "check.brs")]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 60, `${r.pass} checks`);
});
