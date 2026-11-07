# Payments in the plugin store

Paid plugins (Epic 54, story 54.19) go through **pluggable payment providers**: one
`PaymentProvider` interface (`provider.ts`), a registry the store admin configures
(`registry.ts`, the `payment_providers` table of store-commerce) and one module per provider
under `providers/<id>/`. The store's own ledger (`../commerce/`) records every sale, refund,
dispute and payout, whatever provider took it; homes see only entitlements the store signs.

Two modules are here, neither connected to a live account:

| Module | What it is |
|---|---|
| `fake` | CI's and staging's: no network, HMAC-signed JSON webhooks; its settings make it a marketplace (`provider_splits`) or a merchant of record (`store_batches`) |
| `adyen` | Adyen for Platforms: the store sells, hosted Checkout sessions split 85/15 to each developer's balance account, HMAC-signed standard notifications, the store files tax |

Nothing in this repository calls a live payment API or holds a key. The store never uses
Stripe or a Stripe-owned service.

## Enabling a provider (the owner's steps)

Each provider is off until all of this is done. Nothing below is automated.

### Adyen for Platforms

1. **The account.** Open the store's own Adyen for Platforms account (balance platform),
   separate from any account used for Maktaba Cloud: its own company account, a merchant
   account for plugin sales (for example `MaktabaPluginStore`), its own payout bank details.
   Ask Adyen to enable split payments (`splits` on Checkout sessions), hosted onboarding,
   and the countries and currencies to sell in (the Gulf and Egypt first: SA, AE, KW, QA,
   BH, OM, EG; SAR, AED, EGP, USD, EUR, GBP).
2. **Resale in writing.** Get Adyen's written confirmation that the account may resell
   third-party software (plugins by other developers) with split payments. Keep the
   document with the store's records; its date goes in `resale_confirmed_at` (step 6).
   Without it the store does not use the provider live.
3. **API credentials** (Customer Area → Developers → API credentials):
   - a Checkout web service user with the role to create sessions and refunds, for the
     merchant account: its API key;
   - a balance platform API credential with the roles for legal entities, account holders,
     balance accounts and hosted onboarding links: its API key.
4. **The webhook.** Customer Area → Developers → Webhooks → a **Standard webhook**:
   - URL: `https://plugins.maktaba.tv/v1/payments/webhook/adyen`
   - method JSON, HMAC signing on; generate the HMAC key and copy it;
   - events: `AUTHORISATION`, `REFUND`, `REFUND_FAILED`, `NOTIFICATION_OF_CHARGEBACK`,
     `CHARGEBACK`, `CHARGEBACK_REVERSED`.
5. **The secrets**, as Worker secrets of `store-web` (never in a file, CI log or this
   repository):
   ```sh
   wrangler secret put PAYMENTS_ADYEN_API_KEY            # the Checkout API key (step 3)
   wrangler secret put PAYMENTS_ADYEN_PLATFORM_API_KEY   # the balance platform API key (step 3)
   wrangler secret put PAYMENTS_ADYEN_HMAC_KEY           # the webhook's HMAC key (step 4)
   ```
6. **The configuration** (the store admin page, or one row of `payment_providers` in the
   `store-commerce` D1 database): `id` `adyen`, `enabled` 1, `priority` (lower first),
   `mode` `test` first and `live` once tested, `resale_confirmed_at` the date of step 2,
   `regions` and `currencies` the store uses it for (empty: everything the account allows),
   and `settings`:
   ```json
   {"merchantAccount": "MaktabaPluginStore", "mode": "live", "liveUrlPrefix": "<the account's live URL prefix>"}
   ```
   The live URL prefix is in Customer Area → Developers → API URLs. In `test` mode leave it
   out: the module talks to Adyen's test platform.
7. **Try it in test mode** with Adyen's test cards before switching `mode` to `live`.

The store's entitlement key (story 54.14, `use: "entitlement"`) is a separate Worker secret
of its own, made by the owner with the store's other keys; no payment provider sees it.

### Adding a second provider

Two providers can run at once: the store admin gives each its regions, currencies and
order, and a sale goes to the first enabled one that sells to the buyer's country in the
price's currency.

- **A provider that has a module** (for example the fake in staging): repeat steps 1–7 for
  it: its own account, its written resale confirmation, its webhook at
  `https://plugins.maktaba.tv/v1/payments/webhook/<id>`, its secrets
  `PAYMENTS_<ID>_<NAME>` (the names are the module's `secrets`), and its
  `payment_providers` row.
- **A provider with no module yet** (a Gulf or Egypt provider such as Tap Payments, PayTabs
  or Paymob): a contributor adds `providers/<id>/` (`index.ts` and `provider.json`), one line
  in `providers/index.ts`, and tests with recorded shapes; then the owner does steps 1–7 for
  it. Nothing else changes. The steps for a module are in
  [Selling a plugin](../../docs/developers/plugins/selling.md#adding-a-payment-provider-for-contributors).

Developers onboard with each provider they want to be paid through ("Set up payouts" in the
console); a marketplace provider is offered for a sale only when the developer has an active
account with it.
