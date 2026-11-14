---
schema: 1
kind: pr
pr: 1348
title: "The cloud code on Samsung and LG (58.09 part 2)"
date: 2026-11-14
session: 1858de07-50bb-4568-960d-fc30a29571ce
agent: claude-code
---

## What happened
- The Samsung and LG launcher opens on the cloud code: it asks Maktaba Cloud for a code from its
  packaged page, shows it with a QR of the approve page, and polls until a phone approves it. The
  home's code and Find on this network stay as the other ways. On approval the launcher loads the
  home's `/tv/` and hands it the grant over the bridge (`signIn`, origin-checked postMessage, never
  in a URL or a log), and the home's page redeems it in its own origin (`GrantSignIn`).
- The cloud answers `/v1/tv/codes` and `/v1/tv/codes/poll` to a packaged TV app's origin only
  (`null`, `file://`, `app://…`), POST only, no credentials (`middleware.TVCORS`, wired in
  role_api.go with `WithTVOrigins`), with unit tests and a test on the api role's real router.
- Launcher unit tests, launcher e2e with dark screenshots, specs with Arabic, maktaba.dev and the
  real-device rows in samsung.md (14 to 17) and lg.md (13 to 16) shipped with it. Merged as
  02cf03760 after three merges of main (#1356, #1279, then #1357 to #1361).

## What we learned
- The cloud's `home_id` is its server id; `/api/discovery/home` answers with the cluster instance
  id. A check of one against the other fails on every real home, and a mock home that uses one id
  for both hides it. Roku had the same bug (#1354).
- Two security reviews settled how a TV knows the approving home: only by an address a certificate
  proves. Plain http and raw IP addresses prove nothing: the cloud lists the home's plain public
  address as `direct` for older apps, and a discovery `urls` list is whatever answers there says.
  The launcher tries only `grantAddresses` (the cloud's https names); Android TV (#1356) and Roku
  (#1360) follow the same rule.
- `affected-tests` flags a one-line wiring change in a file with many tests (role_api.go); a test
  on the real router that exercises the wiring is a better answer than a `No-Test-Change` trailer.

## Keep doing
- Give each identity check a test whose two ids differ, as on a real home.
- Ask for a security review of any code that hands a secret to an address, before merging.

## Open
- The real-device rows in samsung.md and lg.md, including which Origin each runtime sends.
