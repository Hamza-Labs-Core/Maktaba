// Story 58.15 T03 and T04: VisualText on brs-engine against shared/i18n/visual-vectors.json
// (the reference's outputs, apps/tv/roku/tools/gen_vectors.py), its ASCII fast path against
// its full steps (T04, its cost, is tests/timing/t04.test.mjs). brs-engine's stdout is kept in
// build/logs.

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

test("printable ASCII takes the fast path and gives what the full steps give", () => {
  // vt_AsciiCut and vt_AsciiLines against the full steps (vt_Prepare, vt_Wrap, vt_FitLines,
  // vt_Shape, vt_Order), which VisualText and VisualLines skip for printable ASCII that is not
  // forced right to left
  const strings = [
    "",
    " ",
    "Intro",
    "The Long Night of the Detective",
    "  two  spaces   between   words  ",
    "Supercalifragilisticexpialidocious is long",
    "a b c d e f g h i j k l m n o p",
    "S2 E5 (12:34) - 45 min, [extra] {cut}",
    "trailing space at the cut      here and more words",
  ];
  const code = `
sub Main()
    strs = ${brsJson(strings)}
    pass = 0
    fail = 0
    for each s in strs
        for each w in [0, 1, 2, 5, 8, 12, 40]
            t = vt_Ellipsize(vt_Prepare(s, false), w)
            want = vt_Order(vt_Shape(t, [[0, t.chars.Count()]], false), "ltr")
            got = VisualText(s, w)
            if got = want then pass = pass + 1 else fail = fail + 1 : print "FAIL cut "; w; " ["; s; "]: got ["; got; "], want ["; want; "]"
            for each lines in [0, 1, 2, 3]
                t = vt_Prepare(s, false)
                wantLines = []
                for each line in vt_FitLines(vt_Wrap(t, w), w, lines)
                    wantLines.Push(vt_Order(vt_Shape(t, line.words, line.ellipsis), "ltr"))
                end for
                gotLines = VisualLines(s, w, lines, "auto")
                if FormatJson(gotLines) = FormatJson(wantLines) then
                    pass = pass + 1
                else
                    fail = fail + 1
                    print "FAIL lines "; w; "x"; lines; " ["; s; "]: got "; FormatJson(gotLines); ", want "; FormatJson(wantLines)
                end if
            end for
        end for
    end for
    if VisualIsRtl("Intro 3") then fail = fail + 1 : print "FAIL VisualIsRtl ASCII" else pass = pass + 1
    print "RESULT pass="; pass; " fail="; fail
end sub
${BRS_HELPERS}`;
  const r = runBrs("visualtext-ascii", [writeTest("visualtext_ascii", code)]);
  assert.deepEqual(r.failures, [], r.failures.slice(0, 20).join("\n"));
  assert.equal(r.pass, strings.length * 7 * 5 + 1);
});
