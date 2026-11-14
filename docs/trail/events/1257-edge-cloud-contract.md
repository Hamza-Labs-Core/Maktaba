---
schema: 1
kind: pr
pr: 1257
title: "The broadcast edge's v1 cloud contract; one publisher key"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1257 merged as 0dcfe2481. The edge's posts to the cloud follow one signed v1 contract
  (`X-Maktaba-Edge-Time` in ms; a base64url signature over
  `maktaba-broadcast-edge-v1\n<METHOD>\n<path>\n<ms>\n<hex sha256 body>`), and the edge's plan
  lookups use 50.20's entitlements instead of a separate plan table.
- One publisher key everywhere: the home's server id. The cloud refuses to mint an ingest token
  whose `pub` differs from `home`, and the edge rejects such a token (401 `bad_token`). The Go
  and TypeScript vectors, the edge's test helpers, its 1,000-viewer rehearsal script and
  `docs/broadcast-edge.md` say the same.

## What we learned
- #1242 had minted ingest tokens with the owner's user id, while 50.20's usage intake and 50.26's
  viewer access key by server id. An owner id would also mix the data of every home an owner
  has. Pick the key a plan is entitled by, and enforce it on both ends.
- Every cloud rehearsal failing at once was a compile error in a shared rehearsal package (a
  stand-in edge still calling replaced functions), not the scenarios themselves.

## Keep doing
- Shared test vectors across Go and TypeScript catch a contract drifting on one side.

## Open
- Nothing.
