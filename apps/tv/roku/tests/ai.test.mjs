// Epic 49 story 49.14: AI mode's help on brs-engine (source/ai.brs): the Server-Sent Events
// reader, split anywhere; each event of POST /api/search/ai into the answer's state; the bodies
// of a question and a follow-up; the refusals; the lines in English and Arabic. brs-engine's
// stdout is kept in build/logs.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, runBrs } from "./brs.mjs";

test("AI mode's help: the event reader, the answer's state, bodies, refusals, Arabic lines", () => {
  const r = runBrs("ai", [join(ROKU, "tests", "ai_test.brs"), join(ROKU, "tests", "check.brs")]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 90, `${r.pass} checks`);
});
