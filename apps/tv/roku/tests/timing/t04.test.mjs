// Story 58.15 T04: tests/timing_test.brs shapes 20 long descriptions as one screen and prints
// their cost next to a reference loop's, both the best of 5 runs, interleaved (the log keeps the
// line; the device-test package prints the same line on a Roku Express for docs/stores/roku.md
// row 10).
//
// brs-engine's speed changes from one machine to the next, so the budget is in units of that
// loop, whose statements are of the kinds the shaping code runs (array reads and pushes, string
// comparisons, a for each). It runs alone, after the other test files (package.json's test
// script): node --test runs files side by side, and on a CI runner the screens tests took enough
// of the CPU to move this ratio by a third. Measured in the Linux container, arm64 and x64 alike:
// about 1.65 loops now, 2.7 before the hot paths were reworked (#1347). The ceiling, 2.2, leaves
// a third for a runner's noise and fails on a return to the old cost, a quadratic step or a call
// per character.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, runBrs } from "../brs.mjs";

const T04_MAX_UNITS = 2.2;

test(`shaping 20 long descriptions costs at most ${T04_MAX_UNITS} reference loops on brs-engine (T04)`, () => {
  const r = runBrs("visualtext-timing", [join(ROKU, "tests", "timing_test.brs"), join(ROKU, "tests", "check.brs")]);
  const line = r.out.split("\n").find((l) => l.startsWith("TIMING"));
  console.log(line);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  const m = /TIMING shape=(\d+) loop=(\d+)/.exec(line ?? "");
  assert.ok(m, `no TIMING line in build/logs/visualtext-timing.log:\n${r.out.slice(-2000)}`);
  const shape = Number(m[1]);
  const loop = Number(m[2]);
  assert.ok(loop > 0, line);
  assert.ok(shape / loop <= T04_MAX_UNITS, `${line}: ${(shape / loop).toFixed(2)} units, over ${T04_MAX_UNITS}`);
});
