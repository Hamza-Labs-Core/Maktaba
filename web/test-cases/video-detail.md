# Video detail

- **Route:** `/videos/:videoId` (deep link). `MediaContextCard` also links to `/videos/:videoId?tab=enrichment`.
- **Component:** `src/pages/VideoDetail.tsx` (inner `ChaptersTab`, `FilesTab`, `TranscriptTab`, `ProcessingTab`, `StatsTab`, `StatCard`); children: `src/components/MediaContextCard.tsx`, `src/components/EnrichmentPanel.tsx`, `src/pages/DisplayTitle.tsx`; `@ds/components/Tabs/Tabs` (only the active tab's content is mounted, so each tab fetches when first opened).
- **API calls:**
  - `GET /api/videos/{id}` → `Video { id, title?, filename, description?, state, detected_language?, duration_sec?, library_id?, display_title?, display_title_auto?, display_title_manual?, display_title_source? }`. Any error → "Video not found." ErrorState.
  - Watch tab: `GET /api/videos/{id}/context` (MediaContextCard) → `{ facts?: { rating?: number | Record<string, number>, runtime_min?, genres?: string[], content_rating?, cast?: [{ name, role?, entity_id? }], director?, summary?, summary_lang?, attribution?: [{ provider, url? }] }, related_in_library: [{ video_id, title, reason, via? }], more_like_this: [{ video_id, title, score? }] }`. Errors → minimal card.
  - Metadata tab (EnrichmentPanel): `GET /api/videos/{id}/enrichment` → `{ candidates: [{ id, provider, external_id, confidence, accepted, title?, year?, fields?: [{ field, current, proposed, would_change, protected }] }] }`; `POST .../enrichment/accept { external_id }`, `POST .../enrichment/dismiss { external_id }`, `POST .../enrichment/search { query }` → `{ candidates }`. Toasts on result.
  - Transcript tab: `GET /api/videos/{id}/segments` → `{ items: [{ id, seq, start_sec, end_sec, text, speaker? }] }` (first 400 rendered).
  - Chapters tab: `GET /api/videos/{id}/chapters` → `{ items: [{ id, seq, start_sec, end_sec, title }] }`.
  - Files tab: `GET /api/videos/{id}/subtitles` → `{ items: [{ id, language, format, source, is_default, url }] }`.
  - Processing tab: `GET /api/jobs?limit=200` → `{ items: Job[] }` filtered client-side by `video_id`; live updates via `subscribe("library/{library_id}")` in `src/lib/ws.ts` (only when `library_id` is set), event `payload.job`.
  - Statistics tab: `GET /api/videos/{id}/stats` via `analyticsApi.videoStats` (`src/lib/analytics.ts`) → `VideoStats { total_views, unique_viewers, avg_completion, avg_watch_sec, completion_rate, last_watched_at?, viewers?: [{ user_id, username, times_watched, total_watch_sec, best_percent }] }`.
  - DisplayTitle (admin): `PATCH /api/videos/{id} { display_title: string | null }` → updated `DisplayTitleVideo`.
- **Access:** signed-in user (`RequireAuth`). Server ACL-scopes the video. Admin-only in the UI: DisplayTitle edit/reset (`canEdit = user.is_admin`), per-user stats table (also needs `viewers` from the server). EnrichmentPanel accept/dismiss/search are shown to everyone, but the server requires an editor (admin in v1).
- **Existing tests:** no `VideoDetail.test.tsx`. `src/components/EnrichmentPanel.test.tsx` — covers headline + diff + protected marker, and empty state with manual search CTA. `src/pages/DisplayTitle.test.tsx` — covers read-only view for viewers with source badge, and admin set-by-hand + reset. Gaps: the page itself, all other tabs, MediaContextCard, errors, permissions, crash fallback.

## Summary

| ID     | Title                                                             | Category      | Priority | Covered by existing test?                                                     |
| ------ | ----------------------------------------------------------------- | ------------- | -------- | ----------------------------------------------------------------------------- |
| VID-01 | Header, badges, tabs and Play link render                         | Happy path    | P1       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-02 | Null list fields across tab endpoints render empty states         | Empty state   | P1       | Yes (`VideoDetail.test.tsx`; it.todo: fetch failure still looks like empty)   |
| VID-03 | MediaContextCard null / missing lists and facts                   | Empty state   | P1       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-04 | EnrichmentPanel `candidates: null` / `fields: null`               | Empty state   | P1       | Yes (`VideoDetail.test.tsx`; it.todo: load failure shows No suggestions)      |
| VID-05 | Stats with missing numbers and `viewers: null`                    | Empty state   | P1       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-06 | Render crash shows route error fallback; Back still works         | Error         | P1       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-07 | Video the user has no ACL access to                               | Permissions   | P1       | Yes (`VideoDetail.test.tsx`; e2e-only: real API refuses the tab endpoints)    |
| VID-08 | Non-admin: no title edit, no per-user stats, no enrichment writes | Permissions   | P1       | Yes (`VideoDetail.test.tsx`; it.fails: enrichment writes shown to non-admins) |
| VID-09 | Signed-out user is sent to `/login`                               | Permissions   | P1       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-10 | Unknown id (404) shows "Video not found."                         | Error         | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-11 | 500 / network error is not shown as "not found"                   | Error         | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-12 | Loading state for the header and each tab                         | Loading       | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-13 | Transcript: timestamps link to the player at `?t=`                | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-14 | Transcript over 400 segments                                      | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-15 | Chapters list and links                                           | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-16 | Files tab subtitles list and default badge                        | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-17 | Processing tab rows and live updates                              | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-18 | Stats tab cards; admin per-user table                             | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-19 | Stats error shows ErrorState inside the tab                       | Error         | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-20 | MediaContextCard full card: rating, cast, attribution, related    | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-21 | "Find metadata" opens the Metadata tab                            | Navigation    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-22 | Related link switches to another video and reloads everything     | Navigation    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-23 | Enrichment accept / dismiss / manual search                       | Happy path    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-24 | DisplayTitle validation: empty, whitespace, 200-char cap, error   | Validation    | P2       | Yes (`VideoDetail.test.tsx`)                                                  |
| VID-25 | Arabic titles, description, transcript and UI in Arabic           | RTL           | P2       | Yes (`VideoDetail.test.tsx`; it.todo: RTL arrow keys in DS Tabs)              |
| VID-26 | Tabs keyboard: arrows, Home/End, focus                            | Accessibility | P3       | No                                                                            |
| VID-27 | Phone width: tabs and tables fit                                  | Responsive    | P3       | No                                                                            |
| VID-28 | Light/dark theme readability                                      | Theme         | P3       | No                                                                            |
| VID-29 | Hardcoded English strings in Arabic mode                          | RTL           | P3       | No                                                                            |

## Cases

### VID-01 — Header, badges, tabs and Play link render

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `GET /api/videos/v1` → `{ "id": "v1", "title": "Lecture 1", "filename": "l1.mp4", "description": "Intro talk", "state": "ready", "detected_language": "en", "library_id": "lib-1", "display_title": "Lecture 1", "display_title_source": "rules" }`; `/context` → `{ "related_in_library": [], "more_like_this": [] }`.
- **Steps:**
  1. Open `/videos/v1`.
- **Expected:** h1 "Lecture 1" (`dir="auto"`); badges "en" and "ready" (success tone); DisplayTitle "On-air title: Lecture 1" + "by the rules"; a tablist named "Lecture 1" with tabs Watch, Metadata, Transcript, Chapters, Files, Processing, Statistics; Watch selected; description "Intro talk"; link "▶ Play" `href="/videos/v1/watch"`. With `title: null` the h1 shows "l1.mp4".

### VID-02 — Null list fields across tab endpoints render empty states

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`; it.todo: fetch failure still looks like empty)
- **Preconditions / data:** video as VID-01. For each endpoint run with the list field `null` and with the key missing (`{}`):
  - `/segments` → `items` → Transcript shows "No transcript available."
  - `/chapters` → `items` → Chapters shows "Nothing here yet."
  - `/subtitles` → `items` → Files shows heading "Subtitles" + "Nothing here yet."
  - `/api/jobs?limit=200` → `items` → Processing shows "Nothing here yet."
    Also each endpoint rejecting (500) → same empty states.
- **Steps:**
  1. Open `/videos/v1`; click each tab.
- **Expected:** every tab shows its empty state; no crash, no blank panel. Note: a failed fetch looks the same as "no data" — wanted: an error message on failure (currently errors are swallowed).

### VID-03 — MediaContextCard null / missing lists and facts

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/context` → each of:
  - (a) `{ "related_in_library": null, "more_like_this": null }`
  - (b) `{}`
  - (c) `{ "facts": { "genres": null, "cast": null, "attribution": null, "rating": null }, "related_in_library": [], "more_like_this": [] }`
  - (d) `{ "facts": { "summary": "S", "cast": [ null, { "name": "A" } ] } }`
  - (e) 500 / network error
    List fields: `related_in_library`, `more_like_this`, `facts.genres`, `facts.cast`, `facts.attribution`.
- **Steps:**
  1. Open `/videos/v1` (Watch tab).
- **Expected:** (a)(b)(c)(e) the card shows "No web metadata yet for this video." and a "Find metadata" link; no crash. (d) wanted: summary "S" and "Cast: A" (today `c.name` on `null` throws and crashes the page — likely bug).

### VID-04 — EnrichmentPanel `candidates: null` / `fields: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`; it.todo: load failure shows No suggestions)
- **Preconditions / data:** `/enrichment` → (a) `{ "candidates": null }`, (b) `{}`, (c) 500, (d) `{ "candidates": [ { "id": "c1", "provider": "tmdb", "external_id": "tmdb:1", "confidence": 0.8, "accepted": false, "fields": null } ] }`; also `POST .../search` → `{ "candidates": null }`. List fields: `candidates`, `candidates[].fields`.
- **Steps:**
  1. Open `/videos/v1`; click "Metadata". For the search case type "x" and click "Search manually".
- **Expected:** (a)(b)(c) "No suggestions" / "Nothing found yet. Search with your own words, or search again from what Maktaba knows about this video."; (d) headline "We found this might be tmdb:1" + "80% match", no diff table, Accept/Dismiss shown; search with null → empty state. No crash. (c) wanted: an error message rather than "No suggestions".

### VID-05 — Stats with missing numbers and `viewers: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** admin; `/stats` → (a) `{ "total_views": 0, "unique_viewers": 0, "avg_completion": 0, "avg_watch_sec": 0, "completion_rate": 0, "viewers": null }`; (b) `{}`. List field: `viewers`.
- **Steps:**
  1. Open `/videos/v1`; click "Statistics".
- **Expected:** (a) five cards: "0", "0", "0%", "0m", "0%"; no per-user `details`. (b) no crash; wanted: "0" / "—" instead of "undefined" and "NaN%" (today `String(undefined)` and `formatPercent(undefined)` print those — likely bug).

### VID-06 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** full app routes inside `AppShell`; start at `/library` with one video card `v1`. Make the page throw: `/context` → VID-03 (d) (`cast: [null]`), or mock `MediaContextCard` to throw.
- **Steps:**
  1. Open `/library`; click the `v1` card.
  2. Press browser Back.
  3. Click the "Search" nav link.
- **Expected:** step 1 shows a route-level error fallback inside the shell (not a blank page). Step 2 renders the Library without a reload; step 3 renders Search.

### VID-07 — Video the user has no ACL access to

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`; e2e-only: real API refuses the tab endpoints)
- **Preconditions / data:** non-admin; `GET /api/videos/v-secret` → 404 (or 403) problem+json `{ "title": "video not found", "status": 404 }`. E2e: real API with a user lacking ACL on the video's library.
- **Steps:**
  1. Open `/videos/v-secret` directly.
- **Expected:** ErrorState "Video not found." with description "video not found"; no tab, title, transcript or stats request reveals data (only the header GET is sent). E2e: `/segments`, `/context`, `/stats` for that id also refuse.

### VID-08 — Non-admin: no title edit, no per-user stats, no enrichment writes

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`; it.fails: enrichment writes shown to non-admins)
- **Preconditions / data:** `useAuth` user `{ is_admin: false }`; video with `display_title: "T"`; `/stats` returns `viewers: [ { "user_id": "u2", "username": "sara", ... } ]` (simulate a server bug); `/enrichment` → one candidate; enrichment POSTs → 403 `{ "title": "forbidden" }`.
- **Steps:**
  1. Open `/videos/v1`; check header, Statistics, Metadata.
- **Expected:** no "Change" / "Back to the automatic title" buttons; no "Per-user breakdown" even though `viewers` came back. Wanted: Accept / Dismiss / Search manually hidden or disabled for non-admins (today they are shown and give a "forbidden" error toast — likely bug). With `display_title` empty and non-admin, the DisplayTitle block is not rendered at all. Admin variant: all three are visible.

### VID-09 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** full `App`; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/videos/v1`.
- **Expected:** redirect to `/login` with `state.from = "/videos/v1"`; no video request; after sign-in the user returns to `/videos/v1`.

### VID-10 — Unknown id (404) shows "Video not found."

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `GET /api/videos/nope` → 404 problem+json `{ "title": "not found", "status": 404 }`.
- **Steps:**
  1. Open `/videos/nope`.
- **Expected:** ErrorState (kind not_found) "Video not found." / "not found". Wanted: a link back to the Library (no action today).

### VID-11 — 500 / network error is not shown as "not found"

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** (a) 500 `{ "title": "db down", "status": 500 }`; (b) fetch rejects; (c) 200 JSON body `{bad`.
- **Steps:**
  1. Open `/videos/v1`.
- **Expected:** wanted: a server-error ErrorState ("Something went wrong.") with "Try again". Today every error renders "Video not found." (kind not_found) — likely bug.

### VID-12 — Loading state for the header and each tab

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** header GET pending; then header resolved with each tab endpoint pending.
- **Steps:**
  1. Open `/videos/v1`; then click each tab.
- **Expected:** "Loading…" while the header is pending (no tabs). Each tab shows "Loading…" until its endpoint resolves. MediaContextCard renders nothing while pending.

### VID-13 — Transcript: timestamps link to the player at `?t=`

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/segments` → `{ "items": [ { "id": 1, "seq": 1, "start_sec": 65.7, "end_sec": 70, "text": "Hello", "speaker": "A" }, { "id": 2, "seq": 2, "start_sec": 3725, "end_sec": 3730, "text": "Bye", "speaker": null } ] }`.
- **Steps:**
  1. Open `/videos/v1`; click "Transcript"; click "1:05".
- **Expected:** rows "1:05 A: Hello" and "62:05 Bye" (no speaker label for null). Links `href="/videos/v1/watch?t=65"` and `?t=3725`. Click opens the player route. Note: the timestamp shows minutes over 60 ("62:05") while the library shows "1:02:05" — wanted: consistent h:mm:ss.

### VID-14 — Transcript over 400 segments

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/segments` → 450 items.
- **Steps:**
  1. Open the Transcript tab.
- **Expected:** exactly 400 `p.mkt-transcript__seg`; a muted "Load more" line. Wanted: that "Load more" is a real button that shows the rest (today it is plain text and does nothing — likely bug).

### VID-15 — Chapters list and links

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/chapters` → `{ "items": [ { "id": 1, "seq": 1, "start_sec": 0, "end_sec": 60, "title": "Opening" }, { "id": 2, "seq": 2, "start_sec": 125.9, "end_sec": 300, "title": "المقدمة" } ] }`.
- **Steps:**
  1. Open the Chapters tab.
- **Expected:** ordered list; "0:00 Opening" → `/videos/v1/watch?t=0`; "2:05 المقدمة" (`dir="auto"`) → `?t=125`.

### VID-16 — Files tab subtitles list and default badge

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/subtitles` → `{ "items": [ { "id": 1, "language": "ar", "format": "vtt", "source": "asr", "is_default": true, "url": "/api/videos/v1/subtitles/1.vtt" }, { "id": 2, "language": "en", "format": "srt", "source": "file", "is_default": false, "url": "/api/videos/v1/subtitles/2.srt" } ] }`.
- **Steps:**
  1. Open the Files tab.
- **Expected:** heading "Subtitles"; links "ar · vtt" and "en · srt" with the given hrefs; badge "default" only on the first.

### VID-17 — Processing tab rows and live updates

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** video `library_id: "lib-1"`; `/api/jobs?limit=200` → jobs for `v1` (`stage: "transcribe"`, `state: "running"`, `attempts: 1`, `estimated_remaining_sec: 90`) and one for `v2`. Mock `subscribe` from `src/lib/ws.ts`.
- **Steps:**
  1. Open the Processing tab.
  2. Emit `{ type: "job", payload: { job: { id: <same id>, video_id: "v1", stage: "transcribe", state: "failed", attempts: 2, error: "OOM" } } }`.
  3. Emit a job event for `video_id: "v2"`.
  4. Switch tab away.
- **Expected:** table named "Processing" with one row "Transcribe", "running", "1:30", "1"; `subscribe` called with `"library/lib-1"`. Step 2 updates that row to error badge "failed" + "OOM", attempts "2". Step 3 changes nothing. Step 4 calls the unsubscribe function. With `library_id: null`, `subscribe` is not called. Unknown stage "ocr" shows "ocr".

### VID-18 — Stats tab cards; admin per-user table

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** admin; `/stats` → `{ "total_views": 12, "unique_viewers": 3, "avg_completion": 64.4, "avg_watch_sec": 3720, "completion_rate": 0.25, "viewers": [ { "user_id": "u1", "username": "مها", "times_watched": 4, "total_watch_sec": 7200, "best_percent": 100 } ] }`.
- **Steps:**
  1. Open the Statistics tab; expand "Per-user breakdown".
- **Expected:** cards "12", "3", "64%", "1h 2m", "25%" with their labels; table named "Per-user breakdown" with "مها" (`dir="auto"`), "4", "2h", "100%".

### VID-19 — Stats error shows ErrorState inside the tab

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/stats` → 500 `{ "title": "stats failed", "status": 500 }`.
- **Steps:**
  1. Open the Statistics tab, then switch to Watch.
- **Expected:** ErrorState "Something went wrong." with description "stats failed" inside the tab only; header and other tabs still work.

### VID-20 — MediaContextCard full card: rating, cast, attribution, related

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/context` → `{ "facts": { "rating": { "imdb": 8.53, "tmdb": 7 }, "runtime_min": 95, "genres": ["Drama", "History"], "content_rating": "PG-13", "cast": [ { "name": "A" }, { "name": "B" } ], "director": "D", "summary": "ملخص", "summary_lang": "ar", "attribution": [ { "provider": "TMDB", "url": "https://www.themoviedb.org/movie/1" }, { "provider": "Wikidata" } ] }, "related_in_library": [ { "video_id": "v2", "title": "Part 2", "reason": "same_series" }, { "video_id": "v3", "title": "Other", "reason": "shared_cast", "via": "A" } ], "more_like_this": [ { "video_id": "v4", "title": "Similar", "score": 0.9 } ] }`. Also run with `"rating": 7.25`.
- **Steps:**
  1. Open `/videos/v1`.
- **Expected:** badges "★ IMDB 8.5 · TMDB 7.0", "95 min", "PG-13"; chips "Drama", "History"; summary with `lang="ar"` and `dir="auto"`; "Director: D"; "Cast: A, B"; link "Data from TMDB ↗" with `target="_blank"` and `rel="noopener noreferrer"`, plain text "Data from Wikidata ↗"; rail "Related in your library" with "Part 2 · Same series"-style reason text and "Other" + "Shared cast · A"; rail "More like this" with "Similar" (no reason). No "Find metadata" CTA. Numeric rating shows "★ 7.3" (toFixed(1) of 7.25 → check rounding).

### VID-21 — "Find metadata" opens the Metadata tab

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/context` → `{ "related_in_library": [], "more_like_this": [] }`.
- **Steps:**
  1. Open `/videos/v1`; click "Find metadata".
  2. Also open `/videos/v1?tab=enrichment` directly.
- **Expected:** URL becomes `/videos/v1?tab=enrichment` and the Metadata tab is selected with the EnrichmentPanel shown. Today `Tabs` uses `defaultValue="watch"` and ignores `?tab`, so the click does nothing visible — likely bug.

### VID-22 — Related link switches to another video and reloads everything

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** `/videos/v1` and `/videos/v2` with different titles; v1 context has related `v2`.
- **Steps:**
  1. Open `/videos/v1`; open the Chapters tab; go back to Watch; click related "Part 2".
  2. Press browser Back.
- **Expected:** URL `/videos/v2`; "Loading…" then h1 shows v2's title; header and context are re-fetched for v2; no v1 data remains (title, chapters, display title). Back returns to v1 with v1 data. Note: when the page reloads, the tabs reset to Watch.

### VID-23 — Enrichment accept / dismiss / manual search

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** admin; `/enrichment` → one candidate `{ "external_id": "tmdb:42", "title": "Dune", "year": 2021, "confidence": 0.873, "fields": [ { "field": "title", "current": "dune.mkv", "proposed": "Dune", "would_change": true, "protected": false }, { "field": "description", "current": "mine", "proposed": "theirs", "would_change": false, "protected": true } ] }`; accept/dismiss → 204; search → `{ "candidates": [ { "external_id": "tmdb:7", "title": "Dune 1984", "confidence": 0.5, ... } ] }`. Render within `ToastProvider`.
- **Steps:**
  1. Open the Metadata tab; click "Accept".
  2. Click "Dismiss".
  3. Type "Dune 1984" (with spaces around it) and click "Search manually".
  4. Make accept return 500 `{ "title": "accept failed" }` and click "Accept".
- **Expected:** headline "We found this might be Dune (2021)" + "87% match"; diff table "Current vs. proposed" with "Won't change" on the description row. Step 1: `POST /api/videos/v1/enrichment/accept { external_id: "tmdb:42" }`, toast "Metadata applied.", list re-fetched; Accept shows loading and Dismiss is disabled while busy. Step 2: dismiss POST + toast "Suggestion dismissed.". Step 3: body `{ query: "Dune 1984" }` (trimmed); the new candidate shows. "Search manually" is disabled while the box is empty or only spaces. Step 4: error toast "accept failed". Wanted: pressing Enter in the search box runs the search (not a form today).

### VID-24 — DisplayTitle validation: empty, whitespace, 200-char cap, error

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`)
- **Preconditions / data:** admin; video `display_title: "Auto"`, `display_title_source: "rules"`. `PATCH /api/videos/v1` → (a) 422 problem+json `{ "title": "invalid", "detail": "title too long" }`, (b) success.
- **Steps:**
  1. Click "Change"; clear the box.
  2. Type three spaces.
  3. Paste 250 characters.
  4. Type " New " and Save with (a), then (b).
  5. Click "Change", then press Escape / click "Cancel".
- **Expected:** step 1–2: Save disabled. Step 3: input holds at most 200 chars (`maxLength`). Step 4 (a): `role="alert"` text "title too long", still editing. (b): wanted: body `{ display_title: "New" }` (trimmed; today the raw " New " is sent — likely bug); edit box closes; h1 area updates via `onSaved`. Step 5: Cancel leaves edit mode with the old title; wanted: Escape also cancels (not handled today). Unknown `display_title_source: "foo"` must not show a raw key "video.displayTitle.source.foo".

### VID-25 — Arabic titles, description, transcript and UI in Arabic

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`VideoDetail.test.tsx`; it.todo: RTL arrow keys in DS Tabs)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; video `{ "title": "السيرة النبوية ( 18 )", "description": "وصف قصير", "detected_language": "ar" }`; Arabic transcript segments and chapters.
- **Steps:**
  1. Open `/videos/v1`; go through Watch, Transcript, Chapters.
- **Expected:** `<html dir="rtl" lang="ar">`; h1, description, transcript rows, chapter titles all have `dir="auto"` and render right to left with "( 18 )" not flipped; tab labels from `ar.json`; tab arrow-key order follows visual order in RTL (wanted: ArrowLeft moves to the next tab in RTL; today ArrowRight always means next — check).

### VID-26 — Tabs keyboard: arrows, Home/End, focus

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** VID-01 data.
- **Steps:**
  1. Tab to the "Watch" tab; press ArrowRight, End, Home, ArrowLeft.
- **Expected:** focus and selection move: Metadata, Statistics, Watch, Statistics; only the selected tab has `tabIndex=0`; each tab has `aria-controls` pointing to a `role="tabpanel"`; focus ring visible. Only the selected panel's content is in the DOM.

### VID-27 — Phone width: tabs and tables fit

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375x812; long title; Processing and Statistics tabs with data.
- **Steps:**
  1. Open `/videos/v1`; open each tab.
- **Expected:** no horizontal page scroll; the seven tabs scroll or wrap inside the tablist; tables scroll in their own container; Play button full width or clearly tappable.

### VID-28 — Light/dark theme readability

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; light and dark.
- **Steps:**
  1. Open each tab in both themes.
- **Expected:** badges (state, match %, "Won't change"), diff table protected rows, transcript timestamps, KPI cards and the context card are readable; no hardcoded colors.

### VID-29 — Hardcoded English strings in Arabic mode

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** Arabic locale; subtitles with `is_default: true`; jobs with stage `transcode`; video `state: "ready"`.
- **Steps:**
  1. Open Files, Processing, and look at the header state badge.
- **Expected:** wanted: "default", stage labels ("Transcode", "Probe", ...), job state and the raw video state badge ("ready") come from i18n. Today they are hardcoded English / raw keys — likely bug (low).
