---
schema: 1
kind: pr
pr: 944
title: Each home's LAN address pins stay with that home
date: 2026-11-08
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [942]
---

## What happened
- An automated review on #942 asked whether the Android `PinnedNames` should tie each pin to its home. A read found a real cross-home bug in both apps.
  - The `local_ip` pins (address and port to the name the home's certificate must hold) were one map keyed by `address|port`, across every home and every saved account.
  - When two homes on the same private address and port each sat on their own LAN, the last one listed won.
  - On the second home's network, the app's try of the first home's LAN entry then took the second home's certificate.
- The fix, the same on Android and Apple:
  - pins are kept per home, and a new list replaces that home's own;
  - the name's second label must be `direct.HomeID(server_id)`, which the apps now derive;
  - an address two homes pin is pinned to neither, so it is checked by IP, refused, and the app goes on to the next connection.
- A Go test pins `HomeID("s")` to the value both apps' tests use.

## What we learned
- A trust override keyed by address alone is shared state across homes. Two private networks reuse the same addresses, so a key that ignores which home it belongs to lets one home answer for another.
- OkHttp's pool keeps a connection's earlier hostname check. A test that changes pins between calls must evict the pool (`connectionPool.evictAll()`), or the second call never handshakes.
- The Apple test's certificate encodes the home id. Changing the id rule meant a new test CA and leaf, which are kept to 90 days as Apple requires.

## Keep doing
- Check, in each app's own tests, any value both the apps and the cloud derive, and add a Go test that holds the same value.

## Open
- An address that two of the user's homes share loses its by-IP path for both, and they fall back to the LAN name, direct or relay. A per-home client would keep it; for now this is a deliberate fail-closed trade.
