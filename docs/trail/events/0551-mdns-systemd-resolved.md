---
schema: 1
kind: pr
pr: 551
title: mDNS through systemd-resolved; dotted names stay one label
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 7221566d, follow-ups to #538, in both copies (`api/internal/discovery` and `cmd/maktaba-server/internal/mdns`).
- Linux: systemd-resolved is detected as the responder (resolve1 running, MulticastDNS yes globally and on a link). With RegisterService on its D-Bus interface and polkit `org.freedesktop.resolve1.register-service` allowing it (or root), the announcement goes through D-Bus `RegisterService` (sssqqqaa{say}) on a held-open connection (resolved drops it on close; re-registered if resolved restarts), target = resolved's LLMNRHostname. Otherwise zeroconf announces under `<host>-maktaba` and LocalHostName follows. `.dnssd` files rejected (root-only, read on start/reload). Avahi still wins when both run.
- Windows: a pktmon capture showed DnsServiceConstructInstance splits the name at every ".", escaped or not, and keeps "\" as a byte, so RFC 6763 escaping does not work; a "." in the home name becomes U+2024 on Windows. dns-sd -R, avahi-publish-service and RegisterService take the raw name. zeroconf also split dotted names; its instance is now written as miekg/dns writes a label.

## What we learned
- The Windows responder answers none of the machine's own queries, so the Windows check reads the adapter with pktmon.
- resolved drops a D-Bus service registration when the connection closes; hold the connection open.

## Open
- The resolved path had not run where resolved answers mDNS (run for real in CI by #562).
