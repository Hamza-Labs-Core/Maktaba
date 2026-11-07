// The listing pages (story 54.18 §2): plugins.maktaba.tv and plugins.maktaba.tv/p/<id>, in
// English and in Arabic (/ar/…, right to left), rendered on the server from the signed
// catalog on the CDN so search engines read them. No sign-in to read. Each page offers "Open in
// my Maktaba" (maktaba://plugins/<id> in the installed apps, or the home's own address, asked
// once and remembered on this device only: no cloud hop) and "Download file" for an offline
// install. Every text is escaped: a plugin's words or a review are text, never markup.
import CAPS from "../checks/capabilities.json";
import type { Env } from "./env";

type Lang = "en" | "ar";

const W = {
  en: {
    title: "Maktaba Plugins",
    lead: "Plugins add new functions to your Maktaba home. They run without internet, in a sandbox, with only the access you grant.",
    open: "Open in my Maktaba",
    download: "Download file",
    reviewed: "Reviewed",
    automated: "Automated checks",
    rating: "{avg} out of 5, {n} ratings",
    few: "Not enough ratings",
    capabilities: "What it can do",
    versions: "Versions",
    by: "By {dev}",
    machine: "Arabic machine translated",
    none: "The plugin store is opening soon.",
    lang: "العربية",
    langHref: "/ar",
    reviews: "Reviews",
    home: "Your Maktaba's address (for example http://192.168.1.10:8096)",
  },
  ar: {
    title: "إضافات مكتبة",
    lead: "تضيف الإضافات وظائف جديدة إلى جهازك الرئيسي في مكتبة. وتعمل بلا إنترنت، في عزل، ولا ترى إلا ما تسمح به.",
    open: "افتح في مكتبتي",
    download: "نزّل الملف",
    reviewed: "مراجَعة",
    automated: "فحوص آلية",
    rating: "{avg} من 5، {n} تقييمات",
    few: "التقييمات غير كافية",
    capabilities: "ما يمكنها فعله",
    versions: "الإصدارات",
    by: "من {dev}",
    machine: "ترجمة آلية",
    none: "متجر الإضافات يُفتح قريبًا.",
    lang: "English",
    langHref: "/",
    reviews: "المراجعات",
    home: "عنوان مكتبتك (مثل http://192.168.1.10:8096)",
  },
} as const;

/** HTML-escapes text: a plugin's words and reviews are shown as text, never as markup. */
export function esc(s: unknown): string {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

interface CatVersion {
  version: string;
  sha256: string;
  size: number;
  url: string;
  maktaba: string;
  capabilities: string[];
  review_level: string;
  released: string;
}
interface CatPlugin {
  id: string;
  developer: string;
  names: Record<string, string>;
  summaries: Record<string, string>;
  category: string;
  tags: string[];
  rating_avg_x100: number;
  rating_count: number;
  installs: number;
  machine_translated: string[];
  versions: CatVersion[];
  extra?: { descriptions?: Record<string, string> };
}

async function catalog(env: Env): Promise<CatPlugin[]> {
  const obj = await env.BUCKET.get("catalog-1.json");
  if (!obj) return [];
  const env1 = JSON.parse(await obj.text()) as { signed?: { plugins?: CatPlugin[] } };
  return env1.signed?.plugins ?? [];
}

function capWords(name: string, lang: Lang): string {
  const c = (CAPS as { capabilities: { name: string; words: Record<Lang, string> }[] }).capabilities.find((x) => x.name === name.split(":", 1)[0]);
  return c ? c.words[lang] : name;
}

const fill = (s: string, p: Record<string, string | number>) => s.replace(/\{(\w+)\}/g, (_, k: string) => String(p[k] ?? ""));

function page(lang: Lang, title: string, body: string, path: string): Response {
  const w = W[lang];
  const html = `<!doctype html>
<html lang="${lang}" dir="${lang === "ar" ? "rtl" : "ltr"}">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<link rel="alternate" hreflang="en" href="${esc(path.replace(/^\/ar/, "") || "/")}">
<link rel="alternate" hreflang="ar" href="${esc("/ar" + (path.replace(/^\/ar/, "") === "/" ? "" : path.replace(/^\/ar/, "")))}">
<style>
:root{--bg:#fbfaf7;--fg:#1c1b19;--muted:#6b675f;--line:#ddd8cc;--accent:#1f6f5c;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:#141412;--fg:#ecebe7;--muted:#a39f95;--line:#34322d;--accent:#6cc4ad;color-scheme:dark}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,"Noto Naskh Arabic",sans-serif}
header,main{max-inline-size:56rem;margin-inline:auto;padding:1rem}
header{display:flex;gap:1rem;align-items:center;border-block-end:1px solid var(--line)}
a{color:var(--accent)} .muted{color:var(--muted)} .badge{border:1px solid var(--line);border-radius:4px;padding:0 .4rem;font-size:.85em}
ul.plugins{list-style:none;padding:0} ul.plugins li{border-block-end:1px solid var(--line);padding-block:.75rem}
</style>
</head>
<body>
<header><strong><a href="${lang === "ar" ? "/ar" : "/"}">${esc(w.title)}</a></strong><span style="flex:1"></span><a href="${esc(w.langHref + (path.replace(/^\/ar/, "") === "/" ? "" : path.replace(/^\/ar/, "")))}" lang="${lang === "ar" ? "en" : "ar"}">${esc(w.lang)}</a></header>
<main>${body}</main>
</body>
</html>`;
  return new Response(html, {
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; img-src https://plugins-cdn.maktaba.tv; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
      "Cache-Control": "public, max-age=300",
      "X-Content-Type-Options": "nosniff",
    },
  });
}

function word(m: Record<string, string> | undefined, lang: Lang): string {
  return m?.[lang] || m?.en || "";
}

function newest(vs: CatVersion[]): CatVersion | undefined {
  return [...vs].sort((a, b) => (a.released < b.released ? 1 : -1))[0];
}

function ratingLine(p: CatPlugin, lang: Lang): string {
  const w = W[lang];
  return p.rating_count >= 5 ? fill(w.rating, { avg: (p.rating_avg_x100 / 100).toFixed(1), n: p.rating_count }) : w.few;
}

/** GET / and /ar: every listed plugin. */
export async function listPage(env: Env, lang: Lang): Promise<Response> {
  const w = W[lang];
  const plugins = await catalog(env);
  const items = plugins
    .map(
      (p) =>
        `<li><a href="${lang === "ar" ? "/ar" : ""}/p/${esc(p.id)}"><strong>${esc(word(p.names, lang))}</strong></a> <span class="muted">${esc(word(p.summaries, lang))}</span><br><span class="muted">${esc(fill(w.by, { dev: p.developer }))} · ${esc(ratingLine(p, lang))}</span></li>`,
    )
    .join("");
  const body = `<h1>${esc(w.title)}</h1><p>${esc(w.lead)}</p>${plugins.length ? `<ul class="plugins">${items}</ul>` : `<p>${esc(w.none)}</p>`}`;
  return page(lang, w.title, body, lang === "ar" ? "/ar" : "/");
}

/** GET /p/<id> and /ar/p/<id>: one plugin. */
export async function pluginPage(env: Env, lang: Lang, id: string): Promise<Response> {
  const w = W[lang];
  const p = (await catalog(env)).find((x) => x.id === id);
  if (!p) return new Response("not found", { status: 404, headers: { "Content-Type": "text/plain; charset=utf-8" } });
  const v = newest(p.versions);
  const reviews = await env.DB_CATALOG.prepare("SELECT author, stars, text, reply FROM ratings WHERE plugin = ? AND hidden_at IS NULL ORDER BY updated DESC LIMIT 20")
    .bind(id)
    .all<{ author: string; stars: number; text: string; reply: string }>()
    .then((r) => r.results)
    .catch(() => []);
  const caps = (v?.capabilities ?? []).map((c) => `<li>${esc(capWords(c, lang))}</li>`).join("");
  const machine = lang === "ar" && p.machine_translated.includes("ar") ? ` <span class="badge">${esc(w.machine)}</span>` : "";
  const level = v?.review_level === "reviewed" ? w.reviewed : w.automated;
  const desc = word(p.extra?.descriptions, lang);
  const body = `<h1>${esc(word(p.names, lang))}</h1>
<p>${esc(word(p.summaries, lang))}${machine}</p>
${desc ? `<p style="white-space:pre-line">${esc(desc)}</p>` : ""}
<p class="muted">${esc(fill(w.by, { dev: p.developer }))} · <span class="badge">${esc(level)}</span> · ${esc(ratingLine(p, lang))}</p>
<p><a href="maktaba://plugins/${esc(p.id)}">${esc(w.open)}</a>${v ? ` · <a href="${esc(v.url)}" download>${esc(w.download)}</a> (${esc(v.version)})` : ""}</p>
<h2>${esc(w.capabilities)}</h2><ul>${caps}</ul>
<h2>${esc(w.versions)}</h2><ul>${p.versions.map((x) => `<li dir="ltr">${esc(x.version)} · ${esc(x.maktaba)}</li>`).join("")}</ul>
<h2>${esc(w.reviews)}</h2><ul>${reviews
    .map((r) => `<li>${"★".repeat(r.stars)}${"☆".repeat(5 - r.stars)} <bdi>${esc(r.author)}</bdi><p style="white-space:pre-line">${esc(r.text)}</p>${r.reply ? `<blockquote>${esc(r.reply)}</blockquote>` : ""}</li>`)
    .join("")}</ul>`;
  return page(lang, `${word(p.names, lang)} · ${w.title}`, body, `${lang === "ar" ? "/ar" : ""}/p/${id}`);
}
