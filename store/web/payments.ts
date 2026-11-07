// The store's payment providers in the Worker (story 54.19 §2): the registry built from the
// store admin's payment_providers rows and the Worker's secrets PAYMENTS_<ID>_<NAME>, and the
// providers' webhooks. Nothing here names a provider: each is a module of payments/providers/.

import type { DB } from "../commerce/db";
import { PROVIDER_MODULES } from "../payments/providers/index";
import { ProviderRegistry, configFromRow } from "../payments/registry";
import { type Env, now } from "./env";
import { json } from "./http";
import { handlePaymentWebhook } from "./payments_webhook";

/** store-commerce, or null where it is not bound (no paid plugins). */
export function commerceDB(env: Env): DB | null {
  return env.DB_COMMERCE ? (env.DB_COMMERCE as unknown as DB) : null;
}

/** The enabled providers, in the store admin's order; a row that cannot run says why (issues). */
export async function registryFor(env: Env, db: DB): Promise<ProviderRegistry> {
  const { results } = await db.prepare("SELECT * FROM payment_providers").all<Record<string, unknown>>();
  return new ProviderRegistry(PROVIDER_MODULES, results.map(configFromRow), env as unknown as Record<string, string | undefined>, {
    fetch: (input, init) => fetch(input, init),
    now,
  });
}

/** POST /v1/payments/webhook/{provider}: the provider's events, verified by its module. */
export async function paymentsWebhook(env: Env, req: Request): Promise<Response> {
  const db = commerceDB(env);
  if (!db) return json({ error: "payments_unavailable" }, 503);
  const registry = await registryFor(env, db);
  return handlePaymentWebhook(req, {
    db,
    registry,
    now,
    log: (event, fields) => console.log(JSON.stringify({ event, ...fields })),
  });
}
