import { describe, expect, it } from "vitest";
import { STRINGS } from "./strings";

describe("the install page's strings", () => {
  it("has every key in English and Arabic", () => {
    expect(Object.keys(STRINGS.ar).sort()).toEqual(Object.keys(STRINGS.en).sort());
  });

  it("has Arabic text in every Arabic string", () => {
    for (const [k, v] of Object.entries(STRINGS.ar)) {
      if (k === "langLink") continue;
      expect(/[؀-ۿ]/.test(v), k).toBe(true);
    }
  });

  // found by following the guide on Docker: setup only fills the folder list (libraries are
  // added in Manage), it downloads its tools, and a Docker volume is only uid 1000's
  it("says what comes after setup, as the app names it", () => {
    expect(STRINGS.en.next).toContain("Manage → Manage libraries → Add library");
    expect(STRINGS.ar.next).toContain("الإدارة ← إدارة المكتبات ← إضافة مكتبة");
    expect(STRINGS.en.next).toContain("internet");
    expect(STRINGS.en["problem.data-volume-user"]).toContain("1000");
    expect(STRINGS.ar["problem.data-volume-user"]).toContain("1000");
  });
});
