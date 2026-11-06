---
schema: 1
kind: decision
pr: 591
title: Epic 54 decisions: no Stripe; own stages as first-party plugins
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge fb955c66b. No Stripe ever; 85/15 split; seller of record and payment provider decided at Phase 3 (candidates Paddle, Adyen for Platforms, or developers selling directly with offline licence keys; reselling third-party products must be confirmed in writing); $25 one-time fee to sell; English and Arabic listings with labelled machine translation; source to reviewers for every version; a sticky admin keep-off separate from the store switch-off that cannot be undone; library checkboxes start empty; panels shown to everyone once installed.
- Story 54.19 rewritten with no fixed payment provider, and Epic 50's Stripe became "payment provider TBD (not Stripe)".
- README section 15 and stories 54.22-54.36 (Phase 4) spec turning all 56 of Maktaba's own stages into 15 native, trusted first-party plugins on the same interface, using named statements, a database role per plugin, a per-stage switch, CI parity proofs and nine migration waves.

## Keep doing
- No Stripe or Stripe-owned services for any payments, in specs or code.
