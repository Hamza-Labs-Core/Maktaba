# Plan 32.12 — Docs and runbook — implementation

> Implementation plan for [story-32-12](story-32-12-docs-runbook.md). No code beyond links.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One operator doc, `docs/tv-cluster-and-remote.md`, with anchors per section. | The admin page links straight to the relevant part. |
| D2 | Written with each phase (A: cluster, B: Cloudflare, C: apps). | Docs match what is shipped. |
| D3 | The spike's measured numbers go into the doc, not guesses. | Latency and token behaviour are not documented by Cloudflare. |

## 1. Files

**Create**
- `docs/tv-cluster-and-remote.md` — sections of story AC1 (§1 enable a node … §9 spike findings)

**Modify**
- `web/src/pages/Admin/LiveTV.tsx` — "Help" links per section (`#streaming-hosts`, `#cloudflare`, `#costs`, `#failures`)
- `api/internal/handlers/rules/schema.json` — check every `tv_cluster` / `tv_remote` description (en + ar) against the doc
- `shared/db/migrations/MANIFEST.md` — rows 0120, 0122, 0123 land with their plans (32.6, 32.1, 32.7); this plan checks they are there
- `specs/epics/32-tv-streaming-cluster-remote/README.md` — the owner questions were decided on 2026-10-18 ("Open questions — decided"); this plan records the spike answers to Q2/Q3 and revisits any "accepted default" the measurements contradict

## 2. Outline

1. Streaming on another machine — prerequisites (joined with Epic 31, storage readable, ffmpeg
   with NVENC/VideoToolbox), `maktaba-server serve --role streaming` on the node, switches, the
   Streaming hosts page, what "unfit" reasons mean.
2. Placement — policies, `spill_at`, pins, proxy vs redirect, home NIC load.
3. Failover and maintenance — what viewers see, drain, move now.
4. Cloudflare — subscription, storage (1,000 min = $5/month), token permissions (Stream Write,
   Account Analytics Read), Test connection, enabling remote, LL-HLS.
5. Costs — formula, examples, upload bandwidth, why there is no audio-only saving.
6. Remote viewers — web, apps, messages.
7. Failure playbook — Cloudflare down, `ERR_*` codes, storage full, node failure mid-push, home
   down, leaked URL (rotate signing key), leaked stream key (rotate input keys), budget reached.
8. Plex remote — Plex's own remote access over `/auto/v{n}`; not via Cloudflare.
9. Measured: latency (standard HLS, LL-HLS), token behaviour, reconnect window.

## 3. Tests

Markdown link check over `docs/` and `specs/` (relative links resolve); rules schema description
test (both languages present).
