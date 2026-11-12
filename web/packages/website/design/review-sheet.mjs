// Writes review-sheet.html (stories 60.01 and 60.02 review): every mockup PNG on one
// self-contained page, grouped by part, then mockup, language and scheme, the widths side by
// side at their relative sizes, each linking to the full image; part B adds the tagline
// proposals with their one-liners. `--check` fails when the page is stale.

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { LANGS, MOCKUPS } from "./mockups/make.mjs";
import { readCopy } from "./mockups/part-b.mjs";
import { shotsOf } from "./render.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "review-sheet.html");

const TITLES = {
  "front-door": ["Front door", "The text page as Phase 1's front door"],
  legal: ["Legal page", "The text page as privacy, terms and copyright use it"],
  problem: ["Problem page", "One message: home-unreachable"],
  problems: ["Problem index", "Every message, by area, with the in-page filter"],
  "install-guide": [
    "Install guide in the shell",
    "Epic 59's Unraid guide inside the header and footer",
  ],
  404: ["404", "A page that is not there"],
  components: [
    "Component sheet",
    "Callouts, code and compose blocks, steps, focus, buttons, the open phone menu",
  ],
};
Object.assign(TITLES, {
  "legal-p3": ["Legal page, part B", "The legal page in the part B shell, with the display face"],
  "problem-p3": ["Problem page, part B", "One message in the part B shell"],
  "problems-p3": ["Problem index, part B", "The message index in the part B shell"],
  home: ["Home", "The chosen tagline on the hero, then every section, from copy/home.*.md"],
  download: ["Download", "From copy/download.*.md, with v0.3.131's real files, sizes and digest"],
  "install-p3": ["Install guide, Phase 3", "The Synology guide as the site's install page"],
  help: ["Help index", "The help topics from the app's own catalog"],
  "help-topic": ["Help topic", "The search topic, in the app's own words"],
  wordmark: ["Wordmark", "Lockups for header, footer and social card, LTR and RTL, minimum sizes"],
  foundations: [
    "Foundations",
    "Display face, type scales, the colour extension with tested ratios, icons",
  ],
  "components-b": [
    "Component sheet, part B",
    "Download card, disclosures, frames, feature block, states",
  ],
  "social-home": ["Social card, home page", "1200 × 630, the chosen tagline"],
  "social-download": ["Social card, download page", "1200 × 630"],
});
const PART_A = Object.keys(TITLES).slice(0, 7);
const LANG = { en: "English", ar: "Arabic" };
// An English gloss of the Arabic tagline, for review only; the Arabic is written natively.
const GLOSS = "Your library, on every screen at home.";

function taglines() {
  const cell = (lang) =>
    `<td lang="${lang}" dir="${lang === "ar" ? "rtl" : "ltr"}"><strong>${readCopy("home", lang).tagline}</strong><br /><span class="says">${readCopy("home", lang)["tagline.says"]}</span></td>`;
  const rows = `          <tr>${cell("en")}${cell("ar")}<td class="says">${GLOSS}</td></tr>`;
  return `    <section id="taglines">
      <h2>The tagline</h2>
      <p class="sub">Chosen on 2026-11-12 (Q1), from five proposals per language (copy/README.md lists the others). It fits the social card in three lines or fewer (render.mjs fails otherwise); the home hero and the social card show it in place.</p>
      <div class="table"><table>
        <thead><tr><th scope="col">English</th><th scope="col">Arabic</th><th scope="col">Gloss of the Arabic</th></tr></thead>
        <tbody>
${rows}
        </tbody>
      </table></div>
    </section>`;
}

export function sheet() {
  const section = (name) => {
    const [title, sub] = TITLES[name];
    const rows = LANGS.flatMap((lang) =>
      ["dark", "light"]
        .filter((sc) => shotsOf(name).some(([, x]) => x === sc))
        .map(
          (scheme) => `      <div class="row">
        <h3>${LANG[lang]}, ${scheme}</h3>
        <div class="shots">
${shotsOf(name)
  .filter(([, x]) => x === scheme)
  .map(([w]) => {
    const src = `mockups/png/${name}.${lang}.${w}.${scheme}.png`;
    return `          <figure style="flex: ${w} 1 0"><a href="${src}"><img src="${src}" alt="${title}, ${LANG[lang]}, ${scheme}, ${w} px wide" loading="lazy" /></a><figcaption>${w} px</figcaption></figure>`;
  })
  .join("\n")}
        </div>
      </div>`
        )
    );
    return `    <section id="${name}">
      <h2>${title}</h2>
      <p class="sub">${sub}. Source: <code>mockups/${name}.en.html</code>, <code>mockups/${name}.ar.html</code>.</p>
${rows.join("\n")}
    </section>`;
  };
  const names = Object.keys(MOCKUPS);
  const partB = names.filter((n) => !PART_A.includes(n));
  const sections = `    <h2 class="part">Part A: the shell (approved 2026-11-11)</h2>
${PART_A.map(section).join("\n")}
    <h2 class="part">Part B: the direction, the pages and the copy</h2>
${taglines()}
${partB.map(section).join("\n")}`;
  const count = names.reduce((n, name) => n + LANGS.length * shotsOf(name).length, 0);
  return `<!doctype html>
<!-- Generated by design/review-sheet.mjs (story 60.01). -->
<html lang="en" dir="ltr">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark light" />
    <title>maktaba.tv design review</title>
    <style>
      :root {
        --bg: #0b111c;
        --panel: #0f1725;
        --fg: #e8ecf3;
        --muted: #a9b3c4;
        --rule: #22304a;
        --brass: #e3b252;
        color-scheme: dark;
      }
      @media (prefers-color-scheme: light) {
        :root {
          --bg: #ffffff;
          --panel: #fff9ea;
          --fg: #1c1810;
          --muted: #5a5040;
          --rule: #efe3c8;
          --brass: #8a6212;
          color-scheme: light;
        }
      }
      * { box-sizing: border-box; }
      body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.6 system-ui, sans-serif; }
      header, main { max-inline-size: 90rem; margin-inline: auto; padding-inline: 16px; }
      header { padding-block: 32px 16px; border-block-end: 1px solid var(--rule); }
      h1 { margin: 0; font-size: 28px; line-height: 36px; }
      header p { margin: 8px 0 0; color: var(--muted); max-inline-size: 48rem; }
      nav ul { display: flex; flex-wrap: wrap; gap: 8px 20px; margin: 16px 0 0; padding: 0; list-style: none; }
      a { color: var(--brass); }
      section { padding-block: 32px; border-block-end: 1px solid var(--rule); }
      h2 { margin: 0; font-size: 22px; }
      .sub { margin: 4px 0 16px; color: var(--muted); }
      h3 { margin: 16px 0 8px; font-size: 15px; color: var(--muted); font-weight: 600; }
      .shots { display: flex; gap: 12px; align-items: flex-start; }
      figure { margin: 0; min-inline-size: 0; }
      figure img { display: block; inline-size: 100%; block-size: auto; border: 1px solid var(--rule); border-radius: 6px; }
      figcaption { margin-block-start: 4px; font-size: 13px; color: var(--muted); }
      .part { margin: 48px 0 0; font-size: 26px; color: var(--brass); }
      .note { margin: 16px 0 0; padding: 12px 16px; border-inline-start: 3px solid var(--brass); background: var(--panel); max-inline-size: 60rem; }
      .table { overflow-x: auto; }
      table { border-collapse: collapse; inline-size: 100%; }
      th, td { padding: 10px 12px; border-block-end: 1px solid var(--rule); text-align: start; vertical-align: top; }
      .says { color: var(--muted); font-size: 14px; }
      td[lang="ar"] { font-size: 18px; }
      @media (max-width: 700px) { .shots { flex-direction: column; } figure { flex: none !important; inline-size: 100%; } }
    </style>
  </head>
  <body>
    <header>
      <h1>maktaba.tv: the design, for review</h1>
      <p>Stories 60.01 and 60.02. ${count} renders, English and Arabic: every layout at 360, 768 and 1280 px in dark, with light where the spec asks for it; the tagline heroes at 1280; the social cards at 1200 × 630. Each image opens full size. The spec is <code>design/README.md</code>; the copy is in <code>copy/</code>; the voice guide is <code>copy/voice.md</code>.</p>
      <p class="note"><strong>Screenshots are stand-ins.</strong> Every app screenshot in part B is a devportal Apps screenshot on test data (<code>web/packages/devportal/static/assets/apps/</code>), not a household's library and not drawn. Story 60.09 replaces them with shots of the demo library.</p>
      <p class="note"><strong>Hidden on purpose.</strong> The download page and home show no iPhone, iPad, Apple TV, Android or Android TV app and no server-package card: none is publicly installable yet (Q5, A7). The TV is shown through big screen in the desktop apps and the TV's browser; phones and tablets through the browser.</p>
      <nav aria-label="Mockups"><ul>
        <li><a href="#taglines">The tagline</a></li>
${Object.keys(MOCKUPS)
  .map((name) => `        <li><a href="#${name}">${TITLES[name][0]}</a></li>`)
  .join("\n")}
      </ul></nav>
    </header>
    <main>
${sections}
    </main>
  </body>
</html>
`;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const html = sheet();
  if (process.argv.includes("--check")) {
    if (readFileSync(out, "utf8") !== html) {
      console.error(
        "review-sheet.mjs: review-sheet.html is stale (run node design/review-sheet.mjs)"
      );
      process.exit(1);
    }
  } else {
    writeFileSync(out, html);
  }
}
