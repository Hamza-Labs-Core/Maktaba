// Story 58.12 T01: the channel's logic on brs-engine (API client and token refresh, reaching a
// home and the subnet probe, the compat check against 58.01's shared vectors, the deep-link
// router, Back's steps and the grid's memory window). brs-engine's stdout is kept in build/logs.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, brsJson, runBrs, writeTest } from "./brs.mjs";

const CHECK = join(ROKU, "tests", "check.brs");

test("the channel's logic: API client, refresh, probe, deep links, Back, grid window", () => {
  const r = runBrs("channel", [join(ROKU, "tests", "channel_test.brs"), CHECK]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 120, `${r.pass} checks`);
});

test("the compat check reaches every verdict of shared/compat/vectors.json (58.01)", () => {
  const vectors = JSON.parse(readFileSync(join(ROKU, "..", "..", "..", "shared", "compat", "vectors.json"), "utf8"));
  assert.ok(vectors.cases.length >= 10);
  const code = `sub Main()
    m.pass = 0
    m.fail = 0
    cases = ${brsJson(vectors.cases)}
    for each c in cases
        ck_Eq(c.name, CompatDecide(c.home, c.app), c.verdict)
    end for
    ck_Eq("1.10.0 above 1.9.0", CompatCompareVersions("1.10.0", "1.9.0"), 1)
    ck_Eq("unreadable", CompatCompareVersions("dev", "1.0.0"), invalid)
    app = CompatApp()
    ck_Eq("the channel is tv/roku", app.name + "/" + app.platform, "tv/roku")
    ck_Result()
end sub
`;
  const r = runBrs("compat", [writeTest("compat_test", code), CHECK]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.equal(r.pass, vectors.cases.length + 3);
});
