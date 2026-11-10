// The TrueNAS guide (Epic 59 story 59.10): /install/truenas and /ar/install/truenas are twins.
// Both name the same steps and the same setup command, the Arabic page is right to left and
// mostly Arabic, each TrueNAS screenshot still to come is a marked placeholder in both
// languages, and each page links to the other.
import { describe, expect, it } from "vitest";
import en from "../site/install/truenas/index.html?raw";
import ar from "../site/ar/install/truenas/index.html?raw";

const count = (s: string, re: RegExp) => (s.match(re) ?? []).length;
const shots = (s: string) => [
  ...s.matchAll(/data-shot="screenshots\/truenas\/([^"]+)\.(en|ar)\.png"/g),
];

describe("the TrueNAS guide", () => {
  it("is a pair of pages, each linking to the other", () => {
    expect(en).toMatch(/<html lang="en" dir="ltr">/);
    expect(ar).toMatch(/<html lang="ar" dir="rtl">/);
    expect(en).toContain('href="/ar/install/truenas/"');
    expect(ar).toContain('href="/install/truenas/"');
  });

  it("has the same sections and the same setup command in both languages", () => {
    expect(count(en, /<h2>/g)).toBeGreaterThanOrEqual(5);
    expect(count(ar, /<h2>/g)).toBe(count(en, /<h2>/g));
    const cmd = "/opt/maktaba/server/bin/maktaba-server container setup";
    expect(en).toContain(cmd);
    expect(ar).toContain(cmd);
    for (const s of [en, ar]) {
      expect(s).toContain("maktaba-truenas-custom-app.yaml");
      expect(s).toContain("568");
    }
  });

  it("marks each screenshot still to come, the same ones in both languages", () => {
    const e = shots(en);
    const a = shots(ar);
    expect(e.length).toBeGreaterThan(0);
    expect(e.every((m) => m[2] === "en")).toBe(true);
    expect(a.every((m) => m[2] === "ar")).toBe(true);
    expect(a.map((m) => m[1])).toEqual(e.map((m) => m[1]));
    expect(count(en, /class="placeholder"/g)).toBe(e.length);
  });

  it("is mostly Arabic in Arabic", () => {
    const text = ar
      .replace(/<(pre|code)>[\s\S]*?<\/\1>/g, "")
      .replace(/<[^>]+>/g, " ")
      .replace(/&[a-z]+;/g, " ");
    const arabic = count(text, /[؀-ۿ]/g);
    const latin = count(text, /[A-Za-z]/g);
    expect(arabic / (arabic + latin)).toBeGreaterThan(0.6);
  });
});
