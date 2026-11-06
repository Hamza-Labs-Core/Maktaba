---
schema: 1
kind: pr
pr: 538
title: mDNS through the system responder on Linux and Windows
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge c23d9835. Following #520 (macOS dns-sd), `register` picks a responder: darwin -> dns-sd; any Unix with the avahi-daemon socket -> `avahi-publish-service` (same stdin-lifetime script, restart, TXT re-announce); Avahi without avahi-utils -> zeroconf under `<host>-maktaba` (`LocalHostName` follows); no Avahi -> zeroconf. Windows -> DnsServiceRegister via x/sys/windows (request pinned with runtime.Pinner until the callback; one NewCallback per process; refused registrations retried; deregistration waits up to 2 s); no API -> zeroconf.
- Subprocess, not D-Bus (godbus was only in go.sum). New CI job `unit / mdns-windows` (filter `mdns_windows`) proved real register/withdraw and multicast resolution on windows-latest.

## Open
- systemd-resolved with `MulticastDNS=yes` still got zeroconf beside it, and a home name with "." was not escaped in the Windows instance name (both handled in #551).
