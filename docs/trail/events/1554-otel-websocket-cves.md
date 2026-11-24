---
schema: 1
kind: incident
pr: 1554
title: "Two Go advisories published on 2026-11-23 turned main's CVE gate red; fixed by upgrading otel and gorilla/websocket"
date: 2026-11-23
session: 759def41-3326-452b-a5d6-c371050e2bcc
agent: claude-code
prs: [1554]
---

## What happened
- `cve-gate / CVE gate` on main (run 45267535780) failed on two advisories published that day.
  Both are called:
  - **api:** `go.opentelemetry.io/otel/sdk` v1.45.0 (GO-2026-6974).
  - **cloud:** `github.com/gorilla/websocket` v1.5.3 (GO-2026-6278, weak PRNG for the mask key).
- A red main blocks the next tag. A release with the update fix had to be tagged before about
  08:00Z on 2026-11-24.
- #1554 upgrades, with no suppressions:
  - every otel module goes to v1.47.0 together, in shared/tracing/go and api;
  - the non-blocking otel v1.44.0 in cmd/maktaba-server, tools/release and
    shared/go/releaseverify goes to v1.47.0 too;
  - websocket goes to v1.5.4-0.20240701034025-d67f41855da4.
  - After the change, local govulncheck v1.1.4 (go1.26.11) found nothing in api, cloud or
    cmd/maktaba-server.

## What we learned
- The gate can go red on main without any code change: a newly published advisory is enough.
  Main is then blocked until someone bumps the dependency.
- otel's version is not only in api: shared/tracing/go pins it, and api inherits it through the
  replace. Bump both in the same change, or api's go.mod pins the old sdk.
- In zsh, a `$VAR` holding several `go get` arguments isn't split into words. Go reads it as one
  invalid module path and the loop keeps going. Write the arguments out, or use an array.
- On this Mac, `lsof -iTCP` takes about 3 minutes, so `cmd/maktaba-server/internal/portholder`'s
  `TestFindsItsOwnListener` times out there whatever the change. Read that failure as
  environmental, not as a regression.

## Keep doing
- Fix advisories by upgrading and verify with govulncheck in each gated module; don't suppress.
- Move every otel module together, to the same version.

## Stop doing
- Running `go get` loops with unquoted multi-arg variables under zsh.

## Open
- `golang.org/x/crypto` v0.58.0 (GO-2026-5932, openpgp, not called) is reported but doesn't
  block. v0.59.0 has the module's fix; openpgp has none. It needs its own bump.
- A scheduled govulncheck run between merges could catch new advisories before they turn main
  red at a release deadline.
