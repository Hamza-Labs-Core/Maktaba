---
schema: 1
kind: pr
pr: 1358
title: "Private metrics on their own listener; analytics fold per hour (50.28 security)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1358 merged as 0fee0e742. It fixes two medium findings in code #1251 merged, both confirmed
  against the code first.
- Private metrics: the api and relay roles served the broadcast series (viewers, edge cost,
  budget) to any `/metrics` request whose Host parsed as an IP, and the host filter lets
  `/metrics` through on any Host. They now live on `server.metrics_listen_addr`
  (`MAKTABA_CLOUD_METRICS_ADDR`), which must be loopback or private in every environment and
  differ from the main and admin listeners, as the worker's control port already did. Unset,
  the private series are not served at all. Caddy's private `:9443` proxies to the relays'
  private ports; `setup.sh` adds the env to the relay env files.
- Analytics: countries and devices were folded under k over the whole requested period, whose
  edges the caller moves by the hour (`tz`, a growing today), so two overlapping periods
  differed by one hour's small groups. Each hour is now folded on its own and a period adds its
  hours, so no period and no difference of periods names a group under k.

## What we learned
- Never decide trust from a client-set header (Host included). A separate listener on a
  private address is the boundary; the connection's local address is not, when a proxy sends
  public and private traffic to the same port.
- A k-anonymity fold over caller-chosen windows leaks by differencing. Fold on a grain the
  caller cannot move (the rollup's hour) and sum folded cells.

## Open
- Owner action: set `MAKTABA_CLOUD_METRICS_ADDR` on the api hosts (relays get it from
  `setup.sh`) so private series are scraped again.
