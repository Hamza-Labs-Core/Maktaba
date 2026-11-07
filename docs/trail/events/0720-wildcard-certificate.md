---
schema: 1
kind: pr
pr: 720
title: One wildcard certificate on every relay node, by DNS-01, proven against Pebble
date: 2026-11-06
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [715]
---

## What happened
- Story 50.02: Maktaba's Caddy image (Cloudflare and acme-dns DNS modules) and Caddyfiles for one wildcard per node, the witness's certificate check, a relay.yml job that builds, checks and signs the image, and the rehearsal's cert-on-standby scenario against Pebble.

## What we learned
- Caddy keeps one certificate per name: `relay.test, *.relay.test` gives each node two certificates (the apex and the wildcard), not one with both names as the spec assumed. Both stay far inside Let's Encrypt's duplicate limit.
- Caddy finds the DNS-01 zone by an SOA lookup through its configured resolvers before it presents the record: a test DNS server must answer the zone's SOA, or Caddy fails with "could not find the start of authority" before Pebble is ever asked.
- joohoi/acme-dns answers TXT only for names under its own domain (it takes the first label as the account), so pointing Pebble at it needs a CNAME that Pebble's resolver would have to chase. A 150-line fake (the acme-dns update API plus `x/net/dns/dnsmessage`) was simpler and fully in the runner.
- The cloudflare DNS module validates the token's format at provision time: `caddy validate` needs a token-shaped test value (40 characters), not `x`.
- Pinning: Caddy v2.11.8 and v2.11.9 were days old; v2.11.7 (a month) was taken. `go list -m -json module@version` gives a module version's publish time.
