---
schema: 1
kind: pr
pr: 808
title: Direct remote access, the cloud's side (part 1)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [791, 798]
---

## What happened
- Story 50.15, part 1 of 3:
  - `internal/direct`: names, home id, the authoritative responder, the domain picker, the ACME issuer from a home's CSR, and the outside check;
  - `internal/directjobs` for the worker;
  - the tunnel's direct frames 0x40–0x44;
  - migration 00500005;
  - the home list's HTTPS `direct` entry;
  - the `dns` role and `[direct]` config;
  - `docs/cloud-dns.md`'s direct-name domains.
  
  The home's side and maktaba.dev are part 2; the rehearsal scenario and the `dns` role's deploy are part 3.

## What we learned
- An authoritative name server must answer CAA itself: CAs ask the delegated servers, not the registrar. CAA lookups also climb the tree, so the `<home id>.<domain>` level must exist (NODATA, not NXDOMAIN). `x/net/dns/dnsmessage` has no CAA type; send it as an `UnknownResource` of type 257.
- `stores` imports `direct`, so code that needs both (the worker's jobs, the relay's handler) lives outside `direct`: `internal/directjobs`, `relay.DBDirect`.
- The cloud's flat TOML reader trims quotes only at the ends of a value, so a list arrives as `a", "b`; `parseList` reads both forms. Secrets (EAB HMACs) come from the environment only.
- Write the merge resolver so it checks every file before the commit: one regex that failed on the Arabic CHANGELOG let a merge with markers be committed (caught before pushing, folded back in).

## Open
- Parts 2 and 3 as above. The owner's direct-name domains, their NS delegation, and the ACME accounts (EAB for ZeroSSL and Google) are on the go-live list.
