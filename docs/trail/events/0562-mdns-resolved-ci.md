---
schema: 1
kind: pr
pr: 562
title: mDNS through systemd-resolved runs for real in CI; three bugs fixed
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 3f97c0d5f. New `mdns-linux` job in `_unit.yml` (`tools/ci/mdns-linux.sh`): resolved's mDNS on, Avahi masked, questions from a peer network namespace over a veth. Both copies run as a polkit-denied user (zeroconf `<host>-maktaba`), an allowed user, and root (RegisterService), with plain, dotted, backslash/quote and Arabic names.
- It found three bugs, fixed in both copies: zeroconf never answered A/AAAA for its own target host (`announce_hostaddr.go`); RegisterService split dotted names ("Studio 1.2"); a `\` in a home name crashed systemd-resolved 255 (`resolvedInstance` escapes `\` and `.`, at most 63 bytes).
- `tools/ci/mdns_copies.py` keeps `api/internal/discovery` and `cmd/maktaba-server/internal/mdns` identical.

## What we learned
- systemd-resolved ignores every mDNS packet from its own machine's addresses; test it from a peer network namespace.
- A code path that never ran in CI had three bugs; run it for real.
