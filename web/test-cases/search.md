# Search

- **Routes:** `/search`, with `?q=<term>&mode=<hybrid|fts|semantic>&sid=<saved search>&vp=<In titles page>&tp=<In transcripts page>` (the header search box in `src/components/AppShell.tsx` navigates to `/search?q=<encoded term>`; the global `/` shortcut focuses that box). `/search/video/:videoId?q=&mode=&sid=&vp=&tp=[&t=]` is the matches page of one video (cases SRC-36 to SRC-43).
- **Components:** `src/pages/Search.tsx` (`ResultSection`, `VideoResult`, `TranscriptResult`, `PassageList`, `RecentSearches`, `YouTubeSection`, `ImportModal`), `src/pages/SearchVideo.tsx` (reuses `VideoPlayer` with `markers`, `onVideoElement`, `hideBack`), shared `src/pages/searchShared.tsx` and `src/components/VideoViews.tsx` (`ViewToggle`, `VideoPoster`, also used by the Library); `@ds` `Button`, `Badge`, `Pagination`, `EmptyState`, `ErrorState`, `Modal`, `useToast`.
- **API calls (all via `api` in `src/lib/api.ts`):**
  - `POST /api/search { q, mode, view: "list" | "grid" }` → `{ search_id, expires_at, page_size, passages_page_size, videos: TitleVideo[] (In titles, page 1), videos_total, transcript_videos: TranscriptVideo[] (In transcripts, one per video, page 1), transcript_total }`. `TitleVideo = { video_id, title, title_snippet, duration_sec?, library_id, library_name, poster_url, score, in_transcripts }`; `TranscriptVideo = { …card, best_score, matches, passages: [{ segment_id, start_sec, end_sec, snippet, score }] (top 3), in_title }`. `search_id` is `""` for an admin token (nothing saved, nothing to page).
  - `GET /api/search/results/{sid}?list=titles|transcripts&offset=&limit=` → `{ total, limit, items }` (later pages, Back / reload, a recent search).
  - `GET /api/search/passages?sid=&video_id=&order=score|time&offset=&limit=[&include_times=1]` → `{ video, in_title, title_snippet, total, items, times? }` (Show all on the Search page: `order=score`; matches page: `order=time`, `include_times=1` on the first page).
  - Any sid read can answer **410** with problem `type: "https://maktaba.tv/problems/search-expired"`: the page POSTs the search again with the same q and mode.
  - `GET /api/search/recent` → `{ items: [{ search_id, q, mode, created_at, last_used_at, title_count, transcript_count, live, expires_at }] }` (on mount and after every search); `DELETE /api/search/recent/{sid}`; `DELETE /api/search/recent`.
  - `GET /api/search/youtube/status` (on mount) → `{ available }`: whether the server can run yt-dlp. False ⇒ no "Also search YouTube" toggle and no "From YouTube" section.
  - `GET /api/search/youtube?q=<encoded>` (after the local results, when the toggle is on) → `{ items: [{ youtube_id, title, channel, description_snippet, thumbnail, published_at?, duration_sec?, view_count?, match: { state: in_library | imported | importable, video_id? } }], reason? }` (yt-dlp search, no API key).
  - `GET /api/search/suggest?q=<encoded>` (200 ms debounce) → `{ suggestions: string[] }` → `<datalist id="mkt-suggest">`.
  - `GET /api/search/saved` → `{ items: [{ id, q, mode }] }` on mount and after save/delete.
  - `POST /api/search/save { q, mode }`; `DELETE /api/search/saved/{id}`.
  - `POST /api/videos/{target}/import-youtube { youtube_id, title, channel, description, thumbnail, published_at?, duration_sec? }` (the Import button, admins only; the target video is picked in a dialog). Stages an enrichment candidate; the results are then refetched.
  - Matches page only: `POST /api/stream/sessions` and `GET /api/videos/{id}/chapters` (the player).
- **State in URL / storage:** `q`, `mode`, `sid`, `vp`, `tp` in the URL. A submit pushes `?q&mode`, then the answer's sid replaces that entry; page changes push. Mode in `localStorage["mkt:search:mode"]` (`hybrid` | `fts` | `semantic`, default `hybrid`; a `mode` in the URL wins). List / Grid in `localStorage["mkt:search:view"]` (default `list`). Page sizes of each sid in `sessionStorage["mkt:search:sizes"]` (fallback 20 list / 24 grid, 20 passages). YouTube toggle is component state only.
- **Access:** signed-in user (`RequireAuth`). Server scopes results to libraries the user can access; a sid belongs to its user (another user's sid answers 410).
- **Existing tests:** `src/pages/Search.test.tsx` (SRC-01 to SRC-35), `src/pages/Search.youtube.test.tsx` (SRC-21, SRC-22), `src/pages/SearchVideo.test.tsx` (SRC-36 to SRC-43); the fake saved-search server is `src/test/searchServer.ts`.

## Summary

| ID     | Title                                                                     | Category      | Priority | Covered by existing test?                           |
| ------ | ------------------------------------------------------------------------- | ------------- | -------- | --------------------------------------------------- |
| SRC-01 | Submit POSTs `{ q, mode, view }`; sid in URL; passages link to the player | Happy path    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-02 | In titles / In transcripts under their headings; title opens matches page | Happy path    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-03 | Deep link `/search?q=...` runs the search on load                         | Navigation    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-04 | Header search while already on `/search` runs the new query               | Navigation    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-05 | Back/Forward restores the query and its results (by sid)                  | Navigation    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-06 | List fields null or missing render the empty state                        | Empty state   | P1       | Yes (`Search.test.tsx`)                             |
| SRC-07 | YouTube `items: null`, missing `match`, null `snippet`                    | Empty state   | P1       | Yes (`Search.test.tsx`)                             |
| SRC-08 | Saved `items: null` and suggest `suggestions: null`                       | Empty state   | P1       | Yes (`Search.test.tsx`)                             |
| SRC-09 | Render crash shows route error fallback; Back still works                 | Error         | P1       | Yes (`Search.test.tsx`)                             |
| SRC-10 | Signed-out user is sent to `/login` and keeps the query                   | Permissions   | P1       | Yes (`Search.test.tsx`)                             |
| SRC-11 | Results only from accessible libraries                                    | Permissions   | P1       | No (e2e-only)                                       |
| SRC-12 | Snippet markup is escaped; only `<mark>` is highlighted                   | Validation    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-13 | No results shows "No results..." empty state                              | Empty state   | P2       | Yes (`Search.test.tsx`)                             |
| SRC-14 | Blank / whitespace query does nothing; clearing removes `?q`              | Validation    | P2       | Yes (`Search.test.tsx`)                             |
| SRC-15 | Pending state: button loading, no duplicate submits                       | Loading       | P2       | Yes (`Search.test.tsx`)                             |
| SRC-16 | Search 500 / 400 / network / malformed JSON                               | Error         | P2       | Yes (`Search.test.tsx`)                             |
| SRC-17 | Error clears on the next successful search                                | Error         | P2       | Yes (`Search.test.tsx`)                             |
| SRC-18 | Mode select persists and is sent; change of mode                          | Happy path    | P2       | Yes (`Search.test.tsx`)                             |
| SRC-19 | Suggestions debounce, encode, and drop stale answers                      | Happy path    | P2       | Yes (`Search.test.tsx`)                             |
| SRC-20 | Save, run and delete saved searches                                       | Happy path    | P2       | Yes (`Search.test.tsx`; it.todo: save confirmation) |
| SRC-21 | YouTube block: results, in-library link, reason text                      | Happy path    | P2       | Yes (`Search.test.tsx`)                             |
| SRC-22 | YouTube import dialog: pick, look up, success, error                      | Happy path    | P2       | Yes (`Search.youtube.test.tsx`)                     |
| SRC-23 | Arabic query text, Arabic results and Arabic UI                           | RTL           | P2       | Yes (`Search.test.tsx`)                             |
| SRC-24 | Many results: paged lists replace the window cap                          | Happy path    | P3       | Yes (`Search.test.tsx`: SRC-31)                     |
| SRC-25 | Keyboard and a11y: labels, saved delete name, status updates              | Accessibility | P3       | No                                                  |
| SRC-26 | Phone width: form wraps, saved list reachable                             | Responsive    | P3       | No                                                  |
| SRC-27 | Light/dark theme: `<mark>` and YouTube cards readable                     | Theme         | P3       | No                                                  |
| SRC-28 | In transcripts: one row per video, library, matches, also-in-title        | Happy path    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-29 | Show all loads every passage of a video, paged; Show fewer                | Happy path    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-30 | List / Grid toggle for both lists, sent as `view`, remembered             | Happy path    | P2       | Yes (`Search.test.tsx`)                             |
| SRC-31 | Each list pages on its own: totals, page X of Y, URL vp / tp              | Navigation    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-32 | Reload with sid and pages reads them by sid (no POST)                     | Navigation    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-33 | Expired sid: re-run, pages kept and clamped, notice, no mixing            | Error         | P1       | Yes (`Search.test.tsx`)                             |
| SRC-34 | Expiry on a page change or Show all refreshes the results                 | Error         | P1       | Yes (`Search.test.tsx`)                             |
| SRC-35 | Recent searches: shown, open live by sid, re-run expired, remove, clear   | Happy path    | P1       | Yes (`Search.test.tsx`)                             |
| SRC-36 | Matches page lists every match by time; back link keeps pages             | Happy path    | P1       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-37 | Clicking a match seeks the player and plays                               | Happy path    | P1       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-38 | Next / Previous match from the playback time                              | Happy path    | P1       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-39 | Every match marked on the progress bar                                    | Happy path    | P2       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-40 | Title-only match: highlighted title, no transcript matches                | Empty state   | P2       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-41 | Matches page with an expired or missing sid runs the search               | Error         | P1       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-42 | Matches page for a video not in the results                               | Error         | P2       | Yes (`SearchVideo.test.tsx`)                        |
| SRC-43 | Matches page in Arabic; phone layouts of both pages                       | RTL           | P2       | Partial (`SearchVideo.test.tsx`: phone is e2e-only) |

## Cases

### SRC-01 — Submit POSTs `{ q, mode, view }`; sid in URL; passages link to the player

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `POST /api/search` → `search_id: "sid-1"`, no title matches, one transcript video `vid-1` with passage `{ segment_id: 42, start_sec: 72.5, end_sec: 78, snippet: "the <mark>quick</mark> brown fox" }`.
- **Steps:**
  1. Open `/search`; type "quick" in the box named "Search" (role `combobox` because of `list=`); click button "Search".
- **Expected:** body `{ q: "quick", mode: "hybrid", view: "list" }` (no `limit`, no `include`); URL `/search?q=quick&mode=hybrid&sid=sid-1` (the sid replaces the pushed entry); a passage link `href="/videos/vid-1/watch?t=72"` showing "[1:12 → 1:18]" with "quick" inside a `<mark>`. Heading "In transcripts"; no "In titles" heading (no title matches).

### SRC-02 — In titles / In transcripts under their headings; title opens matches page

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** titles `v9` "Quick start" (3725 s, library "Lectures") and `v8` "No length" (no duration); one transcript video.
- **Steps:**
  1. Search "quick"; click "Quick start".
- **Expected:** h2 "In titles" with a row per video: title link `/search/video/v9?q=quick&mode=hybrid&sid=…`, library "Lectures", duration "1:02:05" (none for `v8`); then h2 "In transcripts". No empty state. The click opens the matches page.

### SRC-03 — Deep link `/search?q=...` runs the search on load

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:search:mode"] = "fts"`; search → one result.
- **Steps:**
  1. Open `/search?q=%D8%A7%D9%84%D8%B3%D9%8A%D8%B1%D8%A9` (السيرة) directly.
- **Expected:** input value "السيرة"; exactly one `POST /api/search` with `{ q: "السيرة", mode: "fts", view: "list" }`; results render. No second POST on re-render (StrictMode included). A `mode` in the URL wins over the stored one.

### SRC-04 — Header search while already on `/search` runs the new query

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** full app inside `AppShell`; start at `/search?q=alpha` (results for alpha); search for "beta" returns different hits.
- **Steps:**
  1. Type "beta" in the header search box (form `role="search"` in the shell) and press Enter.
- **Expected:** URL `/search?q=beta`; wanted: page input shows "beta" and a new POST for "beta" runs, showing beta results. Today the seed effect is mount-only (`didSeed`), so the URL changes but the page keeps showing "alpha" — likely bug.

### SRC-05 — Back/Forward restores the query and its results (by sid)

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** searches "one" and "two" return different results.
- **Steps:**
  1. Search "one", then "two" in the page.
  2. Press Back.
  3. Click a passage (goes to the player), then press Back.
- **Expected:** step 2: URL `/search?q=one&mode=hybrid&sid=sid-1`, input "one", the same results in the same order, read with `GET /api/search/results/sid-1` (no new POST). Step 3: the same again. A `?q=` without a sid (header search, an old link) runs the search.

### SRC-06 — List fields null or missing render the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** search → (a) `{ "videos": null, "transcript_videos": null }`, (b) `{ "total": 0 }`, (c) `{}`; (d) a transcript video with `passages: null`, `matches: 2` and `search_id: ""`. List fields: `videos`, `transcript_videos`, `passages`, `items`.
- **Steps:**
  1. Search "x".
- **Expected:** (a)–(c) EmptyState "No results. Check spelling or try a different mode."; no crash. (d) the row shows "2 matches" with no passages, no "Show all" and no pager buttons (nothing saved to page).

### SRC-07 — YouTube `items: null`, missing `match`, null `snippet`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** tick "Also search YouTube". Search → each of:
  - (a) `{ "hits": [], "youtube": { "items": null } }`
  - (b) `{ "hits": [], "youtube": { "reason": "disabled" } }` (no `items`)
  - (c) `{ "hits": [], "youtube": { "items": [ { "youtube_id": "y1", "title": "T", "channel": "C", "thumbnail": "", "match": null } ] } }`
  - (d) `{ "hits": [ { "segment_id": 1, "video_id": "v1", "start_sec": 0, "end_sec": 1, "snippet": null, "score": 1 } ] }`
    List fields: `youtube.items`.
- **Steps:**
  1. Search "x".
- **Expected:** (a) section "From YouTube" with "No YouTube results."; (b) "YouTube search is off."; (c) the card renders with an "Import" button; (d) the hit renders with an empty snippet. Today (a)(b) crash on `block.items.length`, (c) crashes on `it.match.state`, (d) crashes on `snippet.split` — the page goes blank (likely bugs, same class as Live TV `hosts: null`).

### SRC-08 — Saved `items: null` and suggest `suggestions: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `GET /api/search/saved` → (a) `{ "items": null }`, (b) `{}`, (c) 500; `GET /api/search/suggest` → (a) `{ "suggestions": null }`, (b) `{}`, (c) 500. List fields: `items`, `suggestions`.
- **Steps:**
  1. Open `/search`; type "qu" and wait 250 ms.
- **Expected:** aside "Saved searches" shows "Nothing here yet."; datalist `#mkt-suggest` has no options; no crash; no error UI.

### SRC-09 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** full routes inside `AppShell`; start on `/library`; search → SRC-07 (a) response (crashes today), with "Also search YouTube" ticked — or mock `@ds/components/Badge/Badge` to throw.
- **Steps:**
  1. Go to `/search` via the nav link; tick YouTube; search "x".
  2. Press browser Back.
  3. Click the "Search" nav link again.
- **Expected:** step 1 shows a route-level error fallback inside the shell, not a blank page. Step 2 shows the Library without a reload. Step 3 shows a fresh Search page.

### SRC-10 — Signed-out user is sent to `/login` and keeps the query

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** full `App`; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/search?q=fox`; sign in.
- **Expected:** redirect to `/login`; no search request before sign-in. Wanted: after sign-in, land on `/search?q=fox` and the search runs. Today `RequireAuth` saves only `location.pathname`, so the user lands on `/search` with no query — likely bug.

### SRC-11 — Results only from accessible libraries

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e against the real API: non-admin with ACL on library A only; a word that appears in transcripts and titles in A and B.
- **Steps:**
  1. Sign in; search the word.
- **Expected:** only A's videos and passages; suggestions and YouTube "In your library" matches never reveal B's titles or ids.

### SRC-12 — Snippet markup is escaped; only `<mark>` is highlighted

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** hit `snippet`: `"<img src=x onerror=alert(1)> <mark>fox</mark> <b>bold</b> <mark>unclosed"`.
- **Steps:**
  1. Search "fox".
- **Expected:** no `img` or `b` element is created; their text shows literally; "fox" and "unclosed" are inside `<mark>`; no script runs. Non-mark runs have `dir="auto"`.

### SRC-13 — No results shows "No results..." empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Search.test.tsx`: SRC-06)
- **Preconditions / data:** search → `{ "videos": [], "videos_total": 0, "transcript_videos": [], "transcript_total": 0 }`.
- **Steps:**
  1. Search "nothing".
- **Expected:** EmptyState "No results. Check spelling or try a different mode."; it does not show before the first search; no list headings, no view toggle.

### SRC-14 — Blank / whitespace query does nothing; clearing removes `?q`

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** start at `/search?q=fox` with results.
- **Steps:**
  1. Clear the box; type three spaces; submit.
  2. Click "Save" with the box blank.
- **Expected:** no `POST /api/search` and no `POST /api/search/save`; the URL becomes `/search` (q, mode, sid and pages removed); the old results are cleared. Leading/trailing spaces are trimmed in the URL (`?q=fox&mode=hybrid`) and the request body.

### SRC-15 — Pending state: button loading, no duplicate submits

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** search resolves after 300 ms.
- **Steps:**
  1. Search "fox"; press Enter again twice while pending.
- **Expected:** the "Search" button shows loading. Wanted: only one request in flight, or the latest response wins; today each submit sends a new POST and whichever finishes last overwrites the results (no cancel) — check order with two different queries ("a" slow, "b" fast → final results must be "b").

### SRC-16 — Search 500 / 400 / network / malformed JSON

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** (a) 500 problem+json `{ "title": "search backend down", "status": 500 }`; (b) 400 `{ "title": "query too long", "status": 400 }`; (c) fetch rejects; (d) 200 JSON body `{bad`.
- **Steps:**
  1. Search "fox".
- **Expected:** ErrorState "Something went wrong." with description "search backend down" / "query too long" / "Something went wrong."; no empty state at the same time; previous results cleared; the form stays usable.

### SRC-17 — Error clears on the next successful search

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** first search → 500; second → one hit.
- **Steps:**
  1. Search "a"; then search "b".
- **Expected:** after the second search the ErrorState is gone and the hit shows.

### SRC-18 — Mode select persists and is sent; change of mode

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `localStorage` empty; then `localStorage["mkt:search:mode"] = "bogus"`; then `localStorage` throwing; then `/search?q=x&mode=fts` with "semantic" stored.
- **Steps:**
  1. Open `/search`; select "Semantic" in the select named "Mode"; search "x".
  2. Remount.
  3. Remount with "bogus" and with storage throwing.
  4. Open `/search?q=x&mode=fts`.
- **Expected:** options Hybrid, Keyword, Semantic. Step 1: body `mode: "semantic"`, URL `?q=x&mode=semantic`; storage `"semantic"`. Step 2: select shows Semantic. Step 3: falls back to Hybrid, no crash. Step 4: the search runs with `fts` and the select shows Keyword. Changing the mode alone does not re-run the search (by design).

### SRC-19 — Suggestions debounce, encode, and drop stale answers

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
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
- **Covered by existing test?** Yes (`Search.test.tsx`; it.todo: save confirmation)
- **Preconditions / data:** saved → `{ "items": [ { "id": "s1", "q": "fox", "mode": "fts" } ] }`, then after save → two items; `POST /api/search/save` → 201; `DELETE /api/search/saved/s1` → 204; also save → 500.
- **Steps:**
  1. Open `/search`; type " dog "; click "Save".
  2. Click saved "fox".
  3. Click the delete button next to "fox".
  4. Save with the POST failing.
- **Expected:** step 1: body `{ q: "dog", mode: "hybrid" }`; saved list re-fetched and shows both. Step 2: box shows "fox", mode select "Keyword", POST `{ q: "fox", mode: "fts", view: "list" }`, URL `?q=fox&mode=fts` (plus the sid once answered). Step 3: `DELETE /api/search/saved/s1`, list re-fetched. Step 4: an error toast; wanted also a success confirmation on save. A saved item with an unknown `mode` falls back to Hybrid. Saved searches stay a separate panel from Recent searches.

### SRC-21 — YouTube block: results, in-library link, reason text

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** tick "Also search YouTube"; search → `{ "hits": [], "youtube": { "items": [ { "youtube_id": "abc&x", "title": "عنوان", "channel": "Chan", "description_snippet": "", "thumbnail": "https://i.ytimg.com/vi/abc/hq.jpg", "view_count": 12345, "match": { "state": "in_library", "video_id": "v5" } }, { "youtube_id": "def", "title": "Other", "channel": "C2", "thumbnail": "", "match": { "state": "none" } } ] } }`. Also `{ "youtube": { "items": [], "reason": "error" } }` and an unknown reason "quota".
- **Steps:**
  1. Search "x".
- **Expected:** the local `POST /api/search` has no `include`; `GET /api/search/youtube?q=x` follows it. Section named "From YouTube". First card links to `https://www.youtube.com/watch?v=abc%26x` with `target="_blank"` and `rel="noopener noreferrer"`, shows "Chan · 12,345", and a link to `/videos/v5` with badge "In your library →". Second card has no img and an "Import" button. Reason "error" shows "YouTube is unavailable right now."; unknown reason must not show a raw key `search.youtube.reason.quota` (wanted: fall back to "No YouTube results."). Unticked toggle: no YouTube section.

### SRC-22 — YouTube import dialog: pick, look up, success, error

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.youtube.test.tsx`).
- **Preconditions / data:** signed in as an admin; SRC-21 data with local `videos: [{ "video_id": "v7", "title": "Lesson" }]`; `POST /api/videos/v7/import-youtube` → 200 `{ "staged": true }`, then 404 `{ "title": "video not found" }`. Render within `ToastProvider`.
- **Steps:**
  1. Click "Import" on the second card; Cancel.
  2. Import again, pick "Lesson", click "Import".
  3. Import again, type a title in "Find a video by title", Search, pick a result; the POST fails.
- **Expected:** no `window.prompt`. Step 1: no request. Step 2: the dialog's Import is disabled until a video is picked; `POST /api/videos/v7/import-youtube` with the result's `youtube_id`, title, channel, description, thumbnail (and date/length when known); toast "Metadata staged for “Lesson”…"; the dialog closes and the search runs again, the card now showing "Imported →" linking to `/videos/v7`. Step 3: the lookup is `POST /api/search { q, mode: "fts", view: "list" }` (its `videos` are the choices); error toast "video not found", the dialog stays open. Non-admin: no Import button (the server answers 403).

### SRC-23 — Arabic query text, Arabic results and Arabic UI

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; search for "السيرة النبوية" → title "السيرة النبوية ( 18 )" and a transcript video with one passage `"من <mark>السيرة</mark> النبوية"`.
- **Steps:**
  1. Type the Arabic query and submit; then reload the URL.
- **Expected:** `<html dir="rtl">`; headings «في العناوين» and «في التفريغات», «مطابقة واحدة», «الصفحة ١ من ١ · …», «عمليات البحث الأخيرة» from `ar.json`; body `q` is the exact Arabic string; URL `?q=` is percent-encoded UTF-8 and reloading restores the same text (by sid); snippet and title runs have `dir="auto"`; the time range arrow is "←" in RTL; library names have `dir="auto"`.

### SRC-24 — Many results: paged lists replace the window cap

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`: SRC-31)
- **Preconditions / data:** 45 title matches and 25 transcript videos, `page_size: 20`.
- **Steps:**
  1. Search "the".
- **Expected:** 20 rows per list, "Page 1 of 3 · 45 videos" and "Page 1 of 2 · 25 videos"; no video appears twice in a list (one row per video).

### SRC-25 — Keyboard and a11y: labels, saved delete name, status updates

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** saved → one item "fox"; a search with results.
- **Steps:**
  1. Load `/search`; Tab through the form, results and saved list.
- **Expected:** the box has focus on load (`autoFocus`) and accessible name "Search"; form `role="search"`; select named "Mode"; checkbox named "Also search YouTube"; asides named "Saved searches" and "Recent searches"; the view toggle is a radiogroup named "View"; each pager is a nav named after its list, with page buttons "Page N", "Previous page", "Next page"; the refreshed notice is `role="status"` with a "Dismiss" button; recent remove buttons are named "Remove <query>". The saved-delete "×" button: wanted an accessible name like "Delete saved search fox" — today it is "Close" (likely bug). Wanted: result count / errors announced in a live region (none today). Focus ring visible on every result link.

### SRC-26 — Phone width: form wraps, lists stack, side panels reachable

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e 375x812; results with long Arabic and English titles and snippets; 5 saved and 10 recent searches; YouTube results.
- **Steps:**
  1. Open `/search?q=fox` in List and in Grid view.
- **Expected:** no horizontal page scroll; the form wraps; under 600 px list rows stack (poster above the text) and the grid is one column; the pager wraps; the Saved and Recent panels stack below the results; YouTube thumbnails scale down.

### SRC-27 — Light/dark theme: `<mark>` and YouTube cards readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; light and dark; results with `<mark>` runs and a YouTube block.
- **Steps:**
  1. Search in each theme.
- **Expected:** `<mark>` text has AA contrast against its highlight in both themes (no default yellow-on-dark issue); YouTube section is visually distinct but readable; the empty and error states readable.

### SRC-28 — In transcripts: one row per video, library, matches, also-in-title

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `v1` "Fox one" in both lists with 7 matching passages (`in_title: true`, top 3 sent), `v2` "Other talk" with 1.
- **Steps:**
  1. Search "fox" (List view).
- **Expected:** exactly two rows under "In transcripts", in server order. `v1` row: poster `/api/videos/v1/poster`, title and poster linking to `/search/video/v1?…`, "Lectures", "1:02:05", "7 matches", "Also in title", three passage links `/videos/v1/watch?t=10|20|30`. `v2`: "1 match", no badge, no "Show all".

### SRC-29 — Show all loads every passage of a video, paged; Show fewer

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** one transcript video with 5 passages; `passages_page_size: 2`.
- **Steps:**
  1. Click "Show all 5"; click "Load more" twice; click "Show fewer".
- **Expected:** `GET /api/search/passages?sid=sid-1&video_id=v1&order=score&offset=0&limit=2`, then offsets 2 and 4; 2, 4, then 5 passages, best first, no duplicates; "Load more" disappears at 5; "Show fewer" goes back to the top 3.

### SRC-30 — List / Grid toggle for both lists, sent as `view`, remembered

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** one title and one transcript video; also `localStorage` throwing.
- **Steps:**
  1. Search; switch "View" to "Grid"; on the transcript card click "Show passages".
  2. Remount and search again.
- **Expected:** default List (rows with library and passages). Grid: both lists become cards (poster, title, duration; transcript cards also "1 match"); passages are hidden until "Show passages" (`aria-expanded`), then link to the player; `localStorage["mkt:search:view"] = "grid"`. Step 2: Grid is selected and the POST body has `view: "grid"`. With storage throwing the page still works (List).

### SRC-31 — Each list pages on its own: totals, page X of Y, URL vp / tp

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** 45 titles, 25 transcript videos, `page_size: 20`.
- **Steps:**
  1. In "In titles" click "Page 2"; in "In transcripts" click "Next page"; in "In titles" click "Page 3"; press Back.
- **Expected:** "Page 1 of 3 · 45 videos" / "Page 1 of 2 · 25 videos". Page 2 of titles is read with `GET /api/search/results/sid-1?list=titles&offset=20&limit=20` and shows "Title 21"…"Title 40" in order; the URL gets `vp=2` (pushed), the transcripts list keeps its page. Then `tp=2` shows "Talk 21"…"Talk 25"; links to the matches page carry `vp=2&tp=2`. Page 3 shows 5 rows and "Next page" is disabled. Back returns to `vp=2` with the same rows; no new POST.

### SRC-32 — Reload with sid and pages reads them by sid (no POST)

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** a live saved search `sid-old` (q "fox", mode fts, 45 titles).
- **Steps:**
  1. Open `/search?q=fox&mode=fts&sid=sid-old&vp=3`.
- **Expected:** no `POST /api/search`; `GET …/sid-old?list=titles&offset=40&limit=20` and `…list=transcripts&offset=0…`; rows "Title 41"…"Title 45"; mode select Keyword; no notice. A page past the end shows the last page and fixes the URL.

### SRC-33 — Expired sid: re-run, pages kept and clamped, notice, no mixing

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** `sid=gone` answers 410 `search-expired`; the new run finds 30 titles (2 pages) and 45 transcript videos.
- **Steps:**
  1. Open `/search?q=fox&mode=hybrid&sid=gone&vp=4&tp=2`.
  2. Click "Back to page 1" in the notice.
- **Expected:** one `POST /api/search { q: "fox", mode: "hybrid", view }`; the URL is replaced with the new sid, `vp=2` (clamped), `tp=2`; both lists show pages of the new sid only (no reads of the old sid after the POST, never one list from each). Notice (`role="status"`): "These results were refreshed; the order may have changed." Step 2: pages 1, URL `?q=fox&mode=hybrid&sid=<new>`, the notice goes.

### SRC-34 — Expiry on a page change or Show all refreshes the results

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** a search on screen whose sid then expires.
- **Steps:**
  1. Click "Page 2" of "In titles". 2. (Another run) click "Show all 5".
- **Expected:** the search is POSTed again; the new sid replaces the URL keeping `vp=2`; page 2 of the new sid is shown; the notice shows and "Dismiss" hides it. Show all: same refresh, the rows come back folded.

### SRC-35 — Recent searches: shown, open live by sid, re-run expired, remove, clear

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Search.test.tsx`)
- **Preconditions / data:** recent → `kahf` (fts, 5 minutes ago, 2 / 0, live, its snapshot "Old A", "Old B") and `maryam` (semantic, 4 / 7, `live: false`); a third case where `kahf` is listed live but answers 410.
- **Steps:**
  1. Open `/search`; click "kahf"; click "maryam"; remove "kahf"; click "Clear all".
- **Expected:** panel "Recent searches" (separate from "Saved searches") shows each query with its mode ("Keyword"), "5 minutes ago" (the full date as a tooltip), "2 in titles · 0 in transcripts", and "Results kept" / "Expired, runs again". "kahf" opens `/search?q=kahf&mode=fts&sid=sid-live` with "Old A", "Old B" in that order and no POST. "maryam" POSTs `{ q: "maryam", mode: "semantic", view }`. The list is re-read after each search. Remove → `DELETE /api/search/recent/sid-live`; Clear all → `DELETE /api/search/recent`, then "Nothing here yet.". A live entry that answers 410 runs again with the notice.

### SRC-36 — Matches page lists every match by time; back link keeps pages

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** `/search/video/v1?q=fox&mode=hybrid&sid=sid-a&vp=2&tp=3`; `v1` has matches at 300 s, 10 s, 120 s; `passages_page_size: 2`.
- **Steps:**
  1. Open the page; click "Load more"; click "Back to results".
- **Expected:** `GET /api/search/passages?sid=sid-a&video_id=v1&order=time&offset=0&limit=2&include_times=1`; rows "[0:10] Transcript early fox", "[2:00] …" with `<mark>`; "3 matches"; the h1 is the title; "Matches for fox" where "fox" and "Back to results" link to `/search?q=fox&mode=hybrid&sid=sid-a&vp=2&tp=3`; library name shown. Load more reads `offset=2` and adds "[5:00]"; then no Load more.

### SRC-37 — Clicking a match seeks the player and plays

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** SRC-36; the player opened a direct-play session.
- **Steps:**
  1. Click the "[2:00]" row.
- **Expected:** the video's `currentTime` is 120, `play()` is called, the row is marked current (`aria-current`).

### SRC-38 — Next / Previous match from the playback time

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** SRC-36 (only 2 of 3 rows loaded).
- **Steps:**
  1. Next, Next, Next, Previous; let playback reach 200 s; Previous.
- **Expected:** Previous starts disabled; Next goes to 10, 120, then 300 (from `times`, not loaded yet) and is then disabled; Previous goes to 120; from 200 s Previous goes to 120. Each jump plays.

### SRC-39 — Every match marked on the progress bar

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** SRC-36; video duration 600 s.
- **Steps:**
  1. Let the metadata load.
- **Expected:** three `.mkt-player__mark` at 1.67 %, 20 %, 50 % (`inset-inline-start`, so they mirror in RTL), next to the chapter ticks. The player without `markers` draws none (Video player cases unchanged).

### SRC-40 — Title-only match: highlighted title, no transcript matches

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** `v2` matches only by its title (`total: 0`, `items: []`, `in_title: true`).
- **Steps:**
  1. Open `/search/video/v2?q=fox&mode=hybrid&sid=sid-a`.
- **Expected:** a "Title" row with the `title_snippet` marks; "No matches in the transcript"; "0 matches"; Next match disabled.

### SRC-41 — Matches page with an expired or missing sid runs the search

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** (a) `sid-a` answers 410; (b) no sid in the URL.
- **Steps:**
  1. Open `/search/video/v1?q=fox&mode=fts&sid=sid-a&vp=4&t=30`. 2. Open `/search/video/v1?q=fox&mode=hybrid`.
- **Expected:** (a) `POST /api/search { q: "fox", mode: "fts", view }`; the URL keeps the video and `t`, gets the new sid (replace) and drops `vp` (clamped to the new single page); the matches load from the new sid; the notice shows; the back link uses the new sid. (b) the search runs, no notice.

### SRC-42 — Matches page for a video not in the results

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`SearchVideo.test.tsx`)
- **Preconditions / data:** `GET /api/search/passages` → 404.
- **Steps:**
  1. Open `/search/video/zz?q=fox&mode=hybrid&sid=sid-a`.
- **Expected:** "This video is not in these search results." with a link back to the results; the player still loads.

### SRC-43 — Matches page in Arabic; phone layouts of both pages

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Partial (`SearchVideo.test.tsx`: phone is e2e-only)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; SRC-36 data. e2e at 375x812 for the phone part.
- **Steps:**
  1. Open the matches page.
- **Expected:** «العودة إلى النتائج» with "→", «المطابقة التالية», «عدد المطابقات: ٣»; snippets `dir="auto"`. At phone width the match rows wrap, the Next / Previous buttons wrap, nothing scrolls sideways.
