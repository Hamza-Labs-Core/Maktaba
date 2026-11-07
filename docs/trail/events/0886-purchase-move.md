---
schema: 1
kind: pr
pr: 886
title: A purchase moves to another home, twice a year
date: 2026-11-07
session: 29e8f107-ee7f-4db2-a128-c02a2839240a
agent: claude-code
prs: [861]
---

## What happened
- Story 54.19 §6's move: store-commerce migration `0003_purchase_moves`, `GET /v1/purchases` and `POST /v1/purchases/{id}/move` (verified, re-authenticated, rate-limited, audited), and a Purchases section on the console's Account page in English and Arabic.
- The limit (2 moves per purchase in any 365 days) is checked inside the D1 batch itself: the move row's `INSERT … SELECT` re-reads the count, the purchase's home and its status, and the two home rewrites run only if that row exists. A read-then-write check alone would let two moves at once both pass.
- The home needed no change: it replaces `plugin_entitlements` with what the store returns, so the old home drops the plugin at its next daily refresh.
- Problem: the sqlite test database's `batch()` returned node:sqlite's `{changes}` per statement, while D1 returns `{meta: {changes}}`. Nothing had read batch results before, so the mismatch was invisible until the move did. The shim now answers D1's shape, and `DB.batch` is typed with it.
- Problem: the store's Worker tests need `build/store.wasm`. Homebrew's `cargo` on this Mac has no `wasm32-unknown-unknown` (rustup's has it), so `npm run build` failed until rustup's `cargo` was put first in PATH (`/opt/homebrew/opt/rustup/bin`).
- Problem: the first `npm run typecheck` failed on an `unknown` body in the new Worker test. Caught locally, before the push, as #861's lesson said.

## What we learned
- A test double must answer the real API's shape for every return value, even ones no caller reads yet.
- A limit that money depends on belongs in the write's own condition, not in a read before it.

## Keep doing
- Run `npm run typecheck` (all three configs) and the full `npm test` in `store/` before pushing.

## Open
- The spec's as-built note for the move waits for #861, which edits the same line.
- Not built yet: Buy on plugins.maktaba.tv itself; Apple snapshot references (#876).
