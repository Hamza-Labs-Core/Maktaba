---
schema: 1
kind: pr
pr: 856
title: The home's side of the plans: cap, basic licenses, the apps' words (story 50.19, part 4)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [832, 849, 854, 845]
---

## What happened
- Story 50.19, part 4:
  - a relayed session on a capped plan asks the streaming service for at most the cap less 10 % (it transcodes above it), and the master playlist lists only what fits;
  - the web port keeps the relay's mark and cap only from the relay agent, on `/stream/` too;
  - the home reads `basic` licenses as its home tier;
  - the web, Apple and Android apps name the relay's three refusals.
- #845 merged after one clean run: integration took 4m23s (api 263s), against its earlier 5m15s. Main after #781: 3m09s. It is runner variance near the 5-minute budget.

## What we learned
- The web port's `/stream/` proxy let a LAN client's `X-Maktaba-Via` through; only `/api/` dropped it. Any header that means "from the relay" must be filtered on every proxy that forwards it.
- A license tier alias belongs after the signature check, in a local variable. `Verify` takes a pointer, and changing the signed struct breaks the next verify of the stored copy. The stored row's `tier` must be the normalised one too, because the licenses table has a CHECK.
- The apps' error kinds are shared words (web, Apple, Android). A new server answer gets a kind per app, read from both `code` (the home) and `error` (the relay).
- The desktop player reads the Android client's `ErrorKind` and its `StringsTest` wants a sentence for every kind: CI's Desktop Player job failed on the three new ones until `Strings.kt` had them in English and Arabic. A new `ErrorKind` needs the desktop player's tests run too.
- zsh has no `PIPESTATUS`: check a command's exit code without a pipe.
- Before blaming a change for a local failure, run the same test on a clean origin/main checkout: here a full disk failed `cmd/maktaba-server`'s restore tests on both.

## Open
- The cloud issuing `basic`, once this release's homes are out. The `relay-plans` rehearsal (T13).
