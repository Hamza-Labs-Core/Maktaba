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
];

/** Routes a home calls with its own signature, not a session (no cookie, no Origin). */
const HOME_ROUTES: [string, RegExp, (env: Env, req: Request, ...p: string[]) => Promise<Response>][] = [
  ["POST", /^\/v1\/homes\/([\w-]+)\/seen$/, homes.seen],
  ["PUT", /^\/v1\/uploads\/([A-Za-z0-9_-]{43})$/, dev.workerUpload],
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
    if (env.ASSETS) return env.ASSETS.fetch(req);
    return json({ error: "not_found" }, 404);
  },

  async queue(batch: MessageBatch, env: Env): Promise<void> {
    for (const msg of batch.messages) {
      if (batch.queue.startsWith("store-mail")) {
        await deliver(env, msg.body as Mail);
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
        break;
      case "0 4 1 * *":
        await dev.recheckPrefixes(env);
        break;
    }
  },
} satisfies ExportedHandler<Env>;
