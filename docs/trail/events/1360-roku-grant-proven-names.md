---
schema: 1
kind: pr
pr: 1360
title: "Roku sends the cloud code's grant only to an address the certificate proves"
date: 2026-11-14
session: 67aba93e-38ee-4ea4-a1ea-fdf374e67b7c
agent: claude-code
---

## What happened
- #1360 merged as f9554b86b, a follow-up to #1354 from a second security review. #1354 still
  sent the grant to a plain http or raw-IP connection when the home answering there listed one
  of the cloud's https names among its discovery `urls`. Anyone on the TV's network could answer
  that way (the relay's name is not secret) and redeem the grant at the real home.
- `NetGrantConnections` (apps/tv/roku/source/net.brs) now keeps only the cloud's https
  connections by host name, in the cloud's order, and `code_Redeem` tries only those; the urls
  path (`NetIsApprovedHome`) is gone. Same rule as the launcher (#1348) and Android TV (#1356).
- The screen test now signs in over https: the mock home also listens on `localhost` with a
  certificate made for the run, trusted by brs-engine's `roUrlTransfer` through
  `NODE_EXTRA_CA_CERTS` (it refuses the certificate without it). The mock cloud's plain LAN
  address answers as another home listing the relay, its raw-IP addresses lead to a TCP trap,
  and T03 asserts neither is ever contacted.

## What we learned
- A discovery answer over plain http or a raw IP is the answerer's word, so nothing in it,
  `urls` included, can prove which home it is; only a certificate check on a host name can.
- brs-engine verifies TLS for real and reads `NODE_EXTRA_CA_CERTS`, so Roku tests can cover
  https flows with a throwaway CA instead of falling back to plain http.
- Deleting a test's CA file while `npm test` is running fails every later channel launch
  (each brs-engine process reads `NODE_EXTRA_CA_CERTS` at start): read a mid-run failure
  against what was changed during the run before debugging it.

## Keep doing
- Prove "never contacted" with a trap server that counts TCP connections, not by reading logs.

## Stop doing
- Accepting a server-supplied list as identity proof on a channel nothing authenticates.

## Open
- `agent-trail@v0.2.0`'s `trail check` took over ten minutes locally on 685 events (CPU-bound);
  worth profiling.
