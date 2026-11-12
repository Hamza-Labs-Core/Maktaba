// @vitest-environment node
// Story 60.11 T06's half that needs no browser: the download page's head script
// (download-platform.ts), run as the site serves it (its types stripped, a classic script) against
// a stub page: which platform it marks on <html>, for which address and user agent, and that it
// moves that card first once the page is parsed. e2e/download.spec.ts runs it in a browser.
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { stripTypeScriptTypes } from "node:module";
import { describe, expect, it } from "vitest";

const JS = stripTypeScriptTypes(
  fs.readFileSync(path.join(__dirname, "download-platform.ts"), "utf8")
);

const UA = {
  mac: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
  windows:
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36",
  linux:
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36",
  iphone:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
  android:
    "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Mobile Safari/537.36",
  chromeos:
    "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36",
};

/** Runs the script on a stub page whose list holds `ids`; returns what it marked and the order. */
interface Run {
  ua: string;
  hash?: string;
  touch?: number;
  cards?: string;
  ids?: string[];
}
function run({
  ua,
  hash = "",
  touch = 0,
  cards = "mac windows linux",
  ids = ["mac", "windows", "linux", "nas"],
}: Run) {
  const attrs: Record<string, string> = {};
  const listeners: Array<() => void> = [];
  const list = {
    children: ids.map((id) => ({ id }) as { id: string; parentElement?: unknown }),
    get firstElementChild() {
      return this.children[0];
    },
    insertBefore(el: { id: string }, ref: { id: string }) {
      this.children.splice(this.children.indexOf(el), 1);
      this.children.splice(this.children.indexOf(ref), 0, el);
    },
  };
  for (const c of list.children) c.parentElement = list;
  const document = {
    currentScript: { getAttribute: (n: string) => (n === "data-cards" ? cards : null) },
    documentElement: { setAttribute: (n: string, v: string) => (attrs[n] = v) },
    getElementById: (id: string) => list.children.find((c) => c.id === id) ?? null,
    addEventListener: (type: string, f: () => void) =>
      type === "DOMContentLoaded" && listeners.push(f),
  };
  vm.runInNewContext(JS, {
    document,
    location: { hash },
    navigator: { userAgent: ua, maxTouchPoints: touch },
  });
  const before = attrs["data-platform"] ?? "";
  for (const f of listeners) f();
  return { platform: before, order: list.children.map((c) => c.id) };
}

describe("the download page's head script (T06)", () => {
  it.each([
    ["mac", "mac"],
    ["windows", "windows"],
    ["linux", "linux"],
  ] as const)("a %s user agent puts the %s card first", (ua, want) => {
    const r = run({ ua: UA[ua] });
    expect(r.platform).toBe(want);
    expect(r.order[0]).toBe(want);
    expect(r.order).toHaveLength(4);
  });

  it.each(["iphone", "android", "chromeos"] as const)("a %s gets no preselection", (ua) => {
    expect(run({ ua: UA[ua] })).toEqual({
      platform: "",
      order: ["mac", "windows", "linux", "nas"],
    });
  });

  it("an iPad, which says Macintosh with a touch screen, gets no preselection", () => {
    expect(run({ ua: UA.mac, touch: 5 }).platform).toBe("");
  });

  it("the address's #<platform> wins over the user agent (the home page's Download)", () => {
    expect(run({ ua: UA.windows, hash: "#mac" })).toEqual({
      platform: "mac",
      order: ["mac", "windows", "linux", "nas"],
    });
    expect(run({ ua: UA.mac, hash: "#linux" }).order[0]).toBe("linux");
    // #nas is an anchor, not a computer: the user agent decides
    expect(run({ ua: UA.mac, hash: "#nas" }).platform).toBe("mac");
  });

  it("a platform the release has no card for is never marked", () => {
    expect(run({ ua: UA.windows, cards: "mac linux", ids: ["mac", "linux", "nas"] })).toEqual({
      platform: "",
      order: ["mac", "linux", "nas"],
    });
  });

  it("is a classic script: no import or export, so it runs in the <head> before the first paint", () => {
    expect(JS).not.toMatch(/^\s*(import|export)\b/m);
  });
});
