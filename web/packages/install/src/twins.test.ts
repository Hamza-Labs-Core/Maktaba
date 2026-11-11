// The NAS guides (Epic 59 story 59.14): every guide on maktaba.tv is a pair of pages, English
// and Arabic, mirrored: the same sections with the same ids, the same screenshots still to come
// (each a marked placeholder in both languages), each page linking to its twin and to the NAS
// landing page, and the Arabic page passing maktaba.dev's Latin-share check (T02). The pages a
// script fills (the install page, the Synology guide) are checked on what their script renders.
import BASE from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { describe, expect, it } from "vitest";
import { latinFindings, latinShare, htmlProse, MAX_LATIN_SHARE } from "./latin";
import { mount as mountDocker } from "./page";
import { mount as mountSynology } from "./synologyPage";

const sources = import.meta.glob("../site/**/index.html", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

const page = (lang: "en" | "ar", name: string): string => {
  const key = `../site/${lang === "ar" ? "ar/" : ""}install/${name}/index.html`;
  const s = sources[key];
  if (s === undefined) throw new Error(`no page ${key}`);
  return s;
};

const STATIC = ["nas", "truenas", "unraid", "qnap", "ugreen", "after-setup", "troubleshooting"];
const ALL = [...STATIC, "docker", "synology"];

const h2s = (s: string) => [...s.matchAll(/<h2( id="([^"]+)")?>/g)].map((m) => m[2] ?? "");
const shots = (s: string) => [...s.matchAll(/data-shot="([^"]+)\.(en|ar)\.png"/g)];

describe("the NAS guides", () => {
  it("has an English and an Arabic page for every guide, and nothing else", () => {
    const names = Object.keys(sources).map((k) => k.replace(/^\.\.\/site\//, ""));
    const expected = ALL.flatMap((n) => [`install/${n}/index.html`, `ar/install/${n}/index.html`]);
    expect(names.sort()).toEqual(expected.sort());
  });

  it.each(ALL)("%s: a pair, each page linking to the other", (name) => {
    const en = page("en", name);
    const ar = page("ar", name);
    expect(en).toMatch(/<html lang="en" dir="ltr">/);
    expect(ar).toMatch(/<html lang="ar" dir="rtl">/);
    expect(en).toContain(`href="/ar/install/${name}/"`);
    expect(ar).toContain(`href="/install/${name}/"`);
  });

  it.each(STATIC)("%s: the same sections, ids and screenshots to come in both", (name) => {
    const en = page("en", name);
    const ar = page("ar", name);
    expect(h2s(en).length).toBeGreaterThanOrEqual(3);
    expect(h2s(ar)).toEqual(h2s(en));
    const e = shots(en);
    const a = shots(ar);
    expect(e.every((m) => m[2] === "en")).toBe(true);
    expect(a.every((m) => m[2] === "ar")).toBe(true);
    expect(a.map((m) => m[1])).toEqual(e.map((m) => m[1]));
    expect((en.match(/class="placeholder"/g) ?? []).length).toBe(e.length);
  });

  it.each(STATIC.filter((n) => n !== "nas" && n !== "truenas"))(
    "%s: links back to the NAS landing page in its language",
    (name) => {
      expect(page("en", name)).toContain('href="/install/nas/"');
      expect(page("ar", name)).toContain('href="/ar/install/nas/"');
    }
  );

  it("links every guide from the landing page, in each language", () => {
    for (const n of ALL.filter((x) => !["nas", "after-setup", "troubleshooting"].includes(x))) {
      expect(page("en", "nas")).toContain(`href="/install/${n}/"`);
      expect(page("ar", "nas")).toContain(`href="/ar/install/${n}/"`);
    }
  });

  it("has a troubleshooting section for each message the story names", () => {
    const ids = h2s(page("en", "troubleshooting"));
    for (const id of [
      "write-denied",
      "port-taken",
      "no-access",
      "render-device",
      "plugins-held",
      "not-enough-memory",
    ])
      expect(ids).toContain(id);
  });

  it("has an after-setup section for each topic the story names", () => {
    const ids = h2s(page("en", "after-setup"));
    for (const id of [
      "permissions",
      "quiet-hours",
      "speech",
      "video",
      "proxy",
      "updates",
      "gpu-node",
    ])
      expect(ids).toContain(id);
  });

  it("gives the same setup command in every guide that runs it", () => {
    const cmd = "/opt/maktaba/server/bin/maktaba-server container setup";
    for (const n of ["truenas", "unraid", "qnap", "ugreen"]) {
      expect(page("en", n)).toContain(cmd);
      expect(page("ar", n)).toContain(cmd);
    }
  });

  it("passes the Latin-share check on every static Arabic page (T02)", () => {
    const pages = Object.fromEntries(STATIC.map((n) => [`ar/install/${n}/`, page("ar", n)]));
    expect(latinFindings(pages)).toEqual([]);
  });

  it("fails an Arabic page left in English", () => {
    expect(latinFindings({ "ar/install/qnap/": page("en", "qnap") })).toHaveLength(1);
    expect(latinFindings({ "install/qnap/": page("en", "qnap") })).toEqual([]);
  });

  it("counts a NAS menu name in «quotes» as a name, not as prose", () => {
    expect(htmlProse("<p>من «Control Panel» ← «Shared Folder»</p>")).not.toMatch(/Control/);
    expect(htmlProse("<p>«مثال Example»</p>")).toMatch(/Example/);
  });

  it.each([
    ["docker", mountDocker],
    ["synology", mountSynology],
  ] as const)("%s: the Arabic its script renders passes the Latin-share check", (_, mount) => {
    const root = document.createElement("main");
    mount(root, BASE, "ar");
    expect(latinShare(htmlProse(root.innerHTML))).toBeLessThanOrEqual(MAX_LATIN_SHARE);
  });
});
