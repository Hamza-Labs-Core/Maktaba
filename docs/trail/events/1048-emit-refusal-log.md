---
schema: 1
kind: pr
pr: 1048
title: Every refused plugin event emit is logged with its plugin, kind, size and reason
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- `events/emit` on the plugin API (`api/internal/plugins/events.go`) refused events with only the refusal code returned to the plugin, and logged nothing. Relay `publish` refusals already had a log line (#1021).
- Every refusal now logs one `plugin_emit_refused` warning, the same shape as #1021's `plugin_publish_refused`:
  - `plugin`: empty when the token names no plugin.
  - `kind`: cut to 100 bytes.
  - `size`: the payload's bytes, or the request's when it doesn't decode.
  - `reason`, `code` and `detail`: the reason, the code the plugin got, and a human detail.
  - `job_id`: when the token is a known invocation.

  The token, rate and size refusals made before the emit code runs (`server.go`) log the same way.
- The payload body is never logged. Two refusal details could quote it, so they were reworded: a decode error now names only the field and type or a byte offset, and an unknown schema key is no longer named. Only `code` reaches the plugin, so its HTTP answer is unchanged.
- `api/internal/plugins/events_log_pg_test.go` has one test per reason:
  - unknown_kind, schema, oversize, scope, token, decode, and lineage/quota/rate/loop;
  - an accepted emit, which logs no refusal.

  Each puts a unique marker in the payload and asserts that one record carries the right fields and that the marker is nowhere in the log. Restoring the old schema message, which named the key, made the schema test fail, so the marker check can fail.
- Merged as 8781b8915 on 7019fc7ed with 60 checks green. The child that wrote it handed the merge back because its session could not wait on CI; the parent's watcher merged it.

## What we learned
- A refusal's detail text is a payload leak waiting to happen: an error that echoes the input carries user data into the log. A no-payload test needs a marker string in the payload, not a check on field names.
- A child session that cannot wait on CI should say so and hand the merge back, rather than leaving the PR open with no owner.

## Open
- `encoding` (payload not UTF-8) can't happen: JSON decoding already replaces invalid bytes. `unavailable` (a database error) and `home_unreachable` (a joined machine that can't forward to its home) are logged but have no test.
