---
schema: 1
kind: pr
pr: 778
title: Plugins Phase 2 part 4, the store's catalog, updates and the kill switch on the home (Epic 54, 54.17)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [733, 753, 770]
---

## What happened
- The home's lists loop (`api:plugins_catalog` on core.api's runner): the revocation head every 5 minutes, the list and catalog when they change, straight from the store's CDN, each verified by the chain of trust and kept on disk and in Postgres, the newest seen winning. A store `disable` holds the plugin within one poll and refuses Start, enable, install, update and rollback to a covered version (`409 plugin_revoked`); store installs by catalog version, checked against its sha256; per-plugin nightly automatic updates; a linked home's daily `seen` with telemetry on. Migration 0200.
- Offline (`serve --offline`) the loop fetches and sends nothing, the kept lists age into the expired warning, which stops no plugin, and store installs wait; the store's hosts are listed in `ports.Outbound`.
- A gap in #733's verifier found and closed here: a catalog repeating the newest version with other bytes was accepted (store-keys and the revocation list already refused it); the trust state keeps each catalog's sha256, in Go, Rust and TypeScript, on new shared vectors.

## What we learned
- Run every hostile case through the code a home runs (the loop), not only through the verifier: the same-version catalog swap passed the verifier's tests because none of them asked for it.
- A new outbound host fails `shared/go/ports` TestOutboundHostsAreListed until it is in `ports.Outbound` with what turns it off; gate it once in the one place that makes the request (the CDN client, the store link's `do`).
- Migration slots are taken at merge time: with several branches in flight, renumber right before merging, rebuild `cluster_copy_excluded()` on the newest definition (Up adds, Down restores that one), raise the three SchemaRev constants together, and expect migration-lint to refuse a hole below the highest slot.
- A host test that runs prebuilt guests (`plugind/guests`) fails with a missing stage when the guests are stale: `make plugind-guests` before `cargo test`.

## Open
- PR 5 (54.18) of the series.
- Embedding the current revocation list and store keys in each release (#755).
