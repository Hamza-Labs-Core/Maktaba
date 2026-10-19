# Search

- **Route:** `/search`, optional `?q=<term>` (seeded by the header search box in `src/components/AppShell.tsx`, which navigates to `/search?q=<encoded term>`; also the global `/` shortcut focuses that box).
- **Component:** `src/pages/Search.tsx` (inner `renderSnippet`, `YouTubeSection`); uses `@ds` `Button`, `Badge`, `EmptyState`, `ErrorState`, `useToast`.
- **API calls (all via `api` in `src/lib/api.ts`):**
  - `POST /api/search { q, mode, limit: 50[, include: ["youtube"]] }` → `{ hits: [{ segment_id, video_id, start_sec, end_sec, snippet, score }], videos?: [{ video_id, title, duration_sec? }], total, youtube?: { items: [{ youtube_id, title, channel, description_snippet, thumbnail, published_at?, view_count?, match: { state, video_id? } }], reason? } }`.
  - `GET /api/search/suggest?q=<encoded>` (200 ms debounce) → `{ suggestions: string[] }` → `<datalist id="mkt-suggest">`.
  - `GET /api/search/saved` → `{ items: [{ id, q, mode }] }` on mount and after save/delete.
  - `POST /api/search/save { q, mode }`; `DELETE /api/search/saved/{id}`.
  - `POST /api/videos/{target}/import-youtube { youtube_id }` (YouTube "Import metadata to…", target from `window.prompt`).
- **State in URL / storage:** `?q=` (written with `replace: true` on submit; read once on mount); mode in `localStorage["mkt:search:mode"]` (`hybrid` | `fts` | `semantic`, default `hybrid`). YouTube toggle is component state only.
- **Access:** signed-in user (`RequireAuth`). Server scopes hits to libraries the user can access.
- **Existing tests:** `src/pages/Search.test.tsx` — covers: submit POSTs `{ q, mode, limit }` and renders a `<mark>` snippet; empty hits show "No results"; Arabic title match renders as a link to `/videos/vid-9`. Gaps: `?q=` deep link, Back/Forward, errors, null fields, suggestions, saved searches, YouTube block, mode persistence, crash fallback, a11y.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test?                           |
| ------ | ------------------------------------------------------------ | ------------- | -------- | --------------------------------------------------- |
| SRC-01 | Submit runs search and renders passage hits with time links  | Happy path    | P1       | Partial (`Search.test.tsx`: no link / time check)   |
| SRC-02 | Title matches and passages show under their headings         | Happy path    | P1       | Partial (`Search.test.tsx`: videos only)            |
| SRC-03 | Deep link `/search?q=...` runs the search on load            | Navigation    | P1       | No                                                  |
| SRC-04 | Header search while already on `/search` runs the new query  | Navigation    | P1       | No                                                  |
| SRC-05 | Back/Forward restores the query and its results              | Navigation    | P1       | No                                                  |
| SRC-06 | `hits` / `videos` null or missing render the empty state     | Empty state   | P1       | No                                                  |
| SRC-07 | YouTube `items: null`, missing `match`, null `snippet`       | Empty state   | P1       | No                                                  |
| SRC-08 | Saved `items: null` and suggest `suggestions: null`          | Empty state   | P1       | No                                                  |
| SRC-09 | Render crash shows route error fallback; Back still works    | Error         | P1       | No                                                  |
| SRC-10 | Signed-out user is sent to `/login` and keeps the query      | Permissions   | P1       | No                                                  |
| SRC-11 | Results only from accessible libraries                       | Permissions   | P1       | No                                                  |
| SRC-12 | Snippet markup is escaped; only `<mark>` is highlighted      | Validation    | P1       | Partial (`Search.test.tsx`: mark text only)         |
| SRC-13 | No results shows "No results..." empty state                 | Empty state   | P2       | Yes (`Search.test.tsx`)                             |
| SRC-14 | Blank / whitespace query does nothing; clearing removes `?q` | Validation    | P2       | No                                                  |
| SRC-15 | Pending state: button loading, no duplicate submits          | Loading       | P2       | No                                                  |
| SRC-16 | Search 500 / 400 / network / malformed JSON                  | Error         | P2       | No                                                  |
| SRC-17 | Error clears on the next successful search                   | Error         | P2       | No                                                  |
| SRC-18 | Mode select persists and is sent; change of mode             | Happy path    | P2       | No                                                  |
| SRC-19 | Suggestions debounce, encode, and drop stale answers         | Happy path    | P2       | No                                                  |
| SRC-20 | Save, run and delete saved searches                          | Happy path    | P2       | No                                                  |
| SRC-21 | YouTube block: results, in-library link, reason text         | Happy path    | P2       | No                                                  |
| SRC-22 | YouTube import prompt: cancel, success, error                | Happy path    | P2       | No                                                  |
| SRC-23 | Arabic query text, Arabic results and Arabic UI              | RTL           | P2       | Partial (`Search.test.tsx`: Arabic title link only) |
| SRC-24 | Many hits: window cap and `total`                            | Happy path    | P3       | No                                                  |
| SRC-25 | Keyboard and a11y: labels, saved delete name, status updates | Accessibility | P3       | No                                                  |
| SRC-26 | Phone width: form wraps, saved list reachable                | Responsive    | P3       | No                                                  |
| SRC-27 | Light/dark theme: `<mark>` and YouTube cards readable        | Theme         | P3       | No                                                  |

## Cases

### SRC-01 — Submit runs search and renders passage hits with time links

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `Search.test.tsx` "POSTs /api/search with { q, mode, limit } and renders server hits" checks the body and snippet text, not links or times.
- **Preconditions / data:** `GET /api/search/saved` → `{ "items": [] }`; `POST /api/search` → `{ "hits": [ { "segment_id": 42, "video_id": "vid-1", "start_sec": 72.5, "end_sec": 78.0, "snippet": "the <mark>quick</mark> brown fox", "score": 0.9 } ], "total": 1 }`.
- **Steps:**
  1. Open `/search`; type "quick" in the box named "Search" (role `combobox` because of `list=`); click button "Search".
- **Expected:** body `{ q: "quick", mode: "hybrid", limit: 50 }` (no `include`); URL becomes `/search?q=quick` (replace); one result link `href="/videos/vid-1/watch?t=72"` showing "[1:12 → 1:18]" and the snippet with "quick" inside a `<mark>`. No "Videos"/"In transcripts" headings (no title matches).

### SRC-02 — Title matches and passages show under their headings

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `Search.test.tsx` "lists title matches..." covers videos with no hits.
- **Preconditions / data:** search → `{ "hits": [ <one hit> ], "videos": [ { "video_id": "v9", "title": "Quick start", "duration_sec": 3725 }, { "video_id": "v8", "title": "No length" } ], "total": 1 }`.
- **Steps:**
  1. Search "quick".
- **Expected:** h2 "Videos" with links `/videos/v9` ("Quick start · 62:05") and `/videos/v8` (no time); then h2 "In transcripts" with the passage list. No empty state.

### SRC-03 — Deep link `/search?q=...` runs the search on load

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:search:mode"] = "fts"`; search → one hit.
- **Steps:**
  1. Open `/search?q=%D8%A7%D9%84%D8%B3%D9%8A%D8%B1%D8%A9` (السيرة) directly.
- **Expected:** input value "السيرة"; exactly one `POST /api/search` with `{ q: "السيرة", mode: "fts", limit: 50 }`; results render. No second POST on re-render (StrictMode included).

### SRC-04 — Header search while already on `/search` runs the new query

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** full app inside `AppShell`; start at `/search?q=alpha` (results for alpha); search for "beta" returns different hits.
- **Steps:**
  1. Type "beta" in the header search box (form `role="search"` in the shell) and press Enter.
- **Expected:** URL `/search?q=beta`; wanted: page input shows "beta" and a new POST for "beta" runs, showing beta results. Today the seed effect is mount-only (`didSeed`), so the URL changes but the page keeps showing "alpha" — likely bug.

### SRC-05 — Back/Forward restores the query and its results

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** full routes; search for "one" and "two" return different hits; history: `/library` → `/search?q=one` (via header search) → `/search?q=two` (via header search).
- **Steps:**
  1. Press browser Back.
  2. Press browser Forward.
  3. From `/search?q=two`, click a result (goes to the player), then press Back.
- **Expected:** step 1: URL `/search?q=one`, input "one", results for "one". Step 2: "two" again. Step 3: back on `/search?q=two` with input and results for "two" (re-run on mount). Today steps 1–2 change the URL but not the input or results (currently unhandled — likely bug). Also: submits inside the page use `replace: true`, so in-page searches never create history entries — wanted: push, so Back steps through queries.

### SRC-06 — `hits` / `videos` null or missing render the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** search → (a) `{ "hits": null, "videos": null, "total": 0 }`, (b) `{ "total": 0 }`, (c) `{}`. List fields: `hits`, `videos`.
- **Steps:**
  1. Search "x".
- **Expected:** EmptyState "No results. Check spelling or try a different mode."; no crash.

### SRC-07 — YouTube `items: null`, missing `match`, null `snippet`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** tick "Also search YouTube". Search → each of:
  - (a) `{ "hits": [], "youtube": { "items": null } }`
  - (b) `{ "hits": [], "youtube": { "reason": "disabled" } }` (no `items`)
  - (c) `{ "hits": [], "youtube": { "items": [ { "youtube_id": "y1", "title": "T", "channel": "C", "thumbnail": "", "match": null } ] } }`
  - (d) `{ "hits": [ { "segment_id": 1, "video_id": "v1", "start_sec": 0, "end_sec": 1, "snippet": null, "score": 1 } ] }`
    List fields: `youtube.items`.
- **Steps:**
  1. Search "x".
- **Expected:** (a) section "From YouTube" with "No YouTube results."; (b) "YouTube search is off."; (c) the card renders with an "Import metadata to…" button; (d) the hit renders with an empty snippet. Today (a)(b) crash on `block.items.length`, (c) crashes on `it.match.state`, (d) crashes on `snippet.split` — the page goes blank (likely bugs, same class as Live TV `hosts: null`).

### SRC-08 — Saved `items: null` and suggest `suggestions: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/search/saved` → (a) `{ "items": null }`, (b) `{}`, (c) 500; `GET /api/search/suggest` → (a) `{ "suggestions": null }`, (b) `{}`, (c) 500. List fields: `items`, `suggestions`.
- **Steps:**
  1. Open `/search`; type "qu" and wait 250 ms.
- **Expected:** aside "Saved searches" shows "Nothing here yet."; datalist `#mkt-suggest` has no options; no crash; no error UI.

### SRC-09 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full routes inside `AppShell`; start on `/library`; search → SRC-07 (a) response (crashes today), with "Also search YouTube" ticked — or mock `@ds/components/Badge/Badge` to throw.
- **Steps:**
  1. Go to `/search` via the nav link; tick YouTube; search "x".
  2. Press browser Back.
  3. Click the "Search" nav link again.
- **Expected:** step 1 shows a route-level error fallback inside the shell, not a blank page. Step 2 shows the Library without a reload. Step 3 shows a fresh Search page.

### SRC-10 — Signed-out user is sent to `/login` and keeps the query

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** full `App`; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/search?q=fox`; sign in.
- **Expected:** redirect to `/login`; no search request before sign-in. Wanted: after sign-in, land on `/search?q=fox` and the search runs. Today `RequireAuth` saves only `location.pathname`, so the user lands on `/search` with no query — likely bug.

### SRC-11 — Results only from accessible libraries

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** e2e against the real API: non-admin with ACL on library A only; a word that appears in transcripts and titles in A and B.
- **Steps:**
  1. Sign in; search the word.
- **Expected:** only A's videos and passages; suggestions and YouTube "In your library" matches never reveal B's titles or ids.

### SRC-12 — Snippet markup is escaped; only `<mark>` is highlighted

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Partial — `Search.test.tsx` renders a `<mark>` snippet, no injection check.
- **Preconditions / data:** hit `snippet`: `"<img src=x onerror=alert(1)> <mark>fox</mark> <b>bold</b> <mark>unclosed"`.
- **Steps:**
  1. Search "fox".
- **Expected:** no `img` or `b` element is created; their text shows literally; "fox" and "unclosed" are inside `<mark>`; no script runs. Non-mark runs have `dir="auto"`.

### SRC-13 — No results shows "No results..." empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes — `Search.test.tsx` "renders the empty state when the server returns no hits".
- **Preconditions / data:** search → `{ "hits": [], "total": 0 }`.
- **Steps:**
  1. Search "nothing".
- **Expected:** EmptyState "No results. Check spelling or try a different mode."; it does not show before the first search (hits `null`).

### SRC-14 — Blank / whitespace query does nothing; clearing removes `?q`

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** start at `/search?q=fox` with results.
- **Steps:**
  1. Clear the box; type three spaces; submit.
  2. Click "Save" with the box blank.
- **Expected:** no `POST /api/search` and no `POST /api/search/save`; `q` removed from the URL. Wanted: results from "fox" are cleared too (today they stay on screen under an empty box). Leading/trailing spaces are trimmed in the URL and the request body.

### SRC-15 — Pending state: button loading, no duplicate submits

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** search resolves after 300 ms.
- **Steps:**
  1. Search "fox"; press Enter again twice while pending.
- **Expected:** the "Search" button shows loading. Wanted: only one request in flight, or the latest response wins; today each submit sends a new POST and whichever finishes last overwrites the results (no cancel) — check order with two different queries ("a" slow, "b" fast → final results must be "b").

### SRC-16 — Search 500 / 400 / network / malformed JSON

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** (a) 500 problem+json `{ "title": "search backend down", "status": 500 }`; (b) 400 `{ "title": "query too long", "status": 400 }`; (c) fetch rejects; (d) 200 JSON body `{bad`.
- **Steps:**
  1. Search "fox".
- **Expected:** ErrorState "Something went wrong." with description "search backend down" / "query too long" / "Something went wrong."; no empty state at the same time; previous results cleared; the form stays usable.

### SRC-17 — Error clears on the next successful search

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** first search → 500; second → one hit.
- **Steps:**
  1. Search "a"; then search "b".
- **Expected:** after the second search the ErrorState is gone and the hit shows.

### SRC-18 — Mode select persists and is sent; change of mode

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage` empty; then `localStorage["mkt:search:mode"] = "bogus"`; then `localStorage` throwing.
- **Steps:**
  1. Open `/search`; select "Semantic" in the select named "Mode"; search "x".
  2. Remount.
  3. Remount with "bogus" and with storage throwing.
- **Expected:** options Hybrid, Keyword, Semantic. Step 1: body `mode: "semantic"`; storage `"semantic"`. Step 2: select shows Semantic. Step 3: falls back to Hybrid, no crash. Changing the mode alone does not re-run the search (by design); wanted: a hint or re-run when a query is present.

### SRC-19 — Suggestions debounce, encode, and drop stale answers

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; suggest for "qu" resolves after 500 ms with `["quiet"]`, for "qui" after 10 ms with `["quick", "quiz"]`; Arabic input "سي ر".
- **Steps:**
  1. Type "q", "u" quickly; wait 199 ms; wait 1 more ms.
  2. Type "i"; wait 250 ms; let the slow "qu" answer arrive.
  3. Type "سي ر".
  4. Submit.
- **Expected:** step 1: no request before 200 ms, then one `GET /api/search/suggest?q=qu`. Step 2: final datalist options are "quick", "quiz" (wanted: the late "qu" answer is ignored — today it overwrites them, likely bug). Step 3: URL `q=%D8%B3%D9%8A%20%D8%B1`. Step 4: suggestions cleared. Unmount during the 200 ms wait sends no request (wanted; today the timer is not cleared on unmount).

### SRC-20 — Save, run and delete saved searches

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** saved → `{ "items": [ { "id": "s1", "q": "fox", "mode": "fts" } ] }`, then after save → two items; `POST /api/search/save` → 201; `DELETE /api/search/saved/s1` → 204; also save → 500.
- **Steps:**
  1. Open `/search`; type " dog "; click "Save".
  2. Click saved "fox".
  3. Click the delete button next to "fox".
  4. Save with the POST failing.
- **Expected:** step 1: body `{ q: "dog", mode: "hybrid" }`; saved list re-fetched and shows both. Step 2: box shows "fox", mode select "Keyword", POST `{ q: "fox", mode: "fts" }`. Wanted: the URL also becomes `?q=fox` (today it does not). Step 3: `DELETE /api/search/saved/s1`, list re-fetched. Step 4: wanted: an error toast (today silently ignored); also wanted a success confirmation on save. A saved item with an unknown `mode` must not put the select into an invalid value.

### SRC-21 — YouTube block: results, in-library link, reason text

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** tick "Also search YouTube"; search → `{ "hits": [], "youtube": { "items": [ { "youtube_id": "abc&x", "title": "عنوان", "channel": "Chan", "description_snippet": "", "thumbnail": "https://i.ytimg.com/vi/abc/hq.jpg", "view_count": 12345, "match": { "state": "in_library", "video_id": "v5" } }, { "youtube_id": "def", "title": "Other", "channel": "C2", "thumbnail": "", "match": { "state": "none" } } ] } }`. Also `{ "youtube": { "items": [], "reason": "error" } }` and an unknown reason "quota".
- **Steps:**
  1. Search "x".
- **Expected:** body includes `include: ["youtube"]`. Section named "From YouTube". First card links to `https://www.youtube.com/watch?v=abc%26x` with `target="_blank"` and `rel="noopener noreferrer"`, shows "Chan · 12,345", and a link to `/videos/v5` with badge "In your library →". Second card has no img and an "Import metadata to…" button. Reason "error" shows "YouTube is unavailable right now."; unknown reason must not show a raw key `search.youtube.reason.quota` (wanted: fall back to "No YouTube results."). Unticked toggle: no YouTube section.

### SRC-22 — YouTube import prompt: cancel, success, error

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** SRC-21 data; stub `window.prompt`; `POST /api/videos/v7/import-youtube` → 204, then 404 `{ "title": "video not found" }`. Render within `ToastProvider`.
- **Steps:**
  1. Click "Import metadata to…"; prompt returns `null`.
  2. Prompt returns " v7 ".
  3. Prompt returns "v7" and the POST fails.
- **Expected:** step 1: no request. Step 2: `POST /api/videos/v7/import-youtube { youtube_id: "def" }`, toast "Metadata imported.". Step 3: error toast "video not found". Non-admin: wanted the import button hidden (server requires an editor; today shown to all).

### SRC-23 — Arabic query text, Arabic results and Arabic UI

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Partial — `Search.test.tsx` "lists title matches..." covers an Arabic title link.
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; search for "السيرة النبوية" → hit snippet `"من <mark>السيرة</mark> النبوية"` and video title "السيرة النبوية ( 18 )".
- **Steps:**
  1. Type the Arabic query and submit; then reload the URL.
- **Expected:** `<html dir="rtl">`; placeholder, mode options, "Save", "Saved searches", "Videos", "In transcripts" from `ar.json`; body `q` is the exact Arabic string (no normalisation by the client); URL `?q=` is percent-encoded UTF-8 and reloading restores the same text; snippet and title runs have `dir="auto"`; the "[m:ss → m:ss]" arrow reads correctly in RTL (wanted: arrow mirrors to "←" in RTL).

### SRC-24 — Many hits: window cap and `total`

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** search → 50 hits, `total: 480`; also hits with a duplicated `segment_id`.
- **Steps:**
  1. Search "the".
- **Expected:** 50 result items (limit 50, window 100). Wanted: "50 of 480" or a way to load more (today `total` is ignored). Duplicate `segment_id` must not cause a React key warning or a dropped row.

### SRC-25 — Keyboard and a11y: labels, saved delete name, status updates

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** saved → one item "fox"; a search with results.
- **Steps:**
  1. Load `/search`; Tab through the form, results and saved list.
- **Expected:** the box has focus on load (`autoFocus`) and accessible name "Search"; form `role="search"`; select named "Mode"; checkbox named "Also search YouTube"; aside named "Saved searches". The saved-delete "×" button: wanted an accessible name like "Delete saved search fox" — today it is "Close" (likely bug). Wanted: result count / errors announced in a live region (none today). Focus ring visible on every result link.

### SRC-26 — Phone width: form wraps, saved list reachable

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375x812; results with long Arabic and English snippets; 5 saved searches; YouTube results.
- **Steps:**
  1. Open `/search?q=fox`.
- **Expected:** no horizontal page scroll; the form (box, mode, Search, Save, YouTube toggle) wraps; the saved aside stacks below results and is reachable; YouTube thumbnails scale down.

### SRC-27 — Light/dark theme: `<mark>` and YouTube cards readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; light and dark; results with `<mark>` runs and a YouTube block.
- **Steps:**
  1. Search in each theme.
- **Expected:** `<mark>` text has AA contrast against its highlight in both themes (no default yellow-on-dark issue); YouTube section is visually distinct but readable; the empty and error states readable.
