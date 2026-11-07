---
schema: 1
kind: pr
pr: 865
title: The relay-plans rehearsal (story 50.19 T13)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [856]
---

## What happened
- The `relay-plans` scenario (C17) now runs homes on Free, Basic and Pro behind the rehearsal's relays, with the plans enforced:
  - Free is refused with its tunnel kept.
  - Basic is capped, paced and limited to two streams.
  - Pro past its allowance falls back to Basic's cap.
  - Pay-as-you-go plays in full, and its gigabyte reaches `billing.Fake` once, through an outage.
- The local run passed: 19 requests, 0 failed.
- The relays are a `rehearsalplans` build. Its init calls `rehearsal/plans.Apply()`, which fills the catalog's TBD numbers. `compose-plans.yml` overrides the image and the config.
- While this PR was open, #856 failed CI's Desktop Player job, because a new `ErrorKind` had no desktop sentence. It was fixed there.

## What we learned
- A TBD allowance never runs out, and without a price pay-as-you-go stays off. So any rehearsal of the paid plans needs a build with the numbers set. A build tag keeps them out of every release, the same way `rehearsalbad` does.
- One rehearsal account per pay-as-you-go setting. Pay-as-you-go is per account, so a capped Pro home owned by the same account would have played in full (`seed.HomesOf`).
- The meters flush every 30 s. A scenario that reads usage waits for the flush rather than sleeping a fixed time.

## Keep / stop
- Keep running the affected-tests gate after every follow-up commit, not only before the first push. #856's desktop fix went up without its test and the gate blocked it.

## Open
- The rehearsalplans numbers go when the owner sets the real ones. `plans_test.go` skips itself once nothing is TBD.
- The cloud issuing `basic` once #856's homes are out.
