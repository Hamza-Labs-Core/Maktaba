// maktaba.tv's help (Epic 60 story 60.12): /help/<topic>/ and /ar/help/<topic>/ for every topic
// of Epic 41, and the index, /help/ and /ar/help/, grouped as the app groups them. One source:
// the app's own topics, so the site and the app cannot drift.
//
//   - web/src/help/topics.ts, read as data: the categories and their topics, in the app's order;
//   - web/src/help/content/<lang>/<topic>.md: each topic's Markdown, published as written;
//   - web/src/i18n/<lang>.json: each topic's title and summary, and each category's name, the
//     strings the app's own help index shows;
//   - web/src/App.tsx, and the route modules it imports (serverRoutes.tsx): the app's routes.
//
// The Markdown is the app's (HelpMarkdown, on web/src/lib/markdown.tsx): headings carry their
// {#anchor} as id, paragraphs, bullet and numbered lists, fenced code, quotes, rules, **bold**,
// *italic*, `code`, [links](…) and bare URLs; and pipe tables, which the site draws as tables.
//
// The links are rewritten at build time (renderInline's `link`):
//   - /help/<topic>#<anchor> becomes /help/<topic>/#<anchor> in the page's language, and #<anchor>
//     stays on the page: the topic and its anchor must exist, or the build fails;
//   - an app route (any path the app's router matches, never a hand-made list) is not a page of
//     the site: it is drawn as text, "In Maktaba: **Settings → Rules**", its label the link's own
//     text, its arrows the page's direction; the app's root ("/") is "the home screen";
//   - a path the router does not match fails the build, naming the topic and the link;
//   - an external link (http, https, mailto) stays as written.
//
// A topic whose Arabic file is missing falls back to English in the app; here it fails the build,
// naming it: a public page in the wrong language is a defect (T02).
//
// The pages are 60.01 part B's help layouts (design/mockups/help.*.html, help-topic.*.html) in
// part B's shell; the index's filter is the problem index's script (src/problems-filter.ts),
// which narrows the list in the page, over each topic's title, summary and headings.

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { bLinks, codeBlock, esc, ICONS, inLang, page } from "./shell.mjs";
import { NOSCRIPT } from "./problems.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");

export const LANGS = ["en", "ar"];
export const TOPICS = "web/src/help/topics.ts";
export const CONTENT = "web/src/help/content";
export const CATALOG = "web/src/i18n";
export const ROUTER = "web/src/App.tsx";
export const PATH = "/help/";

/** The pages' words (story 60.12's strings table). */
export const T = {
  en: {
    title: "Help",
    name: "Maktaba",
    description:
      "How to use Maktaba at home: the same help topics the app shows, in English and Arabic.",
    social: "How to use Maktaba at home",
    lead: "How to use Maktaba at home, one topic at a time.",
    sameAsApp: "This is the same help you’ll find in the app, under Help.",
    filter: "Find a topic",
    count: { one: "1 topic", other: "{n} topics" },
    noMatch: "No topic matches. Try a shorter word.",
    toc: "On this page",
    related: "Related topics",
    where: "In Maktaba:",
    home: "the home screen",
    arrow: "→",
    code: "Commands",
    copyCode: "Copy the commands",
  },
  ar: {
    title: "المساعدة",
    name: "مكتبة",
    description:
      "طريقة استخدام برنامج مكتبة في البيت: مواضيع المساعدة نفسها التي يعرضها التطبيق، بالعربية والإنجليزية.",
    social: "طريقة استخدام مكتبة في البيت",
    lead: "طريقة استخدام برنامج مكتبة في البيت، موضوعًا بعد موضوع.",
    sameAsApp: "هذه هي المساعدة نفسها التي تجدونها في التطبيق، في قسم المساعدة.",
    filter: "البحث عن موضوع",
    // Arabic's plural forms (Intl.PluralRules): the filter's script picks one by the count
    count: {
      zero: "لا موضوع",
      one: "موضوع واحد",
      two: "موضوعان",
      few: "{n} مواضيع",
      many: "{n} موضوعًا",
      other: "{n} موضوع",
    },
    noMatch: "لا موضوع يطابق ما كتبتم. جرّبوا كلمة أقصر.",
    toc: "في هذه الصفحة",
    related: "مواضيع ذات صلة",
    where: "في مكتبة:",
    home: "الشاشة الرئيسية",
    arrow: "←",
    code: "الأوامر",
    copyCode: "نسخ الأوامر",
  },
};

/** How many related topics a topic shows. */
export const RELATED = 3;

/** The ids the page itself gives its elements: a heading's anchor may not take one. */
export const PAGE_IDS = [
  "main",
  "toc-title",
  "related-title",
  "announce",
  "lang-switch",
  "site-nav",
];

// ---- the sources --------------------------------------------------------------------------------

const read = (repo, rel) => fs.readFileSync(path.join(repo, rel), "utf8");

/** topics.ts's HELP_CATEGORIES as data: [{ id, topics: [id, …] }], in the app's order. */
export function readCategories(repo) {
  const src = read(repo, TOPICS);
  const start = src.indexOf("HELP_CATEGORIES");
  const end = src.indexOf("] as const", start);
  if (start < 0 || end < 0) throw new Error(`help: ${TOPICS}: no HELP_CATEGORIES … as const`);
  const out = [...src.slice(start, end).matchAll(/id: "([a-z-]+)",\s*topics: \[([^\]]*)\]/g)].map(
    (m) => ({ id: m[1], topics: [...m[2].matchAll(/"([a-z0-9-]+)"/g)].map((t) => t[1]) })
  );
  if (!out.length) throw new Error(`help: ${TOPICS}: no category could be read`);
  const all = out.flatMap((c) => c.topics);
  if (new Set(all).size !== all.length) throw new Error(`help: ${TOPICS}: a topic listed twice`);
  return out;
}

/** The app's catalog in a language, flattened: { "help.topic.search.title": "…" }. */
export function readCatalog(repo, lang) {
  const flat = {};
  const walk = (o, p) => {
    for (const [k, v] of Object.entries(o)) {
      const key = p ? `${p}.${k}` : k;
      if (v && typeof v === "object") walk(v, key);
      else flat[key] = v;
    }
  };
  walk(JSON.parse(read(repo, `${CATALOG}/${lang}.json`)), "");
  return flat;
}

/**
 * The app's routes: every `<Route path="…">` of its router (App.tsx) and of the route modules it
 * imports (`import { serverRoutes } from "./serverRoutes"`), without the catch-all ("*").
 */
export function readRoutes(repo, router = ROUTER) {
  const src = read(repo, router);
  const files = [router];
  for (const m of src.matchAll(/from "\.\/([A-Za-z]+Routes)"/g))
    files.push(path.posix.join(path.posix.dirname(router), `${m[1]}.tsx`));
  const routes = files.flatMap((f) =>
    [...read(repo, f).matchAll(/<Route\s+path="([^"]+)"/g)].map((m) => m[1])
  );
  const out = [...new Set(routes)].filter((r) => r !== "*");
  if (out.length < 20) throw new Error(`help: ${router}: only ${out.length} routes could be read`);
  return out;
}

/** Whether the app's router opens a path: a `:param` takes any one segment, `*` the rest. */
export function matchRoute(routes, p) {
  const segs = p.split("/").filter(Boolean);
  return routes.some((r) => {
    const rs = r.split("/").filter(Boolean);
    for (let i = 0; i < rs.length; i++) {
      if (rs[i] === "*") return true;
      if (i >= segs.length) return false;
      if (rs[i].startsWith(":")) continue;
      if (rs[i] !== segs[i]) return false;
    }
    return rs.length === segs.length;
  });
}

/**
 * Everything the pages are drawn from: { categories, topics: { en: { id: md }, ar }, catalog:
 * { en, ar }, routes }. A topic's file missing in a language is left out of `topics[lang]`:
 * helpPages fails on it, naming it.
 */
export function readSource(repo) {
  const categories = readCategories(repo);
  const ids = categories.flatMap((c) => c.topics);
  const topics = {};
  const catalog = {};
  for (const lang of LANGS) {
    topics[lang] = {};
    for (const id of ids) {
      const f = path.join(repo, CONTENT, lang, `${id}.md`);
      if (fs.existsSync(f)) topics[lang][id] = fs.readFileSync(f, "utf8");
    }
    catalog[lang] = readCatalog(repo, lang);
  }
  return { categories, topics, catalog, routes: readRoutes(repo) };
}

/** What is wrong with the source, one line each: a topic without its file or catalog strings in
 * a language, a category without its name. Empty when every page can be drawn. */
export function sourceFindings(src) {
  const out = [];
  const name = { en: "English", ar: "Arabic" };
  for (const c of src.categories)
    for (const lang of LANGS)
      if (!src.catalog[lang][`help.category.${c.id}`])
        out.push(`help: the category ${c.id} has no ${name[lang]} name (help.category.${c.id})`);
  for (const id of src.categories.flatMap((c) => c.topics))
    for (const lang of LANGS) {
      if (src.topics[lang][id] === undefined)
        out.push(
          `help: the topic ${id} has no ${name[lang]} file (${CONTENT}/${lang}/${id}.md): a public page in the wrong language is a defect`
        );
      for (const k of ["title", "summary"])
        if (!src.catalog[lang][`help.topic.${id}.${k}`])
          out.push(`help: the topic ${id} has no ${name[lang]} ${k} (help.topic.${id}.${k})`);
    }
  return out;
}

// ---- the Markdown (the app's: web/src/lib/markdown.tsx, web/src/help/parse.ts) -----------------

const HEADING = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const ANCHOR = /\s*\{#([a-z0-9-]+)\}\s*$/;
const BULLET = /^\s*[-*+]\s+(.*)$/;
const NUMBERED = /^\s*\d+[.)]\s+(.*)$/;
const TABLE_ROW = /^\s*\|.*\|\s*$/;
const TABLE_RULE = /^\s*\|(\s*:?-{3,}:?\s*\|)+\s*$/;

/** "What it is {#what-it-is}" → { text: "What it is", anchor: "what-it-is" } (parse.ts). */
export function splitHeading(raw) {
  const m = ANCHOR.exec(raw);
  if (!m) return { text: raw.trim(), anchor: null };
  return { text: raw.slice(0, m.index).trim(), anchor: m[1] };
}

const cells = (line) =>
  line
    .trim()
    .replace(/^\||\|$/g, "")
    .split("|")
    .map((c) => c.trim());

/** The blocks of a topic, as the app's parseBlocks reads them, and pipe tables. */
export function parseBlocks(src) {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out = [];
  let i = 0;
  const isTable = (j) => TABLE_ROW.test(lines[j] ?? "") && TABLE_RULE.test(lines[j + 1] ?? "");
  while (i < lines.length) {
    const line = lines[i];
    if (/^\s*$/.test(line)) {
      i++;
      continue;
    }
    if (/^\s*(```|~~~)/.test(line)) {
      const fence = line.trim().slice(0, 3);
      const body = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence)) body.push(lines[i++]);
      i++;
      out.push({ kind: "code", text: body.join("\n") });
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      out.push({ kind: "heading", level: h[1].length, text: h[2] });
      i++;
      continue;
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) {
      out.push({ kind: "rule" });
      i++;
      continue;
    }
    if (isTable(i)) {
      const head = cells(lines[i]);
      i += 2;
      const rows = [];
      while (i < lines.length && TABLE_ROW.test(lines[i])) rows.push(cells(lines[i++]));
      out.push({ kind: "table", head, rows });
      continue;
    }
    if (/^\s*>/.test(line)) {
      const body = [];
      while (i < lines.length && /^\s*>/.test(lines[i]))
        body.push(lines[i++].replace(/^\s*>\s?/, ""));
      out.push({ kind: "quote", text: body.join(" ") });
      continue;
    }
    const ordered = NUMBERED.test(line);
    if (ordered || BULLET.test(line)) {
      const re = ordered ? NUMBERED : BULLET;
      const items = [];
      while (i < lines.length) {
        const m = re.exec(lines[i]);
        if (m) {
          items.push(m[1]);
          i++;
        } else if (items.length > 0 && /^\s{2,}\S/.test(lines[i])) {
          items[items.length - 1] += " " + lines[i++].trim();
        } else break;
      }
      out.push({ kind: "list", ordered, items });
      continue;
    }
    const body = [];
    while (
      i < lines.length &&
      !/^\s*$/.test(lines[i]) &&
      !/^\s*(```|~~~|#{1,6}\s|>)/.test(lines[i]) &&
      !BULLET.test(lines[i]) &&
      !NUMBERED.test(lines[i]) &&
      !isTable(i)
    ) {
      body.push(lines[i++].trim());
    }
    out.push({ kind: "para", text: body.join(" ") });
  }
  return out;
}

/** A topic's anchors: the {#id} of each heading, in order. */
export function anchorsOf(src) {
  return parseBlocks(src)
    .filter((b) => b.kind === "heading")
    .map((b) => splitHeading(b.text).anchor)
    .filter(Boolean);
}

/** A topic's headings below its title, as text, for the index's filter. */
export function headingsOf(src) {
  return parseBlocks(src)
    .filter((b) => b.kind === "heading" && b.level > 1)
    .map((b) => plainInline(splitHeading(b.text).text));
}

/** A line of Markdown without its marks: what the filter and the table of contents read. */
export function plainInline(s) {
  return s
    .replace(/\[([^\]]+)\]\([^)\s]+\)/g, "$1")
    .replace(/(\*\*|__)(.+?)\1/g, "$2")
    .replace(/`([^`]+)`/g, "$1");
}

// one inline token at a time, earliest match first (markdown.tsx's INLINE)
const INLINE =
  /(`[^`]+`)|(\*\*[^*]+\*\*|__[^_]+__)|(\*[^*\s][^*]*\*|_[^_\s][^_]*_)|(\[[^\]]+\]\([^)\s]+\))|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/;

/** "Settings → Rules" with each arrow pointing the page's way: → in English, ← in Arabic. */
const arrows = (s, lang) => s.replace(/[→←]/g, T[lang].arrow);

/** "In Maktaba: **Settings → Rules**", for a link to a page of the app. */
export function where(lang, labelHtml) {
  return `<span class="where">${esc(T[lang].where)} <strong>${labelHtml}</strong></span>`;
}

/**
 * A link of a topic, as the site draws it (the rules at the top of this file). `ctx`: the page's
 * language and topic, `anchors(lang, topic)` (null: no such topic), the app's routes.
 */
export function link(label, href, ctx) {
  const { lang, topic, anchors, routes } = ctx;
  const fail = (why) => {
    throw new Error(`help: ${CONTENT}/${lang}/${topic}.md: the link ${href} ${why}`);
  };
  const text = () => inline(label, ctx);
  if (/^(https?:\/\/|mailto:)/i.test(href)) return `<a href="${esc(href)}">${text()}</a>`;
  if (href.startsWith("#")) {
    const a = href.slice(1);
    if (!anchors(lang, topic)?.includes(a)) fail(`names no heading of this topic`);
    return `<a href="#${esc(a)}">${text()}</a>`;
  }
  if (!href.startsWith("/") || href.startsWith("//")) fail("is neither a path nor an address");
  const help = /^\/help(?:\/([a-z0-9-]+))?\/?(?:#([a-z0-9-]+))?$/.exec(href);
  if (help) {
    const [, id, a] = help;
    if (!id) return `<a href="${inLang(lang, PATH)}">${text()}</a>`;
    const have = anchors(lang, id);
    if (!have) fail("names no help topic");
    if (a && !have.includes(a)) fail(`names no heading of the topic ${id}`);
    return `<a href="${inLang(lang, `/help/${id}/`)}${a ? `#${a}` : ""}">${text()}</a>`;
  }
  const p = href.replace(/[?#].*$/, "");
  if (p === "/") return where(lang, esc(T[lang].home));
  if (!matchRoute(routes, p)) fail(`is not a route of the app's router (${ROUTER})`);
  return where(lang, inline(arrows(label, lang), { ...ctx, inWhere: true }));
}

/** A line of Markdown as HTML: the app's inline marks, and its links as `link` draws them. */
export function inline(text, ctx) {
  let out = "";
  let rest = text;
  while (rest.length > 0) {
    const m = INLINE.exec(rest);
    if (!m) {
      out += esc(rest);
      break;
    }
    out += esc(rest.slice(0, m.index));
    const tok = m[0];
    if (m[1]) out += `<code>${esc(tok.slice(1, -1))}</code>`;
    else if (m[2]) out += `<strong>${inline(tok.slice(2, -2), ctx)}</strong>`;
    else if (m[3]) out += `<em>${inline(tok.slice(1, -1), ctx)}</em>`;
    else if (m[4]) {
      const [, label, href] = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(tok);
      // a link inside an "In Maktaba" label (none today) is its text
      out += ctx.inWhere ? inline(label, ctx) : link(label, href, ctx);
    } else out += `<a href="${esc(tok)}">${esc(tok)}</a>`;
    rest = rest.slice(m.index + tok.length);
  }
  return out;
}

/**
 * A topic's Markdown as the page's article: { lead, html, toc }. The title (`# `) is the page's
 * h1, drawn from the catalog as the app draws it; the text before the first section is the lead;
 * each `## ` with an anchor is a line of the table of contents.
 */
export function renderMarkdown(src, ctx) {
  const blocks = parseBlocks(src);
  const out = [];
  const toc = [];
  const ids = new Set();
  let sections = 0;
  for (const b of blocks) {
    switch (b.kind) {
      case "heading": {
        if (b.level === 1) break;
        const { text, anchor } = splitHeading(b.text);
        const level = Math.min(4, b.level);
        if (anchor) {
          if (ids.has(anchor) || PAGE_IDS.includes(anchor))
            throw new Error(
              `help: ${CONTENT}/${ctx.lang}/${ctx.topic}.md: the anchor ${anchor} is used twice`
            );
          ids.add(anchor);
        }
        if (level === 2) {
          sections++;
          if (anchor) toc.push({ anchor, text: inline(plainInline(text), ctx) });
        }
        out.push(`<h${level}${anchor ? ` id="${anchor}"` : ""}>${inline(text, ctx)}</h${level}>`);
        break;
      }
      case "list": {
        const L = b.ordered ? "ol" : "ul";
        out.push(`<${L}>${b.items.map((it) => `<li>${inline(it, ctx)}</li>`).join("")}</${L}>`);
        break;
      }
      case "code":
        out.push(
          codeBlock(ctx.lang, {
            name: esc(T[ctx.lang].code),
            code: b.text,
            copyName: T[ctx.lang].copyCode,
          })
        );
        break;
      case "table": {
        const head = b.head.map((c) => `<th scope="col">${inline(c, ctx)}</th>`).join("");
        const rows = b.rows
          .map(
            (r) =>
              `<tr>${b.head.map((_, j) => `<td>${inline(r[j] ?? "", ctx)}</td>`).join("")}</tr>`
          )
          .join("");
        out.push(
          `<div class="table-wrap"><table><thead><tr>${head}</tr></thead><tbody>${rows}</tbody></table></div>`
        );
        break;
      }
      case "quote":
        out.push(`<blockquote><p>${inline(b.text, ctx)}</p></blockquote>`);
        break;
      case "rule":
        out.push("<hr />");
        break;
      default:
        out.push(`<p${sections === 0 ? ' class="lead-p"' : ""}>${inline(b.text, ctx)}</p>`);
    }
  }
  return { html: out.join("\n        "), toc };
}

// ---- the documents --------------------------------------------------------------------------------

/** A topic's related topics: those it links to, in the order it first does, then the others of
 * its category, at most RELATED. */
export function relatedOf(id, src, categories) {
  const linked = [...src.matchAll(/\]\(\/help\/([a-z0-9-]+)/g)].map((m) => m[1]);
  const sibling = categories.find((c) => c.topics.includes(id))?.topics ?? [];
  const all = new Set(categories.flatMap((c) => c.topics));
  return [...new Set([...linked, ...sibling])]
    .filter((t) => t !== id && all.has(t))
    .slice(0, RELATED);
}

const card = (lang, id, cat) =>
  `<li><a href="${inLang(lang, `/help/${id}/`)}"><strong>${esc(cat[`help.topic.${id}.title`])}</strong><span>${esc(cat[`help.topic.${id}.summary`])}</span></a></li>`;

/** The index layout (60.01 part B, help): the title, the lead, the line that this is the app's
 * help, the filter (in place from the first paint; NOSCRIPT hides it without JavaScript, when the
 * whole list shows anyway), and each category with its topics. */
export function renderIndex(lang, src, { script = "" } = {}) {
  const t = T[lang];
  const cat = src.catalog[lang];
  const n = src.categories.reduce((k, c) => k + c.topics.length, 0);
  const forms = Object.entries(t.count)
    .map(([k, v]) => ` data-count-${k}="${esc(v)}"`)
    .join("");
  const count = new Intl.PluralRules(lang).select(n);
  const groups = src.categories
    .map(
      (c) => `<section class="help-group" aria-labelledby="cat-${c.id}">
        <h2 id="cat-${c.id}">${esc(cat[`help.category.${c.id}`])}</h2>
        <ul class="topics">${c.topics
          .map((id) =>
            card(lang, id, cat).replace(
              "<li>",
              `<li data-search="${esc(headingsOf(src.topics[lang][id]).join(" · "))}">`
            )
          )
          .join("")}</ul>
      </section>`
    )
    .join("\n      ");
  return `<section class="page-head compact">
    <div class="frame">
      <h1>${esc(t.title)}</h1>
      <p class="lead">${esc(t.lead)}</p>
      <p class="same-help">${ICONS.note}${esc(t.sameAsApp)}</p>
      <div class="filter" role="search">
        <label for="filter">${esc(t.filter)}</label>
        <div class="field">${ICONS.search}<input id="filter" type="search" autocomplete="off" placeholder="${esc(cat["help.searchPlaceholder"] ?? "")}" aria-describedby="count" /></div>
        <p class="count" id="count" aria-live="polite" lang="${lang}"${forms}>${esc((t.count[count] ?? t.count.other).replace("{n}", String(n)))}</p>
      </div>
    </div>
  </section>
  <div class="band">
    <div class="frame help-groups" data-filter-list>
      <p class="no-match" id="no-match" hidden>${esc(t.noMatch)}</p>
      ${groups}
    </div>
  </div>${script ? `\n  <script type="module" src="${esc(script)}"></script>` : ""}`;
}

/** The topic layout (60.01 part B, help topic): the way back to the index, the title, the line
 * that this is the app's help; the table of contents (at the end side from 1024 px), the topic,
 * and its related topics. */
export function renderTopic(lang, id, src) {
  const t = T[lang];
  const cat = src.catalog[lang];
  const md = src.topics[lang][id];
  const ctx = {
    lang,
    topic: id,
    routes: src.routes,
    anchors: (l, tid) => (src.topics[l][tid] === undefined ? null : anchorsOf(src.topics[l][tid])),
  };
  const { html, toc } = renderMarkdown(md, ctx);
  const related = relatedOf(id, md, src.categories);
  const nav = toc.length
    ? `<nav class="toc" aria-labelledby="toc-title">
        <h2 id="toc-title">${esc(t.toc)}</h2>
        <ol>${toc.map((h) => `<li><a href="#${h.anchor}">${h.text}</a></li>`).join("")}</ol>
      </nav>
      `
    : "";
  return `<section class="page-head compact" data-topic="${esc(id)}">
    <div class="frame">
      <p class="crumbs"><a href="${inLang(lang, PATH)}">${ICONS.back}${esc(t.title)}</a></p>
      <h1>${esc(cat[`help.topic.${id}.title`])}</h1>
      <p class="same-help">${ICONS.note}${esc(t.sameAsApp)}</p>
    </div>
  </section>
  <div class="band">
    <div class="frame topic-layout">
      ${nav}<article class="prose">
        ${html}
        <section class="related" aria-labelledby="related-title">
          <h2 id="related-title">${esc(t.related)}</h2>
          <ul class="topics">${related.map((r) => card(lang, r, cat)).join("")}</ul>
        </section>
      </article>
    </div>
  </div>`;
}

// ---- the pages -----------------------------------------------------------------------------------

const hashed = (name, ext, content) =>
  `assets/${name}-${crypto.createHash("sha256").update(content).digest("base64url").slice(0, 10)}.${ext}`;

/** The help pages' own stylesheet (src/help.css: tables, the "In Maktaba" text, the related
 * topics in the article), after part B's. */
export function styleFile() {
  const css = fs.readFileSync(path.join(PKG, "src/help.css"), "utf8");
  return { file: hashed("help", "css", css), content: css };
}

/**
 * Every help page in both languages, for page(): the index and each topic. Fails, naming each,
 * on a topic without its file or strings in a language, or a link it cannot draw.
 */
export function helpPages(src, { resolves = () => false, styles = "", script = "" } = {}) {
  const findings = sourceFindings(src);
  if (findings.length)
    throw new Error(`the help has ${findings.length} problem(s):\n${findings.join("\n")}`);
  // these pages are being written, so `resolves` cannot see them yet: /help/ counts as built
  const links = bLinks((href) => href === PATH || resolves(href));
  const head = styles ? `<link rel="stylesheet" href="${styles}" />` : "";
  const out = [];
  for (const lang of LANGS) {
    const t = T[lang];
    const cat = src.catalog[lang];
    const prefix = lang === "ar" ? "ar/" : "";
    out.push({
      out: `${prefix}help/index.html`,
      lang,
      path: PATH,
      title: `${t.title} · ${t.name}`,
      description: t.description,
      social: t.social,
      body: renderIndex(lang, src, { script }),
      meta: NOSCRIPT,
      variant: "b",
      links,
      head,
    });
    for (const id of src.categories.flatMap((c) => c.topics)) {
      const title = cat[`help.topic.${id}.title`];
      out.push({
        out: `${prefix}help/${id}/index.html`,
        lang,
        path: `/help/${id}/`,
        title: `${title} · ${t.title} · ${t.name}`,
        description: cat[`help.topic.${id}.summary`],
        social: title,
        body: renderTopic(lang, id, src),
        variant: "b",
        links,
        head,
      });
    }
  }
  return out;
}

/** build.mjs's family: the source read, every page in both languages, the stylesheet and the
 * index's filter (the problem index's script). */
export const HELP = {
  name: "help",
  story: "60.12",
  paths: [PATH],
  build({ write, repo, resolves, script }) {
    const s = styleFile();
    write(s.file, s.content);
    const pages = helpPages(readSource(repo), {
      resolves,
      styles: `/${s.file}`,
      script: script("problems-filter.ts"),
    });
    for (const p of pages) write(p.out, page(p));
    return pages.length;
  },
};
