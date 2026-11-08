// Story 54.20 test T05 on the web: the plugin panels on the series and library pages, as a
// text reference of what each page draws (the web has no image snapshots), in English and in
// Arabic (right to left): the section's heading in the page's language, each value with its
// own direction (dir="auto"), a value longer than the text rules allow cut with an ellipsis
// and wrapped, no marker off a video's page; and the panels' colours from the theme's tokens
// (defined for the dark theme), never a hard-coded colour.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, memberUser } from "../../test/pageHarness";
import { MAX_SHOWN } from "../../components/PluginPanel/PluginPanel";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});
// the harness's capabilities have panels off: this file turns them on (as PluginPanels.test)
vi.mock("../../lib/capabilities", async () => {
  const actual =
    await vi.importActual<typeof import("../../lib/capabilities")>("../../lib/capabilities");
  return { ...actual, useCapabilities: () => ({ ...actual.ALL_ON, answered: true, panels: true }) };
});

import { SeriesDetail } from "./SeriesDetail";
import { LibraryPage } from "./LibraryPage";

const LIB = "3f1c2a4e-0000-4000-8000-000000000001";

/** The panel's words, as the plugin writes them in each language. */
const WORDS = {
  en: {
    title: "Scenes",
    plugin: "Scenes plugin",
    era: "Era",
    eraValue: "1920s",
    notes: "Notes",
    long: "A journey north through the desert by night, ".repeat(6).trim(),
    badges: ["Restored", "HD"],
    chip: "History",
    marker: "The road north",
  },
  ar: {
    title: "المشاهد",
    plugin: "إضافة المشاهد",
    era: "الحقبة",
    eraValue: "العشرينيات",
    notes: "ملاحظات",
    long: "رحلة ليلية نحو الشمال عبر الصحراء الواسعة، ".repeat(6).trim(),
    badges: ["مرمّم", "عالي الدقة"],
    chip: "تاريخ",
    marker: "الطريق شمالاً",
  },
} as const;
type Lang = keyof typeof WORDS;

function panels(lang: Lang) {
  const w = WORDS[lang];
  return {
    panels: [
      {
        plugin_id: "dev.example.scenes",
        plugin: w.plugin,
        title: w.title,
        blocks: [
          {
            type: "kv",
            value: [
              { label: w.era, value: w.eraValue },
              { label: w.notes, value: w.long },
            ],
          },
          { type: "badges", value: w.badges.map((text) => ({ text })) },
          { type: "chips", value: [w.chip] },
          { type: "markers", value: [{ t_ms: 754000, label: w.marker }] },
        ],
      },
    ],
  };
}

function serve() {
  get.mockImplementation((url: string) => {
    const path = url.split("?")[0];
    if (path === "/api/panels") {
      const lang = new URLSearchParams(url.split("?")[1]).get("lang") === "ar" ? "ar" : "en";
      return Promise.resolve(panels(lang));
    }
    if (path === "/api/series/s1")
      return Promise.resolve({ id: "s1", name: "Breaking Bad", year: 2008, numbering: "season" });
    if (path === "/api/series/s1/episodes")
      return Promise.resolve({
        numbering: "season",
        seasons: [
          {
            season: 1,
            episodes: [{ video_id: "e1", season: 1, episode: 1, title: "Pilot", progress_pct: 0 }],
          },
        ],
      });
    if (path === "/api/series/s1/missing")
      return Promise.resolve({ gaps: [], numbering: "season" });
    if (path === `/api/libraries/${LIB}/hub`)
      return Promise.resolve({
        library: { id: LIB, name: "Talks", videos: 1 },
        rows: {
          continue_watching: [],
          recently_added: [],
          recently_watched: [],
          most_watched: [],
          series: [],
          years: [],
        },
      });
    if (path === "/api/videos") return Promise.resolve({ items: [], next: null });
    if (path === "/api/me/pinned-libraries") return Promise.resolve({ library_ids: [], items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const PAGES = {
  series: {
    ui: <SeriesDetail />,
    path: "/series/s1",
    route: "/series/:id",
    subject: "series%3As1",
  },
  library: {
    ui: <LibraryPage />,
    path: `/library/${LIB}`,
    route: "/library/:libraryId",
    subject: `library%3A${LIB}`,
  },
} as const;

/** What the panels section draws, one line per element: tag, its own direction, its text. */
function outline(section: Element): string[] {
  const out: string[] = [];
  const dir = (e: Element) => (e.getAttribute("dir") ? `[${e.getAttribute("dir")}]` : "");
  section
    .querySelectorAll("summary, h3, .mkt-ppanel__by, dt, dd, .mk-badge, a, li")
    .forEach((e) => {
      const tag = e.classList.contains("mkt-ppanel__by")
        ? "by"
        : e.classList.contains("mk-badge")
          ? "badge"
          : e.tagName.toLowerCase();
      const text = e.textContent ?? "";
      const shown = text.length > 40 ? `${text.slice(0, 20)}…(${Array.from(text).length})` : text;
      const to = e.tagName === "A" ? ` -> ${decodeURIComponent(e.getAttribute("href") ?? "")}` : "";
      out.push(`${tag}${dir(e)}: ${shown}${to}`);
    });
  return out;
}

async function show(page: keyof typeof PAGES, lang: Lang) {
  localStorage.setItem("mkt:locale", lang);
  serve();
  const p = PAGES[page];
  renderPage(<ToastProvider>{p.ui}</ToastProvider>, {
    path: p.path,
    route: p.route,
    user: memberUser,
  });
  const words = lang === "ar" ? ar : en;
  const summary = await screen.findByText(words["plugins.panels.title"]);
  expect(get).toHaveBeenCalledWith(`/api/panels?subject=${p.subject}&lang=${lang}`);
  await userEvent.setup().click(summary);
  await screen.findByText(WORDS[lang].eraValue);
  const section = summary.closest("details");
  expect(section).not.toBeNull();
  return section!;
}

describe("plugin panels on the series and library pages (story 54.20 T05)", () => {
  beforeEach(() => {
    get.mockReset();
    localStorage.clear();
  });
  afterEach(() => localStorage.clear());

  for (const page of ["series", "library"] as const) {
    for (const lang of ["en", "ar"] as const) {
      it(`${page} page, ${lang}`, async () => {
        const w = WORDS[lang];
        const section = await show(page, lang);

        // the page's direction and language
        expect(document.documentElement.dir).toBe(lang === "ar" ? "rtl" : "ltr");
        expect(document.documentElement.lang).toBe(lang);
        expect(section.hasAttribute("open")).toBe(true);

        // the reference: every value with its own direction, the long one cut, chips search
        // Maktaba, no marker off a video's page
        const head = (lang === "ar" ? ar : en)["plugins.panels.title"];
        expect(outline(section)).toEqual([
          `summary: ${head}`,
          `h3[auto]: ${w.title}`,
          `by[auto]: ${w.plugin}`,
          `dt[auto]: ${w.era}`,
          `dd[auto]: ${w.eraValue}`,
          `dt[auto]: ${w.notes}`,
          `dd[auto]: ${w.long.slice(0, 20)}…(${MAX_SHOWN})`,
          `badge: ${w.badges[0]}`,
          `badge: ${w.badges[1]}`,
          `a[auto]: ${w.chip} -> /search?q=${w.chip}`,
        ]);
        expect(section.textContent).not.toContain(w.marker);

        // the long value: the text rules' length, ending in an ellipsis
        const long = screen.getByText((t) => t.startsWith(w.long.slice(0, 20)));
        expect(Array.from(long.textContent ?? "")).toHaveLength(MAX_SHOWN);
        expect(long.textContent?.endsWith("…")).toBe(true);
        expect(long.closest(".mkt-ppanel__block")).not.toBeNull();
      });
    }
  }
});

describe("plugin panels' styles (story 54.20 T05)", () => {
  const css = readFileSync("src/components/PluginPanel/PluginPanel.css", "utf8");
  const tokens = readFileSync("src/styles/tokens.css", "utf8");
  const darkStart = tokens.indexOf('[data-theme="dark"],\n:root:not([data-theme]) {');
  const dark = tokens.slice(darkStart, tokens.indexOf("}", darkStart));
  const light = tokens.slice(
    tokens.indexOf(":root {"),
    tokens.indexOf("}", tokens.indexOf(":root {"))
  );

  it("takes every colour from the theme's tokens, never a written colour", () => {
    const colours = [
      ...css.matchAll(/(?:^|[;{\s])((?:background-)?color|border(?:-[a-z-]+)?)\s*:\s*([^;]+);/g),
    ];
    expect(colours.length).toBeGreaterThan(0);
    for (const [, prop, value] of colours) {
      expect(value, `${prop}: ${value}`).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
    }
    expect(css).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
  });

  it("names only tokens the light and dark themes define", () => {
    expect(darkStart).toBeGreaterThan(0);
    const used = new Set([...css.matchAll(/var\((--[a-z0-9-]+)/g)].map((m) => m[1]));
    expect(used.size).toBeGreaterThan(0);
    for (const name of used) {
      // a colour is the theme's: defined for dark (and light); spacing and radii on :root
      if (name.startsWith("--color-"))
        expect(dark, `${name} in the dark theme`).toContain(`${name}:`);
      expect(light, `${name} on :root`).toContain(`${name}:`);
    }
  });

  it("wraps a long value inside the panel", () => {
    expect(css).toMatch(/\.mkt-ppanel__block \{[^}]*overflow-wrap: anywhere/);
  });
});
