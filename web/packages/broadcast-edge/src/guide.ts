// The channel guide a home publishes (story 50.22 §6, read by the watch pages in 50.27):
//
//   POST /v1/ingest/<channel>/guide   (ingest token for the channel, X-Maktaba-Time)
//
// One document per channel, schema 1, replaced whole on every post:
//
//   { "schema": 1,
//     "publisher": { "name": "…", "logo": "data:image/png;base64,…" },   the branding (50.27 §1)
//     "channel":   { "name": "…", "number": 7 },
//     "programmes": [ { "start": ISO, "end": ISO, "title": "…",
//                       "attribution": { "title", "by", "licence", "url" } } ] }   CC BY (50.23)
//
// The edge stores what the home sent and serves it to viewers the channel's access lets in. It
// never edits, orders or adds to it (README §14.6): the programmes are only checked for shape,
// sorted by start and cut to the window viewers can see.
import type { Env } from "./env";

export const GUIDE_SCHEMA = 1;
/** The largest guide post the edge takes (a week of short programmes and a small logo). */
export const MAX_GUIDE_BYTES = 256 * 1024;
export const MAX_LOGO_CHARS = 64 * 1024;
export const MAX_PROGRAMMES = 2000;
/** Programmes that ended more than this long ago, or start further ahead, are dropped. */
export const GUIDE_PAST_MS = 24 * 3600 * 1000;
export const GUIDE_AHEAD_MS = 8 * 24 * 3600 * 1000;

const LOGO_RE = /^data:image\/(png|webp|jpeg);base64,[A-Za-z0-9+/]+=*$/;
const MAX_TEXT = 200;

export interface Attribution {
  title: string;
  by: string;
  licence: string;
  url: string;
}

export interface Programme {
  start: string;
  end: string;
  title: string;
  attribution?: Attribution;
}

export interface Branding {
  name: string;
  logo?: string;
}

export interface GuideDoc {
  schema: 1;
  publisher: Branding;
  channel: { name: string; number: number };
  programmes: Programme[];
}

function text(v: unknown, max = MAX_TEXT): string | null {
  if (typeof v !== "string") return null;
  // control characters never reach a page
  const s = v.replace(/[\u0000-\u001f\u007f]/g, " ").trim();
  return s.length > 0 && s.length <= max ? s : null;
}

function httpsURL(v: unknown): string | null {
  const s = text(v, 500);
  if (!s) return null;
  try {
    const u = new URL(s);
    return u.protocol === "https:" ? u.toString() : null;
  } catch {
    return null;
  }
}

function attribution(v: unknown): Attribution | undefined | null {
  if (v === undefined || v === null) return undefined;
  if (typeof v !== "object") return null;
  const a = v as Record<string, unknown>;
  const title = text(a.title);
  const by = text(a.by);
  const licence = text(a.licence, 40);
  const url = httpsURL(a.url);
  if (!title || !by || !licence || !url) return null;
  return { title, by, licence, url };
}

/**
 * validGuide checks a guide post and returns it cleaned (programmes sorted, cut to the window),
 * or the reason it was refused.
 */
export function validGuide(v: unknown, nowMs: number): GuideDoc | string {
  if (typeof v !== "object" || v === null) return "bad_guide";
  const g = v as Record<string, unknown>;
  if (g.schema !== GUIDE_SCHEMA) return "bad_schema";
  const p = g.publisher as Record<string, unknown> | undefined;
  const c = g.channel as Record<string, unknown> | undefined;
  if (!p || typeof p !== "object" || !c || typeof c !== "object") return "bad_guide";
  const pubName = text(p.name, 80);
  const chName = text(c.name, 80);
  const number = c.number;
  if (!pubName || !chName) return "bad_name";
  if (typeof number !== "number" || !Number.isInteger(number) || number < 0 || number > 9999)
    return "bad_number";
  let logo: string | undefined;
  if (p.logo !== undefined) {
    if (typeof p.logo !== "string" || p.logo.length > MAX_LOGO_CHARS || !LOGO_RE.test(p.logo))
      return "bad_logo";
    logo = p.logo;
  }
  if (!Array.isArray(g.programmes) || g.programmes.length > MAX_PROGRAMMES) return "bad_programmes";
  const out: Programme[] = [];
  for (const raw of g.programmes as unknown[]) {
    if (typeof raw !== "object" || raw === null) return "bad_programme";
    const r = raw as Record<string, unknown>;
    const title = text(r.title);
    const start = typeof r.start === "string" ? Date.parse(r.start) : NaN;
    const end = typeof r.end === "string" ? Date.parse(r.end) : NaN;
    const attr = attribution(r.attribution);
    if (!title || !Number.isFinite(start) || !Number.isFinite(end) || end <= start || attr === null)
      return "bad_programme";
    if (end < nowMs - GUIDE_PAST_MS || start > nowMs + GUIDE_AHEAD_MS) continue;
    out.push({
      start: new Date(start).toISOString(),
      end: new Date(end).toISOString(),
      title,
      ...(attr ? { attribution: attr } : {}),
    });
  }
  out.sort((a, b) => a.start.localeCompare(b.start));
  return {
    schema: 1,
    publisher: logo ? { name: pubName, logo } : { name: pubName },
    channel: { name: chName, number },
    programmes: out,
  };
}

function bucket(env: Env): R2Bucket {
  return env.GUIDE ?? env.SEGMENTS;
}

export function guideKey(env: Env, pub: string, ch: string): string {
  return `${env.BROADCAST_ENV}/guide/${pub}/${ch}.json`;
}

function brandKey(env: Env, pub: string): string {
  return `${env.BROADCAST_ENV}/guide/${pub}/_publisher.json`;
}

/** putGuide stores a channel's guide and the publisher's branding it carries. */
export async function putGuide(env: Env, pub: string, ch: string, doc: GuideDoc): Promise<void> {
  const meta = { httpMetadata: { contentType: "application/json" } };
  await bucket(env).put(guideKey(env, pub, ch), JSON.stringify(doc), meta);
  await bucket(env).put(brandKey(env, pub), JSON.stringify(doc.publisher), meta);
}

export async function getGuide(env: Env, pub: string, ch: string): Promise<GuideDoc | null> {
  const o = await bucket(env).get(guideKey(env, pub, ch));
  if (!o) return null;
  try {
    return (await o.json()) as GuideDoc;
  } catch {
    return null;
  }
}

export async function getBranding(env: Env, pub: string): Promise<Branding | null> {
  const o = await bucket(env).get(brandKey(env, pub));
  if (!o) return null;
  try {
    return (await o.json()) as Branding;
  } catch {
    return null;
  }
}

/** nowNext is the programme on air at `at` and the one after it. */
export function nowNext(
  programmes: Programme[],
  at: number
): { now: Programme | null; next: Programme | null } {
  let current: Programme | null = null;
  for (const p of programmes) {
    const s = Date.parse(p.start);
    const e = Date.parse(p.end);
    if (s <= at && at < e) current = p;
    else if (s > at) return { now: current, next: p };
  }
  return { now: current, next: null };
}

/** today is the programmes that overlap the 24 h from `at`'s start of day (UTC). */
export function today(programmes: Programme[], at: number): Programme[] {
  const d = new Date(at);
  const from = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
  const to = from + 24 * 3600 * 1000;
  return programmes.filter((p) => Date.parse(p.end) > from && Date.parse(p.start) < to);
}
