// Story 58.12: the code screen's QR (source/qr.brs) is the web app's encoder (web/src/lib/qr.ts)
// ported to BrightScript. This test runs both on the same texts (the cloud's and a home's verify
// URLs, and texts at each version's limit, up to version 10 where the version bits appear) and
// asks for the same modules, row by row. brs-engine's stdout is kept in build/logs/qr.log.

import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { ROKU, brsJson, runBrs, writeTest } from "./brs.mjs";

const { encodeQR } = await import(join(ROKU, "..", "..", "..", "web", "src", "lib", "qr.ts"));

const rows = (m) => m.map((r) => r.map((d) => (d ? "1" : "0")).join(""));

// a text of n bytes that reads as an address
const sized = (n) => `https://maktaba.tv/tv?c=${"A".repeat(Math.max(0, n - 24))}`.slice(0, n);

const TEXTS = [
  "https://maktaba.tv/tv?c=ABCD2345",
  "http://192.168.1.20:8088/pair?code=HKMN4567",
  "https://1-2-3-4.0f3c9a2e.maktaba.tv/pair?code=HKMN4567",
  "M",
  // the largest text of versions 1 to 9 at level M, and one byte more
  ...[14, 26, 42, 62, 84, 106, 122, 152, 180].flatMap((n) => [sized(n), sized(n + 1)]),
  sized(213),
];

test("the code screen's QR matches the web app's encoder, module for module", () => {
  const cases = TEXTS.map((text) => ({ text, rows: rows(encodeQR(text)) }));
  const code = `sub Main()
    m.pass = 0
    m.fail = 0
    cases = ${brsJson(cases)}
    for each c in cases
        got = QrEncode(c.text)
        if got = invalid then
            ck_True(c.text + " encodes", false)
        else
            ck_Eq(c.text + " size", got.Count(), c.rows.Count())
            same = true
            for y = 0 to got.Count() - 1
                line = ""
                for each d in got[y]
                    if d then line = line + "1" else line = line + "0"
                end for
                if line <> c.rows[y] then
                    same = false
                    print "FAIL " + c.text + " row "; y
                    print "  got  " + line
                    print "  want " + c.rows[y]
                    exit for
                end if
            end for
            ck_True(c.text + " modules", same)
        end if
    end for
    ck_Eq("past version 10", QrEncode(String(214, "A")), invalid)
    ck_Result()
end sub
`;
  const r = runBrs("qr", [writeTest("qr_test", code), join(ROKU, "tests", "check.brs")]);
  assert.deepEqual(r.failures, [], r.failures.join("\n"));
  assert.equal(r.fail, 0);
  assert.equal(r.pass, TEXTS.length * 2 + 1);
  // the cloud's verify URL is a version 3 symbol (29 modules), which the screen draws at 12 px
  assert.equal(cases[0].rows.length, 29);
});
