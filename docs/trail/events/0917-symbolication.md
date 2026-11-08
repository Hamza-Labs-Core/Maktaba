---
schema: 1
kind: pr
pr: 917
title: Epic 53 story 53.16 — Apple crash reports symbolicated on the cloud
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [852]
---

## What happened
- `cloud/internal/symbols` reads a release's dSYM into a compact table per binary and architecture, keyed by the binary's UUID, with Go's standard library only (no Xcode on the cloud). `maktaba-symbols extract|upload` builds the tables where the dSYMs are and uploads only the tables. The cloud never gets the dSYM.
- New routes: `POST /v1/symbols` (a bearer token from `MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE`; 503 `symbols_off` without one) and `GET /v1/admin/symbols` (service admin only). Tables are stored in `symbol_tables`. A report's `crashes` come back symbolicated where a table exists, and the console's report page shows function (file:line) beside each raw frame. A Crash symbols page lists the tables.
- The "N reports" count next to each anonymous error group was already built by 53.17 (#852). Event 0824's open item about it was out of date.

## What we learned
- The lint step that rejects binaries also rejects Mach-O and ELF test fixtures. Keep them gzipped and unpack them in a test helper.
- Apple frames from MetricKit and PLCrashReporter are offsets into `__TEXT`. Go's `debug/macho` and `debug/dwarf` read current DWARF 5 dSYMs without cgo, so symbolication runs on Linux.
- Upload a table made from the dSYM, not the dSYM itself: a few MB per binary instead of hundreds.

## Open
- The Apple release workflow must run `maktaba-symbols upload -version vX.Y.Z <dSYMs>` with a `MAKTABA_SYMBOLS_TOKEN` secret, and the cloud's api nodes need that token provisioned (`deploy/cloud-ha`). Until both are done, uploads are off.
- T07: a canary home's report symbolicated end to end in the cloud rehearsal.
- Android mapping files and desktop stacks aren't symbolicated; the spec asks only for Apple.
