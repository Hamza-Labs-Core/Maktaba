// store-web (story 54.15): the plugin store's Worker on plugins.maktaba.tv. Its routes are the
// store API (/v1/…); the developer console, /link and the account pages are static assets
// (console/). Background work comes from its queues and Cron Triggers. It is independent of
// Maktaba Cloud: it reads no cloud cookie or token and calls no cloud host.
import * as accounts from "./accounts";
import * as admin from "./admin";
import { type Ctx, context, sameOrigin } from "./auth";
import * as dev from "./developers";
import type { Env } from "./env";
import { nightlyExport } from "./exports";
import * as homes from "./homes";
import { Refusal, json, problem, refuse } from "./http";
import { type Mail, deliver } from "./mail";
import { resignPage } from "./publish";
import { runChecks, runnerRoute } from "./checks";
import * as review from "./review";
import * as ratings from "./ratings";
import { listPage, pluginPage } from "./listing";

export { AccountGuard } from "../objects/account-guard";
export { CatalogWriter } from "../objects/catalog-writer";
export { IpBucket } from "../objects/ip-bucket";
export { PairingCode } from "../objects/pairing-code";

type Handler = (ctx: Ctx, ...params: string[]) => Promise<Response>;

const ROUTES: [string, RegExp, Handler][] = [
  ["POST", /^\/v1\/accounts$/, accounts.signUp],
  ["POST", /^\/v1\/accounts\/verify$/, accounts.verifyEmail],
  ["POST", /^\/v1\/accounts\/email$/, accounts.changeEmail],
  ["POST", /^\/v1\/accounts\/password$/, accounts.changePassword],
  ["DELETE", /^\/v1\/accounts\/me$/, accounts.deleteAccount],
  ["POST", /^\/v1\/sessions$/, accounts.signIn],
  ["POST", /^\/v1\/sessions\/passkey\/options$/, accounts.passkeySignInOptions],
  ["POST", /^\/v1\/sessions\/passkey$/, accounts.passkeySignIn],
  ["POST", /^\/v1\/sessions\/end$/, accounts.signOut],
  ["GET", /^\/v1\/me$/, accounts.me],
  ["POST", /^\/v1\/reauth$/, accounts.reauth],
  ["POST", /^\/v1\/reauth\/passkey\/options$/, accounts.reauthPasskeyOptions],
  ["POST", /^\/v1\/mfa\/totp\/begin$/, accounts.totpBegin],
  ["POST", /^\/v1\/mfa\/totp\/confirm$/, accounts.totpConfirm],
  ["POST", /^\/v1\/mfa\/passkey\/options$/, accounts.passkeyOptions],
  ["POST", /^\/v1\/mfa\/passkey$/, accounts.passkeyRegister],
  ["DELETE", /^\/v1\/mfa\/([\w-]+)$/, accounts.removeFactor],
  ["POST", /^\/v1\/developers$/, dev.becomeDeveloper],
  ["GET", /^\/v1\/developers\/me$/, dev.developerMe],
  ["POST", /^\/v1\/developers\/keys$/, dev.addKey],
  ["DELETE", /^\/v1\/developers\/keys\/([0-9a-f]{32})$/, dev.revokeKey],
  ["POST", /^\/v1\/developers\/prefixes$/, dev.claimPrefix],
  ["POST", /^\/v1\/developers\/prefixes\/([a-z0-9.-]+)\/check$/, dev.checkPrefixRoute],
  ["POST", /^\/v1\/submissions$/, dev.startSubmission],
  ["POST", /^\/v1\/flow-submissions$/, dev.startFlowSubmission],
  ["GET", /^\/v1\/flow-submissions\/([\w-]+)$/, dev.getFlowSubmission],
  ["POST", /^\/v1\/submissions\/([\w-]+)\/uploaded$/, dev.uploaded],
  ["GET", /^\/v1\/submissions\/([\w-]+)$/, dev.getSubmission],
  ["POST", /^\/v1\/plugins\/([a-z0-9.-]+)\/withdraw$/, dev.withdraw],
  ["POST", /^\/v1\/homes\/pair$/, homes.pair],
  ["POST", /^\/v1\/homes\/pair\/poll$/, homes.poll],
  ["POST", /^\/v1\/homes\/link$/, homes.link],
  ["GET", /^\/v1\/homes$/, homes.listHomes],
  ["DELETE", /^\/v1\/homes\/([\w-]+)$/, homes.unlink],
  ["POST", /^\/v1\/admin\/bootstrap$/, admin.bootstrap],
  ["POST", /^\/v1\/admin\/roles$/, admin.grantRole],
  ["DELETE", /^\/v1\/admin\/roles\/([\w-]+)\/([a-z_]+)$/, admin.revokeRole],
  ["POST", /^\/v1\/admin\/accounts\/([\w-]+)\/suspend$/, admin.suspend],
  ["POST", /^\/v1\/admin\/accounts\/([\w-]+)\/mfa-reset$/, admin.startMfaReset],
  ["DELETE", /^\/v1\/admin\/accounts\/([\w-]+)\/mfa-reset$/, admin.cancelMfaReset],
  ["POST", /^\/v1\/admin\/revocations$/, admin.addRevocation],
  ["DELETE", /^\/v1\/admin\/revocations\/(\d+)$/, admin.liftRevocation],
  ["POST", /^\/v1\/admin\/resign$/, admin.resignAll],
  ["GET", /^\/v1\/admin\/audit$/, admin.auditLog],
  ["GET", /^\/v1\/admin\/queue$/, review.queue],
  ["GET", /^\/v1\/admin\/submissions\/([\w-]+)$/, review.submission],
  ["GET", /^\/v1\/admin\/submissions\/([\w-]+)\/source$/, review.source],
  ["POST", /^\/v1\/admin\/submissions\/([\w-]+)\/approve$/, review.approve],
  ["POST", /^\/v1\/admin\/submissions\/([\w-]+)\/reject$/, review.reject],
  ["POST", /^\/v1\/admin\/submissions\/([\w-]+)\/ask$/, review.ask],
  ["POST", /^\/v1\/admin\/flagged-words$/, review.flagWord],
  ["POST", /^\/v1\/submissions\/([\w-]+)\/answer$/, review.answer],
  ["POST", /^\/v1\/plugins\/([a-z0-9.-]+)\/ratings$/, ratings.rate],
  ["GET", /^\/v1\/plugins\/([a-z0-9.-]+)\/ratings$/, (ctx, id) => ratings.ratings(ctx.env, id!)],
  ["POST", /^\/v1\/ratings\/([\w-]+)\/reply$/, ratings.reply],
  ["POST", /^\/v1\/ratings\/([\w-]+)\/report$/, ratings.reportRating],
  ["POST", /^\/v1\/plugins\/([a-z0-9.-]+)\/reports$/, ratings.report],
  ["GET", /^\/v1\/admin\/reports$/, ratings.moderationQueue],
  ["POST", /^\/v1\/admin\/reports\/([\w-]+)\/resolve$/, ratings.resolveReport],
  ["POST", /^\/v1\/admin\/ratings\/([\w-]+)\/hide$/, ratings.hideRating],
];

/** Routes a home calls with its own signature, not a session (no cookie, no Origin). */
const HOME_ROUTES: [string, RegExp, (env: Env, req: Request, ...p: string[]) => Promise<Response>][] = [
  ["POST", /^\/v1\/homes\/([\w-]+)\/seen$/, homes.seen],
  ["POST", /^\/v1\/homes\/([\w-]+)\/ratings$/, ratings.rateFromHome],
  ["POST", /^\/v1\/homes\/([\w-]+)\/reports$/, ratings.reportFromHome],
  ["PUT", /^\/v1\/uploads\/([A-Za-z0-9_-]{43})$/, dev.workerUpload],
  // the isolated runner's single-use URLs (54.16 §1): it holds no session and no secret
  ["GET", /^\/v1\/runner\/([A-Za-z0-9_-]{43})$/, runnerRoute],
  ["POST", /^\/v1\/runner\/([A-Za-z0-9_-]{43})$/, runnerRoute],
];

/** The routes table, for the role tests (54.15 T01). */
export const routeTable = ROUTES.map(([m, re]) => [m, re.source] as const);

async function api(req: Request, env: Env): Promise<Response> {
  const url = new URL(req.url);
  for (const [m, re, h] of HOME_ROUTES) {
    const match = re.exec(url.pathname);
    if (match && req.method === m) return h(env, req, ...match.slice(1));
  }
  let methodSeen = false;
  for (const [m, re, h] of ROUTES) {
    const match = re.exec(url.pathname);
    if (!match) continue;
    methodSeen = true;
    if (req.method !== m) continue;
    const ctx = await context(env, req);
    if (m !== "GET") sameOrigin(ctx);
    const res = await h(ctx, ...match.slice(1).map(decodeURIComponent));
    if (ctx.setCookie) res.headers.append("Set-Cookie", ctx.setCookie);
    return res;
  }
  return refuse(methodSeen ? 405 : 404, methodSeen ? "method_not_allowed" : "not_found");
}

/**
 * /admin/… (54.15 §7): its own bundle, sent only after the role check on the server, with a
 * strict CSP, noindex and no-store. Hiding a menu is never the guard: every /v1/admin route
 * checks again.
 */
async function adminPage(req: Request, env: Env): Promise<Response> {
  const ctx = await context(env, req);
  const staffOK =
    ctx.account &&
    (ctx.roles.has("store_reviewer") || ctx.roles.has("store_admin")) &&
    ctx.factors.passkey > 0 &&
    ctx.session?.mfa_kind === "passkey";
  const headers = {
    "Cache-Control": "no-store",
    "X-Robots-Tag": "noindex, nofollow",
    "Content-Security-Policy":
      "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
    "X-Content-Type-Options": "nosniff",
  };
  if (!staffOK) return Response.redirect(`${env.STORE_ORIGIN}/signin?next=/admin`, 302);
  if (!env.ASSETS) return new Response("not built", { status: 503, headers });
  const page = await env.ASSETS.fetch(new Request(new URL("/admin.html", req.url), req));
  const out = new Response(page.body, page);
  for (const [k, v] of Object.entries(headers)) out.headers.set(k, v);
  return out;
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const url = new URL(req.url);
    if (url.pathname.startsWith("/v1/")) {
      try {
        return await api(req, env);
      } catch (e) {
        if (e instanceof Refusal) return problem(e);
        console.error(JSON.stringify({ event: "store_error", path: url.pathname, error: String(e), stack: (e as Error).stack }));
        return json({ error: "internal" }, 500);
      }
    }
    if (url.pathname === "/admin" || url.pathname.startsWith("/admin/")) return adminPage(req, env);
    // the listing pages (54.18 §2), server-rendered for search engines, English and Arabic
    if (req.method === "GET") {
      const m = /^(\/ar)?(?:\/p\/([a-z0-9.-]+))?\/?$/.exec(url.pathname);
      if (m) {
        const lang = m[1] ? "ar" : "en";
        return m[2] ? pluginPage(env, lang, m[2]) : listPage(env, lang);
      }
    }
    if (env.ASSETS) return env.ASSETS.fetch(req);
    return json({ error: "not_found" }, 404);
  },

  async queue(batch: MessageBatch, env: Env): Promise<void> {
    for (const msg of batch.messages) {
      if (batch.queue.startsWith("store-mail")) {
        await deliver(env, msg.body as Mail);
      } else if (batch.queue.startsWith("store-checks")) {
        // a test store drives the checks itself (CHECKS_FROM_QUEUE = "false")
        if (env.CHECKS_FROM_QUEUE !== "false") await runChecks(env, (msg.body as { submission: string }).submission);
      } else if (batch.queue.startsWith("store-catalog")) {
        const b = msg.body as { kind: string; after?: string };
        if (b.kind === "resign") {
          const next = await resignPage(env, b.after ?? "");
          if (next) await env.Q_CATALOG.send({ kind: "resign", after: next });
        }
        await env.CATALOG_WRITER.get(env.CATALOG_WRITER.idFromName("main")).regenerate();
      }
      msg.ack();
    }
  },

  async scheduled(controller: ScheduledController, env: Env): Promise<void> {
    switch (controller.cron) {
      case "0 */6 * * *":
        await admin.finishMfaResets(env);
        await env.CATALOG_WRITER.get(env.CATALOG_WRITER.idFromName("main")).regenerate();
        break;
      case "15 3 * * *":
        await nightlyExport(env);
        await review.backlogAlert(env);
        break;
      case "0 4 1 * *":
        await dev.recheckPrefixes(env);
        break;
    }
  },
} satisfies ExportedHandler<Env>;
