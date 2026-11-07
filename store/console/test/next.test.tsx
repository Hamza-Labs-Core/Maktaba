// `/signin?next=` (story 54.19 §5): sign-in sends the buyer back only to a page of this store
// that sends people there (a buy page, the admin pages); anything else is the safe default.
import { describe, expect, it } from "vitest";
import { NEXT_MAX, safeNext } from "../src/next";

const STORE = "https://plugins.maktaba.tv";

describe("safeNext", () => {
  it("keeps a buy page and the admin pages", () => {
    expect(safeNext("/buy/dev.example.loudness", STORE)).toBe("/buy/dev.example.loudness");
    expect(safeNext("/admin", STORE)).toBe("/admin");
    expect(safeNext("/admin/queue", STORE)).toBe("/admin/queue");
  });

  it.each([
    ["a backslash after the slash", "/\\evil.example"],
    ["protocol-relative", "//evil.example"],
    ["two backslashes", "\\\\evil.example"],
    ["absolute", "https://evil.example"],
    ["absolute to the store itself", "https://plugins.maktaba.tv/buy/x"],
    ["encoded slashes", "/%2F%2Fevil.example"],
    ["an encoded backslash", "/%5Cevil.example"],
    ["a script URL", "javascript:alert(1)"],
    ["a script URL after a slash", "/javascript:alert(1)"],
    ["empty", ""],
    ["a tab after the slash", "/\t/evil.example"],
    ["a newline", "/buy/x\n//evil.example"],
    ["another store page", "/account"],
    ["the root", "/"],
    ["a buy page's neighbour", "/buyer/evil"],
    ["a path climbing out", "/buy/../account"],
    ["very long", `/buy/${"a".repeat(NEXT_MAX)}`],
    ["very long and otherwise fine", `/admin/${"a".repeat(NEXT_MAX)}`],
  ])("refuses %s", (_, raw) => {
    expect(safeNext(raw, STORE)).toBeNull();
  });

  it("refuses what is not a string", () => {
    expect(safeNext(null, STORE)).toBeNull();
    expect(safeNext(undefined, STORE)).toBeNull();
  });
});
