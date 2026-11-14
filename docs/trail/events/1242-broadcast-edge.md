---
schema: 1
kind: pr
pr: 1242
title: "The broadcast edge (story 50.21)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1242 merged as dfedf2103. A new Worker package, `web/packages/broadcast-edge/`, on Maktaba's
  Cloudflare account: homes push each HLS segment once to R2 through `ingest.maktaba.tv`, and
  viewers get playlists and segments from the edge cache (R2 on a miss), with a Stream fallback.
- Ed25519 ingest tokens and control posts signed by the cloud (`kid` for rotation), checked
  against `CLOUD_PUBKEYS`; viewer tokens are HMAC, 10 minutes, in the URL path. Seams for 50.20
  (plan push, usage windows), 50.25 (revocations, `BROADCAST_GLOBAL_OFF`), 50.26 (viewer
  tokens) and 50.28 (Analytics Engine). `docs/broadcast-edge.md` documents it.
- Cloud: `internal/broadcast` (signer, opaque channel ids, `VerifyEdgeRequest`, vectors shared
  with TypeScript); `POST /v1/broadcast/ingest-token` for a home's agent, role-tested;
  `[broadcast]` config refuses to start when enabled with no key. Cloud migration `00500016`
  reserves the `ingest`, `live` and `watch` slugs.
- CI only: `_broadcast-edge.yml` runs T01–T09 in Miniflare, wrangler dry runs, and a rehearsal
  of the real Worker with 1,000 viewers (36 home PUTs, 36 R2 reads, 11,964 cache hits). Nothing
  is deployed.

## What we learned
- Merging 50.20 into it was a real code merge (same handler package, config, integration test):
  50.21's route became `ingest.go` with `IngestDeps`/`MountIngest` beside 50.20's `Deps`.
- With `broadcast.enabled`, the API needs `broadcast.key_path`; every rehearsal and integration
  test that enables broadcasting must mount a key (`tools/rehearsal-cloud/broadcast-ingest.seed`,
  rehearsal-only) or the API exits with code 2.
- Two open PRs claimed the same cloud migration slot more than once; cloud goose allows
  missing, but give each open PR its own slot when it starts.
- Its integration job failed three times on flakes in code it doesn't touch (a port race,
  #1262; a packs goroutine race, #1278; the wall budget, #1326); each got its own fix off main.

## Keep doing
- Stack a story's dependants on its branch and merge in order; fix unrelated reds off main.

## Open
- Owner actions: Cloudflare's written confirmation of R2 + Worker delivery, R2 buckets with a
  1-day lifecycle, the RL namespaces, Worker secrets, `broadcast.key_path` and
  `edge_public_key` on the cloud, the token's R2 permission, ingest/live/watch DNS, and the
  first deploy by hand.
- The publisher key: 50.26 keys by the home's server id, #1242's ingest tokens by the owner's
  user id; reconcile in #1257.
