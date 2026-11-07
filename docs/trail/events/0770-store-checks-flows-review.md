---
schema: 1
kind: pr
pr: 770
title: Plugins Phase 2 part 3, automated checks, the flow check and human review (Epic 54, 54.16)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [733, 753, 759]
---

## What happened
- The store's verification: C1 to C11 in the `store-checks` consumer on the home's own rules in WebAssembly (the package reader, the manifest validator and the import audit, now `plugind/crates/audit`, shared by the host and the store, its names checked equal to Wasmtime's), the isolated runner as a GitHub Actions fallback with single-use URLs and egress cut, the route to a person or to automatic approval, and the review queue on the store's `/admin` with passkeys.
- C12, the flow check, on #759's shared checker: flows are one upload of their members and are refused, reviewed or approved as a whole; nothing in a refused flow is countersigned; review draws the whole flow. A three-plugin cycle submitted together is refused at verification with its path in English and Arabic.

## What we learned
- A store check that must match the home should run the home's code: the audit as a crate shared by host and store, the flow check as #759's checker. A wrapper that also walks the graph is a second implementation; the walk belongs in the shared checker (#759's `resolveDepends`), and the store's part is only loading data.
- The shared checker's lookup is synchronous and the store's manifests are in D1: run it, collect the plugins it asked for that are not loaded, load them, run again until it asks for nothing new.
- The checker's graph includes the upstream plugins it pulled in; what a flow records (members, events, subscriptions) must be its own, or an upstream's new event would look like the flow growing.
- A D1 table's `CHECK` on a status cannot be widened without a rebuild: a separate column (`flow_state`) carries the new state.
- The repository's rules on workflows bind new ones: every tag workflow takes a `tag` input, every artifact download is a three-attempt chain naming its artifact, and every new workflow file sits under a ci-paths filter (`tools/ci/tests`, `ci_paths.py guard`).
- A Mac with Homebrew's Rust first on the PATH has no wasm32 standard library: put rustup's toolchain first to build the store's WebAssembly.

## Keep doing
- Build a store check from the shared corpus's own cases (the flow tests rewrite the corpus's ids under the developer's prefix) rather than hand-made fixtures.

## Open
- PR 4 (54.17) and PR 5 (54.18) of the series.
- The isolated runner on Cloudflare Containers (#756).
