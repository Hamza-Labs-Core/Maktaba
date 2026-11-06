---
schema: 1
kind: pr
pr: 533
title: The tag guard downloaded Sigstore to compare version strings
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Run 41476114340 attempt 1 (PR #525) failed `node-packaging / installer tests (ubuntu-22.04)`: `go run ./cmd/release-order` could not fetch `google.golang.org/protobuf@v1.36.11` (proxy INTERNAL_ERROR, no cache). #525's agent re-ran it to green, against the rules.
- Cause: `releaseorder` imported `shared/go/releaseverify` (Sigstore, gRPC, protobuf: about 40 modules) only for version compares, in this test and in every release's tag guard.
- Fix (#533, merge 43dff42d): stdlib-only `shared/go/releaseverify/version` (Compare, IsPrerelease, SameLine, ValidTag moved unchanged; old names kept as wrappers). releaseorder imports only it. tag-guard-test.sh runs `GOPROXY=off -mod=readonly` with an empty GOMODCACHE (the old import fails 11/11); the release guard step runs `GOPROXY=off`.
- The first push was blocked by the new affected-tests gate (#516) until tests were added.

## What we learned
- A heavy import for one small helper pulls a whole module graph into every run that builds it, and a proxy hiccup then fails the release. Keep small helpers in stdlib-only packages and prove it with `GOPROXY=off`.

## Open
- `wheel + archive`'s `gen-release-json` still fetched the full module set each run (fixed in #546).
- `TestRootStoreRefreshKeepsLastGood` failed on the owner's Mac on unchanged main (fixed in #546).
