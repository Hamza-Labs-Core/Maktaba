// Epic 53 story 53.01 T01: the web's redaction passes the shared cases
// (shared/redact/cases.json), the same table every Maktaba logger passes.
import { describe, expect, it } from "vitest";
import copy from "./redactRules.json";
import { keySensitive, redactAttrs, scrub } from "./redact";

// the shared files are outside src/ (the web's image builds from web/ alone): read them at
// run time, as stageWords.test.ts reads the graph
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const cases = JSON.parse(readFileSync("../shared/redact/cases.json", "utf8")) as {
  scrub: { in: string; out: string }[];
  keys: { key: string; string: boolean; masked: boolean }[];
};
const shared: unknown = JSON.parse(readFileSync("../shared/redact/rules.json", "utf8"));

describe("redact (shared rules)", () => {
  it("is the shared file's copy", () => {
    expect(copy).toEqual(shared);
  });

  for (const c of cases.scrub) {
    it(`scrubs ${JSON.stringify(c.in).slice(0, 50)}`, () => {
      expect(scrub(c.in)).toBe(c.out);
    });
  }

  it("masks the shared keys", () => {
    for (const c of cases.keys) {
      expect(keySensitive(c.key, c.string), c.key).toBe(c.masked);
    }
  });

  it("masks attributes by key and scrubs strings", () => {
    expect(redactAttrs({ token: "abc", n: 3, url: "/a?sig=zz" })).toEqual({
      token: "***REDACTED***",
      n: 3,
      url: "/a?sig=[REDACTED]",
    });
  });
});
