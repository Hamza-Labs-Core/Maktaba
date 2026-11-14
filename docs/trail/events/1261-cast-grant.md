---
schema: 1
kind: pr
pr: 1261
title: "Epic 58 story 58.07, part 1: the cast grant"
date: 2026-11-14
session: 9c854798-d702-4767-80df-d223f28e28eb
agent: claude-code
---

## What happened
- #1261 merged as e1512501c. `POST /api/cast/grants` mints a `cast_`-prefixed RS256 token of
  the home's keys (audience `cast`, at most 6 h) naming one video or one play queue. A grant
  acts as its user, re-read on every request (users, ACL, the minting sign-in's age ceiling),
  is never an admin, and reaches only the routes in `castgrant.Routes` for its items; anything
  else is 403 `cast_scope`. The admin token, a Maktaba Cloud sign-in and a grant cannot mint.
- The home's CORS gained `BearerOrigins` (ACAO for `https://cast.maktaba.tv`, never
  credentials). The signed `?sig=` media on the web port already answers any origin.
- Three CI rounds: the affected-tests gate (jwt.go, the streaming handler and main.go /
  auth_bootstrap.go each needed a test of their own), a stale wiki after main moved, and
  `shared/go/ports`' outbound-hosts test, which scans every `https://` host in Go code.

## What we learned
- `castgrant.Gate` must run **before** the parental gate in `router.New`: the parental gate
  swaps a hidden video's id for a stand-in, so after it a grant for a video hidden since the
  mint answered 403 instead of 404.
- The streaming port's stream gate already sets `Access-Control-Allow-Origin: *` for signed
  media; adding it again in the handler duplicates the header and breaks CORS.
- A new route touches six registries: placement.go, components_routes.go, parental
  surfaces.go, the three OpenAPI files (the wiki reads openapi.json), placement_test's
  everyMount, and the p57 home and bypass fixtures.
- A host named in Go code only as an origin to answer still goes in `ports_test`'s `notCalled`.

## Keep doing
- Run the router PG tests against a scratch Postgres container (inline
  `MAKTABA_TEST_PG_URL=…`: an `export` does not survive between shell calls).

## Stop doing
- `git checkout origin/main -- .` to compare trees: it overwrites the working tree.

## Open
- Full nodes do not accept grants yet (a cast from a node-served library uses the home).
- Senders (58.06) detect the route by its 404 on an older home.
- The receiver that uses the grant is #1267.
