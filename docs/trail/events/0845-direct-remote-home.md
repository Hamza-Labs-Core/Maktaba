---
schema: 1
kind: pr
pr: 845
title: Direct remote access, the home's side (story 50.15, part 2)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [808, 825]
---

## What happened
- Story 50.15, part 2 of 3:
  - the home's `api/internal/directaccess` (UPnP-IGD, NAT-PMP and PCP clients, CGNAT, the key and CSR, the TLS listener on :8444 proxying to the web port as a request from the internet, three loops, `/api/direct`);
  - the relay agent's direct frames and `/admin/cloud-link/direct`;
  - the cloud's `0x45 DIRECT_NAME`;
  - `rules.direct`, and the Settings → Storage & discovery card;
  - `cloud.direct` in `full`, with `remote_access` as relay-or-direct (`feature_any`, #825);
  - the help, the story, CHANGELOGs, maktaba.dev.

## What we learned
- Part 1 had a gap: the cloud checked that a CSR named `*.<home id>.<domain>`, but nothing told the home its domain. Writing the client side found it. Walk the protocol from both ends before shipping one end.
- 8443 was already the web port's HTTPS (`shared/go/ports`). Check the port registry before picking a default; the story's `:8443` became `:8444`.
- The direct listener must say where a request came from: `X-Maktaba-Via: direct`, with the client's forwarding headers dropped. Otherwise the internet reaches the machines' routes (`RefuseRelayToMachines`) and passes as a local caller.
- In the seed, a component's bundle puts it on, and `keepRequirementsOn` turns on what it requires. A component in `full` that requires `cloud.link` would have turned the cloud link on for every full home. Seed it from the relay fact, like `cloud.link`.
- At runtime nothing turns a requirement on: enabling `cloud.direct` without the link leaves it Waiting (`dependency_off`). Only bundles bring requirements.
- A new rules section needs English and Arabic words (`localizeRules.test.ts`) and a help entry (`help.coverage.test.ts`), beyond the schema.
- CodeQL flags every int → uint16/uint32 conversion it can trace back to a parse (`strconv.Atoi` → `uint16(port)`). Parse ports with `strconv.ParseUint(s, 10, 16)` and convert only inside a bounds-checked helper (`portValue`, `leaseSeconds`), returning an error for anything out of range. Write wire-format code that way from the start.
- `since` must be the next release: a release (v0.3.94) went out while this was in progress.

## Open
- Part 3: the `direct-remote` rehearsal (T06, T10, T12) and the `dns` role's deploy.
- The apps' raw-IP LAN entry pinned by home id, and `direct.rebinding`.
