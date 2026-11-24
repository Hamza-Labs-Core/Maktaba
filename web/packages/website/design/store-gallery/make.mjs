// Story 60.14: writes the store gallery mockups (mockups/*.html) from beats.json and
// stores.json, for the owner's review of the style and the beats before the full set is made.
//
//   node design/store-gallery/make.mjs           write the mockups
//   node design/store-gallery/make.mjs --check   fail if a written mockup is stale
//
// Each sheet is one store asset of one app in one language: every shot of that gallery at
// the store's exact pixel size, scaled down side by side, with what each shows under it.
// The "full-*" pages are single shots at their full size. The screens are stand-ins on test
// data (the same kind 60.01 part B used): blank posters, no people, until the new demo
// library lands; beats.json names each one.
//
// The pages are served from the repository's root by render.mjs (the stand-ins are in
// web/packages/devportal and in the apps' screenshot references).

import { readFileSync, writeFileSync, mkdirSync, readdirSync } from "node:fs";
import { dirname, join, posix } from "node:path";
import { fileURLToPath } from "node:url";
import { SPRITE, icon } from "../mockups/shell.mjs";

export const here = dirname(fileURLToPath(import.meta.url));
export const LANGS = ["en", "ar"];
export const BEATS = JSON.parse(readFileSync(join(here, "beats.json"), "utf8"));
export const STORES = JSON.parse(readFileSync(join(here, "stores.json"), "utf8"));

/** The size of a store asset. */
export const sizeOf = (store, asset) => STORES.stores[store].assets[asset].size;

/** The aspect of a surface's screen, for a panel with no stand-in. */
const RATIO = {
  iphone: "1206 / 2622",
  ipad: "16 / 10",
  mac: "16 / 10",
  "android-phone": "1233 / 2673",
  "android-tablet": "16 / 10",
  appletv: "16 / 9",
};

const esc = (s) =>
  String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

/** A stand-in's URL from the repository's root, or null. */
export function standinOf(beat, surface, lang) {
  const t = beat.standin?.[surface];
  if (!t) return null;
  if (beat.standinLangs && !beat.standinLangs.includes(lang)) return null;
  const other = lang === "en" ? "ar" : "en";
  return (
    "/" +
    posix.normalize(posix.join("web", t.replaceAll("{lang}", lang).replaceAll("{other}", other)))
  );
}

/** The beats of an app on a surface, in order. */
export const beatsFor = (app, surface) =>
  BEATS.apps[app].beats.filter((b) => !b.only || b.only.includes(surface));

/** The layout numbers of a captioned shot (portrait or landscape), as CSS custom properties. */
export function layout(W, H, lang) {
  const ar = lang === "ar";
  if (H > W) {
    // portrait: the unit is the width, or less on a squatter canvas (9:16), so the caption
    // takes the same share of the height on every phone size
    const u = Math.min(W, H / 2.17);
    return {
      u,
      "cap-top": 0.085,
      "h-size": ar ? 0.078 : 0.068,
      "h-lh": ar ? 1.4 : 1.16,
      "s-size": ar ? 0.042 : 0.034,
      "s-lh": ar ? 1.55 : 1.45,
      measure: 0.84,
      "screen-w": 0.84,
      "screen-gap": 0.055,
      radius: 0.06,
    };
  }
  // landscape (iPad, Mac, Apple TV, tablets): the unit is the height
  const u = H;
  return {
    u,
    "cap-top": 0.055,
    "h-size": ar ? 0.074 : 0.066,
    "h-lh": ar ? 1.4 : 1.15,
    "s-size": ar ? 0.04 : 0.032,
    "s-lh": ar ? 1.5 : 1.4,
    measure: 0.8,
    "screen-w": 0.78,
    "screen-gap": 0.035,
    radius: 0.014,
  };
}

const vars = (W, H, l) =>
  `--w:${W}px;--h:${H}px;--u:${l.u}px;` +
  Object.entries(l)
    .filter(([k]) => k !== "u")
    .map(([k, v]) => `--${k}:${v}`)
    .join(";");

const dirOf = (lang) => (lang === "ar" ? "rtl" : "ltr");

/** The lockup: the icon, the shot's script first, a brass rule, the other script. */
function lockup(lang) {
  const en = `<span lang="en" dir="ltr">Maktaba</span>`;
  const ar = `<span lang="ar" dir="rtl">مكتبة</span>`;
  return `<span class="wm">${icon(64)}${lang === "ar" ? ar : en}<span class="rule"></span>${lang === "ar" ? en : ar}</span>`;
}

/** One captioned shot: the screen is `src` (the full set's capture), or the beat's stand-in. */
export function captioned({ W, H, lang, beat, surface, src = standinOf(beat, surface, lang) }) {
  const c = beat[lang];
  const screen = src
    ? `<div class="screen" data-nomirror><img src="${src}" alt=""></div>`
    : `<div class="screen todo" style="--ratio:${RATIO[surface] ?? "16 / 9"}" data-nomirror><span>full set: ${esc(beat.capture?.[surface] ?? beat.id)}<br>(${esc(surface)})</span></div>`;
  return `<div class="shot captioned" lang="${lang}" dir="${dirOf(lang)}" style="${vars(W, H, layout(W, H, lang))}">
  <div class="cap" data-cap>${beat.wordmark ? lockup(lang) : ""}<h2 class="h">${esc(c.h)}</h2><p class="sub">${esc(c.sub)}</p></div>
  ${screen}
</div>`;
}

/** One unaltered screen (the TV stores). */
export function plain({ W, H, lang, beat, surface, src = standinOf(beat, surface, lang) }) {
  return `<div class="shot plain" lang="${lang}" style="--w:${W}px;--h:${H}px;--u:${H}px">${src ? `<img src="${src}" alt="">` : ""}</div>`;
}

/** The TV tile: the icon and the name (one script, or both for a single set). */
export function tile({ W, H, lang, both = false, bare = false }) {
  const en = `<span lang="en" dir="ltr">Maktaba</span>`;
  const ar = `<span lang="ar" dir="rtl">مكتبة</span>`;
  const names = both
    ? `${lang === "ar" ? ar : en}<span class="rule"></span>${lang === "ar" ? en : ar}`
    : lang === "ar"
      ? ar
      : en;
  return `<div class="shot tile${bare ? " bare" : ""}" lang="${lang}" dir="${dirOf(lang)}" style="--w:${W}px;--h:${H}px;--u:${Math.min(W, H * 1.6)}px">${icon(64)}<span class="names">${names}</span></div>`;
}

/** Brand art: the library hall, its subject at x (a background-position share). */
export const art = ({ W, H, x = "50%", size = "cover" }) =>
  `<div class="shot art" style="--w:${W}px;--h:${H}px;--art-x:${x};--art-size:${size}"></div>`;

/** The feature graphic (Google Play 1024 x 500, Amazon's promotional image): the app's first
 * headline, and `src` (a landscape screen of the app; the TV's home by default). */
export function feature({
  W,
  H,
  lang,
  app,
  surface = "appletv",
  src = standinOf(BEATS.apps.tv.beats[0], surface, lang),
}) {
  const beat = BEATS.apps[app].beats[0];
  const l = {
    ...layout(W, H, lang),
    u: H,
    "cap-top": 0,
    "h-size": lang === "ar" ? 0.1 : 0.094,
    "s-size": 0.045,
    measure: 0.42,
    "screen-w": 0.62,
    radius: 0.012,
  };
  return `<div class="shot feature" lang="${lang}" dir="${dirOf(lang)}" style="${vars(W, H, l)}">
  <div class="cap" data-cap>${lockup(lang)}<h2 class="h">${esc(beat[lang].h)}</h2></div>
  <div class="screen" data-nomirror><img src="${src}" alt=""></div>
</div>`;
}

// ------------------------------------------------------------------ sheets

const APP_NAME = (app, lang) => BEATS.apps[app].name[lang];

/** A captioned gallery: one store asset of one app. */
function gallery(store, asset, app, surface) {
  return (lang) => {
    const [W, H] = sizeOf(store, asset);
    return beatsFor(app, surface).map((beat, i) => ({
      W,
      H,
      html: captioned({ W, H, lang, beat, surface }),
      label: `${i + 1} · ${beat.id}${beat.ui ? " (other language's screen)" : ""}`,
    }));
  };
}

/** The plain TV set of a store: per language, or the single set (Samsung, LG). */
function tvSet(store, surface, single) {
  return (lang) => {
    const [W, H] = sizeOf(store, "tv");
    const beats = BEATS.apps.tv.beats;
    const picks = single
      ? BEATS.apps.tv.singleSet[store].map(([id, l]) => [beats.find((b) => b.id === id), l])
      : beats.filter((b) => !BEATS.apps.tv.drop?.[store]?.includes(b.id)).map((b) => [b, lang]);
    return picks.map(([beat, l], i) => ({
      W,
      H,
      html: plain({ W, H, lang: l, beat, surface }),
      label: `${i + 1} · ${beat.id}${beat.ui ? (single || l === "en" ? " (Arabic screen)" : " (English screen)") : ""}`,
    }));
  };
}

const one = (W, H, html, label) => ({ W, H, html, label });

export const SHEETS = {
  "apple-player-iphone": {
    title: "App Store · Maktaba Player · iPhone",
    note: '6.9" at 1320 × 2868 drawn here; the required 6.3" set (1206 × 2622) is the same layout. 7 of 10.',
    rows: [{ height: 760, shots: gallery("apple", "iphone-6.9", "player", "iphone") }],
  },
  "apple-player-ipad": {
    title: 'App Store · Maktaba Player for iPad · iPad 13"',
    note: "2752 × 2064, landscape. Stand-ins: the Android tablet's screens until the iPad captures exist.",
    rows: [{ height: 360, shots: gallery("apple", "ipad-13", "player", "ipad") }],
  },
  "apple-player-mac": {
    title: "Mac App Store · Maktaba Player for Mac",
    note: "2880 × 1800. Stand-ins: the desktop Player and the Android tablet until the Mac captures exist.",
    rows: [{ height: 330, shots: gallery("apple", "mac", "player", "mac") }],
  },
  "apple-remote-iphone": {
    title: "App Store · Maktaba Remote · iPhone",
    note: "1320 × 2868 (and 1206 × 2622). The iPad Remote uses the same beats at 2752 × 2064.",
    rows: [{ height: 760, shots: gallery("apple", "iphone-6.9", "remote", "iphone") }],
  },
  "apple-tv": {
    title: "App Store · Maktaba · Apple TV",
    note: "3840 × 2160, captioned (Apple allows text overlays; the other TV stores get the plain screens).",
    rows: [{ height: 330, shots: gallery("apple", "appletv", "tv", "appletv") }],
  },
  "google-player": {
    title: "Google Play · Maktaba Player",
    note: 'Feature graphic 1024 × 500 and icon 512 × 512; phone 1080 × 1920 (9:16); 10" tablet 2560 × 1440. The 7" tablet set is 1080 × 1920 like the phone. Maktaba Remote follows the same layout with its own beats.',
    rows: [
      {
        height: 300,
        shots: (lang) => [
          one(1024, 500, feature({ W: 1024, H: 500, lang, app: "player" }), "feature graphic"),
          one(
            512,
            512,
            `<div class="shot tile bare" style="--w:512px;--h:512px;--u:512px"><img src="/tools/icons/source/maktaba-icon.png" alt="" style="inline-size:512px;block-size:512px"></div>`,
            "icon"
          ),
        ],
      },
      { height: 640, shots: gallery("google", "phone", "player", "android-phone") },
      { height: 300, shots: gallery("google", "tablet-10", "player", "android-tablet") },
    ],
  },
  "tv-stores": {
    title: "TV stores · Maktaba",
    note: "Plain, unaltered 1920 × 1080 screens: Google TV, Fire TV and Samsung ask for the app as it is. Samsung and LG take one set for every language (English screens, the Arabic screen second).",
    rows: [
      {
        label: "Google Play, Android TV: banner 1280 × 720, screenshots",
        height: 200,
        shots: (lang) => [
          one(1280, 720, tile({ W: 1280, H: 720, lang }), "TV banner"),
          ...tvSet("google", "androidtv")(lang),
        ],
      },
      {
        label:
          "Amazon Fire TV: app icon 1280 × 720, background 1920 × 1080 (no title, subject right), screenshots",
        height: 200,
        shots: (lang) => [
          one(1280, 720, tile({ W: 1280, H: 720, lang }), "Fire TV app icon"),
          one(1920, 1080, art({ W: 1920, H: 1080, x: "14%", size: "150%" }), "background"),
          ...tvSet("amazon", "firetv")(lang),
        ],
      },
      {
        label: "Roku: poster 540 × 405, screenshots (up to 6)",
        height: 200,
        shots: (lang) => [
          one(540, 405, tile({ W: 540, H: 405, lang }), "poster"),
          ...tvSet("roku", "roku")(lang),
        ],
      },
      {
        label:
          "Samsung (one set for every language): logo 1920 × 1080, detail background, exactly 4 screenshots",
        height: 200,
        shots: () => [
          one(
            1920,
            1080,
            tile({ W: 1920, H: 1080, lang: "en", both: true }),
            "logo (on its background layer)"
          ),
          one(1920, 1080, art({ W: 1920, H: 1080, x: "10%", size: "150%" }), "detail background"),
          ...tvSet("samsung", "samsung", true)("en"),
        ],
      },
      {
        label: "LG (one set): store icon 400 × 400, screenshots",
        height: 200,
        shots: () => [
          one(
            400,
            400,
            `<div class="shot tile bare" style="--w:400px;--h:400px;--u:400px"><img src="/tools/icons/source/maktaba-icon.png" alt="" style="inline-size:400px;block-size:400px"></div>`,
            "store icon"
          ),
          ...tvSet("lg", "lg", true)("en"),
        ],
      },
    ],
  },
};

/** The full-size pages: one shot each, at its store size. */
export const FULL = {
  "full-iphone-library": (lang) => {
    const [W, H] = sizeOf("apple", "iphone-6.9");
    return {
      W,
      H,
      html: captioned({ W, H, lang, beat: BEATS.apps.player.beats[0], surface: "iphone" }),
    };
  },
  "full-iphone-search": (lang) => {
    const [W, H] = sizeOf("apple", "iphone-6.9");
    return {
      W,
      H,
      html: captioned({ W, H, lang, beat: BEATS.apps.player.beats[1], surface: "iphone" }),
    };
  },
  "full-feature": (lang) => ({
    W: 1024,
    H: 500,
    html: feature({ W: 1024, H: 500, lang, app: "player" }),
  }),
  "full-appletv-library": (lang) => {
    const [W, H] = sizeOf("apple", "appletv");
    return {
      W,
      H,
      scale: 0.5,
      html: captioned({ W, H, lang, beat: BEATS.apps.tv.beats[0], surface: "appletv" }),
    };
  },
};

function page(lang, body, { title, width }) {
  return `<!doctype html>
<html lang="${lang}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=${width}">
<title>${esc(title)}</title>
<link rel="stylesheet" href="/web/packages/website/design/fonts.css">
<link rel="stylesheet" href="/web/packages/website/design/store-gallery/gallery.css">
<link rel="stylesheet" href="/web/packages/website/design/store-gallery/sheet.css">
</head>
<body>
${SPRITE}
${body}
</body>
</html>
`;
}

export const SHEET_WIDTH = 1600;

function sheetHtml(name, lang) {
  const s = SHEETS[name];
  const rows = s.rows
    .map((r) => {
      const shots = r
        .shots(lang)
        .map(({ W, H, html, label }) => {
          const k = r.height / H;
          return `<figure class="cell" style="inline-size:${Math.round(W * k)}px">
  <div class="fit" style="inline-size:${Math.round(W * k)}px;block-size:${r.height}px"><div class="scale" style="transform:scale(${k.toFixed(5)})">${html}</div></div>
  <figcaption>${esc(label)}</figcaption>
</figure>`;
        })
        .join("\n");
      return `<section class="row">${r.label ? `<h2>${esc(r.label)}</h2>` : ""}<div class="shots" dir="${dirOf(lang)}">${shots}</div></section>`;
    })
    .join("\n");
  const head = `<header class="sheet-head"><h1>${esc(s.title)} · ${lang === "ar" ? "العربية" : "English"}</h1><p>${esc(s.note)}</p><p class="standin">Mockup for review (story 60.14). Screens are stand-ins on test data (blank posters, no people); the full set captures each app on the new demo library.</p></header>`;
  return page(lang, `<main class="sheet">${head}${rows}</main>`, {
    title: `${s.title} (${lang})`,
    width: SHEET_WIDTH,
  });
}

function fullHtml(name, lang) {
  const f = FULL[name](lang);
  return page(lang, `<main class="full">${f.html}</main>`, {
    title: `${name} (${lang})`,
    width: f.W,
  });
}

/** Every mockup file make.mjs writes, by name, with its HTML. */
export function mockups() {
  const out = {};
  for (const lang of LANGS) {
    for (const name of Object.keys(SHEETS)) out[`${name}.${lang}.html`] = sheetHtml(name, lang);
    for (const name of Object.keys(FULL)) out[`${name}.${lang}.html`] = fullHtml(name, lang);
  }
  return out;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dir = join(here, "mockups");
  mkdirSync(dir, { recursive: true });
  const want = mockups();
  if (process.argv.includes("--check")) {
    const stale = Object.entries(want).filter(([f, html]) => {
      try {
        return readFileSync(join(dir, f), "utf8") !== html;
      } catch {
        return true;
      }
    });
    const extra = readdirSync(dir).filter((f) => f.endsWith(".html") && !(f in want));
    if (stale.length || extra.length) {
      console.error(`stale: ${stale.map(([f]) => f).join(", ")} extra: ${extra.join(", ")}`);
      console.error("run: node web/packages/website/design/store-gallery/make.mjs");
      process.exit(1);
    }
  } else {
    for (const [f, html] of Object.entries(want)) writeFileSync(join(dir, f), html);
    console.log(`wrote ${Object.keys(want).length} mockups`);
  }
}
