// Epic 53 story 53.01 T01: the web's redaction passes the shared cases
// (shared/redact/cases.json), the same table every Maktaba logger passes.
import { describe, expect, it } from "vitest";
import cases from "../../../shared/redact/cases.json";
import shared from "../../../shared/redact/rules.json";
import copy from "./redactRules.json";
import { keySensitive, redactAttrs, scrub } from "./redact";

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
