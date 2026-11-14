// The watch pages' HTML (story 50.27 §1). Every page belongs to one publisher (or to Maktaba's
// own legal and pairing pages) and links only to that publisher's channels and guide: there is
// no directory, no search and no link to another publisher (README §14.6, T07).
//
// The video is shown as it is: the watermark and strip are burned in and nothing is overlaid
// but the state card. Colours are Maktaba's; the publisher brings only a name and a logo.
import type { Branding, Programme } from "../guide";
import type { ChannelStatus } from "../channel_live";
import { fill, t, type Lang } from "../strings";
import { viewerState } from "./states";

export const HLS_SRC = "https://cdn.jsdelivr.net/npm/hls.js@1.5.20/dist/hls.min.js";
export const TURNSTILE_SRC = "https://challenges.cloudflare.com/turnstile/v0/api.js";
/** Each language's own name, for the switch to it (the same in both pages). */
const LANG_NAME: Record<Lang, string> = { ar: "العربية", en: "English" };
export const LEGAL = ["copyright", "terms", "privacy"] as const;
export type LegalPage = (typeof LEGAL)[number];

export interface LineupChannel {
  id: string;
  name: string;
  number: number;
  state: ChannelStatus;
  now: Programme | null;
  next: Programme | null;
}

export interface Lineup {
  publisher: { id: string } & Branding;
  channels: LineupChannel[];
}

/** The words a channel page's script needs (by key), in the page's language. */
const CLIENT_KEYS = [
  "broadcast.tuning",
  "broadcast.homeOffline",
  "broadcast.off",
  "broadcast.ended",
  "broadcast.revoked",
  "broadcast.full",
  "broadcast.waiting",
  "broadcast.ipLimit",
  "broadcast.tooManyTries",
  "broadcast.dailyLimit",
  "broadcast.geoBlocked",
  "broadcast.invite.used",
  "broadcast.invite.expired",
  "broadcast.inviteRequired",
  "site.verify",
  "site.error",
];

export function esc(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

/** jsonScript is a value safe inside <script type="application/json">. */
function jsonScript(v: unknown): string {
  return JSON.stringify(v).replace(/</g, "\\u003c").replace(/>/g, "\\u003e");
}

function enc(s: string): string {
  return encodeURIComponent(s);
}

export interface PageOpts {
  lang: Lang;
  /** the path after /<lang> (for the language switch), starting with / */
  path: string;
  title: string;
  /** the publisher's header; none on Maktaba's own pages */
  brand?: { id: string } & Branding;
  body: string;
  scripts?: string[];
  status?: number;
  /** the live host, for the page's CSP */
  liveOrigin: string;
  indexable?: boolean;
}

export function csp(liveOrigin: string): string {
  return [
    "default-src 'none'",
    `script-src 'self' https://cdn.jsdelivr.net https://challenges.cloudflare.com`,
    "style-src 'self'",
    "img-src 'self' data:",
    `media-src 'self' blob: ${liveOrigin}`,
    `connect-src 'self' ${liveOrigin}`,
    "frame-src https://challenges.cloudflare.com",
    "form-action 'self'",
    "base-uri 'none'",
    "frame-ancestors 'none'",
  ].join("; ");
}

export function page(o: PageOpts): Response {
  const { lang } = o;
  const other: Lang = lang === "ar" ? "en" : "ar";
  const dir = lang === "ar" ? "rtl" : "ltr";
  const header = o.brand
    ? `<a class="brand" href="/${lang}/${enc(o.brand.id)}">${o.brand.logo ? `<img src="${esc(o.brand.logo)}" alt="">` : ""}<span>${esc(fill(t("guest.onMaktaba", lang), { publisher: o.brand.name }))}</span></a>`
    : `<span class="brand">Maktaba</span>`;
  const legal = LEGAL.map(
    (p) => `<a href="/${lang}/legal/${p}">${esc(t(`site.legal.${p}`, lang))}</a>`
  ).join("");
  const scripts = (o.scripts ?? []).map((s) => `<script src="${esc(s)}" defer></script>`).join("");
  const html = `<!doctype html><html lang="${lang}" dir="${dir}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">${o.indexable ? "" : '<meta name="robots" content="noindex">'}<title>${esc(o.title)}</title><link rel="stylesheet" href="/s/watch.css"></head><body><header>${header}<a href="/${other}${esc(o.path)}" lang="${other}" hreflang="${other}">${LANG_NAME[other]}</a></header><main id="main">${o.body}</main><footer><span>${esc(t("broadcast.free", lang))}</span>${legal}</footer>${scripts}</body></html>`;
  const headers: Record<string, string> = {
    "Content-Type": "text/html; charset=utf-8",
    "Content-Security-Policy": csp(o.liveOrigin),
    "Referrer-Policy": "no-referrer",
  };
  if (o.indexable) headers["X-Maktaba-Indexable"] = "1";
  return new Response(html, { status: o.status ?? 200, headers });
}

function time(iso: string): string {
  // the server writes UTC; the page's script shows the viewer's own zone
  return `<time datetime="${esc(iso)}">${esc(iso.slice(11, 16))}</time>`;
}

function programmeLine(p: Programme, lang: Lang): string {
  return `${time(p.start)} ${esc(p.title)}${p.attribution ? ` <span class="attr">${attributionHTML(p, lang)}</span>` : ""}`;
}

/** attributionHTML is a CC programme's credit (50.23): "{title} by {by}, {licence}" with its link. */
export function attributionHTML(p: Programme, lang: Lang): string {
  const a = p.attribution;
  if (!a) return "";
  const words = fill(t("site.attribution", lang), {
    title: a.title,
    by: a.by,
    licence: a.licence,
  });
  return `<a href="${esc(a.url)}" rel="nofollow noopener" target="_blank">${esc(words)}</a>`;
}

function stateLine(c: LineupChannel, lang: Lang): string {
  const v = viewerState(200, { channel_state: c.state });
  return v.key ? `<span class="state">${esc(t(v.key, lang))}</span>` : "";
}

/** gridPage is the publisher's channels: the ones the guest's pass or the open session allows. */
export function gridPage(lang: Lang, l: Lineup, liveOrigin: string): Response {
  const pub = enc(l.publisher.id);
  const items = l.channels
    .map((c) => {
      const now = c.now
        ? `<div>${esc(t("site.now", lang))}: ${esc(c.now.title)}</div>`
        : `<div class="muted">${esc(t("site.nothingScheduled", lang))}</div>`;
      const next = c.next
        ? `<div class="muted">${esc(t("site.next", lang))}: ${time(c.next.start)} ${esc(c.next.title)}</div>`
        : "";
      return `<li><a href="/${lang}/${pub}/${enc(c.id)}"><div><span class="num">${c.number}</span> ${esc(c.name)}</div>${stateLine(c, lang)}${now}${next}</a></li>`;
    })
    .join("");
  const body =
    l.channels.length > 0
      ? `<h1>${esc(t("site.channels", lang))}</h1><p><a href="/${lang}/${pub}/guide">${esc(t("site.guide", lang))}</a></p><ul class="grid">${items}</ul>`
      : `<h1>${esc(t("site.noChannels", lang))}</h1>`;
  return page({
    lang,
    path: `/${l.publisher.id}`,
    title: fill(t("guest.onMaktaba", lang), { publisher: l.publisher.name }),
    brand: l.publisher,
    body,
    scripts: ["/s/watch.js"],
    liveOrigin,
  });
}

export interface ChannelPageData {
  lineup: Lineup;
  channel: LineupChannel;
  today: Programme[];
  turnstileSiteKey?: string;
  indexable: boolean;
}

/** channelPage is the player, now and next, today's guide, the attribution and Report. */
export function channelPage(lang: Lang, d: ChannelPageData, liveOrigin: string): Response {
  const { lineup: l, channel: c } = d;
  const pub = enc(l.publisher.id);
  const cfg = {
    lang,
    publisher: l.publisher.id,
    channel: c.id,
    channels: l.channels.map((x) => ({ id: x.id, number: x.number, name: x.name })),
    turnstile: d.turnstileSiteKey ?? "",
    strings: Object.fromEntries(CLIENT_KEYS.map((k) => [k, t(k, lang)])),
  };
  const now = c.now
    ? `<p>${esc(t("site.now", lang))}: ${esc(c.now.title)}</p>${c.now.attribution ? `<p class="attr">${attributionHTML(c.now, lang)}</p>` : ""}`
    : "";
  const next = c.next
    ? `<p class="muted">${esc(t("site.next", lang))}: ${time(c.next.start)} ${esc(c.next.title)}</p>`
    : "";
  const today = d.today.length
    ? `<ol class="today">${d.today.map((p) => `<li>${programmeLine(p, lang)}</li>`).join("")}</ol>`
    : `<p class="muted">${esc(t("site.nothingScheduled", lang))}</p>`;
  const chans = l.channels
    .map(
      (x) =>
        `<li><a href="/${lang}/${pub}/${enc(x.id)}" data-ch="${esc(x.id)}" aria-current="${x.id === c.id}"><span class="num">${x.number}</span> ${esc(x.name)}</a></li>`
    )
    .join("");
  const body = `<section class="player" aria-label="${esc(t("site.player", lang))}"><div class="stage"><video id="video" playsinline controls></video><div id="card" class="card over" role="status" aria-live="polite"><p id="msg">${esc(t("broadcast.tuning", lang))}</p><button id="retry" type="button" hidden>${esc(t("guest.tryAgain", lang))}</button><div id="ts"></div></div></div><h1 id="title"><span class="num">${c.number}</span> ${esc(c.name)}</h1>${now}${next}<p><a href="/${lang}/legal/copyright">${esc(t("site.report", lang))}</a></p></section><section aria-labelledby="today-h"><h2 id="today-h">${esc(t("site.today", lang))}</h2>${today}</section><nav aria-labelledby="ch-h"><h2 id="ch-h">${esc(t("site.channels", lang))}</h2><ul class="channels">${chans}</ul><p><a href="/${lang}/${pub}/guide">${esc(t("site.guide", lang))}</a></p></nav><script type="application/json" id="cfg">${jsonScript(cfg)}</script>`;
  const scripts = [HLS_SRC];
  if (d.turnstileSiteKey) scripts.push(TURNSTILE_SRC);
  scripts.push("/s/watch.js");
  return page({
    lang,
    path: `/${l.publisher.id}/${c.id}`,
    title: `${c.name} · ${fill(t("guest.onMaktaba", lang), { publisher: l.publisher.name })}`,
    brand: l.publisher,
    body,
    scripts,
    liveOrigin,
    indexable: d.indexable,
  });
}

/** guidePage is the EPG for the publisher's channels: today's programmes per channel. */
export function guidePage(
  lang: Lang,
  l: Lineup,
  days: Record<string, Programme[]>,
  liveOrigin: string
): Response {
  const pub = enc(l.publisher.id);
  const rows = l.channels
    .map((c) => {
      const ps = days[c.id] ?? [];
      const list = ps.length
        ? `<ol class="today">${ps.map((p) => `<li>${programmeLine(p, lang)}</li>`).join("")}</ol>`
        : `<p class="muted">${esc(t("site.nothingScheduled", lang))}</p>`;
      return `<section><h2><a href="/${lang}/${pub}/${enc(c.id)}"><span class="num">${c.number}</span> ${esc(c.name)}</a></h2>${list}</section>`;
    })
    .join("");
  return page({
    lang,
    path: `/${l.publisher.id}/guide`,
    title: `${t("site.guide", lang)} · ${fill(t("guest.onMaktaba", lang), { publisher: l.publisher.name })}`,
    brand: l.publisher,
    body: `<h1>${esc(t("site.guide", lang))}</h1><p class="muted">${esc(t("site.today", lang))}</p>${rows || `<p>${esc(t("site.noChannels", lang))}</p>`}`,
    scripts: ["/s/watch.js"],
    liveOrigin,
  });
}

const FULL_TEXT: Record<LegalPage, string> = {
  copyright: "copyright",
  terms: "terms",
  privacy: "privacy",
};

/** legalPage is a short summary with a link to the full text on maktaba.tv (Epic 60.07). */
export function legalPage(lang: Lang, which: LegalPage, liveOrigin: string): Response {
  const full = `https://maktaba.tv/${lang === "ar" ? "ar/" : ""}legal/${FULL_TEXT[which]}/`;
  return page({
    lang,
    path: `/legal/${which}`,
    title: t(`site.legal.${which}`, lang),
    body: `<h1>${esc(t(`site.legal.${which}`, lang))}</h1><p>${esc(t(`site.legal.${which}Body`, lang))}</p><p><a href="${full}">${esc(t("site.legal.full", lang))}</a></p>`,
    liveOrigin,
  });
}

export function messagePage(
  lang: Lang,
  path: string,
  key: string,
  liveOrigin: string,
  status = 200
): Response {
  return page({
    lang,
    path,
    title: "Maktaba",
    body: `<h1>${esc(t(key, lang))}</h1>`,
    liveOrigin,
    status,
  });
}

/** pairPage is the phone's side of TV pairing (§2): paste an invite, or send an open session. */
export function pairPage(
  lang: Lang,
  nonce: string,
  pub: string,
  liveOrigin: string,
  result?: { key: string; status: number }
): Response {
  const q = `?tv=${enc(nonce)}${pub ? `&p=${enc(pub)}` : ""}`;
  let body: string;
  if (result) body = `<h1>${esc(t(result.key, lang))}</h1>`;
  else if (!nonce)
    body = `<h1>${esc(t("site.pair.title", lang))}</h1><p>${esc(t("site.pair.noTv", lang))}</p>`;
  else {
    const code = pub
      ? `<label for="code">${esc(t("guest.enterCode", lang))}</label><input id="code" name="code" autocomplete="off" inputmode="text" pattern="[Mm][Kk]-[A-Za-z0-9]{4}" placeholder="MK-XXXX">`
      : "";
    const open = pub
      ? `<button type="submit" name="open" value="1">${esc(t("site.pair.sendOpen", lang))}</button>`
      : "";
    body = `<h1>${esc(t("site.pair.title", lang))}</h1><form method="post" action="/${lang}/pair${esc(q)}"><label for="invite">${esc(t("site.pair.invite", lang))}</label><input id="invite" name="invite" autocomplete="off" inputmode="url">${code}<button type="submit">${esc(t("site.pair.send", lang))}</button>${open}</form>`;
  }
  return page({
    lang,
    path: `/pair${q}`,
    title: t("site.pair.title", lang),
    body,
    liveOrigin,
    status: result?.status ?? 200,
  });
}
