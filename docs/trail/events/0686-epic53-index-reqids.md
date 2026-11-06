---
schema: 1
kind: pr
pr: 686
title: Epic 53 stories 53.08 and 53.09 — the log index and request ids
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The `logs` role (`logs.index`): a SQLite FTS5 index of the keeper's segments, with search, a live tail and "around" proxied by the API to admins.
- One request id (UUIDv7, `X-Request-Id`) from the web and native apps through the API to the pipeline and streaming over gRPC metadata. The web's error screens show its reference.

## What we learned
- The index keeps each line's segment and offset, not the line. Reading a compressed hour means streaming the zstd file to the offset. That is fast enough for a page of results, and it keeps `logs.db` small.
- FTS5 `unicode61 remove_diacritics` folds Latin marks only. Arabic harakat, the dagger alif and tatweel are folded in code, for the indexed text and the query alike.
- A Python unit test that calls `asyncio.run` opens a socket pair and trips the netguard. Use `@pytest.mark.asyncio` instead.
- The Core bundle refuses a switchable process (N9), so a new switchable role belongs to Full and Worker only.

## Stop doing
- Picking a port without reading the registry first (9104 was taken).

## Open
- `origin_request_id` on jobs and the streaming HTTP `rid` parameter.
