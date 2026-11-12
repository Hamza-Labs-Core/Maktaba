// maktaba.tv's problem pages (Epic 60 story 60.06): /problems/<slug>/ and /ar/problems/<slug>/
// for every entry of the registry (shared/api/problems.json, the file the API sends from), and
// the index, /problems/ and /ar/problems/, grouped by area, with a filter that runs in the page.
//
// As the legal pages (legal.mjs) keep their copy apart from one function that draws it, the
// registry becomes a structured document per page (problemDoc, indexDoc), and one function
// renders each (renderProblem, renderIndex). The markup follows 60.01 part A's problem and
// problem-index mockups (the same sections and headings, the "Details for developers" block),
// so the design swaps the two functions and the stylesheet, not the data.
//
// The build fails, naming the entry and the field, when an entry lacks any text in either
// language, names an area or audience this file has no words for, or has a malformed slug or
// status (registryFindings, T10). The Arabic pages pass the Latin-share check on what they
// render (build.mjs step 6, T09): slugs, codes and the identifiers a text names are code.
//
// A help topic (`help`, Epic 41's, published by story 60.12) or install troubleshooting anchor
// (`install`) is linked only when the page it names is in the built site: the build passes
// `resolves` (build.mjs), and the link check runs over what was rendered.

import fs from "node:fs";
import path from "node:path";
import { esc, GITHUB, HELP, inLang, page } from "./shell.mjs";

export const REGISTRY = "shared/api/problems.json";
export const BASE = "https://maktaba.tv/problems/";

export const AUDIENCES = ["anyone", "person", "admin", "app", "node"];
export const LANGS = ["en", "ar"];
export const TEXTS = ["title", "means", "do"];
export const SLUG = /^[a-z0-9_-]+$/;

/** The index's areas, in the order a household meets them, each with its heading. An entry
 * whose area is not here fails the build. */
export const AREAS = [
  ["accounts", "Signing in and accounts", "تسجيل الدخول والحسابات"],
  ["playback", "Watching", "المشاهدة"],
  ["browse", "Collections and play queues", "المجموعات وقوائم التشغيل"],
  ["search", "Search", "البحث"],
  ["libraries", "Libraries", "المكتبات"],
  ["review", "Reviewing subtitles", "مراجعة الترجمات"],
  ["live-tv", "Live TV and remote viewing", "البث المباشر والمشاهدة عن بُعد"],
  ["jobs", "Work in the background", "الأعمال في الخلفية"],
  ["machines", "Your home's machines", "أجهزة البيت"],
  ["components", "Features and components", "الميزات والمكوّنات"],
  ["settings", "Settings", "الإعدادات"],
  ["rules", "Saved settings", "الإعدادات المحفوظة"],
  ["logs", "Logs and crash reports", "السجلات وتقارير الأعطال"],
  ["cloud", "Maktaba Cloud", "سحابة مكتبة"],
  ["licensing", "Plans and licences", "الخطط والتراخيص"],
  ["store", "The plugin store", "متجر الإضافات"],
  ["plugins", "Plugins", "الإضافات"],
  ["apps", "Reports from apps", "تقارير التطبيقات"],
  ["general", "General", "رسائل عامة"],
].map(([id, en, ar]) => ({ id, en, ar }));

/** The pages' words (story 60.06's strings table). {link} marks where a link goes. */
export const T = {
  en: {
    crumbs: "Messages",
    means: "What this means",
    do: "What you can do",
    whoHelps: "Who can help",
    related: "Related pages",
    helpTopic: "Help with this",
    installTopic: "Install troubleshooting",
    details: "Details for developers",
    status: "Status",
    type: "Type",
    code: "Code",
    codes: "Codes",
    who: {
      anyone: "Anyone may see this, signed in or not.",
      person: "You may see this while using Maktaba.",
      admin: "You'll see this if you run the home.",
      app: "An app that works with Maktaba gets this, and may show it to you.",
      node: "Your home's machines send this to each other.",
    },
    helps: {
      person:
        "If it keeps happening, ask the person who runs your home. If that's you, {troubleshooting} goes through the common causes.",
      admin:
        "If it keeps happening, {troubleshooting} goes through the common causes. If it doesn't explain it, tell us on {github}.",
      app: "If an app shows you this, ask the person who runs your home, or the app's maker.",
    },
    troubleshooting: "Troubleshooting",
    github: "GitHub",
    indexTitle: "Messages and what they mean",
    indexDescription:
      "What each message Maktaba can show means, and what you can do about it, grouped by where you meet it.",
    indexLead:
      "Every message Maktaba can show, grouped by where you meet it. Each page says what happened and what you can do.",
    filter: "Find a message",
    placeholder: "A word from the message, or its name",
    count: { one: "1 message", other: "{n} messages" },
    noMatch: "No message matches. Try a shorter word.",
  },
  ar: {
    crumbs: "الرسائل",
    means: "ما معنى هذا",
    do: "ما الذي يمكنكم فعله",
    whoHelps: "من يمكنه المساعدة",
    related: "صفحات ذات صلة",
    helpTopic: "المساعدة في هذا",
    installTopic: "حل مشكلات التثبيت",
    details: "تفاصيل للمطوّرين",
    status: "الحالة",
    type: "النوع",
    code: "الرمز",
    codes: "الرموز",
    who: {
      anyone: "قد تظهر هذه الرسالة لأي شخص، قبل تسجيل الدخول أو بعده.",
      person: "قد تظهر لكم هذه الرسالة أثناء استخدام برنامج مكتبة.",
      admin: "تظهر لكم هذه الرسالة إن كنتم من يدير الجهاز الرئيسي.",
      app: "تصل هذه الرسالة إلى التطبيقات التي تعمل مع برنامج مكتبة، وقد تعرضها عليكم.",
      node: "تتبادل أجهزة البيت هذه الرسالة فيما بينها.",
    },
    helps: {
      person:
        "وإن تكرّر الأمر، فاسألوا من يدير الجهاز الرئيسي في بيتكم. وإن كنتم أنتم من يديره، فصفحة {troubleshooting} تشرح أسبابه الشائعة.",
      admin:
        "وإن تكرّر الأمر، فصفحة {troubleshooting} تشرح أسبابه الشائعة. وإن لم تجدوا فيها الجواب، فأخبرونا على {github}.",
      app: "وإن عرضها عليكم تطبيق، فاسألوا من يدير الجهاز الرئيسي في بيتكم، أو الجهة التي صنعت التطبيق.",
    },
    troubleshooting: "حل المشكلات",
    github: "GitHub",
    indexTitle: "الرسائل ومعانيها",
    indexDescription:
      "معنى كل رسالة قد يعرضها برنامج مكتبة، وما يمكنكم فعله حيالها، مجمّعةً بحسب المكان الذي تظهر فيه.",
    indexLead:
      "كل رسالة قد يعرضها برنامج مكتبة، مجمّعةً بحسب المكان الذي تظهر فيه. وفي صفحة كل رسالة شرح لما حدث، ولما يمكنكم فعله.",
    filter: "البحث عن رسالة",
    placeholder: "كلمة من الرسالة، أو اسمها",
    // Arabic's plural forms (Intl.PluralRules): the filter's script picks one by the count
    count: {
      zero: "لا رسالة",
      one: "رسالة واحدة",
      two: "رسالتان",
      few: "{n} رسائل",
      many: "{n} رسالة",
      other: "{n} رسالة",
    },
    noMatch: "لا رسالة تطابق ما كتبتم. جرّبوا كلمة أقصر.",
  },
};

/** Who can help, by audience: the person and anyone ask the home's admin; the admin and the
 * machines have the troubleshooting page and the project; an app's message goes to its maker. */
const HELPS_FOR = { anyone: "person", person: "person", admin: "admin", node: "admin", app: "app" };

// ---- the registry ---------------------------------------------------------------------------

export function readRegistry(repo) {
  return JSON.parse(fs.readFileSync(path.join(repo, REGISTRY), "utf8"));
}

const LANG_NAME = { en: "English", ar: "Arabic" };

/** What is wrong with the registry, as the build reports it: one line per entry and field
 * ("problems.json: seat-limit: no Arabic do (do.ar)"). Empty when every entry can be drawn. */
export function registryFindings(reg) {
  const out = [];
  const entries = Array.isArray(reg?.problems) ? reg.problems : null;
  if (!entries) return ["problems.json: no problems list"];
  if (reg.base !== BASE) out.push(`problems.json: base is ${reg.base}, not ${BASE}`);
  const areas = new Set(AREAS.map((a) => a.id));
  const seen = new Set();
  entries.forEach((e, i) => {
    const name = typeof e?.slug === "string" && e.slug ? e.slug : `entry ${i}`;
    const bad = (why) => out.push(`problems.json: ${name}: ${why}`);
    if (!SLUG.test(e?.slug ?? "")) bad(`the slug is not [a-z0-9_-]+`);
    else if (seen.has(e.slug)) bad("the slug is listed twice");
    seen.add(e?.slug);
    if (!Number.isInteger(e?.status) || e.status < 400 || e.status > 599)
      bad(`the status ${JSON.stringify(e?.status)} is not an HTTP error status`);
    if (!areas.has(e?.area)) bad(`no heading for the area ${JSON.stringify(e?.area)} (AREAS)`);
    if (!AUDIENCES.includes(e?.audience))
      bad(`the audience ${JSON.stringify(e?.audience)} is not one of ${AUDIENCES.join(", ")}`);
    if (!Array.isArray(e?.codes) || e.codes.some((c) => typeof c !== "string" || !c))
      bad("codes is not a list of strings");
    for (const f of TEXTS)
      for (const lang of LANGS) {
        const v = e?.[f]?.[lang];
        if (typeof v !== "string" || !v.trim()) bad(`no ${LANG_NAME[lang]} ${f} (${f}.${lang})`);
      }
    for (const f of ["help", "install"])
      if (e?.[f] != null && (typeof e[f] !== "string" || !e[f].trim()))
        bad(`${f} is neither null nor a topic`);
  });
  return out;
}

// ---- the documents ---------------------------------------------------------------------------

/** A registry entry's links, as site paths: the help topic ("topic#anchor" → /help/topic/#anchor)
 * and the install troubleshooting anchor (→ /install/troubleshooting/#anchor), in a language. */
export function topicLinks(e, lang) {
  const out = [];
  if (e.help) {
    const [topic, anchor] = e.help.split("#");
    out.push({ kind: "help", href: inLang(lang, `/help/${topic}/${anchor ? `#${anchor}` : ""}`) });
  }
  if (e.install) {
    const anchor = e.install.replace(/^#/, "");
    out.push({ kind: "install", href: inLang(lang, `/install/troubleshooting/#${anchor}`) });
  }
  return out;
}

/** One problem page's content in a language: what renderProblem draws. `resolves(href)` says
 * whether a site path (with its #fragment) opens in the built site. */
export function problemDoc(e, lang, { resolves = () => false } = {}) {
  const t = T[lang];
  return {
    slug: e.slug,
    lang,
    title: e.title[lang],
    description: e.means[lang],
    who: t.who[e.audience],
    means: e.means[lang],
    do: e.do[lang],
    helps: HELPS_FOR[e.audience],
    related: topicLinks(e, lang)
      .filter((l) => resolves(l.href))
      .map((l) => ({ href: l.href, label: l.kind === "help" ? t.helpTopic : t.installTopic })),
    status: e.status,
    type: `${BASE}${e.slug}`,
    codes: e.codes,
  };
}

/** The index's content in a language: the areas in AREAS' order, each with its entries by
 * title, and the empty areas left out. */
export function indexDoc(entries, lang) {
  const collator = new Intl.Collator(lang);
  const areas = AREAS.map((a) => ({
    id: a.id,
    heading: a[lang],
    entries: entries
      .filter((e) => e.area === a.id)
      .map((e) => ({ slug: e.slug, title: e.title[lang] }))
      .sort((x, y) => collator.compare(x.title, y.title)),
  })).filter((a) => a.entries.length);
  return { lang, title: T[lang].indexTitle, description: T[lang].indexDescription, areas };
}

// ---- the rendering (the functions 60.01's design replaces) ---------------------------------

/** Identifiers in a text (snake_case names such as unknown_node, and the registry's hyphenated
 * slugs and codes): code, set in <code>, so the Arabic pages' Latin share counts them as code
 * (T09). A plain word that is also a slug ("expired", "busy") stays a word. */
export function marksCode(text, known = new Set()) {
  return esc(text).replace(/[A-Za-z0-9][A-Za-z0-9_.-]*[A-Za-z0-9]/g, (w) =>
    w.includes("_") || (w.includes("-") && known.has(w)) ? `<code>${w}</code>` : w
  );
}

/** A sentence with {name} links filled in. */
function withLinks(sentence, links) {
  return esc(sentence).replace(/\{([a-z]+)\}/g, (m, name) => {
    const l = links[name];
    if (!l) throw new Error(`problems.mjs: no link {${name}}`);
    return `<a href="${esc(l.href)}">${esc(l.text)}</a>`;
  });
}

/** The problem layout (60.01 part A): the way back to the index, the title, who sees it, what it
 * means, what to do, who can help, the related pages (when any resolves), and the details for
 * developers in small type. */
export function renderProblem(doc, { known = new Set() } = {}) {
  const t = T[doc.lang];
  const helps = withLinks(t.helps[doc.helps], {
    troubleshooting: { href: inLang(doc.lang, HELP), text: t.troubleshooting },
    github: { href: `${GITHUB}/issues`, text: t.github },
  });
  const related = doc.related.length
    ? `
        <aside class="aside-box" aria-labelledby="related-title">
          <h2 id="related-title">${esc(t.related)}</h2>
          <ul>${doc.related.map((r) => `<li><a href="${esc(r.href)}">${esc(r.label)}</a></li>`).join("")}</ul>
        </aside>`
    : "";
  const codes = doc.codes.length
    ? `
            <dt>${esc(doc.codes.length > 1 ? t.codes : t.code)}</dt><dd>${doc.codes.map((c) => `<code>${esc(c)}</code>`).join(" ")}</dd>`
    : "";
  return `<section class="page-head compact" data-problem="${esc(doc.slug)}">
    <div class="frame">
      <p class="crumbs"><a href="${inLang(doc.lang, "/problems/")}">${esc(t.crumbs)}</a></p>
      <h1>${esc(doc.title)}</h1>
      <p class="who">${esc(doc.who)}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame problem-layout">
      <article class="prose">
        <h2 id="means">${esc(t.means)}</h2>
        <p>${marksCode(doc.means, known)}</p>
        <h2 id="do">${esc(t.do)}</h2>
        <p>${marksCode(doc.do, known)}</p>
        <h2 id="who-helps">${esc(t.whoHelps)}</h2>
        <p>${helps}</p>
      </article>
      <div>${related}
        <aside class="aside-box details" aria-labelledby="details-title">
          <h2 id="details-title">${esc(t.details)}</h2>
          <dl>
            <dt>${esc(t.status)}</dt><dd><code>${doc.status}</code></dd>
            <dt>${esc(t.type)}</dt><dd><code>${esc(doc.type)}</code></dd>${codes}
          </dl>
        </aside>
      </div>
    </div>
  </div>`;
}

/** The problem index layout (60.01 part A): the title, the lead, the filter, and the areas,
 * each entry its title and slug. The filter is in place from the first paint (its script only
 * wires it, so nothing moves: no layout shift); without JavaScript the box would do nothing, so
 * NOSCRIPT, in the page's head, hides it, and the whole list is there. */
/** The index's head: without JavaScript, a stylesheet that hides the filter (the CSP allows no
 * inline style). */
export const NOSCRIPT =
  '<noscript><link rel="stylesheet" href="/assets/problems-nojs.css" /></noscript>';

export function renderIndex(doc, { script = "" } = {}) {
  const t = T[doc.lang];
  const n = doc.areas.reduce((k, a) => k + a.entries.length, 0);
  const forms = Object.entries(t.count)
    .map(([k, v]) => ` data-count-${k}="${esc(v)}"`)
    .join("");
  const count = new Intl.PluralRules(doc.lang).select(n);
  const areas = doc.areas
    .map(
      (a) => `
        <section aria-labelledby="area-${a.id}" data-area="${a.id}">
          <h2 id="area-${a.id}">${esc(a.heading)}</h2>
          <ul>${a.entries.map((e) => `<li><a href="${inLang(doc.lang, `/problems/${e.slug}/`)}"><span class="t">${esc(e.title)}</span> <code class="slug">${esc(e.slug)}</code></a></li>`).join("")}</ul>
        </section>`
    )
    .join("");
  return `<section class="page-head compact">
    <div class="frame">
      <h1>${esc(doc.title)}</h1>
      <p class="lead">${esc(T[doc.lang].indexLead)}</p>
      <div class="filter" role="search">
        <label for="filter">${esc(t.filter)}</label>
        <div class="field"><input id="filter" type="search" autocomplete="off" placeholder="${esc(t.placeholder)}" aria-describedby="count" /></div>
        <p class="count" id="count" aria-live="polite" lang="${doc.lang}"${forms}>${esc((t.count[count] ?? t.count.other).replace("{n}", String(n)))}</p>
      </div>
    </div>
  </section>
  <div class="band">
    <div class="frame">
      <p class="no-match" id="no-match" hidden>${esc(t.noMatch)}</p>
      <div class="areas">${areas}
      </div>
    </div>
  </div>${script ? `\n  <script type="module" src="${esc(script)}"></script>` : ""}`;
}

// ---- the pages -------------------------------------------------------------------------------

/**
 * Every problem page and the index, in both languages: [{ out, lang, path, title, description,
 * body }]. Fails, naming each entry and field, on a registry the pages cannot be drawn from.
 */
export function problemPages(reg, { resolves = () => false, script = "" } = {}) {
  const findings = registryFindings(reg);
  if (findings.length)
    throw new Error(
      `the problem registry has ${findings.length} problem(s):\n${findings.join("\n")}`
    );
  const entries = reg.problems;
  const known = new Set(entries.flatMap((e) => [e.slug, ...e.codes]));
  const out = [];
  for (const lang of LANGS) {
    const prefix = lang === "ar" ? "ar/" : "";
    const idx = indexDoc(entries, lang);
    out.push({
      out: `${prefix}problems/index.html`,
      lang,
      path: "/problems/",
      title: idx.title,
      description: idx.description,
      body: renderIndex(idx, { script }),
      meta: NOSCRIPT,
    });
    for (const e of entries) {
      const d = problemDoc(e, lang, { resolves });
      out.push({
        out: `${prefix}problems/${e.slug}/index.html`,
        lang,
        path: `/problems/${e.slug}/`,
        title: d.title,
        description: d.description,
        body: renderProblem(d, { known }),
      });
    }
  }
  return out;
}

/** The family, as build.mjs's FAMILIES runs it: `write(rel, content)`, the repository, whether
 * a site path resolves in what is built so far, and `script(src)`, which publishes a script of
 * src/ and returns its path (the index's filter, src/problems-filter.ts). */
export const PROBLEMS = {
  name: "problems",
  story: "60.06",
  build({ write, repo, resolves, script }) {
    const pages = problemPages(readRegistry(repo), {
      resolves,
      script: script("problems-filter.ts"),
    });
    for (const p of pages) write(p.out, page(p));
    return pages.length;
  },
};
