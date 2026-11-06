---
schema: 1
kind: decision
pr: 606
title: Epic 54 plugin store independent of Maktaba Cloud
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 9046fc512. The store has its own accounts (email verification, MFA required for developers, passkeys for reviewers and store admins, re-auth for sensitive actions, server-side roles), its own D1 databases and deploy, and its own hostname `plugins.maktaba.tv`, with signed files on `plugins-cdn.maktaba.tv`.
- It runs only on Cloudflare (Workers, D1, Durable Objects, R2, Queues, and Containers or a no-secrets CI runner for upload checks), with stated limits, D1 Time Travel plus nightly R2 exports, and an estimated $5 a month at launch.
- Homes link to a store account with a device code, check store-signed entitlements offline, and poll a signed revocation file on the CDN every 5 minutes, so nothing on the plugin path calls the cloud.
- Payments are the store's own, separate from cloud billing and never Stripe.

## Open
- The payment provider, the email provider and the check runner are open decisions.
