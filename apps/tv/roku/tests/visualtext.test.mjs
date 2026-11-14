// Story 58.15 T03 and T04: VisualText on brs-engine against shared/i18n/visual-vectors.json
// (the reference's outputs, apps/tv/roku/tools/gen_vectors.py), and its cost on 20 long
// descriptions. brs-engine's stdout is kept in build/logs.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { BRS_HELPERS, ROKU, brsJson, runBrs, writeTest } from "./brs.mjs";

const VECTORS = JSON.parse(readFileSync(join(ROKU, "..", "..", "..", "shared", "i18n", "visual-vectors.json"), "utf8"));

test("the vector file is there and large enough to mean something", () => {
  assert.ok(VECTORS.vectors.length >= 300, `${VECTORS.vectors.length} vectors`);
  const cats = new Set(VECTORS.vectors.map((v) => v.id.split(".")[0]));
  for (const c of ["join", "lamalef", "tashkeel", "digits", "time", "episode", "mixed", "punct", "trunc", "wrap", "fallback", "title"]) {
    assert.ok(cats.has(c), `no ${c} vectors`);
  }
});

test("VisualText matches the reference on every vector (T03)", () => {
  const pushes = VECTORS.vectors.map((v) => `    vs.Push(${brsJson(v)})`).join("\n");
  const code = `
sub Main()
    vs = []
${pushes}
    pass = 0
    fail = 0
    for each v in vs
        base = t_Str(v.base, "auto")
        if v.lines <> invalid then
            got = VisualLines(v.input, v.widthChars, t_Int(v.maxLines, 0), base)
            ok = got.Count() = v.lines.Count()
            if ok then
                for i = 0 to got.Count() - 1
                    if got[i] <> v.lines[i] then ok = false
                end for
            end if
            if ok then
                pass = pass + 1
            else
                fail = fail + 1
                print "FAIL "; v.id; ": got "; t_Lines(got); ", want "; t_Lines(v.lines)
            end if
        else
            got = VisualTextDir(v.input, t_Int(v.maxWidthChars, 0), base)
            if got = v.visual then
                pass = pass + 1
            else
                fail = fail + 1
                print "FAIL "; v.id; ": got ["; t_Hex(got); "], want ["; t_Hex(v.visual); "]"
            end if
        end if
    end for
    ' the convenience forms agree with the full ones
    if VisualText("الحلقة 3 — Intro", 0) <> VisualTextDir("الحلقة 3 — Intro", 0, "auto") then
        fail = fail + 1
        print "FAIL VisualText differs from VisualTextDir auto"
    end if
    if not VisualIsRtl("الحلقة 3 — Intro") or VisualIsRtl("Intro — الحلقة 3") or VisualIsRtl("123") then
        fail = fail + 1
        print "FAIL VisualIsRtl"
    else
        pass = pass + 1
    end if
    print "RESULT pass="; pass; " fail="; fail
end sub
${BRS_HELPERS}`;
  const r = runBrs("visualtext", [writeTest("visualtext_test", code)]);
  assert.deepEqual(r.failures, [], r.failures.slice(0, 40).join("\n"));
  assert.equal(r.fail, 0);
  assert.equal(r.pass, VECTORS.vectors.length + 1);
});

test("an unsupported string is logged and shown in logical order, without a crash", () => {
  const code = `
sub Main()
    s = "پرسپولیس"
    got = VisualText(s, 0)
    if got = s then print "RESULT pass=1 fail=0" else print "FAIL not logical" : print "RESULT pass=0 fail=1"
end sub
${BRS_HELPERS}`;
  const r = runBrs("visualtext-fallback", [writeTest("visualtext_fallback", code)]);
  assert.deepEqual(r.failures, []);
  assert.equal(r.pass, 1);
  assert.match(r.out, /\[visualtext\] shown in logical order: U\+67E is outside the shaping table/);
});

// T04: 20 long descriptions shaped as one screen, timed and logged (build/logs, and the test
// output). Story 58.15's budget is 50 ms a screen, re-measured on a Roku Express
// (docs/stores/roku.md, row 10). brs-engine is not that machine: a statement costs about 0.6 µs
// and a function call about 10 µs there, and 20 descriptions of 229 characters take about
// 320 ms on a developer Mac. The assertion is a regression ceiling (2 s) that an accidental
// quadratic step or a per-character call would break; the time itself is in the log.
test("shaping 20 long descriptions: timed, under the regression ceiling on brs-engine (T04)", () => {
  const desc =
    "في الحلقة 3 من الموسم 2 (S2 E5)، يعود المحقق إلى المدينة بعد غياب طويل ليكتشف أن كل شيء قد تغيّر: الأصدقاء، الأعداء، وحتى البيت القديم. تبدأ القصة الساعة 12:34 مساءً، وIntro الحلقة يحمل مفاجأة لم يتوقعها أحد. مدة الحلقة 45 دقيقة.";
  const code = `
sub Main()
    d = ${brsJson(desc)}
    descs = []
    for i = 1 to 20
        descs.Push(d + " " + i.ToStr())
    end for
    VisualLines(d, 60, 4, "auto") ' warm the tables, as the channel does once at start
    ts = CreateObject("roTimespan")
    ts.Mark()
    for each s in descs
        VisualLines(s, 60, 4, "auto")
    end for
    ms = ts.TotalMilliseconds()
    print "TIMING 20 descriptions of "; Len(d); " characters: "; ms; " ms"
    if ms <= 2000 then print "RESULT pass=1 fail=0" else print "FAIL "; ms; " ms" : print "RESULT pass=0 fail=1"
end sub
${BRS_HELPERS}`;
  const r = runBrs("visualtext-timing", [writeTest("visualtext_timing", code)]);
  console.log(r.out.split("\n").find((l) => l.startsWith("TIMING")));
  assert.deepEqual(r.failures, []);
  assert.equal(r.pass, 1);
});
