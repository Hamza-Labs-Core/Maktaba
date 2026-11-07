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
- `load-small` failed twice (CPU-seconds per Mbit +54 %, then +77 % over main's last). It was the gate, not the relay. The relay code was identical on main at `2f242fd0a` and `89348c1cd` and in #798's two runs, and those four measured 0.00048 to 0.00085 (a 1.78× spread). A benchmark of the tunnel's data path (the real `relay.Tunnel` to `cloudlink.Multiplexer`, 1 MiB) showed no difference between main and this branch (benchstat p=0.48, n=10, the same 322 allocs per op). Its CPU profile names no direct-access code: the rehearsal homes don't announce `direct`, so the tunnels never turn it on.
- The gate now reads the relay containers' own CPU time from their cgroups (`cpu.stat` `usage_usec`) at the ends of five one-minute rounds and compares the median, only with a baseline measured the same way (`method` in the baseline). The threshold stays at 20 %.

## What we learned
- An authoritative name server must answer CAA itself: CAs ask the delegated servers, not the registrar. CAA lookups also climb the tree, so the `<home id>.<domain>` level must exist (NODATA, not NXDOMAIN). `x/net/dns/dnsmessage` has no CAA type; send it as an `UnknownResource` of type 257.
- `stores` imports `direct`, so code that needs both (the worker's jobs, the relay's handler) lives outside `direct`: `internal/directjobs`, `relay.DBDirect`.
- The cloud's flat TOML reader trims quotes only at the ends of a value, so a list arrives as `a", "b`; `parseList` reads both forms. Secrets (EAB HMACs) come from the environment only.
- A CPU regression gate on a shared runner needs a counter, not samples: `docker stats --no-stream` is an instant's share, and integrating those over one run moved 1.8× on the same code. Measure the process's own CPU time and take a median of rounds. Before changing a threshold or rerunning, check the gate's spread on unchanged code from main's past artifacts.
- A watcher that merges on green would have merged a lucky rerun: when a gate fails, stop the watcher before rerunning anything.
- Write the merge resolver so it checks every file before the commit: one regex that failed on the Arabic CHANGELOG let a merge with markers be committed (caught before pushing, folded back in).

## Open
- Parts 2 and 3 as above. The owner's direct-name domains, their NS delegation, and the ACME accounts (EAB for ZeroSSL and Google) are on the go-live list.
