// The mockups' kit (story 13.09): icons, the two languages, and the window controls.
//
// Each mockup is one HTML file that holds both languages: an element with data-t="key" takes
// COPY[lang][key] as its text (data-th takes it as HTML, for a <bdi> or a <b> inside), and
// <i data-i="name"> becomes that icon. ?lang=ar sets <html lang="ar" dir="rtl">; render.mjs
// loads every mockup in both. The icons follow maktaba.tv's rule (part B): a 24-unit grid,
// a 1.75 stroke, round caps and joins, no fill, currentColor; the ones that point along the
// reading direction carry .dir and mirror in Arabic.

const ICONS = {
  home: '<path d="M4 10.5 12 4l8 6.5V20a1 1 0 0 1-1 1h-4.5v-6h-5v6H5a1 1 0 0 1-1-1z"/>',
  library:
    '<path d="M5 4h3v16H5zM10 4h3v16h-3z"/><path d="m15.2 4.8 2.9-.8 3.6 15.4-2.9.7z"/>',
  live: '<rect x="3" y="7" width="18" height="13" rx="2"/><path d="m8 3 4 4 4-4"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.4-4.4"/>',
  profile: '<circle cx="12" cy="8.5" r="4"/><path d="M4.5 20.5c1.2-3.6 4-5.5 7.5-5.5s6.3 1.9 7.5 5.5"/>',
  settings:
    '<circle cx="12" cy="12" r="2.8"/><path d="M18.47 10.69 L20.53 10.89 L20.53 13.11 L18.47 13.31 L17.50 15.65 L18.82 17.24 L17.24 18.82 L15.65 17.50 L13.31 18.47 L13.11 20.53 L10.89 20.53 L10.69 18.47 L8.35 17.50 L6.76 18.82 L5.18 17.24 L6.50 15.65 L5.53 13.31 L3.47 13.11 L3.47 10.89 L5.53 10.69 L6.50 8.35 L5.18 6.76 L6.76 5.18 L8.35 6.50 L10.69 5.53 L10.89 3.47 L13.11 3.47 L13.31 5.53 L15.65 6.50 L17.24 5.18 L18.82 6.76 L17.50 8.35Z"/>',
  play: '<path d="M7 4.5v15l12.5-7.5z" fill="currentColor"/>',
  pause: '<path d="M7 4.5h3.5v15H7zM13.5 4.5H17v15h-3.5z" fill="currentColor" stroke="none"/>',
  chevron: '<path d="m9 5 7 7-7 7"/>',
  chevdown: '<path d="m6 9 6 6 6-6"/>',
  back: '<path d="M19 12H5M11 6l-6 6 6 6"/>',
  onward: '<path d="M5 12h14M13 6l6 6-6 6"/>',
  heart: '<path d="M12 20s-7.5-4.4-7.5-10A4.5 4.5 0 0 1 12 7.3 4.5 4.5 0 0 1 19.5 10c0 5.6-7.5 10-7.5 10z"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  checkcircle: '<circle cx="12" cy="12" r="9"/><path d="m8 12.3 2.8 2.8L16.2 9.6"/>',
  cc: '<rect x="3" y="5" width="18" height="14" rx="2.5"/><path d="M10.5 10.2a2.4 2.4 0 1 0 0 3.6M17 10.2a2.4 2.4 0 1 0 0 3.6"/>',
  audio: '<path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z"/><path d="M15.5 9a4 4 0 0 1 0 6M18 6.5a7.5 7.5 0 0 1 0 11"/>',
  quality: '<path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/>',
  full: '<path d="M4 9V4h5M15 4h5v5M20 15v5h-5M9 20H4v-5"/>',
  back10:
    '<path d="M4.5 12a7.5 7.5 0 1 0 2.2-5.3L4.5 9"/><path d="M4.5 4.5V9H9"/><text x="12" y="15.2" font-size="6.2" text-anchor="middle" fill="currentColor" stroke="none" font-family="Maktaba Text" font-weight="600">10</text>',
  fwd10:
    '<path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3L19.5 9"/><path d="M19.5 4.5V9H15"/><text x="12" y="15.2" font-size="6.2" text-anchor="middle" fill="currentColor" stroke="none" font-family="Maktaba Text" font-weight="600">10</text>',
  next: '<path d="M5 5v14l10-7z" fill="currentColor"/><path d="M18.5 5v14"/>',
  more: '<circle cx="5.5" cy="12" r="1.3" fill="currentColor"/><circle cx="12" cy="12" r="1.3" fill="currentColor"/><circle cx="18.5" cy="12" r="1.3" fill="currentColor"/>',
  folder: '<path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/>',
  server:
    '<rect x="4" y="4" width="16" height="7" rx="1.8"/><rect x="4" y="13" width="16" height="7" rx="1.8"/><path d="M8 7.5h.01M8 16.5h.01"/>',
  chip: '<rect x="6.5" y="6.5" width="11" height="11" rx="1.5"/><path d="M9.5 3v3.5M14.5 3v3.5M9.5 17.5V21M14.5 17.5V21M3 9.5h3.5M3 14.5h3.5M17.5 9.5H21M17.5 14.5H21"/>',
  disk: '<rect x="3.5" y="5" width="17" height="14" rx="2"/><path d="M3.5 14.5h17M16.5 17h.01"/>',
  db: '<ellipse cx="12" cy="6" rx="7" ry="2.8"/><path d="M5 6v12c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8V6M5 12c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8"/>',
  update: '<path d="M20 12a8 8 0 1 1-2.3-5.6"/><path d="M20 4.5V9h-4.5"/><path d="M12 8v4.5l3 2"/>',
  download: '<path d="M12 4v11M7 10.5l5 5 5-5M5 20h14"/>',
  logs: '<path d="M7 3.5h7l4 4V20a.5.5 0 0 1-.5.5h-10.5a.5.5 0 0 1-.5-.5V4a.5.5 0 0 1 .5-.5z"/><path d="M14 3.5V8h4M9 12h6M9 15.5h6"/>',
  flag: '<path d="M5.5 21V4M5.5 4.5h11l-2 4 2 4h-11"/>',
  pausecircle: '<circle cx="12" cy="12" r="9"/><path d="M10 9v6M14 9v6"/>',
  power: '<path d="M12 3.5v8"/><path d="M7 6.3a7.5 7.5 0 1 0 10 0"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.5 3.6 5.7 3.6 9s-1.1 6.5-3.6 9c-2.5-2.5-3.6-5.7-3.6-9S9.5 5.5 12 3z"/>',
  shield: '<path d="M12 3.5 19 6v5.5c0 4.5-3 7.8-7 9-4-1.2-7-4.5-7-9V6z"/><path d="m9 12 2.2 2.2L15.5 10"/>',
  warn: '<path d="M12 4 21 19.5H3z"/><path d="M12 10v4.5M12 17.3h.01"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.7h.01"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  external: '<path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>',
  grid: '<rect x="4" y="4" width="7" height="7" rx="1.5"/><rect x="13" y="4" width="7" height="7" rx="1.5"/><rect x="4" y="13" width="7" height="7" rx="1.5"/><rect x="13" y="13" width="7" height="7" rx="1.5"/>',
  list: '<path d="M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01"/>',
  filter: '<path d="M4 5.5h16l-6.2 7.3V19l-3.6-1.8v-4.4z"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>',
  history: '<path d="M4 12a8 8 0 1 0 2.3-5.6L4 8.7"/><path d="M4 4.5v4.2h4.2"/><path d="M12 8v4.3l3 1.7"/>',
  playlist: '<path d="M4 6.5h12M4 11.5h12M4 16.5h7"/><path d="M15 14v6l5-3z"/>',
  device: '<rect x="3" y="4.5" width="18" height="12" rx="2"/><path d="M8.5 20h7M12 16.5V20"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 8.5-8.5M16.5 6.5l2.5 2.5M14.5 8.5l2 2"/>',
  help: '<circle cx="12" cy="12" r="9"/><path d="M9.6 9.3a2.5 2.5 0 0 1 4.8 1c0 1.7-2.4 2.2-2.4 3.7M12 17.3h.01"/>',
  signout: '<path d="M14 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4M10 16l4-4-4-4M14 12H4"/>',
  lamp: '<path d="M9 3h6l1 3H8zM8 6h8l-1 9H9zM10 15v3h4v-3M9 21h6"/>',
  window: '<rect x="3.5" y="4.5" width="17" height="15" rx="2"/><path d="M3.5 8.5h17"/>',
  speaker: '<path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z"/><path d="M15.5 9a4 4 0 0 1 0 6"/>',
  pip: '<rect x="3" y="5" width="18" height="14" rx="2"/><rect x="12" y="11.5" width="6.5" height="5" rx="1"/>',
  bell: '<path d="M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 2h-15z"/><path d="M10 20.5a2 2 0 0 0 4 0"/>',
  pin: '<path d="M9 4h6l-1 5 3 3H7l3-3z"/><path d="M12 12v8"/>',
  sparkle: '<path d="M12 3.5 13.8 9 19.5 11 13.8 13 12 18.5 10.2 13 4.5 11 10.2 9z"/>',
  wifi: '<path d="M3.5 9a12 12 0 0 1 17 0M6.5 12.2a7.5 7.5 0 0 1 11 0M9.5 15.4a3.3 3.3 0 0 1 5 0M12 18.5h.01"/>',
  battery: '<rect x="3" y="7.5" width="16" height="9" rx="2"/><path d="M21 10.5v3"/><rect x="5" y="9.5" width="10" height="5" rx="1" fill="currentColor" stroke="none"/>',
  chat: '<path d="M4.5 5.5h15v10h-9l-4.5 4v-4h-1.5z"/>',
};

const LANG = new URLSearchParams(location.search).get("lang") === "ar" ? "ar" : "en";
document.documentElement.lang = LANG;
document.documentElement.dir = LANG === "ar" ? "rtl" : "ltr";

function icon(name, cls = "") {
  const body = ICONS[name];
  if (!body) throw new Error(`no icon ${name}`);
  return `<svg class="i ${cls}" viewBox="0 0 24 24" aria-hidden="true">${body}</svg>`;
}

// Windows caption buttons: minimise, maximise, close (the shapes of Segoe Fluent Icons).
const CAPTION = `<div class="caption-btns" aria-hidden="true">
  <span><svg viewBox="0 0 10 10"><path d="M0 5.5h10"/></svg></span>
  <span><svg viewBox="0 0 10 10"><rect x=".5" y=".5" width="9" height="9" rx="1.5"/></svg></span>
  <span class="close"><svg viewBox="0 0 10 10"><path d="M.5.5l9 9M9.5.5l-9 9"/></svg></span>
</div>`;

// A dialog's title bar: close only.
const CAPTION_CLOSE = `<div class="caption-btns" aria-hidden="true">
  <span class="close"><svg viewBox="0 0 10 10"><path d="M.5.5l9 9M9.5.5l-9 9"/></svg></span>
</div>`;

// GNOME's header bar: the close button alone, at the end (gtk-decoration-layout's default).
const GTK_CLOSE = `<div class="gtk-btns" aria-hidden="true">
  <span><svg viewBox="0 0 10 10"><path d="M1.5 1.5l7 7M8.5 1.5l-7 7"/></svg></span>
</div>`;

// The arch of the app's icon as two brass lines with the lamp's light inside: maktaba.tv's
// one decoration (web/packages/website/design/mockups/shell.mjs ARCH), used here on the
// setup's brand pane and the installer. Symmetric: it never mirrors.
const ARCH = `<svg class="arch-art" viewBox="0 0 360 460" aria-hidden="true">
  <defs><radialGradient id="lamp" cx="180" cy="372" r="190" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#E3B252" stop-opacity="0.34"/><stop offset="0.55" stop-color="#E3B252" stop-opacity="0.08"/><stop offset="1" stop-color="#E3B252" stop-opacity="0"/></radialGradient></defs>
  <path d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460Z" fill="url(#lamp)"/>
  <path class="line" d="M24 460V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V460"/>
  <path class="line soft" d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460"/>
  <path class="line" d="M112 392C140 378 166 378 180 390C194 378 220 378 248 392M180 390V366M112 392V404C140 392 166 392 180 404C194 392 220 392 248 404V392"/>
</svg>`;

const TRAFFIC = `<div class="traffic" aria-hidden="true"><span></span><span></span><span></span></div>`;

function fill(s, vars) {
  return s.replace(/\{(\w+)\}/g, (_, k) => (k in vars ? vars[k] : `{${k}}`));
}

// Words every mockup shares (the apps' own strings where they exist: the Player's
// *Strings.kt, the shell's lang.rs, the web app's i18n).
const COMMON = {
  en: {
    "c.player": "Maktaba Player",
    "c.server": "Maktaba",
    "c.home": "Living room",
    "c.initial": "H",
    "c.search": "Search titles and transcripts",
    "nav.home": "Home",
    "nav.libraries": "Libraries",
    "nav.live": "Live TV",
    "nav.search": "Search",
    "nav.profile": "Profile",
    n1: "1", n2: "2", n3: "3", n4: "4", n5: "5",
  },
  ar: {
    "c.player": "مشغّل مكتبة",
    "c.server": "مكتبة",
    "c.home": "الجهاز الرئيسي",
    "c.initial": "ح",
    "c.search": "ابحث في العناوين والنصوص المفرَّغة",
    "nav.home": "الرئيسية",
    "nav.libraries": "المكتبات",
    "nav.live": "البث المباشر",
    "nav.search": "بحث",
    "nav.profile": "الملف الشخصي",
    n1: "١", n2: "٢", n3: "٣", n4: "٤", n5: "٥",
  },
};

const ICON_SRC = "../../../../web/public/icons/icon-192.png";

/** The Player's window chrome around a <main>: the title bar (Windows' caption buttons, or
 *  GNOME's header bar) and the rail. <div data-shell="player" data-on="libraries"
 *  data-os="win|gtk"> holding the page's <main class="page">. */
function playerShell(el) {
  const os = el.dataset.os ?? "win";
  const on = el.dataset.on;
  const nav = (id, ic) =>
    `<a class="${on === id ? "on" : ""}"><span class="pill">${icon(ic)}</span><span data-t="nav.${id}"></span></a>`;
  const head =
    os === "gtk"
      ? `<header class="titlebar gtk mica">
          <span class="tb-icon">${icon("back", "dir")}</span>
          <div class="app"><img src="${ICON_SRC}" alt=""><span data-t="c.player"></span></div>
          <div class="grow"></div>
          <div class="field search">${icon("search")}<span data-t="c.search"></span><span class="kbd">Ctrl K</span></div>
          <div class="grow"></div>
          <span class="tb-icon">${icon("full")}</span>
          <div class="acct"><span class="av" data-t="c.initial"></span><span data-t="c.home"></span>${icon("chevdown", "s16")}</div>
          ${GTK_CLOSE}
        </header>`
      : `<header class="titlebar mica">
          <div class="app"><img src="${ICON_SRC}" alt=""><span data-t="c.player"></span></div>
          <div class="grow"></div>
          <div class="field search">${icon("search")}<span data-t="c.search"></span><span class="kbd">Ctrl K</span></div>
          <div class="grow"></div>
          <span class="tb-icon">${icon("full")}</span>
          <span class="tb-icon">${icon("help")}</span>
          <div class="acct"><span class="av" data-t="c.initial"></span><span data-t="c.home"></span>${icon("chevdown", "s16")}</div>
          ${CAPTION}
        </header>`;
  const main = el.innerHTML;
  el.className = `win ${os === "gtk" ? "gnome" : "win11"}`;
  el.innerHTML = `${head}<div class="body mica"><nav class="rail">
      ${nav("home", "home")}${nav("libraries", "library")}${nav("live", "live")}${nav("search", "search")}
      <span class="spacer"></span>${nav("profile", "profile")}</nav>${main}</div>`;
}

/** Apply the page's COPY to the document: text, HTML, attributes and icons. */
function applyKit(PAGE) {
  for (const el of document.querySelectorAll("[data-shell=player]")) playerShell(el);
  const COPY = { en: { ...COMMON.en, ...PAGE.en }, ar: { ...COMMON.ar, ...PAGE.ar } };
  const words = COPY[LANG];
  if (!words) throw new Error(`no copy for ${LANG}`);
  document.title = words.title ?? document.title;
  for (const el of document.querySelectorAll("[data-t]")) {
    const k = el.dataset.t;
    if (!(k in words)) throw new Error(`missing ${LANG} copy: ${k}`);
    if (/<[a-z]/i.test(words[k])) throw new Error(`${k} holds markup: use data-th`);
    el.textContent = words[k];
  }
  for (const el of document.querySelectorAll("[data-th]")) {
    const k = el.dataset.th;
    if (!(k in words)) throw new Error(`missing ${LANG} copy: ${k}`);
    el.innerHTML = words[k];
  }
  for (const el of document.querySelectorAll("[data-i]")) {
    el.outerHTML = icon(el.dataset.i, el.className);
  }
  for (const el of document.querySelectorAll("[data-caption]"))
    el.outerHTML = el.dataset.caption === "close" ? CAPTION_CLOSE : CAPTION;
  for (const el of document.querySelectorAll("[data-gtk]")) el.outerHTML = GTK_CLOSE;
  for (const el of document.querySelectorAll("[data-traffic]")) el.outerHTML = TRAFFIC;
  for (const el of document.querySelectorAll("[data-arch]")) el.outerHTML = ARCH;
  document.documentElement.dataset.ready = "1";
}

window.MK = { LANG, icon, applyKit, fill };
