// Writes the part A mockups (story 60.01): one HTML file per layout and language, from the
// shell below and each page's copy in English and Arabic (each written in its own
// language, in the voice of ../../copy/voice.md). `node make.mjs` rewrites them;
// `node make.mjs --check` fails when a committed file differs from what it would write.
//
// The mockups use the real tokens (../site-tokens.css), the real fonts (../fonts.css) and
// the shell's own stylesheet and script (../shell.css, ../shell.js), so what is reviewed is
// what 60.03 builds on.

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const ICON = "../../../../public/icons/icon-192.png";
const RELEASES = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest";
const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";

// ------------------------------------------------------------------ icons
// One style: 24-unit grid, drawn at 20 px, 1.75 stroke, round caps and joins, currentColor.
// `dir-icon` marks the ones that mean a direction: they mirror on an Arabic page.

const svg = (body, cls = "") =>
  `<svg${cls ? ` class="${cls}"` : ""} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`;
const I = {
  onward: svg('<path d="M5 12h14M13 6l6 6-6 6"/>', "dir-icon"),
  back: svg('<path d="M15 6l-6 6 6 6"/>', "dir-icon"),
  out: svg('<path d="M14 5h5v5M19 5l-8 8M18 14v4a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h4"/>', "dir-icon"),
  menu: svg('<path d="M4 7h16M4 12h16M4 17h16"/>'),
  lang: svg('<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.6 2.6 3.6 5.6 3.6 8.5s-1 5.9-3.6 8.5c-2.6-2.6-3.6-5.6-3.6-8.5s1-5.9 3.6-8.5z"/>'),
  copy: svg('<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5V6a1.5 1.5 0 0 0-1.5-1.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5"/>'),
  note: svg('<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5M12 8h.01"/>'),
  warning: svg('<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>'),
  tip: svg('<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z"/>'),
  search: svg('<circle cx="11" cy="11" r="6.5"/><path d="M16 16l4 4"/>'),
  person: svg('<circle cx="12" cy="8" r="3.5"/><path d="M5 20c.8-3.6 3.6-5.5 7-5.5s6.2 1.9 7 5.5"/>'),
};

// The arch of the app's icon, drawn as two brass lines with the lamp's light inside: the
// front door's and the 404's one decoration. Symmetric, so it never mirrors; hidden from
// assistive technology and under 1024 px.
const ARCH = `<svg class="arch" viewBox="0 0 360 460" aria-hidden="true" focusable="false">
  <defs><radialGradient id="lamp" cx="180" cy="372" r="190" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#E3B252" stop-opacity="0.34"/><stop offset="0.55" stop-color="#E3B252" stop-opacity="0.08"/><stop offset="1" stop-color="#E3B252" stop-opacity="0"/></radialGradient></defs>
  <path d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460Z" fill="url(#lamp)"/>
  <path class="line" d="M24 460V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V460"/>
  <path class="line soft" d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460"/>
  <path class="line" d="M112 392C140 378 166 378 180 390C194 378 220 378 248 392M180 390V366M112 392V404C140 392 166 392 180 404C194 392 220 392 248 404V392"/>
</svg>`;

// ------------------------------------------------------------------ the shell's words

const SHELL = {
  en: {
    name: "Maktaba",
    skip: "Skip to the page",
    menu: "Menu",
    nav: "Main",
    install: "Install",
    messages: "Messages",
    download: "Download",
    other: { lang: "ar", dir: "rtl", label: "العربية" },
    footerAbout:
      "Maktaba keeps a household’s films and series on its own computer and plays them on every screen at home. Free and open source, under the MIT licence.",
    groups: [
      ["Get Maktaba", [["Install guides", "/install/"], ["Downloads on GitHub", RELEASES, true]]],
      ["Help", [["Help topics", "/help/"], ["Messages and what they mean", "/problems/"]]],
      ["Legal", [["Privacy", "/legal/privacy/"], ["Terms", "/legal/terms/"], ["Copyright", "/legal/copyright/"]]],
      ["Elsewhere", [["maktaba.dev, for developers", "https://maktaba.dev/", true], ["Source on GitHub", GITHUB, true]]],
    ],
    footerNav: "Site",
    copyright: "© 2026 Hamza Labs",
    readIn: "اقرأ هذه الصفحة بالعربية",
    copied: "Copied",
  },
  ar: {
    name: "مكتبة",
    skip: "الانتقال إلى محتوى الصفحة",
    menu: "القائمة",
    nav: "الرئيسية",
    install: "التثبيت",
    messages: "الرسائل",
    download: "التنزيل",
    other: { lang: "en", dir: "ltr", label: "English" },
    footerAbout:
      "يحفظ برنامج مكتبة أفلام البيت ومسلسلاته على حاسوبه الخاص، ويعرضها على كل شاشة فيه. وهو برنامج حرّ مفتوح المصدر برخصة MIT.",
    groups: [
      ["الحصول على مكتبة", [["أدلة التثبيت", "/install/"], ["التنزيلات على GitHub", RELEASES, true]]],
      ["المساعدة", [["مواضيع المساعدة", "/help/"], ["الرسائل ومعانيها", "/problems/"]]],
      ["الشؤون القانونية", [["الخصوصية", "/legal/privacy/"], ["شروط الاستخدام", "/legal/terms/"], ["حقوق النشر", "/legal/copyright/"]]],
      ["روابط أخرى", [["موقع المطوّرين maktaba.dev", "https://maktaba.dev/", true], ["الشيفرة المصدرية على GitHub", GITHUB, true]]],
    ],
    footerNav: "روابط الموقع",
    copyright: "© 2026 Hamza Labs",
    readIn: "Read this page in English",
    copied: "تم النسخ",
  },
};

const twin = (lang, path) => (lang === "en" ? `/ar${path}` : path);
const local = (lang, path) => (lang === "en" || /^https?:/.test(path) ? path : `/ar${path}`);

function header(lang, current) {
  const s = SHELL[lang];
  const item = (key, href, ext) =>
    `<li><a href="${ext ? href : local(lang, href)}"${current === key ? ' aria-current="page"' : ""}>${s[key]}${ext ? ` ${I.out}` : ""}</a></li>`;
  return `<a class="skip" href="#main">${s.skip}</a>
<header class="site-header">
  <div class="frame bar">
    <a class="mark" href="${local(lang, "/")}"><img src="${ICON}" width="32" height="32" alt="" />${s.name}</a>
    <nav class="nav" id="site-nav" aria-label="${s.nav}">
      <ul>
        ${item("install", "/install/")}
        ${item("messages", "/problems/")}
        ${item("download", RELEASES, true)}
      </ul>
    </nav>
    ${langSwitch(lang, current)}
    <button class="menu-button" type="button" aria-expanded="false" aria-controls="site-nav">${I.menu}${s.menu}</button>
  </div>
</header>`;
}

function langSwitch(lang, page) {
  const o = SHELL[lang].other;
  return `<a class="lang-switch" href="${twin(lang, PATHS[page] ?? "/")}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${I.lang}<span>${o.label}</span></a>`;
}

function footer(lang, page) {
  const s = SHELL[lang];
  const o = s.other;
  const groups = s.groups
    .map(
      ([title, links]) => `<div>
        <h2>${title}</h2>
        <ul>${links
          .map(([t, href, ext]) => `<li><a href="${ext ? href : local(lang, href)}">${t}${ext ? ` ${I.out}` : ""}</a></li>`)
          .join("")}</ul>
      </div>`
    )
    .join("\n      ");
  return `<footer class="site-footer">
  <nav class="frame cols" aria-label="${s.footerNav}">
      <div class="about">
        <a class="mark" href="${local(lang, "/")}"><img src="${ICON}" width="24" height="24" alt="" />${s.name}</a>
        <p>${s.footerAbout}</p>
      </div>
      ${groups}
  </nav>
  <div class="frame"><div class="base">
    <p><span dir="ltr">${s.copyright}</span></p>
    <a href="${twin(lang, PATHS[page] ?? "/")}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${s.readIn}</a>
  </div></div>
</footer>
<p class="visually-hidden" id="announce" aria-live="polite"></p>`;
}

const PATHS = {
  front: "/",
  legal: "/legal/privacy/",
  problem: "/problems/home-unreachable/",
  problems: "/problems/",
  install: "/install/unraid/",
  lost: "/instal/synology/",
  components: "/",
};

// A 404 answers the address that was asked for, which is no page: it carries no canonical or
// hreflang alternates (they would name a dead address, and the link check rightly refuses one),
// and asks not to be indexed.
const NOT_FOUND_HEAD = '<meta name="robots" content="noindex" />';

function page({ lang, key, title, description, body, current, bodyClass = "", extraCss = [], menuOpen = false }) {
  const dir = lang === "ar" ? "rtl" : "ltr";
  const path = PATHS[key];
  const en = `https://maktaba.tv${path}`;
  const ar = `https://maktaba.tv/ar${path}`;
  const fonts = (lang === "ar" ? ["400-arabic", "600-arabic", "400-latin"] : ["400-latin", "600-latin"])
    .map((f) => `<link rel="preload" href="../../static/fonts/maktaba-text-${f}.woff2" as="font" type="font/woff2" crossorigin />`)
    .join("\n    ");
  const css = [...extraCss, "../site-tokens.css", "../fonts.css", "../shell.css", "mockup.css"]
    .map((h) => `<link rel="stylesheet" href="${h}" />`)
    .join("\n    ");
  return `<!doctype html>
<!-- Generated by design/mockups/make.mjs (story 60.01 part A). Edit make.mjs, not this file. -->
<html lang="${lang}" dir="${dir}"${menuOpen ? ' data-menu="open"' : ""}>
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark light" />
    <title>${title}</title>
    <meta name="description" content="${description}" />
    ${key === "lost" ? NOT_FOUND_HEAD : `<link rel="canonical" href="${lang === "en" ? en : ar}" />
    <link rel="alternate" hreflang="en" href="${en}" />
    <link rel="alternate" hreflang="ar" href="${ar}" />
    <link rel="alternate" hreflang="x-default" href="${en}" />`}
    ${fonts}
    ${css}
    <script>document.documentElement.classList.add("js");</script>
    <script src="../shell.js" defer></script>
  </head>
  <body${bodyClass ? ` class="${bodyClass}"` : ""}>
${header(lang, current)}
${body}
${footer(lang, key)}
  </body>
</html>
`;
}

// ------------------------------------------------------------------ shared parts

function codeBlock(lang, { name, code, copyName, copied = false }) {
  const t = lang === "ar" ? { copy: "نسخ", done: "تم النسخ" } : { copy: "Copy", done: "Copied" };
  return `<div class="code">
  <div class="code-bar"><span class="name">${name}</span><button class="copy" type="button" aria-label="${copyName}" data-done="${t.done}"${copied ? " data-show-copied" : ""}>${I.copy}<span class="copy-label">${t.copy}</span></button></div>
  <pre><code>${code}</code></pre>
</div>`;
}

function callout(kind, label, text) {
  return `<div class="callout" data-kind="${kind}" role="note">${I[kind]}<p><span class="label">${label}</span> ${text}</p></div>`;
}

// ------------------------------------------------------------------ the front door (text page)

const FRONT = {
  en: {
    title: "Maktaba: your films and series, at home",
    description:
      "Maktaba runs on a computer or a NAS in your home, keeps your films and series in order, and plays them on every screen.",
    h1: "Your own library of films and series, at home",
    lead: "Maktaba runs on a computer or a NAS in your home. It keeps your films, series and recordings in order, makes subtitles for them, and plays them on your phone, your computer and your TV.",
    primary: "Choose your install guide",
    secondary: "Download the apps",
    facts: [
      ["One person sets it up", "Everyone at home watches, each with their own sign-in."],
      ["It updates itself", "New versions install in the quiet hours you choose."],
      ["Your library stays yours", "Your films stay at home. Watching while you’re away is up to you."],
    ],
    factsLabel: "What Maktaba does for your home",
    ways: [
      ["Install on a NAS or a computer", "Step-by-step guides for Synology, TrueNAS, Unraid, QNAP, UGREEN and any machine with Docker. Most take about 15 minutes.", "Choose your guide", "/install/"],
      ["Apps for every screen", "Maktaba for Mac, iPhone, iPad, Apple TV, Android phones and Android TV. Until this site has a download page, the apps come with each release on GitHub.", "Downloads on GitHub", RELEASES],
      ["When Maktaba shows a message", "Each message has a page here that says what happened and what you can do about it.", "Messages and what they mean", "/problems/"],
      ["For developers", "The source code, the guide to writing plugins and the API are on maktaba.dev.", "Go to maktaba.dev", "https://maktaba.dev/"],
    ],
    waysLabel: "Where to start",
  },
  ar: {
    title: "مكتبة: أفلامكم ومسلسلاتكم في بيتكم",
    description:
      "يعمل برنامج مكتبة على حاسوب أو خادم تخزين في بيتكم، فيرتّب أفلامكم ومسلسلاتكم ويعرضها على كل شاشة.",
    h1: "مكتبتكم من الأفلام والمسلسلات، في بيتكم",
    lead: "يعمل برنامج مكتبة على حاسوب أو خادم تخزين في بيتكم. يرتّب أفلامكم ومسلسلاتكم وتسجيلاتكم، ويصنع لها الترجمات، ويعرضها على الهاتف والحاسوب والتلفاز.",
    primary: "اختيار دليل التثبيت",
    secondary: "تنزيل التطبيقات",
    facts: [
      ["يُعِدّه شخص واحد", "ويشاهد كل من في البيت، ولكلٍّ منهم حسابه."],
      ["يحدّث نفسه", "تُثبَّت الإصدارات الجديدة في الساعات الهادئة التي تختارونها."],
      ["مكتبتكم تبقى لكم", "تبقى أفلامكم في بيتكم، والمشاهدة من خارجه باختياركم."],
    ],
    factsLabel: "ما يقدّمه برنامج مكتبة لبيتكم",
    ways: [
      ["التثبيت على خادم تخزين أو حاسوب", "أدلة خطوة بخطوة لأجهزة Synology وTrueNAS وUnraid وQNAP وUGREEN، ولأي جهاز فيه Docker. ويستغرق أغلبها نحو 15 دقيقة.", "اختيار الدليل", "/install/"],
      ["تطبيقات لكل شاشة", "تطبيقات مكتبة لأجهزة Mac وiPhone وiPad وApple TV، ولهواتف Android وأجهزة Android TV. وإلى أن تُضاف صفحة التنزيل إلى هذا الموقع، تجدونها مع كل إصدار على GitHub.", "التنزيلات على GitHub", RELEASES],
      ["حين تظهر لكم رسالة", "لكل رسالة من برنامج مكتبة صفحة هنا تشرح ما حدث، وما يمكنكم فعله.", "الرسائل ومعانيها", "/problems/"],
      ["للمطوّرين", "الشيفرة المصدرية ودليل كتابة الإضافات وواجهة البرمجة كلها على موقع maktaba.dev.", "الانتقال إلى maktaba.dev", "https://maktaba.dev/"],
    ],
    waysLabel: "من أين تبدؤون",
  },
};

function front(lang) {
  const c = FRONT[lang];
  const href = (h) => local(lang, h);
  return page({
    lang,
    key: "front",
    title: c.title,
    description: c.description,
    body: `<main id="main">
  <section class="page-head with-arch">
    ${ARCH}
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
      <div class="actions">
        <a class="button" href="${href("/install/")}">${c.primary}${I.onward}</a>
        <a class="button quiet" href="${RELEASES}">${c.secondary}</a>
      </div>
    </div>
  </section>
  <section class="band tight" aria-label="${c.factsLabel}">
    <div class="frame">
      <ul class="facts">
        ${c.facts.map(([a, b]) => `<li><strong>${a}</strong><span>${b}</span></li>`).join("\n        ")}
      </ul>
    </div>
  </section>
  <section class="band" style="padding-block-start: 0" aria-label="${c.waysLabel}">
    <div class="frame">
      <ul class="ways">
        ${c.ways
          .map(
            ([h, p, l, u]) => `<li>
          <h2>${h}</h2>
          <p>${p}</p>
          <a class="onward" href="${href(u)}">${l}${/^https?:/.test(u) ? I.out : I.onward}</a>
        </li>`
          )
          .join("\n        ")}
      </ul>
    </div>
  </section>
</main>`,
  });
}

// ------------------------------------------------------------------ the legal page (text page)

const LEGAL = {
  en: {
    title: "Privacy · Maktaba",
    description: "What maktaba.tv, the Maktaba apps and Maktaba Cloud collect, why, and for how long.",
    h1: "Privacy",
    meta: "Last changed on 11 November 2026",
    inShort: "In short",
    short: [
      "This website sets no cookies, runs no analytics and loads nothing from other companies.",
      "Maktaba’s apps talk to your home, the computer that runs Maktaba, and to no one else.",
      "A problem report reaches us only when you and the person who runs your home both agree to send it. We keep it for 90 days.",
    ],
    toc: "On this page",
    sections: [
      ["website", "This website", [
        "maktaba.tv sets no cookies and runs no analytics script. Its fonts and pictures come from maktaba.tv itself, so your browser asks no other company for anything.",
        "Cloudflare, which serves the site, counts requests for the whole site, as it does for any site it serves. Its own privacy policy covers that count.",
        "The install guides keep everything you type in the page. None of it is sent anywhere.",
      ]],
      ["apps", "The apps", [
        "The apps send what you watch, your settings and your sign-in to your home only. They don’t send them to Maktaba or to anyone else.",
      ]],
      ["reports", "Problem reports", [
        "When an app stops working, it can offer to send a report to Maktaba. It sends one only when you agree and the person who runs your home has turned reports on. We keep each report for 90 days, then delete it. You can ask us to delete it sooner.",
      ]],
      ["cloud", "Maktaba Cloud", [
        "Maktaba Cloud is optional. When your home uses it, this section says what the cloud keeps, why, and for how long. Write to <a href=\"mailto:privacy@maktaba.tv\">privacy@maktaba.tv</a> with any question.",
      ]],
      ["children", "Children", [
        "Maktaba runs on a household’s own computer, and this site collects nothing from anyone, whatever their age.",
      ]],
      ["changes", "Changes", [
        "When this page changes, its date changes too. Every earlier version is in the page’s <a href=\"https://github.com/Hamza-Labs-Core/Maktaba/commits/main\">history on GitHub</a>.",
      ]],
    ],
  },
  ar: {
    title: "الخصوصية · مكتبة",
    description: "ما يجمعه موقع maktaba.tv وتطبيقات مكتبة وسحابة مكتبة، ولماذا، وإلى متى.",
    h1: "الخصوصية",
    meta: "آخر تعديل في 11 نوفمبر 2026",
    inShort: "باختصار",
    short: [
      "لا يضع هذا الموقع ملفات تعريف الارتباط، ولا يشغّل أدوات إحصاء، ولا يحمّل شيئًا من شركات أخرى.",
      "تتصل تطبيقات مكتبة بالجهاز الرئيسي في بيتكم، أي الحاسوب الذي يشغّل البرنامج، ولا تتصل بأحد غيره.",
      "لا يصلنا تقرير عن مشكلة إلا إذا وافقتم أنتم ومن يدير الجهاز الرئيسي على إرساله، ونحتفظ به 90 يومًا.",
    ],
    toc: "في هذه الصفحة",
    sections: [
      ["website", "هذا الموقع", [
        "لا يضع موقع maktaba.tv ملفات تعريف الارتباط، ولا يشغّل أي أداة إحصاء. وخطوطه وصوره كلها منه، فلا يطلب متصفحكم شيئًا من أي شركة أخرى.",
        "تحصي شركة Cloudflare، التي تقدّم الموقع، عدد الطلبات على الموقع كله، كما تفعل مع كل موقع تقدّمه. وتخضع هذه الإحصاءات لسياسة الخصوصية الخاصة بها.",
        "تحتفظ أدلة التثبيت بكل ما تكتبونه داخل الصفحة نفسها، ولا يُرسَل منه شيء إلى أي مكان.",
      ]],
      ["apps", "التطبيقات", [
        "ترسل التطبيقات ما تشاهدونه وإعداداتكم وبيانات دخولكم إلى الجهاز الرئيسي فقط، ولا ترسلها إلى مكتبة ولا إلى أي جهة أخرى.",
      ]],
      ["reports", "تقارير المشكلات", [
        "حين يتوقف تطبيق عن العمل، قد يعرض إرسال تقرير إلينا. ولا يرسله إلا بموافقتكم، وبعد أن يفعّل من يدير الجهاز الرئيسي التقارير. نحتفظ بكل تقرير 90 يومًا ثم نحذفه، ولكم أن تطلبوا حذفه قبل ذلك.",
      ]],
      ["cloud", "سحابة مكتبة", [
        "سحابة مكتبة اختيارية. وحين يستخدمها بيتكم، يبيّن هذا القسم ما تحتفظ به السحابة، ولماذا، وإلى متى. ولأي سؤال، راسلونا على <a href=\"mailto:privacy@maktaba.tv\" dir=\"ltr\">privacy@maktaba.tv</a>.",
      ]],
      ["children", "الأطفال", [
        "يعمل برنامج مكتبة على حاسوب البيت نفسه، ولا يجمع هذا الموقع شيئًا من أحد، أيًّا كان عمره.",
      ]],
      ["changes", "التعديلات", [
        "حين تتغيّر هذه الصفحة يتغيّر تاريخها أيضًا. وكل نسخة سابقة منها موجودة في <a href=\"https://github.com/Hamza-Labs-Core/Maktaba/commits/main\">سجلّها على GitHub</a>.",
      ]],
    ],
  },
};

function legal(lang) {
  const c = LEGAL[lang];
  return page({
    lang,
    key: "legal",
    title: c.title,
    description: c.description,
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="meta"><time datetime="2026-11-11">${c.meta}</time></p>
    </div>
  </section>
  <div class="band">
    <div class="frame text-layout with-toc">
      <nav class="toc" aria-labelledby="toc-title">
        <h2 id="toc-title">${c.toc}</h2>
        <ol>
          ${c.sections.map(([id, h], i) => `<li><a href="#${id}"${i === 0 ? ' aria-current="true"' : ""}>${h}</a></li>`).join("\n          ")}
        </ol>
      </nav>
      <article class="prose">
        <section class="summary" aria-labelledby="short-title">
          <h2 id="short-title">${c.inShort}</h2>
          <ul>${c.short.map((s) => `<li>${s}</li>`).join("")}</ul>
        </section>
        ${c.sections
          .map(([id, h, ps]) => `<h2 id="${id}">${h}</h2>\n        ${ps.map((p) => `<p>${p}</p>`).join("\n        ")}`)
          .join("\n        ")}
      </article>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ the problem page

const PROBLEM = {
  en: {
    title: "Your home can’t be reached · Maktaba",
    description: "What it means when Maktaba can’t reach your home, and what you can do.",
    crumb: "Messages",
    h1: "Your home can’t be reached",
    who: "You may see this while using Maktaba.",
    means: "What this means",
    meansText: "The computer that runs Maktaba didn’t answer. It may be turned off, restarting after an update, or not connected to the network. Your library is safe: nothing is lost while it’s away.",
    do: "What you can do",
    doSteps: [
      "Check that the computer is on and connected to the network.",
      "If it’s updating, wait a minute. Maktaba tries again by itself every few seconds.",
      "Open the app again on the screen you’re using.",
    ],
    help: "Who can help",
    helpText: "If it still doesn’t answer, ask the person who runs your home. If that’s you, <a href=\"/install/troubleshooting/\">Troubleshooting</a> goes through it step by step.",
    related: "Related pages",
    relatedLinks: [["Troubleshooting: when the home doesn’t answer", "/install/troubleshooting/"], ["After setup: updates and quiet hours", "/install/after-setup/"]],
    details: "Details for developers",
    status: "Status",
    type: "Type",
    code: "Code",
  },
  ar: {
    title: "تعذّر الوصول إلى الجهاز الرئيسي · مكتبة",
    description: "ما معنى ألّا يصل برنامج مكتبة إلى الجهاز الرئيسي، وما الذي يمكنكم فعله.",
    crumb: "الرسائل",
    h1: "تعذّر الوصول إلى الجهاز الرئيسي",
    who: "قد تظهر لكم هذه الرسالة أثناء استخدام مكتبة.",
    means: "ما معنى هذا",
    meansText: "لم يردّ الحاسوب الذي يشغّل برنامج مكتبة. ربما هو مطفأ، أو يعيد التشغيل بعد تحديث، أو غير متصل بالشبكة. ومكتبتكم في أمان، فلا يضيع منها شيء ما دام غائبًا.",
    do: "ما الذي يمكنكم فعله",
    doSteps: [
      "تأكدوا من أن الحاسوب يعمل ومتصل بالشبكة.",
      "إن كان يُحدَّث فانتظروا دقيقة، فالبرنامج يعيد المحاولة وحده كل بضع ثوانٍ.",
      "افتحوا التطبيق من جديد على الشاشة التي تستخدمونها.",
    ],
    help: "من يمكنه المساعدة",
    helpText: "وإن بقي لا يردّ، فاسألوا من يدير الجهاز الرئيسي في بيتكم. وإن كنتم أنتم من يديره، فصفحة <a href=\"/ar/install/troubleshooting/\">حل المشكلات</a> تشرح ما تفعلونه خطوةً خطوة.",
    related: "صفحات ذات صلة",
    relatedLinks: [["حل المشكلات: حين لا يردّ الجهاز الرئيسي", "/ar/install/troubleshooting/"], ["بعد الإعداد: التحديثات والساعات الهادئة", "/ar/install/after-setup/"]],
    details: "تفاصيل للمطوّرين",
    status: "الحالة",
    type: "النوع",
    code: "الرمز",
  },
};

function problem(lang) {
  const c = PROBLEM[lang];
  return page({
    lang,
    key: "problem",
    current: "messages",
    title: c.title,
    description: c.description,
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <p class="crumbs"><a href="${local(lang, "/problems/")}">${I.back}${c.crumb}</a></p>
      <h1>${c.h1}</h1>
      <p class="who">${I.person}${c.who}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame problem-layout">
      <article class="prose">
        <h2>${c.means}</h2>
        <p>${c.meansText}</p>
        <h2>${c.do}</h2>
        <ol>${c.doSteps.map((s) => `<li>${s}</li>`).join("")}</ol>
        <h2>${c.help}</h2>
        <p>${c.helpText}</p>
      </article>
      <div>
        <aside class="aside-box" aria-labelledby="related-title">
          <h2 id="related-title">${c.related}</h2>
          <ul>${c.relatedLinks.map(([t, h]) => `<li><a href="${h}">${t}</a></li>`).join("")}</ul>
        </aside>
        <aside class="aside-box details" aria-labelledby="details-title">
          <h2 id="details-title">${c.details}</h2>
          <dl>
            <dt>${c.status}</dt><dd><code>503</code></dd>
            <dt>${c.type}</dt><dd><code>https://maktaba.tv/problems/home-unreachable</code></dd>
            <dt>${c.code}</dt><dd><code>home_unreachable</code></dd>
          </dl>
        </aside>
      </div>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ the problem index

const AREAS = [
  [{ en: "Signing in", ar: "تسجيل الدخول" }, [
    ["invalid-credentials", "The name or password is wrong", "الاسم أو كلمة المرور غير صحيحة"],
    ["account-locked", "This account is locked for a while", "هذا الحساب مقفل مؤقتًا"],
    ["token-expired", "Your sign-in has ended", "انتهت جلسة تسجيل الدخول"],
    ["password-change-required", "Choose a new password to carry on", "اختاروا كلمة مرور جديدة للمتابعة"],
    ["signin-required", "Sign in to see this", "سجّلوا الدخول لرؤية هذا"],
  ]],
  [{ en: "Your home", ar: "الجهاز الرئيسي" }, [
    ["home-unreachable", "Your home can’t be reached", "تعذّر الوصول إلى الجهاز الرئيسي"],
    ["home-busy", "Your home is busy right now", "الجهاز الرئيسي مشغول الآن"],
    ["home-timeout", "Your home took too long to answer", "تأخّر الجهاز الرئيسي في الرد"],
    ["component-off", "This part of Maktaba is turned off", "هذا الجزء من مكتبة متوقف"],
  ]],
  [{ en: "Libraries", ar: "المكتبات" }, [
    ["library-roots-overlap", "Two library folders overlap", "مجلدان من مجلدات المكتبة متداخلان"],
    ["library-roots-unreachable", "A library folder can’t be reached", "تعذّر الوصول إلى أحد مجلدات المكتبة"],
    ["library-no-access", "Maktaba can’t read this folder", "لا يستطيع البرنامج قراءة هذا المجلد"],
    ["library-name-exists", "A library already has this name", "توجد مكتبة أخرى بهذا الاسم"],
    ["library-read-only", "This library is read-only", "هذه المكتبة للقراءة فقط"],
  ]],
  [{ en: "Watching", ar: "المشاهدة" }, [
    ["video-unavailable-offline", "This video needs your home, and it isn’t answering", "يحتاج هذا الفيديو إلى الجهاز الرئيسي، وهو لا يردّ"],
    ["transcode-busy", "Too many videos are being prepared at once", "يُجهَّز الآن عدد كبير من الفيديوهات معًا"],
    ["streaming-unavailable", "Playback isn’t available right now", "التشغيل غير متاح الآن"],
  ]],
  [{ en: "Live TV", ar: "البث المباشر" }, [
    ["tuner-off", "The TV tuner is turned off", "موالف التلفاز متوقف"],
    ["channel-number-exists", "Another channel has this number", "توجد قناة أخرى بهذا الرقم"],
    ["channel-not-planned", "This channel has no schedule yet", "لا جدول لهذه القناة بعد"],
  ]],
  [{ en: "People and seats", ar: "الأشخاص والمقاعد" }, [
    ["seat-limit", "This home has no free seats", "لا مقاعد شاغرة في هذا البيت"],
    ["last-admin", "A home needs at least one admin", "يحتاج البيت إلى مدير واحد على الأقل"],
    ["username-exists", "Someone already has this name", "هذا الاسم مستخدَم من قبل"],
    ["email-exists", "This email is already in use", "هذا البريد الإلكتروني مستخدَم من قبل"],
  ]],
];

const INDEX = {
  en: {
    title: "Messages and what they mean · Maktaba",
    description: "Every message Maktaba can show, what it means and what you can do.",
    h1: "Messages and what they mean",
    lead: "Every message Maktaba can show, grouped by where you meet it. Each page says what happened and what you can do.",
    label: "Find a message",
    placeholder: "A word from the message, or its name",
    many: "{n} messages",
    one: "1 message",
    none: "No message matches. Try a shorter word.",
  },
  ar: {
    title: "الرسائل ومعانيها · مكتبة",
    description: "كل رسالة قد يعرضها برنامج مكتبة، ومعناها، وما يمكنكم فعله.",
    h1: "الرسائل ومعانيها",
    lead: "كل رسالة قد يعرضها برنامج مكتبة، مجمّعةً بحسب المكان الذي تظهر فيه. وفي صفحة كل رسالة شرح لما حدث، ولما يمكنكم فعله.",
    label: "البحث عن رسالة",
    placeholder: "كلمة من الرسالة، أو اسمها",
    many: "{n} رسالة",
    one: "رسالة واحدة",
    none: "لا رسالة تطابق ما كتبتم. جرّبوا كلمة أقصر.",
  },
};

function problems(lang) {
  const c = INDEX[lang];
  const total = AREAS.reduce((n, [, items]) => n + items.length, 0);
  return page({
    lang,
    key: "problems",
    current: "messages",
    title: c.title,
    description: c.description,
    body: `<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
      <div class="filter" role="search">
        <label for="filter">${c.label}</label>
        <div class="field">${I.search}<input id="filter" type="search" autocomplete="off" placeholder="${c.placeholder}" aria-describedby="count" /></div>
        <p class="count" id="count" aria-live="polite" data-many="${c.many}" data-one="${c.one}">${c.many.replace("{n}", total)}</p>
      </div>
    </div>
  </section>
  <div class="band">
    <div class="frame">
      <p class="no-match" id="no-match" hidden>${c.none}</p>
      <div class="areas">
        ${AREAS.map(
          ([area, items], i) => `<section aria-labelledby="area-${i}">
          <h2 id="area-${i}">${area[lang]}</h2>
          <ul>${items
            .map(([slug, en, ar]) => `<li><a href="${local(lang, `/problems/${slug}/`)}"><span class="t">${lang === "en" ? en : ar}</span><span class="slug">${slug}</span></a></li>`)
            .join("")}</ul>
        </section>`
        ).join("\n        ")}
      </div>
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ an install guide in the shell

function install(lang) {
  const guide = readFileSync(join(here, "src", `unraid.${lang}.html`), "utf8");
  return page({
    lang,
    key: "install",
    current: "install",
    title: lang === "en" ? "Install Maktaba on Unraid · Maktaba" : "تثبيت Maktaba على Unraid · مكتبة",
    description:
      lang === "en"
        ? "Install Maktaba on Unraid from Community Applications: the template, the shares and the setup."
        : "تثبيت مكتبة على Unraid من متجر التطبيقات المجتمعية: القالب والمشاركات والإعداد.",
    bodyClass: "in-shell",
    extraCss: ["vendor/install.css"],
    body: `<main id="main" class="frame guide-frame">
${guide}</main>`,
  });
}

// ------------------------------------------------------------------ the 404

const LOST = {
  en: {
    title: "This page isn’t here · Maktaba",
    description: "This page isn’t on maktaba.tv.",
    h1: "This page isn’t here",
    lead: "The address may have a typo, or the page has moved. One of these may be what you were looking for.",
    places: [
      ["Maktaba’s front door", "What Maktaba is, and how to start", "/"],
      ["Install guides", "A NAS, Docker, or a guide for your system", "/install/"],
      ["Messages and what they mean", "What a message means and what you can do", "/problems/"],
      ["Help", "Answers about using Maktaba at home", "/help/"],
    ],
    asked: "The address you asked for:",
  },
  ar: {
    title: "هذه الصفحة غير موجودة · مكتبة",
    description: "هذه الصفحة غير موجودة على maktaba.tv.",
    h1: "هذه الصفحة غير موجودة",
    lead: "ربما في العنوان خطأ في الكتابة، أو نُقلت الصفحة إلى مكان آخر. وقد تجدون ما تبحثون عنه في إحدى هذه الصفحات.",
    places: [
      ["الصفحة الرئيسية", "ما برنامج مكتبة، وكيف تبدؤون", "/"],
      ["أدلة التثبيت", "خادم تخزين، أو Docker، أو دليل لنظامكم", "/install/"],
      ["الرسائل ومعانيها", "ما تعنيه الرسالة، وما يمكنكم فعله", "/problems/"],
      ["المساعدة", "أجوبة عن استخدام مكتبة في البيت", "/help/"],
    ],
    asked: "العنوان الذي طلبتموه:",
  },
};

function lost(lang) {
  const c = LOST[lang];
  return page({
    lang,
    key: "lost",
    title: c.title,
    description: c.description,
    body: `<main id="main" class="lost">
  <section class="page-head with-arch">
    ${ARCH}
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
      <ul class="places">
        ${c.places.map(([t, d, h]) => `<li><a href="${local(lang, h)}"><strong>${t}</strong><span>${d}</span></a></li>`).join("\n        ")}
      </ul>
      <p class="asked">${c.asked} <code>/instal/synology/</code></p>
    </div>
  </section>
</main>`,
  });
}

// ------------------------------------------------------------------ the component sheet

const COMPOSE = `services:
  maktaba:
    image: ghcr.io/hamza-labs-core/maktaba-server:latest
    container_name: maktaba
    restart: unless-stopped
    user: "1000:1000"
    stop_grace_period: 90s
    environment:
      MAKTABA_PLATFORM: docker
      TZ: Asia/Riyadh
    network_mode: host
    volumes:
      - maktaba:/var/lib/maktaba
      - /srv/media:/srv/media
volumes:
  maktaba:`;

const SHEET = {
  en: {
    title: "The shell’s components · Maktaba design",
    description: "Part A’s components, as they look and behave.",
    h1: "The shell’s components",
    lead: "Each component as a page uses it, with its states. On a phone this page opens with the menu showing.",
    hint: { lang: "ar", dir: "rtl", text: "هذه الصفحة متوفرة بالعربية أيضًا.", link: "قراءتها بالعربية", close: "إخفاء" },
    hintTitle: "The language hint",
    hintNote: "Shown on an English page when the browser prefers Arabic (and the other way round). Dismissed for good with one press.",
    buttons: "Buttons and links",
    b1: "Choose your install guide",
    b2: "Download the apps",
    bLong: "انتقلوا إلى صفحة التنزيلات على GitHub",
    bLongNote: "A long Arabic label at 360 px wraps rather than truncates:",
    focus: "Focus",
    focusNote: "A 2 px brass ring, 3 px off the edge, on every link, button and field. Shown here as the keyboard sees it.",
    callouts: "Callouts",
    note: ["Note:", "When the array is stopped, libraries show No access, and nothing in them is treated as deleted."],
    warning: ["Warning:", "Keep Maktaba’s data folder on a pool or a single disk, never on the array’s <code>/mnt/user</code> share. Its database would be slow and could be damaged there."],
    tip: ["Tip:", "Set Container Path to the same path as Host Path. Machines that join this home later then find the same share."],
    code: "Code and compose blocks",
    terminal: "In the container’s Console",
    copyCommand: "Copy the command",
    copied: "After a press: the label reads Copied for two seconds, and a screen reader hears it.",
    compose: "The compose file",
    copyCompose: "Copy the compose file",
    steps: "The step list",
    s: [
      ["Find Maktaba in Apps", "Apps → search <strong>Maktaba</strong> → <strong>Install</strong>."],
      ["Fill in the template", "Keep <code>/mnt/cache/appdata/maktaba</code> for the data, and add your media share."],
      ["Run the setup", "Docker → Maktaba’s icon → <strong>Console</strong>, and run:"],
    ],
    bidi: "Mixed scripts in one line",
    bidiText: "On an Arabic page, a Latin run is isolated, so its punctuation stays with it:",
  },
  ar: {
    title: "مكوّنات إطار الموقع · تصميم مكتبة",
    description: "مكوّنات الجزء الأول كما تبدو وكما تعمل.",
    h1: "مكوّنات إطار الموقع",
    lead: "كل مكوّن كما تستخدمه الصفحات، بحالاته المختلفة. وعلى الهاتف تُفتح هذه الصفحة والقائمة ظاهرة.",
    hint: { lang: "en", dir: "ltr", text: "This page is also in English.", link: "Read it in English", close: "Hide" },
    hintTitle: "تلميح اللغة",
    hintNote: "يظهر على الصفحة العربية حين يفضّل المتصفح الإنجليزية، والعكس. ويختفي نهائيًا بضغطة واحدة.",
    buttons: "الأزرار والروابط",
    b1: "اختيار دليل التثبيت",
    b2: "تنزيل التطبيقات",
    bLong: "الانتقال إلى صفحة التنزيلات على GitHub",
    bLongNote: "التسمية العربية الطويلة تلتف على سطرين عند عرض 360 بكسل، ولا تُقتطع:",
    focus: "التركيز",
    focusNote: "حلقة نحاسية بسمك 2 بكسل، تبعد 3 بكسل عن الحافة، على كل رابط وزر وحقل. وتظهر هنا كما يراها من يستخدم لوحة المفاتيح.",
    callouts: "التنبيهات",
    note: ["ملاحظة:", "حين تتوقف المصفوفة، تظهر المكتبات بحالة «لا وصول»، ولا يُعدّ شيء منها محذوفًا."],
    warning: ["تحذير:", "اجعلوا مجلد بيانات مكتبة على مجمّع تخزين أو قرص واحد، لا على مشاركة المصفوفة <code>/mnt/user</code> أبدًا، فقاعدة البيانات تكون عليها بطيئة وعرضة للتلف."],
    tip: ["نصيحة:", "اجعلوا «Container Path» مطابقًا لـ«Host Path»، فتجد الأجهزة التي تنضم إلى البيت لاحقًا المشاركة نفسها."],
    code: "كتل الشيفرة وملف compose",
    terminal: "في وحدة التحكم الخاصة بالحاوية",
    copyCommand: "نسخ الأمر",
    copied: "بعد الضغط: تصبح التسمية «تم النسخ» لثانيتين، ويسمعها قارئ الشاشة.",
    compose: "ملف compose",
    copyCompose: "نسخ ملف compose",
    steps: "قائمة الخطوات",
    s: [
      ["ابحثوا عن مكتبة في التطبيقات", "من «Apps» ابحثوا عن <strong>Maktaba</strong> ← <strong>«Install»</strong>."],
      ["املؤوا القالب", "أبقوا <code>/mnt/cache/appdata/maktaba</code> للبيانات، وأضيفوا مشاركة الوسائط."],
      ["شغّلوا الإعداد", "من «Docker» ← أيقونة مكتبة ← <strong>«Console»</strong>، ثم شغّلوا:"],
    ],
    bidi: "نصّان مختلفان في سطر واحد",
    bidiText: "على الصفحة العربية يُعزل كل مقطع لاتيني، فتبقى علامات ترقيمه معه:",
  },
};

function components(lang) {
  const c = SHEET[lang];
  const setup = "/opt/maktaba/server/bin/maktaba-server container setup";
  const bidiSample =
    lang === "ar"
      ? `<p>افتحوا <bdi lang="en">Admin → Updates</bdi>، ثم اختاروا ساعات التحديث. أو شغّلوا <code>docker compose up -d</code> في الطرفية.</p>`
      : `<p>Open <bdi lang="ar">الإعدادات ← التحديثات</bdi> on an Arabic screen, or run <code>docker compose up -d</code> in the terminal.</p>`;
  return page({
    lang,
    key: "components",
    title: c.title,
    description: c.description,
    menuOpen: true,
    body: `<div class="lang-hint" lang="${c.hint.lang}" dir="${c.hint.dir}">
  <div class="frame"><p>${c.hint.text} <a href="#" hreflang="${c.hint.lang}">${c.hint.link}</a></p><button class="button quiet sheet-small" type="button">${c.hint.close}</button></div>
</div>
<main id="main">
  <section class="page-head compact">
    <div class="frame">
      <h1>${c.h1}</h1>
      <p class="lead">${c.lead}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame sheet prose">
      <h2>${c.hintTitle}</h2>
      <p>${c.hintNote}</p>

      <h2>${c.buttons}</h2>
      <div class="row"><a class="button" href="#">${c.b1}${I.onward}</a><a class="button quiet" href="#">${c.b2}</a></div>
      <p>${c.bLongNote}</p>
      <div class="row narrow"><a class="button" href="#">${c.bLong}${I.out}</a></div>

      <h2>${c.focus}</h2>
      <p>${c.focusNote}</p>
      <div class="row"><a class="button demo-focus" href="#">${c.b1}</a><a class="demo-focus" href="#">${c.b2}</a></div>

      <h2>${c.callouts}</h2>
      ${callout("note", ...c.note)}
      ${callout("warning", ...c.warning)}
      ${callout("tip", ...c.tip)}

      <h2>${c.code}</h2>
      ${codeBlock(lang, { name: c.terminal, code: setup, copyName: c.copyCommand })}
      <p>${c.copied}</p>
      ${codeBlock(lang, { name: c.terminal, code: setup, copyName: c.copyCommand, copied: true })}
      ${codeBlock(lang, { name: `${c.compose}: <code>docker-compose.yml</code>`, code: COMPOSE, copyName: c.copyCompose })}

      <h2>${c.steps}</h2>
      <ol class="steps">
        ${c.s
          .map(
            ([h, p], i) => `<li><h3>${h}</h3><p>${p}</p>${i === 2 ? codeBlock(lang, { name: c.terminal, code: setup, copyName: c.copyCommand }) : ""}</li>`
          )
          .join("\n        ")}
      </ol>

      <h2>${c.bidi}</h2>
      <p>${c.bidiText}</p>
      ${bidiSample}
    </div>
  </div>
</main>`,
  });
}

// ------------------------------------------------------------------ write

export const MOCKUPS = {
  "front-door": front,
  legal,
  problem,
  problems,
  "install-guide": install,
  "404": lost,
  components,
};

export const LANGS = ["en", "ar"];

function main(argv) {
  const check = argv.includes("--check");
  const stale = [];
  for (const [name, make] of Object.entries(MOCKUPS)) {
    for (const lang of LANGS) {
      const file = join(here, `${name}.${lang}.html`);
      const html = make(lang);
      if (check) {
        let current = null;
        try {
          current = readFileSync(file, "utf8");
        } catch {
          // missing: stale
        }
        if (current !== html) stale.push(`${name}.${lang}.html`);
      } else {
        writeFileSync(file, html);
      }
    }
  }
  if (stale.length) {
    console.error(`make.mjs: stale mockups (run node make.mjs):\n  ${stale.join("\n  ")}`);
    process.exit(1);
  }
  console.log(`make.mjs: ${check ? "checked" : "wrote"} ${Object.keys(MOCKUPS).length * LANGS.length} mockups`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main(process.argv.slice(2));
