---
schema: 1
kind: pr
pr: 1336
title: "A cloud TV code sign-in re-checks the cloud link (story 58.03)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1336 merged as f95ae78d5. The approval ticket now records the approving cloud account
  (`pairing_tickets.cloud_user_id`, home slot 0226). The sign-in is recorded in one transaction
  (`cloudlink.Store.RecordLinkedSignIn`) that locks the link row and checks it still names that
  account; if not, the TV's new refresh tokens are revoked and it gets 410 with
  `reason: cloud_link_changed`. `CompleteLink` takes the same lock, so a relink and a sign-in
  can't interleave. Approvals from an older release (no account) are refused; they last 10 min.
- Found by a background security review on #1266; the code was 58.03's on main (#1240).

## What we learned
- A home migration needs `SchemaRev` raised together in api, streaming and pipeline; without it
  the update replica's API crash-loops ("knows migrations up to 0225") and every guard test fails.
- The home's goose runs without allow-missing: an open PR's home slot is a claim the whole stack
  must respect. #1336 took 0226 and the broadcast stack moved up to 0227–0229.

## Keep doing
- Re-check, inside the write's transaction, every condition an earlier approval relied on.

## Open
- Nothing.
