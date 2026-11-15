---
schema: 1
kind: pr
pr: 1393
title: "Broadcast: guest sharing ends with the plan; machine hosts keep the publishable limit"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1393 merged as b60072e9f. When an account's plan loses broadcasting (Free, Basic, an
  unknown plan, or a subscription no longer live), `SetEffectivePlan`, called by both the
  payment webhook and the console's plan change, revokes every invite of the account's homes
  (`revoked_by = plan_ended`), puts open channels back to invite only and raises the home's
  access version in the same transaction; the edge gets the access state after commit, and
  `PushPending` retries within a minute. `past_due` still counts as live, so a payment being
  retried revokes nothing; a dispute suspends without calling `SetEffectivePlan`.
- Re-subscribing does not revive old invites (added to 50.20's edge cases). The
  Broadcaster-to-Pro downgrade now pushes its channel changes at once, not at the hourly
  refresh.
- Home migration 0230 adds `broadcast_channels` to a machine host's database copy (no
  credential travels), so `channel_publishable_only()` limits on machines too; before, a
  machine running while the home was away saw no limit. SchemaRev is 230 in all three places.
  A Postgres test fails if the publishable rule reads any table the machine copy leaves out.
- The review added `edgepush_test.go`: affected-tests needs a test in the changed file's own
  package.

## What we learned
- A rule enforced by a SQL function is only as wide as the tables its replica holds; test the
  function's table reads against the copy's exclusion list.

## Open
- An invite created at the same moment as a console plan change could slip past the
  revocation (a theoretical race); the webhook may push one home twice per event (harmless).
