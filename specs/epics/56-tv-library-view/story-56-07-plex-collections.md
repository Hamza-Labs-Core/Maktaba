# Story 56.07 — Plex collections, read into Maktaba

> Epic 56 · Phase 2 · Depends on: 56.06 (slot 0192) · Needed by: 56.08

## Why

A home that used Plex before Maktaba has collections there that people made by hand over
years. Maktaba already indexes Plex (`plex_index` fills `plex_items`, rating key ↔ video) and
writes its own season and topic collections into Plex (`plex_collection`, `plex_groups`), but
never reads Plex's collections back. Moving from Plex should not lose them.

## Description

### 1. The stage

`plex_collections_import`, in `pipeline/src/maktaba_pipeline/extensions/plex/`, runs after
`plex_index` and on the 6-hour reconciler, for every Plex section mapped to a Maktaba library:

1. Reads the section's collections (`/library/sections/{key}/collections`) and each one's
   children (`/library/collections/{ratingKey}/children`), through the existing Plex client and
   its stored token (never logged, never in an error).
2. Skips collections Maktaba itself wrote (`external_links` with target `plex_collection` and
   that rating key), so nothing loops.
3. Maps each child's rating key to a video through `plex_items`; children with no video are
   counted, not imported. A collection with fewer than 2 mapped videos is skipped.
4. Writes a collection with `source_kind = 'plex'`, `source_ref = <server>:<ratingKey>`, the Plex
   title, Plex's summary as `description`, and Plex's order (its custom order when set, else
   Plex's sort title order). A Plex smart collection is read as its current members on each run.
5. Keeps admin overrides and deletes collections that vanished from Plex, as 56.06 does.

### 2. Admin view

Manage libraries (web) shows, per library, "Plex collections: {n} imported, {m} videos not in
Maktaba" with a link to the skipped list, so an admin knows which Plex items are missing.

### 3. Turning it off

Component part `collections.plex` (Epic 51), default on when Plex is linked.

## Acceptance criteria

- **Given** a Plex section with a manual collection of 8 items, 7 indexed in Maktaba, **when** the
  stage runs, **then** a Maktaba collection with those 7 in Plex's order exists, and the admin page
  says 1 video is not in Maktaba.
- **Given** a Plex collection Maktaba wrote, **then** it is not imported.
- **Given** a collection deleted in Plex, **when** the stage runs again, **then** the Maktaba
  collection is deleted.
- **Given** Plex unreachable, **then** the stage parks with the queue's dependency class and changes
  nothing.
- **Given** any log line or error of the stage, **then** it holds no Plex token (a test scans them).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | pytest with a fake Plex HTTP server | manual collection 8 / 7 mapped | run | 7 in order; 1 skipped counted |
| T02 | pytest | Maktaba-written collection in `external_links` | run | skipped |
| T03 | pytest | smart Plex collection, members change | run twice | members follow |
| T04 | pytest | deleted in Plex | run | deleted |
| T05 | pytest | Plex 503 | run | parked; no writes |
| T06 | pytest | captured logs | run with a token | token absent |
| T07 | unit (web) | Manage libraries | render counts en/ar | strings |

## Strings

| Key | English | Arabic |
|---|---|---|
| `collections.badge.plex` | Plex collection | مجموعة Plex |
| `collections.kind.plex` | From Plex | من Plex |
| `libraryAdmin.plex.collections` | Plex collections: {n} imported, {m} videos not in Maktaba | مجموعات Plex: استُورد {n}، و{m} مقطعًا غير موجود في مكتبة |
| `libraryAdmin.plex.skipped` | Show the videos not in Maktaba | اعرض المقاطع غير الموجودة في مكتبة |
| `queue.stage.plex_collections_import.title` | Read Plex collections | قراءة مجموعات Plex |
| `queue.stage.plex_collections_import.desc` | Copies each Plex collection of a linked library into Maktaba. | ينسخ كل مجموعة Plex لمكتبة مرتبطة إلى مكتبة. |

## Edge cases

- **Two Plex servers mapping one library**: `source_ref` carries the server, so each server's
  collections are separate.
- **A Plex collection spanning sections**: Plex collections belong to one section; not possible.
- **Same Plex collection and YouTube playlist with the same members**: both kept; the admin can
  hide one.

## Build plan

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/extensions/plex/collections_import.py` (new), stage registry | the stage |
| `pipeline/src/maktaba_pipeline/engine/reconcile.py` | 6-hour pass |
| `api/internal/handlers/libraries/manage.go` | counts and the skipped list |
| `web/src/pages/` Manage libraries card | the line and link |
| `web/src/i18n/{en,ar}.json` | strings |
