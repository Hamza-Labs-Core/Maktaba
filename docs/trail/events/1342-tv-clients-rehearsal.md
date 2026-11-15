---
schema: 1
kind: pr
pr: 1342
title: "Epic 58's role table, the cast grant's router walk and the tv-clients rehearsal (58.17)"
date: 2026-11-14
session: 3362708f-09da-4409-9621-0e79b17fa243
agent: claude-code
---

## What happened
- #1342 merged as f74fa8356. It adds no server code. An audit of 58.17 against main found the
  OpenAPI half already done by each Epic 58 PR: every TV route in `openapi.yaml`, the JSON and the
  Arabic, with `TestOpenAPIDescribesEveryRoute` as the coverage check. The cloud rehearsal's
  `tv-code` (#1240, #1250) already covered T04.
- Missing: one role table for the story's eight principals, the router walk for the `cast`
  principal, the `tv-clients` rehearsal and `cloud/docs/tv-codes.md`. #1342 added them:
  - `TestP58RoleTable`, on Postgres behind the production router;
  - `TestP58RoleTableNamesEveryRoute`, where `router.P58Routes` reads Epic 58's routes from the
    walk of every mount;
  - `TestCastGrantWalkRefusesEveryOtherRoute`, under which 598 mounted routes answer a grant
    403 `cast_scope` without reaching a handler;
  - the `tv-clients` job of `_integration.yml`, whose required tests are listed in
    `tools/ci/tv-clients-tests.txt` and held to the code by `TestTVClientsListNamesEveryTest`.
- The rehearsal drives the home over the LAN and through the relay path, as each TV does:
  - compat: the update pages, the floor's 426, a home rolled back to before the contract;
  - the cloud code, redeemed with each TV's header;
  - one session per caps vector, whose recorded `device_caps` reach streaming intact;
  - a capped relay.
  It runs the existing cast-grant and 58.14 bypass tests, and streaming's `TestCapsVectors`
  beside them.
- The story was corrected to match the code:
  - a cloud sign-in mints no cast grant (`cast_via_cloud`);
  - a library the caller does not read answers 403;
  - the TV draws its QR code itself (no `qr.png`);
  - the rehearsal runs on Postgres only.
- CI: lint failed once on revive's `redefines-builtin-id` (a variable named `cap`). An Apple
  Player UI shard failed on the dropped-tap flake (issue #639); one rerun of that shard passed.

## What we learned
- When a spec table and shipped code disagree, read the code's reason before changing anything.
  The spec gave a cloud play sign-in 200 on `POST /api/cast/grants`; #1267 refuses it on purpose,
  and the stricter rule stays.
- The home has no SQLite path for these routes: the handlers use Postgres casts and functions.
  The `.sqlite.sql` migrations keep schema parity only.
- `POST /api/stream/sessions/{id}/errors` looks nothing up, by design, and answers 202 to any
  signed-in caller. The report is logged under the caller's own account.
- A test harness without the auth gate turns "no one" into 403. A role table's anonymous column
  goes through `RequireAuthExcept`, as the home does.
- golangci-lint (revive) runs only in CI's lint job. Running `golangci-lint run` locally on the
  changed package before pushing would have saved a round.

## Keep doing
- Audit a story against main before building: here most of the OpenAPI half was already shipped.
- A rehearsal job lists the tests it requires and a test holds that list to the code (the
  parental-bypass pattern).

## Stop doing
- Pushing Go without running `golangci-lint run` on the packages changed.

## Open
- A video of a library the caller does not read answers 403, not 404, across the home, so a
  caller can tell such a video exists. Whether library access should answer as missing, as
  parental limits do, is a decision for an Epic of its own.
- Cast grants on a cluster node (58.17's edge case) have no test running a node's gate against
  its copy of the database.
- 58.13's Roku player (#1313), 58.14's TV screens (#1331) and the iPhone sender (#1287) were
  to add their rows to the rehearsal. All three have merged (#1287 on 2026-11-15), but
  `tools/ci/tv-clients-tests.txt` has no rows from them: its one later change (5c8fa3af5)
  listed #1336's link-recheck test.
