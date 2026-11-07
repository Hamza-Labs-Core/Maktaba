# Selling a plugin

> **Status:** the store's payments side is **on `main`**: the payment provider interface and
> registry, the store's own ledger, refunds, payouts and signed entitlements, with a test
> provider and an Adyen for Platforms module that are not connected to any live account.
> Buying needs the store itself (Phase 2), which is not built yet, so nothing can be bought
> today. The design is in story 54.19 of
> [Epic 54](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/README.md).

## What it costs and what you get

- Publishing free plugins is free. Selling needs a one-time **$25** developer fee, paid once
  per developer account, and **active payouts** with at least one of the store's payment
  providers. Without either, setting a price answers `seller_fee_required` or
  `payouts_required`.
- You get **85 %** of each sale and Maktaba keeps **15 %**. The split is worked out in the
  currency's smallest unit: Maktaba's share is rounded down and you get the rest, so a $10
  sale is $8.50 to you and $1.50 to the store, and a 37.50 SAR sale is 31.88 SAR to you.
  Nothing else is taken from your share by the store; the provider's own fees are shown as
  the provider states them.
- Prices are **one-time** or **yearly**, from $1 to $200 or the same in EUR, GBP, SAR, AED or
  EGP. A new price applies to new purchases; a yearly renewal keeps the old price for the
  current term.
- A listed free plugin stays free (`free_stays_free`): to sell, publish a new plugin id.

## Setting up payouts

In the developer console, **Set up payouts** opens the payment provider's own onboarding:
your identity and your bank details go to the provider, never to Maktaba. The store keeps
only the provider's reference for your account and whether it can pay you (`active`,
`pending` or `restricted`). If the provider restricts your account, new sales pause and the
buyers who already paid keep their plugin.

The store can run several payment providers at once, and you may be asked to set up payouts
with more than one, so buyers in more countries can pay you.

## How you are paid

Each provider pays developers in one of two ways, and the console says which:

- **Split payments** (a marketplace provider such as Adyen for Platforms): each sale is split
  when the buyer pays, and the provider pays your 85 % to your account itself, on its own
  schedule. The store's monthly statement lists those sales.
- **Monthly payouts** (a merchant of record that sells for the store): the store pays you
  once a month what you earned (sales, less refunds and lost disputes), after a store admin
  approves the batch. Under $25 (or the same in your currency) rolls over to the next month,
  and a payout the bank sends back is yours again for the next run.

You get a statement each month, in English and Arabic.

## Refunds and disputes

- A buyer can get a refund within **14 days**, without a reason. Your share of a refund is
  reversed, and the home's entitlement ends at its next daily refresh. A later refund is a
  store admin's decision.
- A **dispute** (a chargeback) the store loses ends the entitlement and reverses your share
  the same way; one the store wins changes nothing.
- A plugin withdrawn for malware is refunded to every buyer automatically.

## What the home sees

The home never deals with money or a payment provider. After a purchase it downloads a
**plugin entitlement** signed by the store (the store's entitlement key, listed under the
plugin root every server embeds): the home's store id and key, your plugin id, the purchase
and a validity of 31 days, refreshed daily. The home checks it offline, so a store outage of
less than 31 days changes nothing for a paying home, and a Maktaba Cloud outage of any
length changes nothing at all. Without a valid entitlement your plugin is *Waiting*
(`entitlement_missing`) on that home: its queue and data are kept, and it comes back by
itself when a new entitlement arrives. A copy of a paid package on another home installs but
stays *Waiting*.

Your plugin never handles payment: no capability touches money, and no string, panel or call
can show a price, a paywall or a link.

## The payment providers

Payments belong to the store, separate from Maktaba Cloud's billing. The store does not
depend on one payment provider: a provider is one module behind the store's
`PaymentProvider` interface, and the store admin can enable several, each for the buyer
countries and currencies it serves, in an order. A sale goes to the first that sells to the
buyer's country in the price's currency. Whatever provider takes a sale, the purchase, the
ledger, the refunds and the payouts are the store's own records. A provider is used for real
sales only after it has confirmed in writing that it allows the resale of third-party
plugins. The store never uses Stripe or any Stripe-owned service.

| Provider | How it sells | How you are paid | Status |
|---|---|---|---|
| Test provider | either way (for CI and staging) | either way | in CI only |
| Adyen for Platforms | the store sells; each payment is split | split payments to your balance account | the module is on `main`; no live account yet |

### Adding a payment provider (for contributors)

A provider is a folder, `store/payments/providers/<id>/`:

1. `index.ts` exports a `ProviderModule`: its `id`, the Worker secrets it needs (named
   `PAYMENTS_<ID>_<NAME>`), `parseConfig` for its settings (merchant account, region and so
   on; never a secret) and `create`, which answers a `PaymentProvider`: its capability
   descriptor (seller model, how developers are paid, subscriptions, refunds, disputes, who
   works out tax, regions, currencies), checkout, webhook verification (the signature is
   checked with its secret before the body is read, and its events are answered in the
   store's own words), refunds, payouts, and the optional tax and dispute hooks.
2. `provider.json` lists its markers: its SDK packages, API hosts and key prefixes. CI's
   `payment_provider_allowlist.py` fails when any of them, or any known payment SDK or host,
   appears outside the folder of a provider that declares it.
3. One line in `store/payments/providers/index.ts` adds the module to the list.
4. Tests with recorded request and webhook shapes, never a live call.

Nothing else changes: the registry, the ledger, entitlements and payouts take any module.
