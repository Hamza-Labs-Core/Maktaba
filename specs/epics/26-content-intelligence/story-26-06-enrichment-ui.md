# Story 26.6 — Enrichment review UI

## Description

Surface the enrichment candidates from
[Story 26.5](story-26-05-web-metadata-enrichment.md) as **suggestions**
the user reviews, not as faits accomplis. On a video's detail page (and
in a library-wide "Review matches" queue), the user sees:

> **We found this might be _The Matrix (1999)_** — 92% match
> [Accept]  [Dismiss]  [Search manually…]

Accepting promotes the candidate's fields onto the video (skipping
user-owned fields per provenance). Dismissing hides the candidate.
Manual search lets the user override the automatic match by querying a
provider directly. For series, a **batch accept** applies the matched
episode metadata for the whole show at once.

This story owns the **API endpoints** (Go, `api/`) and the **web UI**
(React, `web/`). The decisions are persisted in `enrichment_decisions`
and `media_field_provenance` (slot 0078).

## Acceptance criteria

- `GET /api/videos/{id}/enrichment` returns ranked candidates with
  provider, mapped fields preview, confidence, and which fields each
  candidate would change vs. which are user-owned (and thus protected).
- **Accept** (`POST …/accept` with a chosen `external_id`) promotes that
  candidate's fields to `videos`, **skipping** any field in
  `media_field_provenance` marked user-owned; the response enumerates
  applied vs. skipped fields. The applied fields are recorded as
  `origin='enrichment'` in provenance (so they remain replaceable, and a
  later user edit flips them to user-owned).
- **Dismiss** (`POST …/dismiss`) hides all candidates for the video (or a
  single `external_id`); dismissed matches are not re-surfaced unless the
  user runs a manual search or re-enrich.
- **Manual search** (`POST …/search` with `{query, year?, provider?}`)
  runs a fresh provider search (rate-limited, cached) and returns fresh
  candidates without auto-applying anything.
- **Batch accept for a series** (`POST /api/series/{id}/enrichment/accept-all`)
  applies the best episode match to every episode in the series in one
  operation, honouring per-episode provenance, and returns a per-episode
  applied/skipped/failed summary.
- Every accept is **reversible**: a "Revert to original" action restores
  the pre-accept values from provenance history.
- The UI shows a confidence indicator, a side-by-side "current vs.
  proposed" diff, and clearly marks protected (user-owned) fields as
  "won't change".
- A library-level review queue lists videos with pending high-confidence
  matches, supports keyboard-driven accept/dismiss, and reflects
  per-library `settings.enrich.auto_accept_threshold` (default off; when
  set, matches above it are pre-applied and shown as "auto-applied,
  undo?").
- All endpoints enforce the existing library ACL
  ([`api/internal/handlers/libraryacl`](../../../api/internal/handlers/libraryacl)):
  only users who can edit a library can accept/dismiss.

## Test cases

- `test_get_candidates_marks_protected_fields` — user-edited title →
  candidate response flags `title` as protected.
- `test_accept_applies_and_skips` — accept → non-protected fields
  written to `videos`, protected skipped, response lists both.
- `test_accept_records_provenance` — applied fields recorded
  `origin='enrichment'`; a subsequent user edit flips them to user-owned.
- `test_dismiss_hides_candidates` — dismiss → `GET …/enrichment` returns
  empty until manual search/re-enrich.
- `test_manual_search_no_autoapply` — manual search returns candidates;
  `videos` unchanged until an explicit accept.
- `test_series_batch_accept` — accept-all over a 10-episode series →
  per-episode summary; protected fields respected per episode.
- `test_revert_restores_original` — accept then revert → `videos`
  fields restored to pre-accept values.
- `test_auto_accept_threshold` — set threshold 0.9; a 0.95 match is
  pre-applied and shown as undoable; a 0.7 match stays a suggestion.
- `test_acl_enforced` — a read-only user gets 403 on accept/dismiss.
- `test_ui_diff_renders` (web) — VideoDetail enrichment panel renders
  current-vs-proposed diff and Accept/Dismiss/Search controls.

## Edge cases

- **No candidates.** UI shows an empty state with a "Search manually"
  CTA; no error.
- **Accept then video re-parsed/re-enriched.** Accepted fields persist
  (provenance `origin='enrichment'`); re-enrich proposes refreshed
  values as a new suggestion rather than silently overwriting.
- **Conflicting accepts on a series** (an episode already individually
  accepted). Batch accept skips episodes with a newer user/enrichment
  decision and reports them as skipped.
- **Field that is user-owned on some episodes only.** Batch accept is
  per-episode; protection is evaluated per episode, not for the series.
- **Concurrent edit.** Optimistic-concurrency on `videos.updated_at`;
  an accept against a stale version returns 409 and the UI refetches.
- **Auto-applied then disputed.** Even auto-applied (above-threshold)
  fields are recorded with provenance and are revertible; the UI keeps an
  "undo auto-match" affordance for a session.
- **Dismissed but later wanted.** Re-enrich or manual search clears the
  dismissal for the video.
