// POST /v1/payments/webhook/<provider> on the store-web Worker (story 54.19 §2).
//
// The provider's module verifies the signature with its own secret before the body is
// trusted; the events are applied to the ledger, each once; the answer is 200 (with the
// body the provider expects) only after the D1 writes committed, so the provider sends
// again anything lost. A bad signature is 401 and nothing is stored.

import { applyEvents } from "../commerce/ledger.js";
import type { DB } from "../commerce/db.js";
import { WebhookSignatureError } from "../payments/provider.js";
import type { ProviderRegistry } from "../payments/registry.js";

export const WEBHOOK_PATH = /^\/v1\/payments\/webhook\/([a-z0-9][a-z0-9_-]{0,31})$/;

/** The largest webhook body read (providers send a few KB). */
export const MAX_WEBHOOK_BYTES = 256 * 1024;

export async function handlePaymentWebhook(
  request: Request,
  deps: { db: DB; registry: ProviderRegistry; now: () => Date; log?: (event: string, fields: Record<string, unknown>) => void }
): Promise<Response> {
  const m = new URL(request.url).pathname.match(WEBHOOK_PATH);
  if (!m || request.method !== "POST") return new Response("not found", { status: 404 });
  const id = m[1]!;
  const choice = deps.registry.get(id);
  if (!choice) return new Response("not found", { status: 404 });
  const len = Number(request.headers.get("content-length") ?? "0");
  if (len > MAX_WEBHOOK_BYTES) return new Response("too large", { status: 413 });
  const body = await request.text();
  if (body.length > MAX_WEBHOOK_BYTES) return new Response("too large", { status: 413 });
  let events;
  try {
    events = await choice.provider.parseWebhook({ headers: request.headers, body });
  } catch (e) {
    if (e instanceof WebhookSignatureError) {
      deps.log?.("payment_webhook_refused", { provider: id });
      return new Response("signature", { status: 401 });
    }
    throw e;
  }
  const r = await applyEvents(deps.db, id, events, deps.now());
  deps.log?.("payment_webhook", { provider: id, ...r });
  return new Response(choice.provider.webhookAck(), { status: 200 });
}
