// The status page's HTML (Epic 50 story 50.11 §3): written whole by the build, so it reads
// without JavaScript; the script only picks the language on a first visit and adds local times.
import strings from "./strings.json" with { type: "json" };

export const LANGS = ["en", "ar"];

export function t(lang, key, vars = {}) {
  const s = strings[lang]?.[key];
  if (s === undefined) throw new Error(`no ${lang} string ${key}`);
  return s.replace(/\{(\w+)\}/g, (_, k) => {
    if (!(k in vars)) throw new Error(`${key}: no value for {${k}}`);
    return vars[k];
  });
}

export function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

/** 0.997 as "99.70%"; null as the no-data words. */
export function pct(lang, v) {
  if (v === null || v === undefined) return t(lang, "status.noData");
  const p = Math.floor(v * 10000) / 100; // never rounded up into the target
  return `${p.toFixed(2)}%`;
}

function when(iso) {
  return `<time datetime="${esc(iso)}">${esc(`${iso.slice(0, 10)} ${iso.slice(11, 16)} UTC`)}</time>`;
}

function prefix(lang) {
  return lang === "en" ? "/" : `/${lang}/`;
}

/** A small, safe subset of Markdown: paragraphs, "- " lists, "### " headings, https links. */
export function md(text) {
  const inline = (s) => esc(s).replace(/\[([^\]]+)\]\((https:\/\/[^\s)]+)\)/g, (_, a, u) => `<a href="${u}">${a}</a>`);
  const out = [];
  for (const block of text.split(/\n\s*\n/)) {
    const lines = block.split("\n").filter((l) => l.trim());
    if (!lines.length) continue;
    if (lines.every((l) => /^\s*- /.test(l))) {
      out.push(`<ul>${lines.map((l) => `<li>${inline(l.replace(/^\s*- /, ""))}</li>`).join("")}</ul>`);
    } else if (/^### /.test(lines[0])) {
      out.push(`<h3>${inline(lines[0].slice(4))}</h3>`);
      if (lines.length > 1) out.push(`<p>${inline(lines.slice(1).join(" "))}</p>`);
    } else {
      out.push(`<p>${inline(lines.join(" "))}</p>`);
    }
  }
  return out.join("\n");
}

const CSS = `
:root{--bg:#fafaf8;--fg:#1d1d1b;--muted:#5d5d58;--card:#fff;--line:#e3e2dc;--ok:#2e8540;--minor:#c98a00;--major:#c4352b;--maint:#2f6fbf;--none:#d6d5cf}
@media (prefers-color-scheme:dark){:root{--bg:#141413;--fg:#ecebe6;--muted:#a3a29b;--card:#1d1d1b;--line:#33332f;--none:#3a3a36}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Tahoma,sans-serif}
main{max-width:860px;margin:0 auto;padding:24px 16px 48px}header{display:flex;justify-content:space-between;align-items:baseline;gap:12px;flex-wrap:wrap}
h1{font-size:1.4rem;margin:0}a{color:inherit}.lang{font-size:.95rem}
.headline{margin:20px 0;padding:16px;border-radius:10px;color:#fff;font-weight:600;font-size:1.1rem}
.s-operational{background:var(--ok)}.s-degraded{background:var(--minor)}.s-outage{background:var(--major)}.s-maintenance{background:var(--maint)}.s-none{background:var(--none);color:var(--fg)}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:16px;margin:12px 0}
.row{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap}.state{font-weight:600}
.t-operational{color:var(--ok)}.t-degraded{color:var(--minor)}.t-outage{color:var(--major)}.t-maintenance{color:var(--maint)}.t-none{color:var(--muted)}
.bars{display:flex;gap:2px;margin:10px 0 4px}.bars span{flex:1;height:28px;border-radius:2px;background:var(--none)}
.bars .ok{background:var(--ok)}.bars .minor{background:var(--minor)}.bars .major{background:var(--major)}
.axis,.muted{color:var(--muted);font-size:.85rem}.axis{display:flex;justify-content:space-between;flex-wrap:wrap;gap:0 8px}
h2{font-size:1.1rem;margin:28px 0 8px}h3{font-size:1rem;margin:12px 0 4px}.post h3{margin-top:0}
.policy{font-size:.92rem}time{white-space:nowrap}
@media (max-width:520px){.bars{gap:1px}.bars span{height:22px}}
`;

const SCRIPT = `(function(){try{var p=location.pathname,ar=p.indexOf("/ar/")===0;
var k="maktaba-status-lang",c=localStorage.getItem(k);
if(!c&&!ar&&p==="/"){var l=(navigator.languages&&navigator.languages[0])||navigator.language||"";if(/^ar/i.test(l)){location.replace("/ar/");return}}
document.querySelectorAll("a[data-lang]").forEach(function(a){a.addEventListener("click",function(){try{localStorage.setItem(k,a.getAttribute("data-lang"))}catch(e){}})});
}catch(e){}
document.querySelectorAll("time[datetime]").forEach(function(el){var d=new Date(el.getAttribute("datetime"));if(isNaN(d))return;
var s=document.createElement("span");s.className="muted";s.textContent=" ("+d.toLocaleString(document.documentElement.lang,{dateStyle:"medium",timeStyle:"short"})+")";el.after(s)})})();`;

function shell(lang, title, bodyHTML, otherHref) {
  const other = lang === "en" ? "ar" : "en";
  return `<!doctype html>
<html lang="${lang}" dir="${lang === "ar" ? "rtl" : "ltr"}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<link rel="alternate" hreflang="${other}" href="${esc(otherHref)}">
<style>${CSS}</style>
</head>
<body>
<main>
<header><h1><a href="${prefix(lang)}">${esc(t(lang, "status.title"))}</a></h1>
<a class="lang" data-lang="${other}" href="${esc(otherHref)}" lang="${other}">${esc(t(lang, "status.language"))}</a></header>
${bodyHTML}
</main>
<script>${SCRIPT}</script>
</body>
</html>
`;
}

function cellClass(u) {
  if (u === null) return "";
  if (u >= 0.999) return "ok";
  if (u >= 0.99) return "minor";
  return "major";
}

function stateWords(lang, s) {
  return s ? t(lang, `status.state.${s}`) : t(lang, "status.noDataYet");
}

function componentCard(lang, name, c) {
  const cells = c.days.map((d) => {
    const label = t(lang, "status.day", { date: d.date, percent: pct(lang, d.uptime) });
    return `<span class="${cellClass(d.uptime)}" title="${esc(label)}"></span>`;
  }).join("");
  return `<section class="card">
<div class="row"><strong>${esc(t(lang, `status.component.${name}`))}</strong><span class="state t-${c.state || "none"}">${esc(stateWords(lang, c.state))}</span></div>
<div class="bars" role="img" aria-label="${esc(t(lang, "status.uptime90", { percent: pct(lang, c.uptime90) }))}">${cells}</div>
<div class="axis"><span>${esc(t(lang, "status.daysAgo90"))}</span><span>${esc(t(lang, "status.uptime90", { percent: pct(lang, c.uptime90) }))}</span><span>${esc(t(lang, "status.today"))}</span></div>
</section>`;
}

function postSummary(lang, p) {
  const comps = p.components.map((c) => t(lang, `status.component.${c}`)).join(lang === "ar" ? "، " : ", ");
  const time = p.kind === "maintenance"
    ? t(lang, "status.window", { start: when(p.starts), end: when(p.ends) })
    : t(lang, "status.started", { time: when(p.started) }) + (p.resolved ? ` · ${t(lang, "status.resolvedAt", { time: when(p.resolved) })}` : "");
  return `<article class="card post">
<h3><a href="${prefix(lang)}i/${esc(p.id)}/">${esc(p.title[lang])}</a></h3>
<div class="muted">${esc(t(lang, `status.incident.${p.state}`))} · ${esc(t(lang, "status.affects", { components: comps }))}</div>
<div class="muted">${time}</div>
</article>`;
}

/** The front page in one language. */
export function page(lang, m) {
  const headState = m.overall;
  const anyMeasured = Object.values(m.components).some((c) => c.state);
  const head = anyMeasured ? t(lang, `status.overall.${headState}`) : t(lang, "status.noDataYet");
  const parts = [];
  parts.push(`<div class="headline s-${anyMeasured ? headState : "none"}">${esc(head)}</div>`);
  parts.push(`<p class="muted">${t(lang, "status.updated", { time: when(m.updated_at ?? m.now) })}</p>`);
  if (m.open.length) {
    parts.push(`<h2>${esc(t(lang, "status.currentIncidents"))}</h2>`);
    parts.push(...m.open.map((p) => postSummary(lang, p)));
  }
  if (m.maintenance.length) {
    parts.push(`<h2>${esc(t(lang, "status.maintenance"))}</h2>`);
    parts.push(...m.maintenance.map((p) => postSummary(lang, p)));
  }
  for (const [name, c] of Object.entries(m.components)) parts.push(componentCard(lang, name, c));
  const payURL = m.config.payments_status_url;
  parts.push(`<section class="card"><div class="row"><strong>${esc(t(lang, "status.component.payments"))}</strong></div>
<p class="muted">${payURL ? `<a href="${esc(payURL)}">${esc(t(lang, "status.payments.link"))}</a>` : esc(t(lang, "status.payments.soon"))}</p></section>`);
  const target = `${Number(m.target).toFixed(1)}%`;
  parts.push(`<section class="policy"><p><strong>${esc(t(lang, "status.component.relay"))}.</strong> ${esc(t(lang, "status.target", { target, percent: pct(lang, m.lastMonth) }))}</p>
<p class="muted" data-key="uptime.policy">${esc(t(lang, "uptime.policy"))}</p></section>`);
  parts.push(`<h2>${esc(t(lang, "status.pastIncidents"))}</h2>`);
  if (m.past.length) parts.push(...m.past.map((p) => postSummary(lang, p)));
  else parts.push(`<p class="muted">${esc(t(lang, "status.noIncidents"))}</p>`);
  const other = lang === "en" ? "/ar/" : "/";
  return shell(lang, t(lang, "status.title"), parts.join("\n"), other);
}

/** One incident's or maintenance notice's page in one language. */
export function postPage(lang, p) {
  const other = lang === "en" ? `/ar/i/${p.id}/` : `/i/${p.id}/`;
  const body = `${postSummary(lang, p).replace("<h3>", "<h2>").replace("</h3>", "</h2>")}
<section class="card">${md(p.body[lang])}</section>
<p><a href="${prefix(lang)}">${esc(t(lang, "status.back"))}</a></p>`;
  return shell(lang, `${p.title[lang]} · ${t(lang, "status.title")}`, body, other);
}
