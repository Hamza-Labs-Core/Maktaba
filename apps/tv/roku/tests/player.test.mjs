// Story 58.13: the player's logic on brs-engine. T01: the capability report built from each Roku
// model's recorded roDeviceInfo answers is 58.02's vector (shared/playback/caps-vectors.json).
// T02: an Arabic cue goes to the channel's own layer, shaped as 58.15's reference shapes it
// (shared/i18n/visual-vectors.json); an English one to Roku's renderer. The rest of the player's
// logic and T03's deep links are in tests/player_test.brs. brs-engine's output is kept in
// build/logs.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, brsJson, runBrs, writeTest } from "./brs.mjs";

const CHECK = join(ROKU, "tests", "check.brs");
const REPO = join(ROKU, "..", "..", "..");

test("the player's logic: format, subtitles, cues, Up next, retry, queue, trickplay, deep links", () => {
  const r = runBrs("player", [join(ROKU, "tests", "player_test.brs"), CHECK]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.ok(r.pass >= 60, `${r.pass} checks`);
});

test("T01: device_caps from three Roku models' roDeviceInfo answers are 58.02's vectors", () => {
  const file = JSON.parse(readFileSync(join(REPO, "shared", "playback", "caps-vectors.json"), "utf8"));
  const roku = file.vectors.filter((v) => v.profile === "roku");
  assert.ok(roku.length >= 3, "three Roku models in the vectors");
  const code = `sub Main()
    m.pass = 0
    m.fail = 0
    vectors = ${brsJson(roku.map((v) => ({ id: v.id, answers: v.answers })))}
    for each v in vectors
        print "CAPS "; v.id; " "; FormatJson(CapsFromAnswers(v.answers))
        m.pass = m.pass + 1
    end for
    ck_Result()
end sub
`;
  const r = runBrs("caps", [writeTest("caps_test", code), CHECK]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  const got = Object.fromEntries([...r.out.matchAll(/^CAPS (\S+) (.*)$/gm)].map((m) => [m[1], JSON.parse(m[2])]));
  for (const v of roku) {
    assert.ok(got[v.id], `${v.id}: no report (see build/logs/caps.log)`);
    assert.deepEqual(got[v.id], v.device_caps, `${v.id}: the report differs from the vector`);
  }
});

test("T02: an Arabic cue goes to the channel's own layer in visual order; English to Roku's", () => {
  const vectors = JSON.parse(readFileSync(join(REPO, "shared", "i18n", "visual-vectors.json"), "utf8")).vectors;
  const pick = (id) => vectors.find((v) => v.id === id);
  const a = pick("time.07");
  const b = pick("mixed.04");
  assert.ok(a && b, "the vectors this test reads");
  const vtt = `WEBVTT\n\n00:00:00.000 --> 00:00:04.000\n${a.input}\n<b>${b.input}</b>\n\n00:00:05.000 --> 00:00:06.000\nHello there\n`;
  const code = `sub Main()
    m.pass = 0
    m.fail = 0
    cues = VttParse(${brsJson(vtt)})
    ck_Eq("two cues", cues.Count(), 2)
    ck_Eq("Arabic: own layer", PlayerSubtitleMode("ar"), "own")
    ck_Eq("English: Roku's renderer", PlayerSubtitleMode("en"), "roku")
    ck_Eq("the Arabic cue, each line shaped, its first line on top", PlayerCueLines(VttCueAt(cues, 1), 60), ${brsJson(a.visual + "\n" + b.visual)})
    ck_Eq("an English cue is left as it is", PlayerCueLines(VttCueAt(cues, 5.5), 60), "Hello there")
    ck_Result()
end sub
`;
  const r = runBrs("player_cues", [writeTest("player_cues_test", code), CHECK]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.equal(r.pass, 5);
});
