// Part B's mockups (story 60.01 part B, with 60.02 part B's copy): the home page with the
// chosen tagline, the download page, the install page as Phase 3 lays it out, the help
// index and a help topic, the wordmark, foundations and component sheets, and the social
// cards. The home and download text is read from ../../copy/<page>.<lang>.md, so the
// renders show the copy under review; the help text from the app's own topics and catalog
// (web/src/help, web/src/i18n), as 60.12 renders it.
//
// Screenshots are stand-ins until 60.09's demo-library captures exist: real dark screens of
// the apps on test data, from maktaba.dev's Apps pages (web/packages/devportal/static/
// assets/apps/). The review sheet labels each as a stand-in.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { loadTokens, resolvedThemes } from "../../../../design-system/build/build-tokens.mjs";
import { contrast, composite, parseColor } from "../../../../design-system/build/colors.mjs";
import {
  ARCH,
  attr,
  I,
  RELEASES,
  SPRITE,
  callout,
  codeBlock,
  here,
  icon,
  local,
  page,
  svg,
  wordmark,
} from "./shell.mjs";

const web = join(here, "..", "..", "..", "..");
const SHOTS = "../../../devportal/static/assets/apps";

// ------------------------------------------------------------------ copy

/** A copy file: `## key` headings, each followed by its text. Backticks become <code>. */
export function readCopy(name, lang) {
  const text = readFileSync(join(here, "..", "..", "copy", `${name}.${lang}.md`), "utf8");
  const out = {};
  let key = null;
  let buf = [];
  const flush = () => {
    if (key) out[key] = inline(buf.join(" ").trim());
  };
  for (const line of text.split("\n")) {
    const m = /^## (\S+)\s*$/.exec(line);
    if (m) {
      flush();
      key = m[1];
      buf = [];
    } else if (key && line.trim()) {
      buf.push(line.trim());
    }
  }
  flush();
  return out;
}

const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const inline = (s) =>
  esc(s)
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
// the text of an inline() string: its tags dropped until none is left, any < still in it as an
// entity (the string is our own copy, but this is what an attribute may carry)
const plain = (s) => {
  let out = s;
  for (let prev = ""; prev !== out; ) {
    prev = out;
    out = out.replace(/<[^<>]*>/g, "");
  }
  return out.replace(/</g, "&lt;").replace(/>/g, "&gt;");
};

// ------------------------------------------------------------------ icons (part B)
// The shell's style: 24-unit grid, 1.75 stroke, round caps and joins, currentColor.

const P = {
  star: svg(
    '<path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6L5.6 18.4"/>'
  ),
  library: svg('<path d="M5 4v16M9 4v16M13.5 5l4.5 14.5M4 20h16"/>'),
  subtitles: svg(
    '<rect x="3.5" y="5.5" width="17" height="13" rx="2"/><path d="M7 14h4M13 14h4M7 11h10"/>'
  ),
  live: svg(
    '<circle cx="12" cy="12" r="1.8"/><path d="M8.2 8.2a5.4 5.4 0 0 0 0 7.6M15.8 8.2a5.4 5.4 0 0 1 0 7.6M5.4 5.4a9.3 9.3 0 0 0 0 13.2M18.6 5.4a9.3 9.3 0 0 1 0 13.2"/>'
  ),
  search: I.search,
  updates: svg('<path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4.5v4h-4"/>'),
  server: svg(
    '<rect x="4.5" y="3.5" width="15" height="7" rx="1.5"/><rect x="4.5" y="13.5" width="15" height="7" rx="1.5"/><path d="M8 7h.01M8 17h.01"/>'
  ),
  check: svg('<path d="M5 12.5l4.5 4.5L19 7.5"/>'),
  download: svg('<path d="M12 4v11M7 10.5l5 5 5-5M5 19.5h14"/>'),
  tv: svg('<rect x="3" y="4.5" width="18" height="12" rx="1.5"/><path d="M9 20h6M12 16.5V20"/>'),
  phone: svg('<rect x="7" y="3" width="10" height="18" rx="2.2"/><path d="M11 18h2"/>'),
  laptop: svg('<rect x="5" y="5" width="14" height="10" rx="1.2"/><path d="M3 18.5h18"/>'),
  window: svg(
    '<rect x="3.5" y="4.5" width="17" height="15" rx="1.5"/><path d="M3.5 8.5h17"/>'
  ),
  terminal: svg(
    '<rect x="3.5" y="4.5" width="17" height="15" rx="1.5"/><path d="M7.5 10l2.5 2-2.5 2M12.5 14.5h4"/>'
  ),
  box: svg(
    '<rect x="5" y="3.5" width="14" height="17" rx="1.5"/><path d="M9 8h6M9 11.5h6M9 17h.01M12 17h.01"/>'
  ),
  clock: svg('<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>'),
  info: I.note,
  lock: svg(
    '<rect x="5.5" y="10.5" width="13" height="9" rx="1.5"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>'
  ),
  shield: svg('<path d="M12 3.5l7 2.5v5.5c0 4.4-3 7.6-7 9-4-1.4-7-4.6-7-9V6z"/>'),
  chev: svg('<path d="M9 6l6 6-6 6"/>', "chev"),
  play: svg('<path d="M8 5.5v13l10.5-6.5z"/>'),
  memory: svg(
    '<rect x="4" y="7" width="16" height="10" rx="1.5"/><path d="M8 7V4.5M12 7V4.5M16 7V4.5M8 19.5V17M12 19.5V17M16 19.5V17"/>'
  ),
};

/** The apps' brass star, as the section kicker carries it. */
const kicker = (text) => `<p class="kicker">${P.star}${text}</p>`;

// The arch as a single line, for the hero's stage and the privacy band.
const ARCH_LINE = `<svg class="arch-line" viewBox="0 0 360 400" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true" focusable="false"><path vector-effect="non-scaling-stroke" d="M24 400V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V400"/><path vector-effect="non-scaling-stroke" opacity="0.45" d="M42 400V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V400"/></svg>`;

// ------------------------------------------------------------------ screenshots and frames

/** Stand-in screenshots: [file stem, width, height]. Each has an -en and an -ar file. */
export const SHOT = {
  bigHome: ["big-home", 1280, 720],
  bigLibraries: ["big-libraries", 1280, 720],
  webHome: ["web-home", 1200, 750],
  webLibrary: ["web-library", 1200, 750],
  webPhone: ["web-phone", 480, 1044],
  desktopHome: ["desktop-player-home", 1200, 750],
  installSynology: ["install-synology", 1280, 900],
};

const DEVICE_BAR = {
  window: (lang) =>
    `<div class="bar" aria-hidden="true"><span class="dots"><i></i><i></i><i></i></span><span class="title">${lang === "ar" ? "مشغّل مكتبة" : "Maktaba Player"}</span></div>`,
  browser: (lang) =>
    `<div class="bar" aria-hidden="true"><span class="dots"><i></i><i></i><i></i></span><span class="address">${P.lock}${lang === "ar" ? "مكتبة" : "Maktaba"}</span></div>`,
};

/**
 * A screenshot in its device frame. `shotLang` picks the picture's language (a page shows
 * its own; the two-language section shows both). Pictures never mirror (data-nomirror).
 */
function device(kind, shot, { lang, shotLang = lang, alt, name, caption, eager = false, width }) {
  const [stem, w, h] = shot;
  const src = `${SHOTS}/${stem}-${shotLang}.webp`;
  const img = `<img src="${src}" width="${w}" height="${h}" alt="${attr(alt)}" lang="${shotLang}" data-nomirror${eager ? ' fetchpriority="high"' : ' loading="lazy" decoding="async"'} />`;
  const bar = DEVICE_BAR[kind] ? DEVICE_BAR[kind](lang) : "";
  const stand = kind === "tv" ? '<span class="stand"></span>' : "";
  const cap =
    name || caption
      ? `<figcaption>${name ? `<strong>${name}</strong>` : ""}${caption ?? ""}</figcaption>`
      : "";
  const style = width ? ` style="inline-size: ${width}"` : "";
  return `<figure class="device" data-kind="${kind}"${style}><div class="frame-wrap"><div class="frame-fit"><div class="body">${bar}<div class="screen">${img}</div></div>${stand}</div></div>${cap}</figure>`;
}

// ------------------------------------------------------------------ the release (60.11's data)
// The mockup's numbers are v0.3.131's own (the GitHub Releases API, 11 November 2026).

const REL = {
  version: "0.3.131",
  date: { en: "11 November 2026", ar: "11 نوفمبر 2026" },
  files: {
    dmg: ["Maktaba_0.3.131_aarch64.dmg", 202574614],
    coreDmg: ["Maktaba-Core_0.3.131_aarch64.dmg", 187434392],
    exe: ["Maktaba_0.3.131_x64-setup.exe", 145712510],
    portable: ["maktaba-windows-portable-0.3.131.zip", 13648906],
    playerMsi: ["Maktaba-Player_0.3.131_x64.msi", 140740040],
    appimage: ["Maktaba_0.3.131_amd64.AppImage", 254241272],
    deb: ["Maktaba_0.3.131_amd64.deb", 214397434],
    rpm: ["Maktaba-0.3.131-1.x86_64.rpm", 214551081],
    appimageArm: ["Maktaba_0.3.131_aarch64.AppImage", 333572616],
    playerDeb: ["maktaba-player_0.3.131_amd64.deb", 122786668],
    installSh: ["install.sh", 23048],
    compose: ["maktaba-docker-compose.yml", 6754],
    synology: ["maktaba-synology-compose.yaml", 5254],
    truenas: ["maktaba-truenas-custom-app.yaml", 4104],
  },
  digest: {
    dmg: "e186d0640826872af4c59369d115c5f0e16260d008757a06201ea0674ebf227e",
  },
};

function size(bytes, lang) {
  const mb = bytes / 1e6;
  const [n, unit] =
    mb >= 1
      ? [mb >= 100 ? Math.round(mb).toString() : mb.toFixed(1), lang === "ar" ? "ميغابايت" : "MB"]
      : [(bytes / 1e3).toFixed(bytes >= 1e4 ? 0 : 1), lang === "ar" ? "كيلوبايت" : "KB"];
  return `${n} ${unit}`;
}

const COSIGN = `cosign verify-blob --bundle checksums.txt.sigstore.json \\
  --certificate-identity https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v0.3.131 \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\
  checksums.txt
sha256sum --ignore-missing -c checksums.txt`;

// ------------------------------------------------------------------ the home page

const HOME_PATH = { home: "/", download: "/download/" };

function heroSection(lang, c) {
  return `<section class="hero" aria-labelledby="hero-title">
    <div class="frame grid">
      <div>
        <h1 class="display-1" id="hero-title">${c.tagline}</h1>
        <p class="lead">${c["hero.lead"]}</p>
        <div class="actions">
          <a class="button" href="${local(lang, "/download/#mac")}">${P.download}${c["hero.download"]}</a>
          <a class="button quiet" href="#how">${c["hero.how"]}</a>
        </div>
        <p class="fine">${c["hero.fine"]}</p>
      </div>
      <div class="stage">
        ${ARCH_LINE}
        ${device("tv", SHOT.bigHome, { lang, alt: plain(c["hero.shot.alt"]), eager: true })}
      </div>
    </div>
  </section>`;
}

function home(lang, { heroOnly = false } = {}) {
  const c = readCopy("home", lang);
  const other = lang === "ar" ? "en" : "ar";
  const screens = [
    ["tv", SHOT.bigLibraries],
    ["phone", SHOT.webPhone],
    ["tablet", SHOT.webLibrary],
    ["window", SHOT.desktopHome, "computer"],
    ["browser", SHOT.webHome],
  ];
  const does = [
    ["library", P.library, "/help/library/"],
    ["subtitles", P.subtitles, "/help/transcription/"],
    ["live", P.live, "/help/watch-tv/"],
    ["search", P.search, "/help/search/"],
    ["updates", P.updates, "/help/updates/"],
    ["runs", P.server, "/install/"],
  ];
  const body = heroOnly
    ? `<main id="main">
  ${heroSection(lang, c)}
</main>`
    : `<main id="main">
  ${heroSection(lang, c)}
  <section class="band shot" id="how" aria-labelledby="screens-title">
    <div class="frame">
      <div class="section-head center">
        ${kicker(c["screens.kicker"])}
        <h2 class="display-2" id="screens-title">${c["screens.h2"]}</h2>
        <p>${c["screens.lead"]}</p>
      </div>
      <ul class="shelf">
        ${screens
          .map(
            ([kind, shot, key = kind]) =>
              `<li data-kind="${kind}">${device(kind, shot, {
                lang,
                alt: plain(c[`screens.${key}.alt`]),
                name: c[`screens.${key}.name`],
                caption: c[`screens.${key}.caption`],
              })}</li>`
          )
          .join("\n        ")}
      </ul>
    </div>
  </section>
  <section class="band" aria-labelledby="does-title">
    <div class="frame">
      <div class="section-head">
        ${kicker(c["does.kicker"])}
        <h2 class="display-2" id="does-title">${c["does.h2"]}</h2>
      </div>
      <ul class="features">
        ${does
          .map(
            ([k, ic, href]) => `<li class="feature">
          <span class="ring">${ic}</span>
          <h3>${c[`does.${k}.title`]}</h3>
          <p>${c[`does.${k}.text`]}</p>
          <a class="onward" href="${local(lang, href)}">${c[`does.${k}.link`]}${I.onward}</a>
        </li>`
          )
          .join("\n        ")}
      </ul>
    </div>
  </section>
  ${arabicSection(lang, c, other)}
  <section class="band" aria-labelledby="stays-title">
    <div class="frame stays">
      <div>
        <div class="section-head">
          ${kicker(c["stays.kicker"])}
          <h2 class="display-2" id="stays-title">${c["stays.h2"]}</h2>
        </div>
        <ul class="promises">
          ${["home", "say", "ads", "open"]
            .map(
              (k) =>
                `<li>${P.check}<div><strong>${c[`stays.${k}.title`]}</strong><span>${c[`stays.${k}.text`]}</span></div></li>`
            )
            .join("\n          ")}
        </ul>
        <p style="margin-block-start: var(--spacing-8)"><a class="onward" href="${local(lang, "/legal/privacy/")}">${c["stays.link"]}${I.onward}</a></p>
      </div>
      ${ARCH_LINE}
    </div>
  </section>
  <section class="band section" aria-labelledby="start-title">
    <div class="frame">
      <div class="section-head center">
        ${kicker(c["start.kicker"])}
        <h2 class="display-2" id="start-title">${c["start.h2"]}</h2>
      </div>
      <ol class="start-steps">
        ${[1, 2, 3]
          .map((n) => `<li><h3>${c[`start.${n}.title`]}</h3><p>${c[`start.${n}.text`]}</p></li>`)
          .join("\n        ")}
      </ol>
      <div class="start-actions">
        <a class="button" href="${local(lang, "/download/")}">${P.download}${c["start.download"]}</a>
        <a class="button quiet" href="${local(lang, "/install/")}">${c["start.guides"]}${I.onward}</a>
      </div>
    </div>
  </section>
</main>`;
  return page({
    lang,
    key: "home",
    current: null,
    title: plain(c["meta.title"]),
    description: plain(c["meta.description"]),
    social: plain(c["social.title"]),
    variant: "b",
    body,
  });
}

/** Arabic first-class: the same screen in both languages, the page's own first. */
function arabicSection(lang, c, other) {
  const order = [lang, other];
  return `<section class="band shot" aria-labelledby="arabic-title">
    <div class="frame">
      <div class="section-head">
        ${kicker(c["arabic.kicker"])}
        <h2 class="display-2" id="arabic-title">${c["arabic.h2"]}</h2>
        <p>${c["arabic.lead"]}</p>
      </div>
      <ul class="pair">
        ${order
          .map(
            (l) =>
              `<li>${device("browser", SHOT.webHome, {
                lang,
                shotLang: l,
                alt: plain(c[`arabic.${l}.alt`]),
                caption: c[`arabic.${l}.caption`],
              })}</li>`
          )
          .join("\n        ")}
      </ul>
    </div>
  </section>`;
}

// ------------------------------------------------------------------ the download page

function download(lang) {
  const c = readCopy("download", lang);
  const h = readCopy("home", lang);
  const F = REL.files;
  const row = (label, [file, bytes]) =>
    `<li><span>${label}</span><span class="size"><code>${file}</code> · ${size(bytes, lang)}</span></li>`;
  const facts = (primary) =>
    `<dl class="facts-row"><dt>${c["version.label"]}</dt><dd>${c["version.label"]} ${REL.version}</dd><dt>${c["date.label"]}</dt><dd>${REL.date[lang]}</dd><dt>${c["size.label"]}</dt><dd>${size(primary[1], lang)}</dd></dl>`;
  const card = ({ id, ic, mine = false, primary, href, others, verify, guide }) => {
    const main = `<div class="card-main">
          ${mine ? `<span class="badge">${P.check}${c.mine}</span>` : ""}
          <div class="card-head"><span class="ring">${ic}</span><h2>${c[`${id}.name`]}</h2></div>
          <p class="needs">${c[`${id}.needs`]}</p>
          ${primary ? facts(primary) : ""}
          <a class="button" href="${href}">${primary ? P.download : ""}${c[`${id}.primary`]}${primary ? "" : I.onward}</a>
          <a class="onward" href="${local(lang, guide)}">${c.guide}${I.onward}</a>
        </div>`;
    const more = `<div class="card-more">
          <details class="disclose"${mine ? " open" : ""}><summary>${P.chev}${c.other}</summary><ul>${others.join("")}</ul></details>
          ${verify ? `<details class="disclose"${mine || id === "nas" ? " open" : ""}><summary>${P.chev}${c.verify}</summary>${verify}</details>` : ""}
        </div>`;
    return `<li class="card${mine ? " mine" : ""}" id="${id}" aria-labelledby="${id}-name">${main.replace("<h2>", `<h2 id="${id}-name">`)}
        ${more}
      </li>`;
  };
  const macVerify = `<p>${c["mac.verify.text"]}</p><code class="hash">sha256:${REL.digest.dmg}</code>${codeBlock(lang, {
    name: "Terminal",
    code: `shasum -a 256 ${F.dmg[0]}`,
    copyName: lang === "ar" ? "نسخ الأمر" : "Copy the command",
  })}<p>${c["mac.verify.after"]}</p>`;
  const nasVerify = `<p>${c["nas.verify.text"]}</p>${codeBlock(lang, {
    name: lang === "ar" ? "في الطرفية" : "In a terminal",
    code: COSIGN,
    copyName: lang === "ar" ? "نسخ الأوامر" : "Copy the commands",
  })}`;
  const cards = [
    card({
      id: "mac",
      ic: P.laptop,
      mine: true,
      primary: F.dmg,
      href: `${RELEASES.replace("/latest", "/download/v0.3.131")}/${F.dmg[0]}`,
      others: [row(c["mac.other.core"], F.coreDmg)],
      verify: macVerify,
      guide: "/help/mac-app/",
    }),
    card({
      id: "windows",
      ic: P.window,
      primary: F.exe,
      href: "#",
      others: [row(c["windows.other.portable"], F.portable), row(c["windows.other.player"], F.playerMsi)],
      guide: "/help/desktop-player/",
    }),
    card({
      id: "linux",
      ic: P.terminal,
      primary: F.appimage,
      href: "#",
      others: [
        row(c["linux.other.deb"], F.deb),
        row(c["linux.other.rpm"], F.rpm),
        row(c["linux.other.arm"], F.appimageArm),
        row(c["linux.other.player"], F.playerDeb),
        row(c["linux.other.headless"], F.installSh),
      ],
      guide: "/install/docker/",
    }),
    card({
      id: "nas",
      ic: P.box,
      href: local(lang, "/install/nas/"),
      others: [row(c["nas.other.compose"], F.compose), row(c["nas.other.synology"], F.synology), row(c["nas.other.truenas"], F.truenas)],
      verify: nasVerify,
      guide: "/install/nas/",
    }),
  ];
  const body = `<main id="main">
  <section class="page-head" aria-labelledby="dl-title">
    <div class="frame">
      <h1 id="dl-title">${c.h1}</h1>
      <p class="lead">${c.lead}</p>
      <p class="meta">${c.version.replace("{version}", REL.version).replace("{date}", REL.date[lang])}</p>
      <p class="platform-note">${P.laptop}${c.note}</p>
    </div>
  </section>
  <section class="band" aria-label="${attr(plain(c.h1))}">
    <div class="frame">
      <ul class="cards">
      ${cards.join("\n      ")}
      </ul>
      <p class="all-files"><a href="${RELEASES}">${c.all}${I.out}</a></p>
    </div>
  </section>
  <section class="band" style="padding-block-start: 0" aria-labelledby="need-title">
    <div class="frame">
      <div class="need">
        <div>
          <h2 class="display-2" id="need-title">${c["need.h2"]}</h2>
          <p>${c["need.p1"]}</p>
          <p>${c["need.p2"]}</p>
          <a class="onward" href="${local(lang, "/install/")}">${c["need.link"]}${I.onward}</a>
        </div>
        <ul class="diagram" aria-hidden="true">
          <li class="node home"><span class="ring">${P.server}</span>${c["need.home"]}</li>
          <li class="arrow">${I.onward}</li>
          <li><ul class="screens">
            <li>${P.tv}${c["need.tv"]}</li>
            <li>${P.phone}${c["need.phone"]}</li>
            <li>${P.laptop}${c["need.computer"]}</li>
          </ul></li>
        </ul>
      </div>
    </div>
  </section>
  ${arabicSection(lang, h, lang === "ar" ? "en" : "ar")}
</main>`;
  return page({
    lang,
    key: "download",
    current: "download",
    title: `${plain(c["meta.title"])} · ${lang === "ar" ? "مكتبة" : "Maktaba"}`,
    description: plain(c["meta.description"]),
    social: plain(c["social.title"]),
    variant: "b",
    body,
  });
}

// ------------------------------------------------------------------ install, as Phase 3 lays it out

const INSTALL = {
  en: {
    title: "Install Maktaba on a Synology NAS · Maktaba",
    description: "Install Maktaba on a Synology NAS as one Container Manager project, in about 15 minutes.",
    crumb: "Install guides",
    h1: "Install Maktaba on a Synology NAS",
    lead: "On DSM 7.2 or newer, Maktaba runs as one Container Manager project. It takes about 15 minutes, and Maktaba updates itself after that.",
    chips: [[P.clock, "About 15 minutes"], [P.box, "DSM 7.2 or newer"], [P.memory, "4 GB of memory or more"]],
    toc: "On this page",
    sections: ["Before you start", "Your NAS", "Step by step", "After setup"],
    prepare: [
      "A DSM account with administrator rights, and Container Manager from Package Center.",
      "An SSD volume, if the NAS has one: Maktaba keeps its database there, and browsing is faster.",
      "The shared folders that hold your videos, and which of them Maktaba may write to.",
    ],
    private: ["Tip:", "Your answers stay in this page. Nothing you type is sent anywhere."],
    answers: "Answer a few questions about your NAS, and the page writes the project file for you.",
    formCaption: "The questions, and the project file they make",
    formAlt: "The Synology guide's questions: the model, its memory, the user's number and the folders, beside the project file they make.",
    steps: [
      ["Make a user for Maktaba", "In <strong>Control Panel → User &amp; Group</strong>, create the user <code>maktaba</code>, with a strong password and no administrator rights."],
      ["Make Maktaba’s folder", "In <strong>File Station</strong>, create the folder <code>maktaba</code> inside the shared folder <code>docker</code>, and give the user <code>maktaba</code> <strong>Read/Write</strong> on it."],
      ["Share your video folders", "In <strong>Control Panel → Shared Folder</strong>, give <code>maktaba</code> <strong>Read only</strong> on each folder of videos, or <strong>Read/Write</strong> where Maktaba may save subtitles."],
      ["Create the project", "In <strong>Container Manager → Project → Create</strong>, name it <code>maktaba</code>, choose the path <code>docker/maktaba</code>, paste the project file, then <strong>Next</strong> and <strong>Done</strong>."],
      ["Finish the setup", "When the project runs, open its terminal (<strong>Container → maktaba → Details → Terminal → Create</strong>) and run:"],
    ],
    warn: ["Warning:", "If the user <code>maktaba</code> can’t write to <code>docker/maktaba</code>, the project stops at once. Its log names the folder: give that user Read/Write there and start the project again."],
    file: "The project file: <code>compose.yaml</code>",
    copyFile: "Copy the project file",
    terminal: "In the container’s terminal",
    copyCommand: "Copy the command",
    after: "Maktaba now runs on the NAS. Open it from any browser at home, and it takes you through the rest.",
    afterLink: "After setup: updates and quiet hours",
  },
  ar: {
    title: "تثبيت برنامج مكتبة على خادم Synology · مكتبة",
    description: "تثبيت برنامج مكتبة على خادم Synology مشروعًا واحدًا في Container Manager، في نحو 15 دقيقة.",
    crumb: "أدلة التثبيت",
    h1: "تثبيت برنامج مكتبة على خادم Synology",
    lead: "على DSM 7.2 أو أحدث، يعمل برنامج مكتبة مشروعًا واحدًا في Container Manager. ويستغرق التثبيت نحو 15 دقيقة، ثم يحدّث البرنامج نفسه بعد ذلك.",
    chips: [[P.clock, "نحو 15 دقيقة"], [P.box, "DSM 7.2 أو أحدث"], [P.memory, "ذاكرة 4 غيغابايت أو أكثر"]],
    toc: "في هذه الصفحة",
    sections: ["قبل أن تبدؤوا", "خادم التخزين عندكم", "خطوةً خطوة", "بعد الإعداد"],
    prepare: [
      "حساب في DSM بصلاحيات المدير، وتطبيق Container Manager من مركز الحزم.",
      "وحدة تخزين SSD إن كانت في الجهاز، فيها يحفظ البرنامج قاعدة بياناته، فيصبح التصفح أسرع.",
      "المجلدات المشتركة التي تحفظ مقاطعكم، وأيّها يُسمح للبرنامج بالكتابة فيه.",
    ],
    private: ["نصيحة:", "تبقى إجاباتكم في هذه الصفحة، ولا يُرسَل شيء مما تكتبونه إلى أي مكان."],
    answers: "أجيبوا عن أسئلة قليلة عن خادم التخزين عندكم، فتكتب الصفحة ملف المشروع لكم.",
    formCaption: "الأسئلة، وملف المشروع الذي تكتبه الصفحة منها",
    formAlt: "أسئلة دليل Synology: الطراز وذاكرته ورقم المستخدم والمجلدات، وبجانبها ملف المشروع الذي يُكتب منها.",
    steps: [
      ["أنشئوا مستخدمًا للبرنامج", "من لوحة التحكم <bdi lang=\"en\"><strong>Control Panel → User &amp; Group</strong></bdi>، أنشئوا المستخدم <code>maktaba</code> بكلمة مرور قوية، ومن دون صلاحيات المدير."],
      ["أنشئوا مجلد البرنامج", "في <bdi lang=\"en\"><strong>File Station</strong></bdi>، أنشئوا المجلد <code>maktaba</code> داخل المجلد المشترك <code>docker</code>، وامنحوا المستخدم <code>maktaba</code> إذن القراءة والكتابة عليه."],
      ["شاركوا مجلدات المقاطع", "من <bdi lang=\"en\"><strong>Control Panel → Shared Folder</strong></bdi>، امنحوا <code>maktaba</code> إذن القراءة فقط على كل مجلد فيه مقاطع، أو القراءة والكتابة حيث يُسمح للبرنامج بحفظ الترجمات."],
      ["أنشئوا المشروع", "من <bdi lang=\"en\"><strong>Container Manager → Project → Create</strong></bdi>، سمّوه <code>maktaba</code>، واختاروا المسار <code>docker/maktaba</code>، والصقوا ملف المشروع، ثم «Next» و«Done»."],
      ["أكملوا الإعداد", "حين يعمل المشروع، افتحوا طرفيّته (<bdi lang=\"en\"><strong>Container → maktaba → Details → Terminal → Create</strong></bdi>) وشغّلوا:"],
    ],
    warn: ["تحذير:", "إن لم يستطع المستخدم <code>maktaba</code> الكتابة في <code>docker/maktaba</code>، يتوقف المشروع فورًا، ويذكر سجلّه اسم المجلد. فامنحوه إذن القراءة والكتابة هناك، وشغّلوا المشروع من جديد."],
    file: "ملف المشروع: <code>compose.yaml</code>",
    copyFile: "نسخ ملف المشروع",
    terminal: "في طرفية الحاوية",
    copyCommand: "نسخ الأمر",
    after: "يعمل البرنامج الآن على خادم التخزين. افتحوه من أي متصفح داخل البيت، وسيأخذكم في بقية الخطوات.",
    afterLink: "بعد الإعداد: التحديثات والساعات الهادئة",
  },
};

const SYNOLOGY_COMPOSE = `services:
  maktaba:
    image: ghcr.io/hamza-labs-core/maktaba-server:latest
    container_name: maktaba
    restart: unless-stopped
    user: "1026:100"
    stop_grace_period: 90s
    mem_limit: 4g
    environment:
      MAKTABA_PLATFORM: dsm
      TZ: Asia/Riyadh
    network_mode: host
    volumes:
      - /volume1/docker/maktaba:/var/lib/maktaba
      - /volume1/Movies:/volume1/Movies:ro`;

function installPage(lang) {
  const c = INSTALL[lang];
  const ids = ["before", "nas", "steps", "after"];
  const setup = "/opt/maktaba/server/bin/maktaba-server container setup";
  return page({
    lang,
    key: "install-p3",
    current: "install",
    title: c.title,
    description: c.description,
    variant: "b",
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <p class="crumbs"><a href="${local(lang, "/install/")}">${I.back}${c.crumb}</a></p>
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
      <ul class="meta-chips">${c.chips.map(([ic, t]) => `<li>${ic}${t}</li>`).join("")}</ul>
    </div>
  </section>
  <div class="band">
    <div class="frame text-layout with-toc">
      <nav class="toc" aria-labelledby="toc-title">
        <h2 id="toc-title">${c.toc}</h2>
        <ol>${c.sections.map((s, i) => `<li><a href="#${ids[i]}"${i === 2 ? ' aria-current="true"' : ""}>${s}</a></li>`).join("")}</ol>
      </nav>
      <article class="prose">
        <h2 id="before">${c.sections[0]}</h2>
        <ul class="checklist">${c.prepare.map((p) => `<li>${P.check}<span>${p}</span></li>`).join("")}</ul>
        <h2 id="nas">${c.sections[1]}</h2>
        <p>${c.answers}</p>
        ${callout("tip", ...c.private)}
        ${device("browser", SHOT.installSynology, { lang, alt: c.formAlt, caption: c.formCaption })}
        <h2 id="steps">${c.sections[2]}</h2>
        <ol class="steps">
          ${c.steps
            .map(([h, p], i) => {
              const extra =
                i === 1
                  ? callout("warning", ...c.warn)
                  : i === 3
                    ? codeBlock(lang, { name: c.file, code: SYNOLOGY_COMPOSE, copyName: c.copyFile })
                    : i === 4
                      ? codeBlock(lang, { name: c.terminal, code: setup, copyName: c.copyCommand })
                      : "";
              return `<li><h3>${h}</h3><p>${p}</p>${extra}</li>`;
            })
            .join("\n          ")}
        </ol>
        <h2 id="after">${c.sections[3]}</h2>
        <p>${c.after}</p>
        <p><a class="onward" href="${local(lang, "/install/after-setup/")}">${c.afterLink}${I.onward}</a></p>
      </article>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ help (60.12)

function catalog(lang) {
  const tree = JSON.parse(readFileSync(join(web, "src", "i18n", `${lang}.json`), "utf8"));
  const flat = {};
  const walk = (o, p) => {
    for (const [k, v] of Object.entries(o)) {
      const key = p ? `${p}.${k}` : k;
      if (v && typeof v === "object") walk(v, key);
      else flat[key] = v;
    }
  };
  walk(tree, "");
  return flat;
}

const HIDDEN_TOPICS = ["apple-apps", "apple-tv", "android-apps", "android-tv"];

function helpCategories() {
  const src = readFileSync(join(web, "src", "help", "topics.ts"), "utf8");
  const block = src.slice(src.indexOf("HELP_CATEGORIES"), src.indexOf("] as const"));
  return [...block.matchAll(/id: "([a-z-]+)",\s*topics: \[([^\]]*)\]/g)].map((m) => [
    m[1],
    [...m[2].matchAll(/"([a-z-]+)"/g)].map((t) => t[1]),
  ]);
}

const HELP = {
  en: {
    title: "Help · Maktaba",
    description: "How to use Maktaba at home: the same help topics the app shows, in English and Arabic.",
    h1: "Help",
    lead: "How to use Maktaba at home, one topic at a time.",
    same: "This is the same help you’ll find in the app, under Help.",
    label: "Find a topic",
    many: "{n} topics",
    one: "1 topic",
    crumb: "Help",
    toc: "On this page",
    related: "Related topics",
    where: "In Maktaba:",
  },
  ar: {
    title: "المساعدة · مكتبة",
    description: "طريقة استخدام برنامج مكتبة في البيت: مواضيع المساعدة نفسها التي يعرضها التطبيق، بالعربية والإنجليزية.",
    h1: "المساعدة",
    lead: "طريقة استخدام برنامج مكتبة في البيت، موضوعًا بعد موضوع.",
    same: "هذه هي المساعدة نفسها التي تجدونها في التطبيق، تحت «المساعدة».",
    label: "البحث عن موضوع",
    many: "{n} موضوعًا",
    one: "موضوع واحد",
    crumb: "المساعدة",
    toc: "في هذه الصفحة",
    related: "مواضيع ذات صلة",
    where: "في مكتبة:",
  },
};

function help(lang) {
  const c = HELP[lang];
  const cat = catalog(lang);
  // Q5: the site names no app that is not publicly installable, so the topics for the
  // native phone, tablet and TV apps wait until those apps are public.
  const groups = helpCategories()
    .map(([id, t]) => [id, t.filter((x) => !HIDDEN_TOPICS.includes(x))])
    .filter(([, t]) => t.length);
  const total = groups.reduce((n, [, t]) => n + t.length, 0);
  return page({
    lang,
    key: "help",
    current: "help",
    title: c.title,
    description: c.description,
    variant: "b",
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
      <p class="same-help">${P.info}${c.same}</p>
      <div class="filter" role="search">
        <label for="filter">${c.label}</label>
        <div class="field">${I.search}<input id="filter" type="search" autocomplete="off" placeholder="${cat["help.searchPlaceholder"]}" aria-describedby="count" /></div>
        <p class="count" id="count" aria-live="polite" data-many="${c.many}" data-one="${c.one}">${c.many.replace("{n}", total)}</p>
      </div>
    </div>
  </section>
  <div class="band">
    <div class="frame help-groups">
      ${groups
        .map(
          ([id, topics]) => `<section class="help-group" aria-labelledby="cat-${id}">
        <h2 id="cat-${id}">${cat[`help.category.${id}`]}</h2>
        <ul class="topics">${topics
          .map(
            (t) =>
              `<li><a href="${local(lang, `/help/${t}/`)}"><strong>${esc(cat[`help.topic.${t}.title`])}</strong><span>${esc(cat[`help.topic.${t}.summary`])}</span></a></li>`
          )
          .join("")}</ul>
      </section>`
        )
        .join("\n      ")}
    </div>
  </div>
</main>`,
  });
}

/**
 * A slice of a help topic's Markdown, rendered as 60.12 renders it: headings with their
 * ids, paragraphs, lists, bold, code; app routes become "In Maktaba: …" text, help links
 * become site links. Enough of the language to show the layout, not the whole renderer.
 */
function helpMarkdown(md, lang, { maxSections = 2, maxSub = 3 } = {}) {
  const c = HELP[lang];
  const out = [];
  const toc = [];
  let h2 = 0;
  let h3InSection = 0;
  let list = null;
  const closeList = () => {
    if (list) out.push(`</${list}>`);
    list = null;
  };
  const md2 = (s) =>
    esc(s)
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, text, href) => {
        if (/^https?:/.test(href)) return `<a href="${href}">${text}</a>`;
        const help = /^\/help\/([a-z-]+)(#[a-z0-9-]+)?$/.exec(href);
        if (help) return `<a href="${local(lang, `/help/${help[1]}/`)}${help[2] ?? ""}">${text}</a>`;
        return `<span class="where">${c.where} <strong>${text}</strong></span>`;
      });
  for (const raw of md.split("\n")) {
    const line = raw.trimEnd();
    let m;
    if (/^# /.test(line)) continue;
    if ((m = /^## (.+?) \{#([a-z0-9-]+)\}$/.exec(line))) {
      closeList();
      h2 += 1;
      h3InSection = 0;
      if (h2 > maxSections) break;
      out.push(`<h2 id="${m[2]}">${md2(m[1])}</h2>`);
      toc.push([m[2], m[1]]);
      continue;
    }
    if ((m = /^### (.+?) \{#([a-z0-9-]+)\}$/.exec(line))) {
      closeList();
      h3InSection += 1;
      if (h3InSection > maxSub) break;
      out.push(`<h3 id="${m[2]}">${md2(m[1])}</h3>`);
      continue;
    }
    if (h2 === 0) {
      if (line.trim()) out.push(`<p class="lead-p">${md2(line)}</p>`);
      continue;
    }
    if ((m = /^- (.+)$/.exec(line))) {
      if (list !== "ul") {
        closeList();
        out.push("<ul>");
        list = "ul";
      }
      out.push(`<li>${md2(m[1])}</li>`);
      continue;
    }
    if ((m = /^\d+\. (.+)$/.exec(line))) {
      if (list !== "ol") {
        closeList();
        out.push("<ol>");
        list = "ol";
      }
      out.push(`<li>${md2(m[1])}</li>`);
      continue;
    }
    closeList();
    if (line.trim()) out.push(`<p>${md2(line)}</p>`);
  }
  closeList();
  return { html: out.join("\n        "), toc };
}

function helpTopic(lang) {
  const c = HELP[lang];
  const cat = catalog(lang);
  const md = readFileSync(join(web, "src", "help", "content", lang, "search.md"), "utf8");
  const { html, toc } = helpMarkdown(md, lang);
  const related = ["big-screen", "videos", "transcription"];
  return page({
    lang,
    key: "help-topic",
    current: "help",
    title: `${cat["help.topic.search.title"]} · ${c.h1} · ${lang === "ar" ? "مكتبة" : "Maktaba"}`,
    description: cat["help.topic.search.summary"],
    variant: "b",
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <p class="crumbs"><a href="${local(lang, "/help/")}">${I.back}${c.crumb}</a></p>
      <h1>${esc(cat["help.topic.search.title"])}</h1>
      <p class="same-help">${P.info}${c.same}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame topic-layout">
      <nav class="toc" aria-labelledby="toc-title">
        <h2 id="toc-title">${c.toc}</h2>
        <ol>${toc.map(([id, t], i) => `<li><a href="#${id}"${i === 0 ? ' aria-current="true"' : ""}>${esc(t)}</a></li>`).join("")}</ol>
      </nav>
      <article class="prose">
        ${html}
        <section class="related" aria-labelledby="related-title">
          <h2 id="related-title">${c.related}</h2>
          <ul class="topics">${related
            .map(
              (t) =>
                `<li><a href="${local(lang, `/help/${t}/`)}"><strong>${esc(cat[`help.topic.${t}.title`])}</strong><span>${esc(cat[`help.topic.${t}.summary`])}</span></a></li>`
            )
            .join("")}</ul>
        </section>
      </article>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ the wordmark sheet

const WM = {
  en: {
    title: "The wordmark · Maktaba design",
    h1: "The wordmark",
    lead: "The app icon with the name, in the display face. The page’s own script in the header; both scripts in the footer and on the social card.",
    header: "In the header",
    headerNote: "Icon 32 px, the name at 23 px, 11 px apart. The page’s script only: Maktaba on English pages, مكتبة on Arabic ones.",
    both: "Both scripts: footer and social card",
    bothNote: "The page’s script first, then a brass rule, then the other script. On an Arabic page the order runs right to left.",
    sizes: "Sizes",
    sizesNote: "From the largest use down to the smallest allowed.",
    min: "Smallest",
    minNote: "Under 24 px the name is left out, and the icon stands alone: the favicon and the touch icons.",
    space: "Clear space",
    spaceNote: "Half the icon’s width on every side, kept free of text and edges.",
    surfaces: "On the site’s surfaces",
    never: "Never",
    nevers: [
      "Recolour the icon, or set it on a light tile.",
      "Stretch, rotate or mirror it. The arch is symmetric, and stays as drawn in Arabic.",
      "Set the name in another face, or letter-space the Arabic.",
      "Put the two names on one line without the rule between them.",
    ],
    dark: "Dark",
    light: "Light",
  },
  ar: {
    title: "الشعار · تصميم مكتبة",
    h1: "الشعار",
    lead: "أيقونة التطبيق مع الاسم بخط العناوين: بلغة الصفحة وحدها في الترويسة، وباللغتين في التذييل وفي بطاقة المشاركة.",
    header: "في الترويسة",
    headerNote: "الأيقونة 32 بكسل، والاسم 23 بكسل، وبينهما 11 بكسل. بلغة الصفحة وحدها: Maktaba في الصفحات الإنجليزية، ومكتبة في العربية.",
    both: "باللغتين: التذييل وبطاقة المشاركة",
    bothNote: "اسمٌ بلغة الصفحة أولًا، ثم خط نحاسي، ثم الاسم باللغة الأخرى. وفي الصفحة العربية يجري الترتيب من اليمين إلى اليسار.",
    sizes: "المقاسات",
    sizesNote: "من أكبر استعمال إلى أصغر مقاس مسموح.",
    min: "أصغر مقاس",
    minNote: "تحت 24 بكسل يُترك الاسم، وتبقى الأيقونة وحدها: أيقونة المتصفح وأيقونات الشاشة الرئيسية.",
    space: "المساحة الخالية",
    spaceNote: "نصف عرض الأيقونة من كل جهة، لا نص فيها ولا حافة.",
    surfaces: "على أسطح الموقع",
    never: "لا نفعل",
    nevers: [
      "تغيير ألوان الأيقونة، أو وضعها على مربع فاتح.",
      "مطّها أو تدويرها أو قلبها. فالقوس متناظر، ويبقى كما رُسم في الصفحات العربية.",
      "كتابة الاسم بخط آخر، أو مدّ المسافات بين الحروف العربية.",
      "جمع الاسمين في سطر واحد بلا الخط الفاصل بينهما.",
    ],
    dark: "داكن",
    light: "فاتح",
  },
};

function wordmarkSheet(lang) {
  const c = WM[lang];
  const other = lang === "ar" ? "en" : "ar";
  return page({
    lang,
    key: "wordmark",
    title: c.title,
    description: c.lead,
    variant: "b",
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame sheet prose wide">
      <h2>${c.header}</h2>
      <p>${c.headerNote}</p>
      <div class="specimen">${wordmark(lang, { size: 32 })}<span class="dim">32</span></div>
      <div class="specimen" dir="${other === "ar" ? "rtl" : "ltr"}" lang="${other}">${wordmark(other, { size: 32 })}<span class="dim">32</span></div>
      <h2>${c.both}</h2>
      <p>${c.bothNote}</p>
      <div class="specimen">${wordmark(lang, { size: 56, both: true })}</div>
      <div class="specimen" dir="${other === "ar" ? "rtl" : "ltr"}" lang="${other}">${wordmark(other, { size: 56, both: true })}</div>
      <h2>${c.sizes}</h2>
      <p>${c.sizesNote}</p>
      <div class="specimen sizes">${[72, 48, 32, 24].map((s) => `<span class="size-cell">${wordmark(lang, { size: s })}<span class="dim">${s}</span></span>`).join("")}</div>
      <h2>${c.min}</h2>
      <p>${c.minNote}</p>
      <div class="specimen sizes">${[24, 16].map((s) => `<span class="size-cell">${icon(s)}<span class="dim">${s}</span></span>`).join("")}<span class="size-cell"><img src="../../../../public/icons/favicon-32.png" width="16" height="16" alt="" /><span class="dim">favicon-32.png @16</span></span></div>
      <h2>${c.space}</h2>
      <p>${c.spaceNote}</p>
      <div class="specimen"><span class="clear-space">${wordmark(lang, { size: 64 })}</span></div>
      <h2>${c.surfaces}</h2>
      <div class="surfaces">
        <div class="surface s-bg">${wordmark(lang, { size: 40 })}</div>
        <div class="surface s-hero">${wordmark(lang, { size: 40 })}</div>
        <div class="surface s-light" data-theme="light">${wordmark(lang, { size: 40 })}<span class="dim">${c.light}</span></div>
      </div>
      <h2>${c.never}</h2>
      <ul>${c.nevers.map((n) => `<li>${n}</li>`).join("")}</ul>
    </div>
  </div>
</main>`,
    extraCss: [],
  });
}

// ------------------------------------------------------------------ the foundations sheet

const FOUND = {
  en: {
    title: "Foundations · Maktaba design",
    h1: "Type and colour",
    lead: "The display face for headlines, the text face for everything else; the site’s colours with the contrast each pair reaches, dark and light.",
    display: "The display face: Maktaba Display (Amiri Bold)",
    text: "The text face: Maktaba Text (IBM Plex Sans Arabic)",
    sample: "Your own library, on every screen at home",
    sampleAr: "مكتبتكم، على كل شاشة في البيت",
    body: "Maktaba keeps your films, series and recordings on a computer or a NAS in your home, and plays them on every screen in the house.",
    bodyAr: "يحفظ برنامج مكتبة أفلامكم ومسلسلاتكم وتسجيلاتكم على حاسوب أو خادم تخزين في بيتكم، ويعرضها على كل شاشة فيه.",
    colour: "Colour",
    colourNote: "Each pair as the tests check it: text 4.5:1, large text and parts of controls 3:1.",
    cols: ["Pair", "Where", "Needs", "Dark", "Light"],
    budget: "Font budget",
    budgetNote: "Per page, every file a page can load: an English page 49 KB, an Arabic page 142 KB, inside 150 KB.",
  },
  ar: {
    title: "الأسس · تصميم مكتبة",
    h1: "الخط واللون",
    lead: "خط العناوين للعناوين الكبيرة، وخط النص لكل ما سواها؛ وألوان الموقع مع نسبة التباين لكل زوج منها، في الوضعين الداكن والفاتح.",
    display: "خط العناوين: <bdi>Maktaba Display (Amiri Bold)</bdi>",
    text: "خط النص: <bdi>Maktaba Text (IBM Plex Sans Arabic)</bdi>",
    sample: "Your own library, on every screen at home",
    sampleAr: "مكتبتكم، على كل شاشة في البيت",
    body: "Maktaba keeps your films, series and recordings on a computer or a NAS in your home, and plays them on every screen in the house.",
    bodyAr: "يحفظ برنامج مكتبة أفلامكم ومسلسلاتكم وتسجيلاتكم على حاسوب أو خادم تخزين في بيتكم، ويعرضها على كل شاشة فيه.",
    colour: "اللون",
    colourNote: "كل زوج كما تفحصه الاختبارات: النص 4.5:1، والنص الكبير وأجزاء أدوات التحكم 3:1.",
    cols: ["الزوج", "مكانه", "المطلوب", "داكن", "فاتح"],
    budget: "ميزانية الخطوط",
    budgetNote: "لكل صفحة، بكل ملف قد تحمّله: الصفحة الإنجليزية 49 كيلوبايت، والعربية 142 كيلوبايت، ضمن 150 كيلوبايت.",
  },
};

const SCALE_LATIN = [
  ["display-1", "60 / 68", "≥ 1024"],
  ["display-1-phone", "40 / 48", "< 768"],
  ["display-2", "44 / 52", ""],
  ["display-3", "32 / 40", ""],
];
const SCALE_ARABIC = [
  ["display-1", "66 / 100", "≥ 1024"],
  ["display-1-phone", "44 / 68", "< 768"],
  ["display-2", "48 / 76", ""],
  ["display-3", "36 / 56", ""],
];

// The pairs part B adds, shown with part A's main ones (tests/contrast.test.mjs checks all).
export const SHOWN_PAIRS = [
  ["color.semantic.fg", "color.semantic.bg", 4.5, "body text"],
  ["color.semantic.muted", "site.surface.shot", 4.5, "captions on the screenshot band"],
  ["color.semantic.fg", "site.surface.shot", 4.5, "a caption's name"],
  ["color.semantic.accent-text", "site.surface.shot", 4.5, "kicker and links on the band"],
  ["color.semantic.accent-text", "site.surface.section", 4.5, "a topic's title, a step number"],
  ["site.accent.cta-fg", "site.accent.cta", 4.5, "the call to action"],
  ["color.semantic.fg", ["color.semantic.accent-soft", "site.surface.section"], 4.5, "the badge, the platform note"],
  ["site.accent.mark", "site.surface.shot", 3, "the shelf line"],
  ["site.accent.mark", "site.surface.section", 3, "the chosen card's edge, a feature ring"],
  ["site.line.control", "site.surface.section", 3, "a card icon's edge"],
];

// The "where" column on the Arabic sheet, in the order of SHOWN_PAIRS.
const WHERE_AR = [
  "نص الصفحة",
  "التعليقات على شريط الصور",
  "اسم الصورة في تعليقها",
  "العنوان الصغير والروابط على شريط الصور",
  "عنوان الموضوع ورقم الخطوة",
  "زر الإجراء الرئيسي",
  "الشارة وملاحظة النظام",
  "خط الرف",
  "حافة البطاقة المختارة وحلقة الميزة",
  "حافة أيقونة البطاقة",
];

const THEMES = resolvedThemes(await loadTokens());

const get = (tree, path) => path.split(".").reduce((n, k) => n?.[k], tree);
const colorOf = (tree, ref) =>
  Array.isArray(ref) ? composite(get(tree, ref[0]), get(tree, ref[1])) : parseColor(get(tree, ref));
const hex = ({ r, g, b }) => "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("").toUpperCase();

function foundations(lang) {
  const c = FOUND[lang];
  const t = THEMES;
  const swatch = (tree, ref) => `<span class="sw" style="background:${hex(colorOf(tree, ref))}"></span>`;
  const rows = SHOWN_PAIRS.map(([fg, bg, min, whereEn], i) => {
    const where = lang === "ar" ? WHERE_AR[i] : whereEn;
    const ratio = (name) => contrast(colorOf(t[name], fg), colorOf(t[name], bg)).toFixed(2);
    const label = `${fg.replace(/^(color\.semantic|site)\./, "")} / ${(Array.isArray(bg) ? bg[0] : bg).replace(/^(color\.semantic|site)\./, "")}`;
    return `<tr><td><span class="pair-sw">${swatch(t.dark, bg)}${swatch(t.dark, fg)}</span><code>${label}</code></td><td>${where}</td><td>${min}</td><td>${ratio("dark")}</td><td>${ratio("light")}</td></tr>`;
  }).join("");
  const scale = (rows, ar) =>
    rows
      .map(
        ([step, px, when]) =>
          `<div class="type-row"><span class="dim"><code>${step}</code> <bdi dir="ltr">${px}${when ? ` · ${when}` : ""}</bdi></span><p class="spec ${step}${ar ? " ar" : ""}" lang="${ar ? "ar" : "en"}" dir="${ar ? "rtl" : "ltr"}">${ar ? c.sampleAr : `<bdi>${c.sample}</bdi>`}</p></div>`
      )
      .join("");
  const first = lang === "ar";
  return page({
    lang,
    key: "foundations",
    title: c.title,
    description: c.lead,
    variant: "b",
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame sheet wide">
      <h2>${c.display}</h2>
      ${first ? scale(SCALE_ARABIC, true) + scale(SCALE_LATIN, false) : scale(SCALE_LATIN, false) + scale(SCALE_ARABIC, true)}
      <h2>${c.text}</h2>
      <div class="type-row"><span class="dim"><code>body</code> <bdi dir="ltr">17 / 28</bdi></span><p lang="en" dir="ltr" class="spec body-en"><bdi>${c.body}</bdi></p></div>
      <div class="type-row"><span class="dim"><code>body</code> <bdi dir="ltr">18 / 32</bdi></span><p lang="ar" dir="rtl" class="spec body-ar">${c.bodyAr}</p></div>
      <h2>${c.colour}</h2>
      <p>${c.colourNote}</p>
      <div class="table-wrap"><table class="contrast"><thead><tr>${c.cols.map((h) => `<th scope="col">${h}</th>`).join("")}</tr></thead><tbody>${rows}</tbody></table></div>
      <h2>${c.budget}</h2>
      <p>${c.budgetNote}</p>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ the component sheet (part B)

const COMP = {
  en: {
    title: "Part B’s components · Maktaba design",
    h1: "Part B’s components",
    lead: "Frames, icons, the download card and its states, feature blocks, buttons in every state, and a step with a screenshot.",
    frames: "Screenshot frames",
    framesNote: "One per kind of screen. The picture keeps its own shape; the frame is symmetric and never mirrors.",
    icons: "Icons",
    iconsNote: "24-unit grid, 1.75 stroke, round caps. The first row mirrors on an Arabic page; the rest never do.",
    mirrors: "Mirror",
    never: "Never mirror",
    buttons: "Button states",
    states: ["Default", "Hover", "Pressed", "Focus", "Not available"],
    nav: "Current page",
    card: "The download card",
    cardNote: "Closed, hovered and focused. The visitor’s own platform is the wide card at the top of the page.",
    feature: "A feature block",
    step: "A step with a screenshot",
  },
  ar: {
    title: "مكوّنات الجزء الثاني · تصميم مكتبة",
    h1: "مكوّنات الجزء الثاني",
    lead: "الإطارات والأيقونات وبطاقة التنزيل بحالاتها، وكتل الميزات، والأزرار بكل حالاتها، وخطوة فيها صورة.",
    frames: "إطارات الصور",
    framesNote: "إطار لكل نوع من الشاشات. تبقى الصورة بشكلها، والإطار متناظر لا ينقلب أبدًا.",
    icons: "الأيقونات",
    iconsNote: "شبكة من 24 وحدة، وخط بسمك 1.75، وأطراف مستديرة. الصف الأول ينقلب في الصفحة العربية، والباقي لا ينقلب أبدًا.",
    mirrors: "تنقلب",
    never: "لا تنقلب أبدًا",
    buttons: "حالات الأزرار",
    states: ["العادية", "عند المرور", "عند الضغط", "عند التركيز", "غير متاح"],
    nav: "الصفحة الحالية",
    card: "بطاقة التنزيل",
    cardNote: "مغلقة، وعند المرور، وعند التركيز. أما نظام الزائر نفسه فبطاقته العريضة في أعلى الصفحة.",
    feature: "كتلة ميزة",
    step: "خطوة فيها صورة",
  },
};

function componentsB(lang) {
  const c = COMP[lang];
  const h = readCopy("home", lang);
  const d = readCopy("download", lang);
  const F = REL.files;
  const mirrorIcons = [I.onward, I.back, I.out, P.chev];
  const fixedIcons = [P.play, P.check, P.search, P.download, P.library, P.subtitles, P.live, P.updates, P.server, P.tv, P.phone, P.laptop, P.window, P.terminal, P.box, P.star, I.lang, I.copy];
  const smallCard = (state) => `<li class="card${state === "hover" ? " is-hover" : ""}${state === "focus" ? " demo-focus" : ""}">
        <div class="card-main">
          <div class="card-head"><span class="ring">${P.window}</span><h3 class="card-title">${d["windows.name"]}</h3></div>
          <p class="needs">${d["windows.needs"]}</p>
          <dl class="facts-row"><dt>${d["version.label"]}</dt><dd>${d["version.label"]} ${REL.version}</dd><dt>${d["size.label"]}</dt><dd>${size(F.exe[1], lang)}</dd></dl>
          <a class="button" href="#">${P.download}${d["windows.primary"]}</a>
        </div>
        <div class="card-more"><details class="disclose"><summary>${P.chev}${d.other}</summary></details></div>
      </li>`;
  return page({
    lang,
    key: "components-b",
    title: c.title,
    description: c.lead,
    variant: "b",
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame sheet wide">
      <h2>${c.frames}</h2>
      <p>${c.framesNote}</p>
      <ul class="shelf">
        <li data-kind="tv">${device("tv", SHOT.bigHome, { lang, alt: plain(h["hero.shot.alt"]), name: "tv", caption: "1.1% bezel, 10 px radius, stand 34%" })}</li>
        <li data-kind="phone">${device("phone", SHOT.webPhone, { lang, alt: plain(h["screens.phone.alt"]), name: "phone", caption: "3.4% bezel" })}</li>
        <li data-kind="tablet">${device("tablet", SHOT.webLibrary, { lang, alt: plain(h["screens.tablet.alt"]), name: "tablet", caption: "2.6% bezel" })}</li>
        <li data-kind="window">${device("window", SHOT.desktopHome, { lang, alt: plain(h["screens.computer.alt"]), name: "window", caption: "30 px bar, app name" })}</li>
        <li data-kind="browser">${device("browser", SHOT.webHome, { lang, alt: plain(h["screens.browser.alt"]), name: "browser", caption: "30 px bar, site name only" })}</li>
      </ul>
      <h2>${c.icons}</h2>
      <p>${c.iconsNote}</p>
      <p class="dim">${c.mirrors}</p>
      <div class="icon-grid">${mirrorIcons.map((i) => `<span>${i.replace('class="chev"', 'class="dir-icon"')}</span>`).join("")}</div>
      <p class="dim">${c.never}</p>
      <div class="icon-grid">${fixedIcons.map((i) => `<span>${i}</span>`).join("")}</div>
      <h2>${c.buttons}</h2>
      <div class="state-grid">
        ${c.states
          .map((s, i) => {
            const cls = ["", " is-hover", " is-active", " demo-focus", ""][i];
            const dis = i === 4 ? ' aria-disabled="true"' : "";
            return `<div><span class="dim">${s}</span><a class="button${cls}"${dis} href="#">${P.download}${d["mac.primary"]}</a><a class="button quiet${cls}"${dis} href="#">${h["hero.how"]}</a></div>`;
          })
          .join("\n        ")}
      </div>
      <h2>${c.card}</h2>
      <p>${c.cardNote}</p>
      <ul class="cards">${["", "hover", "focus"].map(smallCard).join("")}</ul>
      <h2>${c.feature}</h2>
      <ul class="features"><li class="feature"><span class="ring">${P.subtitles}</span><h3>${h["does.subtitles.title"]}</h3><p>${h["does.subtitles.text"]}</p><a class="onward" href="#">${h["does.subtitles.link"]}${I.onward}</a></li></ul>
      <h2>${c.step}</h2>
      <ol class="steps"><li><h3>${INSTALL[lang].sections[1]}</h3><p>${INSTALL[lang].answers}</p>${device("browser", SHOT.installSynology, { lang, alt: INSTALL[lang].formAlt, caption: INSTALL[lang].formCaption })}</li></ol>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ social cards (1200 × 630)

function social(lang, { title, sub }) {
  const dir = lang === "ar" ? "rtl" : "ltr";
  const fonts = lang === "ar" ? ["maktaba-display-700-arabic", "maktaba-display-700-latin", "maktaba-text-400-arabic", "maktaba-text-400-latin"] : ["maktaba-display-700-latin", "maktaba-text-400-latin"];
  return `<!doctype html>
<!-- Generated by design/mockups/make.mjs (story 60.01 part B): a social card, 1200 × 630. Edit part-b.mjs, not this file. -->
<html lang="${lang}" dir="${dir}">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=1200" />
    <title>${plain(title)}</title>
    ${fonts.map((f) => `<link rel="preload" href="../../static/fonts/${f}.woff2" as="font" type="font/woff2" crossorigin />`).join("\n    ")}
    <link rel="stylesheet" href="../site-tokens.css" />
    <link rel="stylesheet" href="../fonts.css" />
    <link rel="stylesheet" href="../shell.css" />
    <link rel="stylesheet" href="../site.css" />
    <link rel="stylesheet" href="mockup.css" />
  </head>
  <body class="social-card">
    ${SPRITE}
    <div class="card-canvas">
      ${ARCH_LINE}
      <div class="card-top">${wordmark(lang, { size: 56, both: true })}</div>
      <p class="card-title" data-fit>${title}</p>
      <p class="card-foot"><span dir="ltr">maktaba.tv</span><span>${sub}</span></p>
    </div>
  </body>
</html>
`;
}

const SOCIAL_SUB = {
  home: { en: "Free and open source, in Arabic and English", ar: "برنامج حرّ مفتوح المصدر، بالعربية والإنجليزية" },
  download: { en: "Mac · Windows · Linux · NAS", ar: "Mac وWindows وLinux وخوادم التخزين" },
};

// ------------------------------------------------------------------ the list

/** name → (lang) => html. Render settings for each are in RENDER below. */
export const PART_B = {
  home: (lang) => home(lang),
  download,
  "install-p3": installPage,
  help,
  "help-topic": helpTopic,
  wordmark: wordmarkSheet,
  foundations,
  "components-b": componentsB,
  // the tagline chosen on 2026-11-12 (Q1): proposal 1
  "social-home": (lang) =>
    social(lang, { title: readCopy("home", lang).tagline, sub: SOCIAL_SUB.home[lang] }),
  "social-download": (lang) =>
    social(lang, { title: readCopy("download", lang)["social.title"], sub: SOCIAL_SUB.download[lang] }),
};

/** How each part B mockup renders: widths, light widths, full page or one viewport. */
export const RENDER_B = {
  home: { light: [1280] },
  download: { light: [1280] },
  ...Object.fromEntries(
    ["social-home", "social-download"].map((k) => [
      k,
      { widths: [1200], full: false, height: 630, social: true },
    ])
  ),
};

export { HOME_PATH };
