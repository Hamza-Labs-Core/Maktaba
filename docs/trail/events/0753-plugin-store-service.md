---
schema: 1
kind: pr
pr: 753
title: Plugins Phase 2 part 2, the plugin store service, store accounts and linking a home (Epic 54, 54.15)
date: 2026-11-06
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [733]
---

## What happened
- The plugin store as its own service in `store/`, on Cloudflare Workers, D1, R2, Durable Objects and Queues, independent of Maktaba Cloud and tested only in Miniflare: store accounts (email verification, Argon2id, TOTP, passkeys, recovery codes, re-auth, lockout, rate limits, the 7-day MFA-reset hold), developers (agreement, keys, prefixes, submissions), countersigning through `plugind/crates/storewasm` (the home's reader and validator built to wasm32), the signed lists from one `CatalogWriter`, store admin, the encrypted nightly export, the console in English and Arabic, and linking a home by device code with `MKS-Home` signed requests. Nothing deployed; the owner's go-live list is in `store/README.md`.
- Merged with main, the store is one npm package with 54.19's commerce and payments; pnpm left the store's build, deploy and runbook.

## What we learned
- Homebrew's `cargo` comes first on this Mac's PATH and has no wasm32 standard library: build the store's wasm with `~/.rustup/toolchains/stable-aarch64-apple-darwin/bin` first on the PATH.
- workerd refuses a compatibility date newer than its own build; the vitest pool's Miniflare needs a date at or before it.
- Argon2id in wasm makes every account a test creates slow; under load the Worker tests passed the 5 s default, so they get 30 s.
- A queue consumer in Miniflare races tests that drive the same work by hand: a binding (`CHECKS_FROM_QUEUE`) lets tests turn it off.
- Signed-list versions must never go below the copy already on the CDN, or a database restored from a backup would publish a list homes refuse as a rollback.
- An `Authorization` value split on `=` cuts base64 padding: split on the first separator only.

## Keep doing
- Read a package in the store with the home's own Rust reader (wasm), not a second implementation.

## Open
- PRs 3-5 of the series: checks and review (54.16), the home's catalog loop and kill switch (54.17), the store browser (54.18).
- Real email provider choice (the code speaks Postmark) and the Cloudflare resources, DNS and secrets, all the owner's.
