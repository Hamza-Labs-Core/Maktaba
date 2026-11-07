---
schema: 1
kind: pr
pr: 743
title: The home checks plugin entitlements offline (Epic 54, story 54.19 §6)
date: 2026-11-06
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
prs: [735]
---

## What happened
- The home's offline check of a store-signed entitlement (key use, signature over canonical JSON, home id and key, validity with the 72-hour grace), with shared vectors that the store's TypeScript signer reproduces byte for byte and the Go verifier answers case by case.

## What we learned
- Ed25519 is deterministic, so cross-language vectors need only a fixed test seed: WebCrypto imports a seed as PKCS #8 (`302e020100300506032b657004220420` followed by the 32 bytes), and the signer test can compare whole signature files.
- Go's `json.Encoder` escapes `<`, `>` and `&` unless `SetEscapeHTML(false)`, and JavaScript's `JSON.stringify` never does: a canonical form both sides sign must turn that escaping off.

## Open
- The `entitlement_missing` gate and the daily refresh need 54.14's verified store-keys.json and 54.15's home link (Phase 2).
