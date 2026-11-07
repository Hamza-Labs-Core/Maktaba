---
schema: 1
kind: pr
pr: 850
title: Buying a paid plugin (Epic 54, story 54.19 §5)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
---

## What happened
- The store's Worker now runs its payment providers. The registry is built from `payment_providers` and the Worker secrets, and `POST /v1/payments/webhook/{provider}` is wired. #735 had built both, but neither was reachable.
- A linked home asks for a one-time purchase intent signed with its own key (store-commerce `0002_purchase_intents`). The admin's browser posts it to the store's scriptless `/buy/<plugin>` pages. There the buyer signs in to the account the home is linked to and sees the price in a currency an enabled provider sells to their country. The pay step redirects to the provider's checkout.
- The catalog carries a paid plugin's product and prices. Browse shows the price, and either Buy or "Link this home to buy", in English and Arabic.
- The store console's sign-in follows `?next=` only on the store's own origin and only to `/buy/…` or `/admin`.

## What we learned
- Building a library is not shipping it. #735's registry and webhook handler had tests but no route. A paid plugin's first sale needed the wiring as much as the Buy page did.
- An "open a tab, then post" flow must open the tab on the click itself. A tab opened after the API answers is a blocked pop-up. The tab opens as `about:blank`, its `opener` is cut, and the form is posted into it by name.
- A `next` check by string prefix misses `/\evil.example` and similar. Resolve with `new URL(next, origin)`, compare origins, then allow-list path prefixes. Test the backslash, encoded-slash, `javascript:`, empty and very long cases.
- `ports.Outbound` is checked by host only. A new call to a host that is already listed passes the test unnoticed, so list each new kind of call by name. #781's entitlement refresh had been missing from it.

## Keep doing
- Coordinate file by file with the session that owns the neighbouring code before branching. The store delegate's map of the lines #835–#837 touched let this PR merge main cleanly twice.

## Stop doing
- Shipping a provider-neutral library without the one route that makes it reachable.

## Open
- Buy on plugins.maktaba.tv itself (picking a linked home), the `/paid/*` CDN path for paid packages, and moving a purchase to another home.
