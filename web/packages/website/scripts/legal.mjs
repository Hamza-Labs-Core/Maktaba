// maktaba.tv's legal pages (Epic 60 story 60.07): /legal/privacy/, /legal/terms/ and
// /legal/copyright/, in English and Arabic.
//
// The content is copy (copy/legal/<page>.<lang>.md, written in each language), and the build
// turns it into a structured document ({ title, description, updated, summary, sections }),
// which one function renders (renderLegal). The design of story 60.01 part A replaces that
// function, not the copy.
//
// The privacy page is built from what the code says, not written apart from it:
//   - its cloud section is privacy.CurrentPolicy(), read through `go run ./cmd/privacy-json`
//     (cloud/), exactly as the relay serves it at GET /privacy. The English renders each value
//     as it is; the Arabic page's copy (copy/legal/policy.ar.json) gives each value's Arabic,
//     so a policy value without its Arabic fails the build, naming the section (T01);
//   - its apps section is written from docs/app-privacy.md's "What the apps send, and where".
//     Each language's copy records the sha256 of that section it was written from
//     (`apps-source:`), so a change to the source without the page's copy fails the build,
//     naming the section (T02).
//   - its date follows the policy's: the later of the copy's `updated` and the policy's
//     `updated_at`.
// The terms page names the licence from LICENSE (its first line), and fails the build if the
// copy was written for another one.
//
// A copy file: a front matter (title, description, updated, and the page's own keys), the
// summary (the text before the first heading), then sections, each `## Heading {#id}`. The
// text is a small Markdown: paragraphs, `-` and `1.` lists (one level of nesting), **bold**,
// `code` and [links](url). A line `{{name}}` alone is a block the build fills in (the policy,
// the notice form), and `{{name}}` in a sentence a value (the licence).

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { esc, GITHUB } from "./shell.mjs";
import { renderNoticeForm, AGENT_EMAIL } from "../src/copyright-form.ts";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
const REPO = path.resolve(PKG, "../../..");
export const COPY = path.join(PKG, "copy/legal");

/** The legal pages, each in both languages, at /legal/<name>/ and /ar/legal/<name>/. */
export const LEGAL = ["privacy", "terms", "copyright"];

const T = {
  en: {
    updated: "Last changed {date}",
    summary: "In short",
    toc: "On this page",
    months: "January February March April May June July August September October November December",
    retention: "{days} days, then deleted",
  },
  ar: {
    updated: "آخر تعديل: {date}",
    summary: "باختصار",
    toc: "في هذه الصفحة",
    months: "يناير فبراير مارس أبريل مايو يونيو يوليو أغسطس سبتمبر أكتوبر نوفمبر ديسمبر",
    retention: "{days} يومًا، ثم تُحذف",
  },
};

/** 2026-11-11 as the page writes it: "11 November 2026", «11 نوفمبر 2026» (Western digits). */
export function formatDate(iso, lang) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) throw new Error(`not a date (YYYY-MM-DD): ${iso}`);
  return `${+m[3]} ${T[lang].months.split(" ")[+m[2] - 1]} ${m[1]}`;
}

// ---- the copy -------------------------------------------------------------------------------

/** A copy file: { meta, summary, sections: [{ id, heading, md }] }. */
export function parseCopy(text, name = "copy") {
  const fm = /^---\n([\s\S]*?)\n---\n/.exec(text);
  if (!fm) throw new Error(`${name}: no front matter (--- … ---)`);
  const meta = {};
  for (const line of fm[1].split("\n")) {
    const i = line.indexOf(":");
    if (i < 0) throw new Error(`${name}: not a "key: value" line: ${line}`);
    meta[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  for (const k of ["title", "description", "updated"])
    if (!meta[k]) throw new Error(`${name}: no ${k} in its front matter`);
  formatDate(meta.updated, "en");
  const rest = text.slice(fm[0].length);
  const parts = rest.split(/^## /m);
  const summary = parts.shift().trim();
  if (!summary) throw new Error(`${name}: no summary before the first section`);
  const sections = parts.map((p) => {
    const nl = p.indexOf("\n");
    const head = (nl < 0 ? p : p.slice(0, nl)).trim();
    const m = /^(.*?)\s*\{#([a-z0-9-]+)\}$/.exec(head);
    if (!m) throw new Error(`${name}: a section heading without its {#id}: ## ${head}`);
    return { id: m[2], heading: m[1], md: nl < 0 ? "" : p.slice(nl + 1).trim() };
  });
  if (!sections.length) throw new Error(`${name}: no sections`);
  return { meta, summary, sections };
}

/** The inline Markdown: **bold**, `code`, [text](url); everything else escaped. */
export function inline(text) {
  const re = /`([^`]+)`|\*\*(.+?)\*\*|\[([^\]]+)\]\(([^)\s]+)\)/g;
  let out = "";
  let at = 0;
  for (const m of text.matchAll(re)) {
    out += esc(text.slice(at, m.index));
    if (m[1] !== undefined) out += `<code>${esc(m[1])}</code>`;
    else if (m[2] !== undefined) out += `<strong>${inline(m[2])}</strong>`;
    else out += `<a href="${esc(m[4])}">${inline(m[3])}</a>`;
    at = m.index + m[0].length;
  }
  return out + esc(text.slice(at));
}

/** The block Markdown: paragraphs, lists (one nested level), and `{{name}}` blocks filled from
 * `blocks`. */
export function renderMd(md, blocks = {}, name = "copy") {
  const out = [];
  for (const block of md.split(/\n\s*\n/)) {
    const b = block.replace(/\s+$/, "");
    if (!b.trim()) continue;
    const slot = /^\{\{([a-z-]+)\}\}$/.exec(b.trim());
    if (slot) {
      if (!(slot[1] in blocks)) throw new Error(`${name}: no block {{${slot[1]}}} to fill in`);
      if (blocks[slot[1]]) out.push(blocks[slot[1]]);
      continue;
    }
    if (/^\s/.test(b))
      throw new Error(
        `${name}: an indented block outside a list (a list item has one paragraph): ${b.trim().slice(0, 60)}`
      );
    if (/^(- |\d+\. )/.test(b)) out.push(list(b.split("\n"), name));
    else
      out.push(
        `<p>${inline(
          b
            .split("\n")
            .map((l) => l.trim())
            .join(" ")
        )}</p>`
      );
  }
  return out.join("\n");
}

function list(lines, name) {
  const ordered = /^\d+\. /.test(lines[0]);
  const items = [];
  for (const line of lines) {
    const top = /^(?:- |\d+\. )(.*)$/.exec(line);
    if (top) {
      items.push({ text: [top[1]], sub: [] });
      continue;
    }
    const item = items.at(-1);
    if (!item || !/^ {2,}\S/.test(line))
      throw new Error(`${name}: a list line out of place: ${line}`);
    const nested = /^ {2,}(?:- |\d+\. )/.test(line);
    if (nested || item.sub.length) item.sub.push(line.replace(/^ {2}/, ""));
    else item.text.push(line.trim());
  }
  const tag = ordered ? "ol" : "ul";
  const lis = items.map((i) => {
    const sub = i.sub.length ? `\n${list(i.sub, name)}` : "";
    return `<li>${inline(i.text.join(" "))}${sub}</li>`;
  });
  return `<${tag}>\n${lis.join("\n")}\n</${tag}>`;
}

export function readCopy(name, lang, dir = COPY) {
  const file = path.join(dir, `${name}.${lang}.md`);
  return parseCopy(fs.readFileSync(file, "utf8"), path.relative(REPO, file));
}

// ---- the sources ----------------------------------------------------------------------------

/** privacy.CurrentPolicy() as the relay serves it: `go run ./cmd/privacy-json` in cloud/. */
export function readPolicy(repo = REPO) {
  const out = execFileSync("go", ["run", "./cmd/privacy-json"], {
    cwd: path.join(repo, "cloud"),
    encoding: "utf8",
    stdio: ["ignore", "pipe", "inherit"],
  });
  return JSON.parse(out);
}

export const APPS_HEADING = "## What the apps send, and where";

/** docs/app-privacy.md's section the apps section is written from, and its sha256. */
export function appsSource(text) {
  const start = text.indexOf(`${APPS_HEADING}\n`);
  if (start < 0) throw new Error(`docs/app-privacy.md: no "${APPS_HEADING}" section`);
  const end = text.indexOf("\n## ", start + APPS_HEADING.length);
  const section = text.slice(start, end < 0 ? undefined : end).trim();
  return `sha256:${crypto.createHash("sha256").update(section).digest("hex")}`;
}

/** LICENSE's name: its first line ("MIT License"). */
export function licenceName(text) {
  const first = text.split("\n")[0].trim();
  if (!first) throw new Error("LICENSE: no first line naming the licence");
  return first;
}

/** What the pages are built from. `policy` is read only when the privacy page is built. */
export function readSources(repo = REPO) {
  let policy;
  return {
    get policy() {
      policy ??= readPolicy(repo);
      return policy;
    },
    appPrivacy: fs.readFileSync(path.join(repo, "docs/app-privacy.md"), "utf8"),
    license: fs.readFileSync(path.join(repo, "LICENSE"), "utf8"),
  };
}

// ---- the policy (the privacy page's cloud section) -------------------------------------------

/** The policy's fields, in the order the page lists them. */
export const POLICY_FIELDS = [
  "controller",
  "contact",
  "purpose",
  "data_categories",
  "not_collected",
  "lawful_basis",
  "retention_days",
  "rights",
];

function policyCopy(lang, dir = COPY) {
  return JSON.parse(fs.readFileSync(path.join(dir, `policy.${lang}.json`), "utf8"));
}

/** The policy's values in the page's language: English as the relay says it, Arabic from
 * copy/legal/policy.ar.json. Fails, naming the section, on a value with no Arabic or an
 * Arabic entry for a value the policy no longer has. */
export function policyValues(policy, lang, copy) {
  const where = `copy/legal/policy.${lang}.json`;
  const missing = POLICY_FIELDS.filter((f) => !(f in policy));
  if (missing.length)
    throw new Error(`privacy, the cloud section (#cloud): the policy has no ${missing.join(", ")}`);
  const used = new Set();
  const say = (v) => {
    if (lang === "en") return v;
    used.add(v);
    const t = copy.text?.[v];
    if (!t)
      throw new Error(
        `privacy, the cloud section (#cloud): CurrentPolicy() says "${v}", which ${where} does not give in Arabic: update the page's copy`
      );
    return t;
  };
  const out = {};
  for (const f of POLICY_FIELDS) {
    const v = policy[f];
    if (f === "retention_days") out[f] = T[lang].retention.replace("{days}", String(v));
    else if (Array.isArray(v)) out[f] = v.map(say);
    else if (f === "contact") out[f] = v;
    else out[f] = say(v);
  }
  if (lang !== "en") {
    const stale = Object.keys(copy.text ?? {}).filter((k) => !used.has(k));
    if (stale.length)
      throw new Error(
        `privacy, the cloud section (#cloud): ${where} gives Arabic for values CurrentPolicy() no longer has: ${stale.map((s) => `"${s}"`).join(", ")}`
      );
  }
  return out;
}

/** The policy as the page shows it: a list of terms and values, each marked with its field
 * (data-field), so the built page can be compared with the policy (T01). */
export function renderPolicy(values, labels) {
  const rows = POLICY_FIELDS.map((f) => {
    const v = values[f];
    const dd = Array.isArray(v)
      ? `<ul>\n${v.map((x) => `<li>${esc(x)}</li>`).join("\n")}\n</ul>`
      : f === "contact"
        ? `<a href="mailto:${esc(v)}">${esc(v)}</a>`
        : esc(v);
    if (!labels[f]) throw new Error(`copy/legal/policy.*.json: no label for ${f}`);
    return `<dt>${esc(labels[f])}</dt>\n<dd data-field="${f}">${dd}</dd>`;
  });
  return `<dl class="policy" data-policy="CurrentPolicy">\n${rows.join("\n")}\n</dl>`;
}

// ---- the documents --------------------------------------------------------------------------

/**
 * A legal page's structured content in one language: what renderLegal draws.
 * @param {string} name
 * @param {"en" | "ar"} lang
 * @param {{ sources: any, notice?: { endpoint: string, sitekey: string } | null,
 *   noticeScript?: string, dir?: string }} opts
 */
export function legalDoc(name, lang, { sources, notice = null, noticeScript = "", dir = COPY }) {
  const copy = readCopy(name, lang, dir);
  const where = `copy/legal/${name}.${lang}.md`;
  const blocks = {};
  let updated = copy.meta.updated;
  let licence = null;
  if (name === "privacy") {
    const policy = sources.policy;
    const want = appsSource(sources.appPrivacy);
    if (copy.meta["apps-source"] !== want)
      throw new Error(
        `privacy, the apps section (#apps): docs/app-privacy.md's "${APPS_HEADING.slice(3)}" changed since ${where} was written (apps-source: ${copy.meta["apps-source"] ?? "none"}, the source is now ${want}): update the apps section in both languages and its apps-source line`
      );
    const pc = policyCopy(lang, dir);
    blocks.policy = renderPolicy(policyValues(policy, lang, pc), pc.labels);
    if (policy.updated_at > updated) updated = policy.updated_at;
  }
  if (name === "terms") {
    licence = licenceName(sources.license);
    if (copy.meta.licence !== licence)
      throw new Error(
        `terms, the software section (#software): LICENSE is the ${licence}, and ${where} was written for ${copy.meta.licence ?? "no licence"}: update the page's copy`
      );
  }
  if (name === "copyright")
    blocks.form = notice ? renderNoticeForm(lang, notice, noticeScript) : "";
  const fill = (md) => (licence ? md.replaceAll("{{licence}}", licence) : md);
  return {
    name,
    lang,
    title: copy.meta.title,
    description: copy.meta.description,
    updated,
    summary: renderMd(fill(copy.summary), {}, where),
    sections: copy.sections.map((s) => ({
      id: s.id,
      heading: s.heading,
      html: renderMd(fill(s.md), blocks, where),
    })),
  };
}

/** The legal layout (60.01 part A): the title, the date it last changed, the summary, the
 * table of contents, the sections. The one function the design replaces. */
export function renderLegal(doc) {
  const t = T[doc.lang];
  const date = `<time datetime="${doc.updated}">${esc(formatDate(doc.updated, doc.lang))}</time>`;
  const toc = doc.sections
    .map((s) => `<li><a href="#${s.id}">${inline(s.heading)}</a></li>`)
    .join("\n");
  const sections = doc.sections
    .map(
      (s) => `<section aria-labelledby="${s.id}">
<h2 id="${s.id}">${inline(s.heading)}</h2>
${s.html}
</section>`
    )
    .join("\n\n");
  return `<article class="legal" data-legal="${doc.name}">
<h1>${esc(doc.title)}</h1>
<p class="legal-updated">${t.updated.replace("{date}", date)}</p>

<section class="legal-summary" aria-labelledby="summary">
<h2 id="summary">${esc(t.summary)}</h2>
${doc.summary}
</section>

<nav class="legal-toc" aria-labelledby="toc">
<h2 id="toc">${esc(t.toc)}</h2>
<ol>
${toc}
</ol>
</nav>

${sections}
</article>`;
}

/** The checks across a page's two languages: the same sections, in the same order. */
export function twinSections(en, ar) {
  const a = en.sections.map((s) => s.id).join(" ");
  const b = ar.sections.map((s) => s.id).join(" ");
  if (a !== b)
    throw new Error(`${en.name}: the English sections (${a}) and the Arabic (${b}) differ`);
}

/**
 * Every legal page in both languages, for the build: [{ name, lang, path, title,
 * description, body }].
 * @param {{ sources?: any, notice?: { endpoint: string, sitekey: string } | null,
 *   noticeScript?: string }} [opts]
 */
export function legalPages({ sources = readSources(), notice = null, noticeScript = "" } = {}) {
  const out = [];
  for (const name of LEGAL) {
    const docs = ["en", "ar"].map((lang) =>
      legalDoc(name, lang, { sources, notice, noticeScript })
    );
    twinSections(docs[0], docs[1]);
    for (const d of docs)
      out.push({
        name,
        lang: d.lang,
        path: `/legal/${name}/`,
        title: d.title,
        description: d.description,
        body: renderLegal(d),
      });
  }
  return out;
}

export { AGENT_EMAIL, GITHUB };
