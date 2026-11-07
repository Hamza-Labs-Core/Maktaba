// Test helpers: a store-commerce database, a registry of fakes and an entitlement key.

import { signFake } from "../payments/providers/fake/index.js";
import { PROVIDER_MODULES } from "../payments/providers/index.js";
import type { ProviderEvent } from "../payments/provider.js";
import { type ProviderConfig, ProviderRegistry } from "../payments/registry.js";
import { Signer } from "../signing/sig.js";
import { testDB } from "./sqlite.js";

export const SECRET = "test-webhook-secret";
export const T0 = new Date("2026-11-06T10:00:00Z");

export function fakeConfig(over: Partial<ProviderConfig> = {}): ProviderConfig {
  return {
    id: "fake",
    enabled: true,
    priority: 10,
    mode: "test",
    regions: [],
    currencies: [],
    resaleConfirmedAt: null,
    settings: {},
    ...over,
  };
}

export function registry(configs: ProviderConfig[] = [fakeConfig()], now = () => T0, fetchFn?: typeof fetch) {
  return new ProviderRegistry(
    PROVIDER_MODULES,
    configs,
    {
      PAYMENTS_FAKE_WEBHOOK_SECRET: SECRET,
      PAYMENTS_ADYEN_API_KEY: "test-checkout-key",
      PAYMENTS_ADYEN_PLATFORM_API_KEY: "test-platform-key",
      PAYMENTS_ADYEN_HMAC_KEY: "44782DEF547AAA06C910C43932B1EB0C71FC68D9D0C057550C48EC2ACF6BA056",
    },
    { fetch: fetchFn ?? (async () => new Response("no network in tests", { status: 599 })), now }
  );
}

export async function fakeWebhook(events: ProviderEvent[], secret = SECRET) {
  const body = JSON.stringify({ events });
  return new Request("https://plugins.maktaba.tv/v1/payments/webhook/fake", {
    method: "POST",
    headers: { "x-fake-signature": await signFake(secret, body), "content-type": "application/json" },
    body,
  });
}

/** A test entitlement key E (a seed from a public label, never for production). */
export async function testSigner(): Promise<{ signer: Signer; publicRaw: Uint8Array }> {
  const seed = new Uint8Array(
    await crypto.subtle.digest("SHA-256", new TextEncoder().encode("maktaba-plugins TEST KEY, never for production: entitlement-1")),
  );
  const signer = await Signer.fromSeed(seed);
  return { signer, publicRaw: signer.publicRaw };
}

/** An entitlement envelope's signed object. */
export const signedOf = (envelope: string) => JSON.parse(envelope).signed as Record<string, string>;

export { testDB };
