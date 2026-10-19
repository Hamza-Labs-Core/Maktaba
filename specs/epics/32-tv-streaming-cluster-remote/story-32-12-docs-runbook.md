# Story 32.12 — Docs and runbook

## Description

*As the owner, I want one page that tells me how to turn on TV streaming on another machine and
remote viewing through Cloudflare, what it costs, and what to do when something breaks.*

## Acceptance criteria

- **AC1** `docs/tv-cluster-and-remote.md` (linked from the Live TV admin page and the README of
  this epic) covers:
  1. enabling a streaming node (join first — Epic 31; then `serve --role streaming` on the node;
     the switches; what the Streaming hosts page shows);
  2. choosing the policy, `spill_at`, pins, proxy vs redirect, and the home's network load;
  3. failover behaviour and drain/move for maintenance;
  4. Cloudflare setup: a Stream subscription, buying storage (one 1,000-minute block = $5/month; it holds about two concurrent 8 h pushes,
     buy another block per two more), creating an
     API token with Stream Write and Account Analytics Read, entering it, Test connection, enabling
     remote;
  5. limits, budget and cost examples (README table); the upload bandwidth one push uses
     (≈ 6.5 Mb/s; the default 22 channels ≈ 143 Mb/s of the 1 Gb/s uplink; encoders are the limit);
  6. what remote viewers do (web, apps), what they see on each error;
  7. the failure playbook: Cloudflare down (remote unavailable; LAN unaffected), storage quota
     exhausted, ingest errors (`ERR_*` meanings), a node failing mid-push, the home down,
     rotating the signing key after a leak, rotating input stream keys;
  8. **Plex remote**: Plex Live TV outside the house is Plex's own remote access — Plex pulls the
     channel from the home's `/auto/v{n}` and serves it; Maktaba does not send Plex streams to
     Cloudflare; those viewers use tuners like local Plex viewers;
  9. the spike findings of 32.6 (latency, token behaviour).
- **AC2** The settings descriptions in the rules schema (`tv_cluster`, `tv_remote`) are short,
  plain and match the doc; English and Arabic.
- **AC3** The Live TV admin page links each section to the matching doc heading.
- **AC4** `shared/db/migrations/MANIFEST.md` has rows for 0120, 0123, 0124 when those files land (each in
  its plan's PR).

## Test cases

- **TC1** Docs link check (relative links resolve).
- **TC2** Rules schema test: every new key has a description in both languages.

## Edge cases

- **EC1** Users without an Epic 25 account: the doc explains `remote_detection =
  relay_or_public_ip` with their own reverse proxy or tunnel.

## Dependencies

All other stories (written alongside each phase).
