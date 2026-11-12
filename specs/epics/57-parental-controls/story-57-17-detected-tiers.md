# Story 57.17 — Tiers detected from transcripts

> Epic 57 · Phase 2b · Depends on: 57.01 · Needed by: 57.12, 57.13
> **Status:** built (checked against the code 2026-11-12): the stage, the recompute, the review routes, and the review
> page on the web.

## Why

Most of a home's content has no rating: lectures, home videos, YouTube, recordings. A filter
that hides everything unrated leaves a child with almost nothing, and one that shows it fails
open. The home already has each video's transcript, title, description and classification. It
can work out a likely tier from them, so that unrated becomes the exception.

## Description

### 1. What is detected

A pipeline stage, `content_screen`, runs after transcription and classification. For each video
it reads the transcript (`transcript_segments`), the title and description, and the
classification (`videos.content_type`, topics and tags). It writes one row in
`video_content_signals` (slot 0224):

- `tier`: 0, 9, 13, 16, 18, or null when it cannot tell;
- `confidence`, 0 to 1;
- `categories`: how strongly each of violence, sexual content, coarse language, drugs and
  alcohol, frightening scenes and mature themes shows, with up to three transcript times each
  as evidence;
- `model` and `input_hash`, so a changed transcript or model reruns it and nothing else does.

`content_screen` is a core stage: it runs inside the pipeline, after the transcript's second
listen, on the home only. It is not a plugin. It asks the home's own model through
`pipeline/src/maktaba_pipeline/llm`: the model of the `tagcheck` setting, the one the home's
other checks ask. The transcript never leaves the home (README Q11). A home with no model gets
no row and no detected tiers. Arabic and English are the languages it reads; a transcript in
another language, and a video with no transcript, get a row that says it was not checked.

### 2. How it feeds the tier

- `detected` is a metadata source in README §6.1 step 3. It counts only when `confidence` is at
  or above the line (`app_settings` `parental.detect_min_confidence`, 0.8 at first).
- The strictest of the step-3 sources wins, so detection can raise a metadata rating and never
  lower one.
- An admin's tier on the video or its container always wins (steps 1 and 2).
- Below the line the video stays unrated and goes on the **review list**.
- The stage writes only `video_content_signals`, under its own Postgres role
  (`maktaba_fp_content_screen`). The API's `parental_recompute` job writes `videos.age_tier`
  (README §2.4).
- Once an admin has answered a video on the review list, its signal rates nothing.

### 3. Review

`GET /api/parental/review` (admin: every library; a guardian: the libraries their children can
read; anyone else 403) lists what an admin has not decided yet, with the categories and the
evidence times. Each item has a `state`, and `?state=` filters by it:

| State | Meaning |
|---|---|
| `low_confidence` | a signal under the confidence line: it rates nothing |
| `unconfirmed` | a detected tier at or above the line that no admin has answered |
| `not_checked` | no transcript, or a language the stage does not read |

Only an admin answers: `POST /api/parental/review/{video_id}` `{tier | null}` confirms, changes
or clears the tier. That is an admin tier (step 1) and marks the signal reviewed. A guardian
reads the list and cannot answer it.

### 4. What it is not

It does not cut, mute or censor anything, and it does not look at the picture. It is one source
for a tier, and it can be wrong; that is why a person's tier beats it and why low confidence
rates nothing.

## Acceptance criteria

- **Given** an unrated video whose transcript clearly has coarse language, **when** the stage
  runs with confidence 0.9, **then** its tier is the detected one with source `detected`.
- **Given** confidence 0.5, **then** the video stays unrated and is on the review list.
- **Given** metadata says 13 and detection says 16, **then** 16; **given** metadata 16 and
  detection 9, **then** 16.
- **Given** an admin sets 9 on that video, **then** 9 with source `admin_video`.
- **Given** the transcript is unchanged, **when** the stage is queued again, **then** it does
  no model call.
- **Given** a viewer, **when** they open the review list, **then** 403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | pytest | a fixed set of English and Arabic transcripts with expected tiers, model faked | stage | rows as expected; hash skip works |
| T02 | pytest | no transcript; unsupported language | stage | null tier, not checked |
| T03 | Go unit | detected against metadata and admin tiers, above and below the line | `Resolve` | the rules of section 2 |
| T04 | Go Postgres | signals at several confidences | review route as admin, guardian, viewer | list; scoped list; 403 |
| T05 | Go Postgres and SQLite | migration 0224 up and down | — | clean |
| T06 | role grants test | the stage's role | write `videos.age_tier` | refused |

## Edge cases

- **A long transcript**: scored in windows; the video takes the strictest window that passes
  the confidence line.
- **Music and Quran recitation**: the classification marks them; the stage skips them and the
  library default applies.
- **A model change**: every video is queued again at low priority; old tiers stay until the new
  row lands.

## Build plan

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/classify/content_screen.py` (new), tests | the stage |
| `tools/plugins/roles.py` and the generated `fp_roles` migration | the stage's grants |
| `api/internal/parental/resolve.go` | the `detected` source |
| `api/internal/handlers/parental/review.go` (new) | the review list |
| `shared/db/migrations/0224_content_signals{,.sqlite}.sql`, `MANIFEST.md` | table, check |
| `shared/api/openapi.yaml`, `openapi.ar.json` | route, English and Arabic |
