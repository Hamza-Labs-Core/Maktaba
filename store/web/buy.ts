// Buying a plugin (story 54.19 §5). A linked home asks for a purchase intent with its own
// signature when its admin clicks Buy (POST /v1/homes/{id}/purchase-intents); the admin's
// browser posts the intent to /buy/<plugin>, where the buyer signs in to the store account
// the home is linked to, sees the price in a currency an enabled provider sells to their
// country, and is sent to that provider's hosted checkout. The provider's webhook records the
// purchase (payments_webhook.ts) and the home picks up its entitlement.
//
// The store's own plugin page has Buy too: GET /buy/<plugin>/home asks the signed-in buyer
// which of the account's linked homes it is for, and its POST makes the same intent the home
// would have asked for, then continues on the same page as the home's Buy.
//
// The pages run no script: plain forms and links, every text escaped, a CSP that allows
// nothing but inline style and forms to this origin (and the provider's checkout, which the
// pay step redirects to). The home's address is never sent here.

import type { DB } from "../commerce/db";
import { SaleRefused, startPurchase } from "../commerce/sales";
import type { Currency } from "../payments/provider";
import { type Account, context, limit } from "./auth";
import { sha256hex, token } from "./crypto";
import { type Env, now } from "./env";
import { cookie, json, refuse } from "./http";
import { signedHome } from "./homes";
import { esc } from "./listing";
import { commerceDB, registryFor } from "./payments";

type Lang = "en" | "ar";
type Kind = "one_time" | "yearly";

/** How long an intent lasts (the admin clicks Buy and pays within it). */
export const INTENT_MS = 10 * 60_000;
/** The intent's cookie between the home's post and the pay step (same site only). */
export const BUY_COOKIE = "__Host-mks_buy";
const PLUGIN = "[a-z0-9][a-z0-9.-]{0,127}";
const TOKEN = /^[A-Za-z0-9_-]{43}$/;

const WORDS = {
  en: {
    title: "Buy {name}",
    forHome: "For your home “{home}”.",
    oneTime: "{price}, one time",
    yearly: "{price} a year",
    pay: "Continue to payment",
    note: "You pay on the payment provider's page. Your Maktaba picks up the purchase by itself within a few minutes.",
    expired: "This purchase link has expired or was already used. Start again with Buy in your Maktaba.",
    otherAccount: "This home is linked to another store account. Sign out, then sign in with that account to buy for it.",
    notSoldHere: "This plugin can't be bought from your country yet.",
    noProvider: "This plugin can't be bought right now: no payment method is available for it.",
    notForSale: "This plugin isn't for sale.",
    doneTitle: "Thank you",
    done: "Your purchase is being confirmed. Your Maktaba checks for it every 15 seconds and starts the plugin by itself; you can close this page.",
    plugin: "The plugin's page",
    whichHome: "Which home is it for?",
    next: "Continue",
    noHome: "Link one of your homes to this store account first: in your Maktaba, open Settings → Plugins → Store account and choose Link.",
  },
  ar: {
    title: "شراء {name}",
    forHome: "لجهازك الرئيسي «{home}».",
    oneTime: "{price}، مرة واحدة",
    yearly: "{price} سنويًا",
    pay: "متابعة إلى الدفع",
    note: "تدفع في صفحة مزوّد الدفع. ويلتقط جهازك الرئيسي الشراء وحده خلال دقائق.",
    expired: "انتهت صلاحية رابط الشراء هذا أو استُخدم من قبل. ابدأ من جديد بزر «اشترِ» في مكتبة.",
    otherAccount: "هذا الجهاز الرئيسي مرتبط بحساب متجر آخر. سجّل الخروج، ثم سجّل الدخول بذلك الحساب لتشتري له.",
    notSoldHere: "لا يمكن شراء هذه الإضافة من بلدك بعد.",
    noProvider: "لا يمكن شراء هذه الإضافة الآن: لا تتوفر لها طريقة دفع.",
    notForSale: "هذه الإضافة ليست للبيع.",
    doneTitle: "شكرًا لك",
    done: "يجري تأكيد شرائك. يتحقق جهازك الرئيسي منه كل 15 ثانية ويشغّل الإضافة وحده؛ يمكنك إغلاق هذه الصفحة.",
    plugin: "صفحة الإضافة",
    whichHome: "لأي جهاز رئيسي هي؟",
    next: "متابعة",
    noHome: "اربط أحد أجهزتك الرئيسية بحساب المتجر هذا أولًا: في مكتبة، افتح الإعدادات ← الإضافات ← حساب المتجر واختر «ربط».",
  },
} as const;

type Words = { -readonly [K in keyof (typeof WORDS)["en"]]: string };
const words = (lang: Lang): Words => WORDS[lang];
const fill = (s: string, v: Record<string, string>) => s.replace(/\{(\w+)\}/g, (_, k: string) => v[k] ?? "");

/** A buy page: no script, nothing loaded from elsewhere, never cached or indexed. */
function page(lang: Lang, title: string, body: string, status = 200, extra: HeadersInit = {}): Response {
  const html = `<!doctype html>
<html lang="${lang}" dir="${lang === "ar" ? "rtl" : "ltr"}">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem}.price{font-size:1.5rem;font-weight:600}.muted{color:#555}button{font:inherit;padding:.6rem 1.2rem}</style>
</head>
<body><main><h1>${esc(title)}</h1>${body}</main></body></html>`;
  const h = new Headers(extra);
  h.set("Content-Type", "text/html; charset=utf-8");
  h.set("Cache-Control", "no-store");
  h.set("X-Robots-Tag", "noindex, nofollow");
  h.set("X-Content-Type-Options", "nosniff");
  h.set("Referrer-Policy", "no-referrer");
  h.set(
    "Content-Security-Policy",
    "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self' https:",
  );
  return new Response(html, { status, headers: h });
}

const message = (lang: Lang, title: string, text: string, status = 200) => page(lang, title, `<p>${esc(text)}</p>`, status);

interface Intent {
  id_hash: string;
  home_id: string;
  plugin_id: string;
  product: Kind;
  lang: Lang;
  expires_at: string;
  used_at: string | null;
}

async function intentOf(db: DB, raw: string | undefined | null, plugin: string): Promise<Intent | null> {
  if (!raw || !TOKEN.test(raw)) return null;
  const row = await db
    .prepare("SELECT * FROM purchase_intents WHERE id_hash = ?")
    .bind(await sha256hex(raw))
    .first<Intent>();
  if (!row || row.used_at || row.plugin_id !== plugin || Date.parse(row.expires_at) <= now().getTime()) return null;
  return row;
}

/** The product a plugin sells: one-time first, else yearly; null when it sells none. */
async function productOf(db: DB, plugin: string, want?: string): Promise<Kind | null> {
  const { results } = await db.prepare("SELECT kind FROM products WHERE plugin_id = ? AND active = 1").bind(plugin).all<{ kind: Kind }>();
  const kinds = results.map((r) => r.kind);
  if (want) return kinds.includes(want as Kind) ? (want as Kind) : null;
  return kinds.includes("one_time") ? "one_time" : kinds.includes("yearly") ? "yearly" : null;
}

/** POST /v1/homes/{id}/purchase-intents: a linked home's one-time intent for its admin's Buy. */
export async function purchaseIntent(env: Env, req: Request, id: string): Promise<Response> {
  const raw = new Uint8Array(await req.arrayBuffer());
  if (raw.length > 4096) refuse(413, "body_too_large");
  const home = await signedHome(env, req, raw);
  if (!home || home.id !== id) refuse(401, "home_signature_invalid");
  const db = commerceDB(env);
  if (!db) refuse(503, "payments_unavailable");
  let b: { plugin_id?: unknown; product?: unknown; lang?: unknown };
  try {
    b = JSON.parse(new TextDecoder().decode(raw)) as typeof b;
  } catch {
    return refuse(400, "body_not_json");
  }
  const plugin =
    typeof b.plugin_id === "string" && new RegExp(`^${PLUGIN}$`).test(b.plugin_id)
      ? b.plugin_id
      : refuse(400, "field_invalid", { field: "plugin_id" });
  const product = await productOf(db!, plugin, typeof b.product === "string" ? b.product : undefined);
  if (!product) refuse(404, "not_for_sale");
  const lang: Lang = b.lang === "ar" ? "ar" : "en";
  const { t, expires } = await newIntent(db!, home!.id, plugin, product!, lang);
  return json({ intent: t, buy_url: `${env.STORE_ORIGIN}/buy/${plugin}`, product, expires_at: expires.toISOString() });
}

/** A new intent for a home (its token, kept only as a sha256), used once within INTENT_MS. */
async function newIntent(db: DB, homeId: string, plugin: string, product: Kind, lang: Lang): Promise<{ t: string; expires: Date }> {
  const t = token();
  const at = now();
  const expires = new Date(at.getTime() + INTENT_MS);
  await db
    .prepare(
      `INSERT INTO purchase_intents (id_hash, home_id, plugin_id, product, lang, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(await sha256hex(t), homeId, plugin, product, lang, at.toISOString(), expires.toISOString())
    .run();
  return { t, expires };
}

/** The intent's cookie and the way on to the buy page, as after the home's own post. */
function toBuyPage(plugin: string, t: string): Response {
  return new Response(null, {
    status: 303,
    headers: {
      Location: `/buy/${plugin}`,
      "Set-Cookie": `${BUY_COOKIE}=${t}; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=${INTENT_MS / 1000}`,
      "Cache-Control": "no-store",
      "Referrer-Policy": "no-referrer",
    },
  });
}

/**
 * Buy from the store's own plugin page: GET /buy/<plugin>/home lists the signed-in account's
 * linked homes to choose from; POST makes that home's intent and goes on to the buy page.
 */
async function chooseHome(env: Env, req: Request, db: DB, plugin: string, lang: Lang): Promise<Response> {
  const w = words(lang);
  const title = fill(w.title, { name: await pluginName(env, plugin, lang) });
  const product = await productOf(db, plugin);
  if (!product) return message(lang, title, w.notForSale, 404);
  const here = `/buy/${plugin}/home${lang === "ar" ? "?lang=ar" : ""}`;
  const ctx = await context(env, req);
  if (!ctx.account) {
    if (req.method !== "GET") return message(lang, title, w.expired, 401);
    return new Response(null, { status: 303, headers: { Location: `/signin?next=${encodeURIComponent(here)}`, "Cache-Control": "no-store" } });
  }
  const { results: homes } = await env.DB_INSTALLS.prepare(
    "SELECT id, name FROM homes WHERE account = ? AND unlinked_at IS NULL ORDER BY linked_at",
  )
    .bind(ctx.account.id)
    .all<{ id: string; name: string }>();
  if (req.method === "POST") {
    // from the form below only (the session cookie is SameSite=Lax; this is the CSRF check)
    if (req.headers.get("Origin") !== env.STORE_ORIGIN) return message(lang, title, w.expired, 403);
    await limit(ctx, "buy_intent", 30, 3600_000);
    const homeId = (await form(req)).get("home");
    // only a home linked to this account now: another account's, or an unlinked one, is refused
    if (!homeId || !homes.some((h) => h.id === homeId)) return message(lang, title, w.expired, 400);
    const { t } = await newIntent(db, homeId, plugin, product, lang);
    return toBuyPage(plugin, t);
  }
  if (req.method !== "GET") return message(lang, title, w.expired, 405);
  if (homes.length === 0) return message(lang, title, w.noHome);
  const options = homes
    .map(
      (h, i) =>
        `<p><label><input type="radio" name="home" value="${esc(h.id)}"${i === 0 ? " checked" : ""} required> <bdi>${esc(h.name || h.id)}</bdi></label></p>`,
    )
    .join("");
  return page(
    lang,
    title,
    `<form method="post" action="${esc(here)}"><fieldset><legend>${esc(w.whichHome)}</legend>${options}</fieldset><p><button type="submit">${esc(w.next)}</button></p></form>`,
  );
}

/** The currency of a buyer's country, then the store's usual ones. */
const LOCAL: Record<string, Currency> = { SA: "SAR", AE: "AED", EG: "EGP", GB: "GBP" };
const EURO = new Set("AT BE CY DE EE ES FI FR GR HR IE IT LT LU LV MT NL PT SI SK".split(" "));
function currencyOrder(country: string): string[] {
  const local = LOCAL[country] ?? (EURO.has(country) ? "EUR" : "USD");
  return [...new Set([local, "USD", "EUR", "GBP", "SAR", "AED", "EGP"])];
}

/** A price in its currency's usual form, in the page's language. */
export function formatPrice(minor: number, currency: string, lang: Lang): string {
  const f = new Intl.NumberFormat(lang === "ar" ? "ar-EG" : "en-US", { style: "currency", currency });
  const digits = f.resolvedOptions().maximumFractionDigits ?? 2;
  return f.format(minor / 10 ** digits);
}

interface Offer {
  currency: string;
  minor: number;
}

/** The price this buyer can pay, or why none: the first currency an enabled provider sells to them. */
async function offerFor(
  env: Env,
  db: DB,
  plugin: string,
  kind: Kind,
  country: string,
): Promise<Offer | "not_for_sale" | "no_provider_for_region" | "no_provider"> {
  const prod = await db
    .prepare("SELECT developer_id FROM products WHERE plugin_id = ? AND kind = ? AND active = 1")
    .bind(plugin, kind)
    .first<{ developer_id: string }>();
  if (!prod) return "not_for_sale";
  const { results: prices } = await db
    .prepare("SELECT currency, amount_minor FROM prices WHERE plugin_id = ? AND kind = ?")
    .bind(plugin, kind)
    .all<{ currency: string; amount_minor: number }>();
  const { results: accts } = await db
    .prepare("SELECT provider_id FROM provider_accounts WHERE developer_id = ? AND status = 'active'")
    .bind(prod.developer_id)
    .all<{ provider_id: string }>();
  const registry = await registryFor(env, db);
  let region = false;
  for (const currency of currencyOrder(country)) {
    const p = prices.find((x) => x.currency === currency);
    if (!p) continue;
    const c = registry.choose({
      country,
      currency: currency as Currency,
      subscription: kind === "yearly",
      developerProviders: accts.map((a) => a.provider_id),
    });
    if (typeof c !== "string") return { currency, minor: p.amount_minor };
    if (c !== "no_provider_for_region") region = true;
  }
  return region || prices.length === 0 ? "no_provider" : "no_provider_for_region";
}

async function pluginName(env: Env, plugin: string, lang: Lang): Promise<string> {
  const r = await env.DB_CATALOG.prepare("SELECT names FROM plugins WHERE id = ?").bind(plugin).first<{ names: string }>();
  try {
    const names = r ? (JSON.parse(r.names) as Record<string, string>) : {};
    return names[lang] || names.en || plugin;
  } catch {
    return plugin;
  }
}

/** The home's name and whether this account is the one it is linked to now. */
async function homeFor(env: Env, homeId: string, account: Account): Promise<{ name: string; mine: boolean } | null> {
  const h = await env.DB_INSTALLS.prepare("SELECT name, account FROM homes WHERE id = ? AND unlinked_at IS NULL")
    .bind(homeId)
    .first<{ name: string; account: string }>();
  return h ? { name: h.name, mine: h.account === account.id } : null;
}

const country = (req: Request) => {
  const c = (req as Request & { cf?: { country?: string } }).cf?.country ?? req.headers.get("CF-IPCountry") ?? "";
  return /^[A-Z]{2}$/.test(c) ? c : "";
};

async function form(req: Request): Promise<URLSearchParams> {
  const len = Number(req.headers.get("Content-Length") ?? "0");
  if (len > 4096) return new URLSearchParams();
  const bytes = new Uint8Array(await req.arrayBuffer());
  return bytes.length > 4096 ? new URLSearchParams() : new URLSearchParams(new TextDecoder().decode(bytes));
}

const clearBuyCookie = `${BUY_COOKIE}=; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=0`;

/**
 * The buy pages, or null for any other path: POST /buy/<plugin> (the home's intent), GET
 * /buy/<plugin> (sign in, the price), POST /buy/<plugin>/pay (to the provider's checkout) and
 * GET /buy/<plugin>/done (the provider sends the buyer back here).
 */
export async function buyPage(env: Env, req: Request): Promise<Response | null> {
  const url = new URL(req.url);
  const m = new RegExp(`^/buy/(${PLUGIN})(/pay|/done|/home)?/?$`).exec(url.pathname);
  if (!m) return null;
  const plugin = m[1]!;
  const step = m[2] ?? "";
  if (step === "/done" && req.method === "GET") {
    const lang: Lang = url.searchParams.get("lang") === "ar" ? "ar" : "en";
    const w = words(lang);
    return page(
      lang,
      w.doneTitle,
      `<p>${esc(w.done)}</p><p><a href="${lang === "ar" ? "/ar" : ""}/p/${esc(plugin)}">${esc(w.plugin)}</a></p>`,
    );
  }
  const db = commerceDB(env);
  if (!db) return message("en", WORDS.en.title.replace("{name}", plugin), WORDS.en.noProvider, 503);
  if (step === "/home") return chooseHome(env, req, db, plugin, url.searchParams.get("lang") === "ar" ? "ar" : "en");

  // the home's post: keep the intent in a same-site cookie, then the page itself (a GET)
  if (step === "" && req.method === "POST") {
    const t = (await form(req)).get("intent");
    const intent = await intentOf(db, t, plugin);
    if (!intent) return message("en", words("en").title.replace("{name}", plugin), WORDS.en.expired, 400);
    return toBuyPage(plugin, t!);
  }

  const t = cookie(req, BUY_COOKIE);
  const intent = await intentOf(db, t, plugin);
  const lang: Lang = intent?.lang ?? "en";
  const w = words(lang);
  const title = fill(w.title, { name: await pluginName(env, plugin, lang) });
  if (!intent) return message(lang, title, w.expired, 400);
  const ctx = await context(env, req);
  if (!ctx.account) {
    if (req.method !== "GET") return message(lang, title, w.expired, 400);
    return new Response(null, {
      status: 303,
      headers: { Location: `/signin?next=${encodeURIComponent(`/buy/${plugin}`)}`, "Cache-Control": "no-store" },
    });
  }
  const home = await homeFor(env, intent.home_id, ctx.account);
  if (!home) return message(lang, title, w.expired, 400);
  if (!home.mine) return message(lang, title, w.otherAccount, 403);
  const offer = await offerFor(env, db, plugin, intent.product, country(req));
  if (typeof offer === "string") {
    const why = offer === "not_for_sale" ? w.notForSale : offer === "no_provider_for_region" ? w.notSoldHere : w.noProvider;
    return message(lang, title, why);
  }

  if (step === "" && req.method === "GET") {
    const price = fill(intent.product === "yearly" ? w.yearly : w.oneTime, { price: formatPrice(offer.minor, offer.currency, lang) });
    return page(
      lang,
      title,
      `<p>${esc(fill(w.forHome, { home: home.name }))}</p>
<p class="price">${esc(price)}</p>
<form method="post" action="/buy/${esc(plugin)}/pay"><input type="hidden" name="currency" value="${esc(offer.currency)}"><button type="submit">${esc(w.pay)}</button></form>
<p class="muted">${esc(w.note)}</p>`,
    );
  }

  if (step === "/pay" && req.method === "POST") {
    // the pay step comes from the page above only
    if (req.headers.get("Origin") !== env.STORE_ORIGIN) return message(lang, title, w.expired, 403);
    const currency = (await form(req)).get("currency");
    if (currency !== offer.currency) return message(lang, title, w.expired, 400);
    const at = now();
    // the intent is used once: a second click, or a second tab, gets the expired page
    const used = await db
      .prepare("UPDATE purchase_intents SET used_at = ? WHERE id_hash = ? AND used_at IS NULL AND expires_at > ?")
      .bind(at.toISOString(), intent.id_hash, at.toISOString())
      .run();
    if ((used.meta.changes ?? 0) !== 1) return message(lang, title, w.expired, 400);
    const purchaseId = `pur_${token()}`;
    try {
      const registry = await registryFor(env, db);
      const r = await startPurchase(db, registry, {
        purchaseId,
        buyer: { accountId: ctx.account.id, email: ctx.account.email, country: country(req) },
        homeId: intent.home_id,
        pluginId: plugin,
        kind: intent.product,
        currency: offer.currency,
        returnUrl: `${env.STORE_ORIGIN}/buy/${plugin}/done${lang === "ar" ? "?lang=ar" : ""}`,
        now: at,
      });
      await db.prepare("UPDATE purchase_intents SET purchase_id = ? WHERE id_hash = ?").bind(purchaseId, intent.id_hash).run();
      return new Response(null, {
        status: 303,
        headers: { Location: r.url, "Set-Cookie": clearBuyCookie, "Cache-Control": "no-store", "Referrer-Policy": "no-referrer" },
      });
    } catch (e) {
      if (!(e instanceof SaleRefused)) throw e;
      const code = e.code;
      const why = code === "not_for_sale" ? w.notForSale : code === "no_provider_for_region" ? w.notSoldHere : w.noProvider;
      return message(lang, title, why);
    }
  }
  return message(lang, title, w.expired, 405);
}
