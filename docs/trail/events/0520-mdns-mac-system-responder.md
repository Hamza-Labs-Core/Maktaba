---
schema: 1
kind: pr
pr: 520
title: Maktaba renamed the Mac on the network: announce through dns-sd on macOS
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- macOS renamed "Mahmouds-Mac-Studio.local" to "...-2.local". maktaba-api and maktaba-server each ran a zeroconf responder (`lsof -iUDP:5353`) announcing via RegisterProxy with the Mac's own Bonjour name. That publishes A/AAAA records for the name with the cache-flush (owner) bit. mDNSResponder saw other answerers for its name and renamed the Mac; our servers then claimed the new name on restart.
- Fix (#520, branch fix/mdns-system-responder): register() returns an `announcement` (SetText, Shutdown). On macOS it runs `dns-sd -R` under /bin/sh, which ends it when our stdin pipe closes (Shutdown, or our process dying any way), and restarts it if it ends by itself. Elsewhere zeroconf as before. The same file lives in api/internal/discovery and cmd/maktaba-server/internal/mdns.

## What we learned
- A POSIX shell gives a background job /dev/null as stdin, so the watcher must read a copy (`exec 3<&0 ... read _ <&3`), or it kills dns-sd at once.
- Announcing the host's own name from a second responder makes the system responder rename the host.

## Open
- Linux hosts with avahi-daemon and Windows (own mDNS) had the same conflict risk and still used zeroconf (taken up in #538).
