# Epic 11 — Web UI: Spec-vs-Implementation Gap Analysis

**Verdict:** Epic 11 is a thin Phase-10 scaffold, not an implementation. 8/14 stories have a page file but every page is a minimal stub; ~6% of ACs are complete. Multiple pages call API paths that **do not exist / use the wrong HTTP verb**, so the two flagship features (player, search) and the auth bootstrap are wired to dead endpoints.

Method: every AC checked against `web/src/**` and cross-checked against the actually-mounted Go routes (`api/internal/handlers/**` `r.Get/Post/...` registrations). Audit self-claims were not trusted.

---

## Endpoint reality check (web call → mounted route)

| Web call | File:line | Mounted in API? | Status |
|---|---|---|---|
| `GET /api/auth/me` | `lib/auth.tsx:39` | **No** — no `/api/auth/me` / `/api/me` handler anywhere | unwired |
| `GET /api/videos?...` | `pages/LibraryBrowser.tsx:43` | Yes (`videos.go:118`) but returns `{items,next}` not `{items,next_cursor}` | partial (field mismatch) |
| `GET /api/videos/{id}` | `pages/VideoDetail.tsx:28` | Yes (`videos.go`) | wired |
| `GET /api/videos/{id}/stream` | `pages/VideoPlayer.tsx:29` | **No** — only `POST /api/stream/sessions` exists | unwired |
| `GET /api/search?q=` | `pages/Search.tsx:42` | **Verb mismatch** — server mounts `r.Post("/api/search")` (`search.go:109`) | broken |
| `GET /api/jobs?limit=100` | `pages/ProcessingQueue.tsx:30` | Yes (`jobs.go`, `{items}`) | wired |
| WS `/ws/v1/events` | `lib/ws.ts:9` | **No** — server mounts `/ws/jobs`, `/ws/library/{id}`, `/ws/playback/{video_id}` | unwired |
| WS msg `{type:"job.updated", job}` | `ProcessingQueue.tsx:39` | Server emits `{type, at, payload}` (`ws.go:36-38`) — no `.job` key | broken |
| `GET /api/users/me/sessions` | `pages/Settings.tsx:71` | **No** — only `DELETE /api/users/{id}/sessions/{session_id}` (admin) | unwired |
| `POST /api/auth/logout-all` | `lib/auth.tsx:76` | Yes (`auth.go:98`) | wired |

---

## Per-story AC status

### 11.1 Library browser — **partial/stub**
| AC | Status | Evidence |
|---|---|---|
| Grid poster/title/duration/lang/processing badge | partial | `LibraryBrowser.tsx:91-108` renders poster+title+duration only; no language flag, no processing badge |
| List view (filename, size, modified, state) | missing | Only a CSS class swap `mkt-grid`/`mkt-list:92`; identical fields, no extra columns |
| Grid/list toggle persists in localStorage per user | missing | `view` is `useState` (`:31`), never persisted |
| Sort options (6 incl. recently-watched, lang, asc/desc) | partial | `:57-61` only recent/title/duration; no asc/desc, no recently-watched/language |
| Filter chips (lang/type/duration/speaker/tag/library), URL-encoded | missing | No filter UI at all; only `library_id` from route param |
| Cursor pagination "Load more" `?cursor=&limit=60` | missing | Single fetch; response field is `next_cursor` but API returns `next` (`videos.go:217`) |
| Empty states (admin "Scan now" / "Clear filters") | partial | Generic `common.empty` (`:89`); no admin CTA, no clear-filters |
| MISSING/READY_NO_AUDIO/SUPERSEDED/CORRUPTED badges | missing | No state badge rendering |
| EC: poster 404 placeholder; bidi-isolate titles; slow-net | missing | None implemented |

### 11.2 Video detail page — **stub**
| AC | Status | Evidence |
|---|---|---|
| Header (title/poster/duration/flags/type/library/path admin) | partial | `VideoDetail.tsx:46-47` title + optional description only |
| Tabs Watch/Transcript/Chapters/Files/Processing | missing | No tabs; flat sections (`:51-73`) |
| Processing tab (jobs rows, state, ETA, controls) | missing | Not rendered |
| Canonical stage names | n/a | No processing UI |
| Subtitle track list + set-default | missing | None |
| Audio track list | missing | None |
| Live WS updates `/ws/jobs` `/ws/library/{id}` | missing | No WS subscription in this page |
| Reaped-job reason / failed truncation / MISSING / SUPERSEDED redirect | missing | None |
| Transcript virtualization (TanStack Virtual) | missing | No transcript fetch (`/api/videos/{id}/segments` exists, unused) |

### 11.3 Video player — **broken/stub**
| AC | Status | Evidence |
|---|---|---|
| Always `POST /api/stream/sessions` before play | **missing/broken** | `VideoPlayer.tsx:29` does `GET /api/videos/{id}/stream` — endpoint not mounted; correct `POST /api/stream/sessions` never called |
| HLS.js / Vidstack with fallback | missing | Plain `<video src>` (`:50-57`); no HLS.js, no Vidstack (not in package.json) |
| ABR / stats overlay | missing | — |
| Arabic subtitle styling controls | missing | Static `<track>` only (`:58-60`) |
| Chapter ticks on scrubber | missing | — |
| Speed control 0.5–2× | missing | Native controls only |
| Keyboard map (Space/J/L/K/M/,/./0-9/F/C/+−) | missing | None |
| Picture-in-picture button, survives nav | missing | — |
| Watch progress POST every 10s/pause/seek | missing | No progress posting |
| Resume offer cross-device | missing | — |
| All EC (manifest expiry, audio switch, autoplay block, HLS bootstrap fail) | missing | — |

### 11.4 Search — **broken/stub**
| AC | Status | Evidence |
|---|---|---|
| Hits `POST /api/search` hybrid default | **broken** | `Search.tsx:42` uses `api.get`; server is `r.Post("/api/search")` (`search.go:109`) → 404/405 |
| Header search box w/ 200ms debounce + `/api/search/suggest` | missing | Search box only on `/search` page; AppShell has none; no debounce; suggest unused |
| Per-hit snippet + `[mm:ss → mm:ss]` deep-link `/watch?t=` | partial | `:86-95` links `?t=start_sec`; route is `/videos/{id}/watch` not `/watch/{id}`; no formatted timecode range |
| Facet sidebar w/ counts | missing | None |
| Mode toggle FTS/Semantic/Hybrid persisted | missing | Only video/segment scope select (`:64-71`) |
| "did you mean" empty state | missing | Generic empty |
| Saved searches `POST /api/search/save` + sidebar | missing | Endpoint mounted but unused by web |
| Virtualized list | missing | Plain `<ul>` |
| `<mark>` bidi highlight; sanitize FTS chars; spinner/timeout budgets | missing | Snippet rendered as plain text (`:94`) |

### 11.5 Processing queue — **broken/stub**
| AC | Status | Evidence |
|---|---|---|
| Load last-24h jobs, "Show all" | partial | `ProcessingQueue.tsx:30` fetches `?limit=100`; no time window / show-all |
| Row: poster+title, stage/state badge, progress, ETA, attempts | partial | `:71-84` stage/state/progress/video_id only; no poster/title/ETA/attempts; `progress_pct` field not in API `Job` struct (`jobs.go`) |
| Inline actions Pause/Resume/Cancel/Retry/priority | missing | Endpoints exist (`/api/jobs/{id}/pause` etc.) but no UI |
| Bulk actions | missing | — |
| Per-stage cards + counts + `oldest_pending_age_sec` | missing | `/api/queue/stats` mounted, never called |
| Live WS `job.progress`, ≤1Hz throttle | **broken** | Subscribes `/ws/v1/events` (not mounted) and checks `msg.job` (server sends `{type,at,payload}`) |
| Force-pause / heartbeat staleness / WS reconcile | missing | — |
| Empty state admin CTA | missing | Generic empty (`:56`) |

### 11.6 Settings — **stub**
| AC | Status | Evidence |
|---|---|---|
| Libraries add/edit/delete w/ purge confirm | missing | No Libraries section (`Settings.tsx:26-37` only Account/Sessions/Appearance) |
| STT Backends list/health/config/Test | missing | `/api/settings/stt-backends` mounted, unused |
| Search section (hybrid weights etc.) | missing | — |
| Playback section | missing | — |
| Account: change password | missing | Only "Sign out everywhere" (`:50-59`) |
| Account: list active sessions w/ revoke (11.14) | **broken** | `SessionsTab` calls `GET /api/users/me/sessions` — not mounted; no revoke button |
| Account: PAT management (11.13) | missing | No UI, no endpoint |
| Appearance: theme/language/density | partial | Theme light/dark radio only (`:113-134`); no System, no language, no density |
| About section | missing | — |
| Admin "Unlock user" | missing | — |
| EC (path 422, If-Match 409, purge confirm) | missing | — |

### 11.7 Responsive design — **missing**
- No Tailwind (absent from `web/package.json`); AC mandates Tailwind breakpoint scale.
- `app.css` has only 3 media/query-ish lines; no `sm/md/lg/xl/2xl` system.
- No bottom tab bar ≤640px; no collapsible icon sidebar; no 44×44 touch targets; no zoom/overflow handling. **0/7 ACs.**

### 11.8 Dark/light theme — **partial**
| AC | Status | Evidence |
|---|---|---|
| Tokens on `:root[data-theme]` | complete | `tokens.css:6`, dark via `[data-theme="dark"]` |
| `system` honors prefers-color-scheme, live OS toggle | **missing** | `theme.ts` only supports `"light"\|"dark"`; no `system` option, no `matchMedia` change listener; spec/Settings demand System |
| Persist + applied before first paint (inline blocking script in index.html) | partial | `applyInitialTheme()` in `main.tsx:13` runs after JS bundle — `index.html` has **no** inline blocking script → FOUC on cold/slow load (AC explicitly requires inline script) |
| 150ms transition, no layout shift | missing | Not implemented |
| EC: corrupted key → fallback `system` | missing | Falls back to `light`, not `system` (`theme.ts:31`) |

### 11.9 Keyboard shortcuts — **missing**
No global shortcut layer, no `g l`/`g s` leader sequences, no `/` focus, no `?` help overlay, no IME guard. **0/AC.**

### 11.10 Offline PWA — **unwired**
| AC | Status | Evidence |
|---|---|---|
| SW registered after first paint | **unwired** | `public/sw.js` exists but **no `navigator.serviceWorker.register`** anywhere (`src/`, `index.html`, `vite.config.ts` all clean) → SW never loads |
| App-shell cache-first w/ build-hash bust | partial | `sw.js:6-12` static `mkt-shell-v1`, no build-hash, no 30-day max-age |
| SWR metadata TTL 5min | missing | `sw.js:28` explicitly skips all `/api/` |
| Idempotency-Key replay queue / bgsync | missing | No queue, no `Idempotency-Key` generation |
| Offline banner / queue UI in Settings | missing | — |
| Install prompt at session 3 | missing | — |
| Update "Reload" toast | missing | — |

### 11.11 Accessibility — **missing/partial**
- Some ARIA present (`role="toolbar"`, `role="radiogroup"`, `role="alert"`, `aria-label`s).
- **Missing:** skip-to-content link (none in AppShell/index.html), no `prefers-reduced-motion` CSS, no visible focus-ring tokens / `:focus-visible`, no `aria-live` error regions, no axe-core CI, no `docs/a11y.md`, player has no ARIA scaffolding (it's a stub). State badges are color-only (`ProcessingQueue.tsx:75` `mkt-state--{state}` text exists but no icon). **~1/10 ACs.**

### 11.12 i18n / RTL — **partial**
| AC | Status | Evidence |
|---|---|---|
| No string inlined in JSX | **violated** | Hardcoded English everywhere: "Grid"/"List"/"Sort" (`LibraryBrowser.tsx:57-78`), "Chapters"/"Speakers" (`VideoDetail.tsx:53,66`), "Back" (`VideoPlayer.tsx:48`), "Transcript"/"Title" (`Search.tsx:69-70`), "Stage/State/Progress" (`ProcessingQueue.tsx:64-67`), all of Settings |
| Strings in `web/src/i18n/{locale}.json` | **violated** | No `i18n/` dir; ~16 keys inlined in `lib/i18n.tsx:18-49` |
| `dir="rtl"` + Arabic numerals | partial | `i18n.tsx:78-79` sets `dir`; no Arabic-numeral / Intl number formatting |
| Mirrored layouts | partial | Single rule `[dir="rtl"] .mkt-shell__nav` in app.css; not systematic |
| Intl date/number formatting | missing | None |
| bidi-isolate, ICU plural, missing-key warning | missing | `t()` returns key on miss (`:86`), no warning |

### 11.13 PAT management API — **missing (entire story)**
No `personal_access_tokens` table migration, no `POST/GET/DELETE /api/me/tokens`, no `/api/users/{id}/tokens`, no PAT verifier, no Settings UI. Grep of `api/internal` for `me/tokens`/`personal_access` → 0 hits.

### 11.14 Active sessions API — **missing/unwired**
No `GET /api/me/sessions`, no `DELETE /api/me/sessions/{id}`. Only `DELETE /api/users/{id}/sessions/{session_id}` (admin, `auth.go:100`) and `.../refresh-tokens/{family_id}` exist. Web `Settings.tsx:71` calls non-existent `GET /api/users/me/sessions`; no per-session revoke UI, no "revoke all others" wired to `logout-all` from the list, no current-session pin.

---

## AC roll-up (approx, ~95 leaf ACs across 14 stories)

| Status | Count | Notes |
|---|---|---|
| complete | ~5 | theme tokens, basic login, logout-all wiring, some ARIA roles |
| partial | ~14 | library list render, theme persist (FOUC), search deep-link, RTL dir attr |
| stub | ~22 | pages exist but render placeholder-level only |
| broken | ~8 | search GET-vs-POST, player wrong endpoint, WS path + envelope |
| unwired/missing | ~46 | 11.7/11.9/11.13 entirely; PWA reg; 11.14; most of 11.2/11.3/11.5/11.6 |

---

## Top gaps by impact

1. **Search is non-functional (verb mismatch).** `Search.tsx:42` issues `GET /api/search`; the server only mounts `r.Post("/api/search")` (`search.go:109`). Every search returns 404/405. Flagship feature, zero working path.
2. **Video player wired to a non-existent endpoint.** `VideoPlayer.tsx:29` `GET /api/videos/{id}/stream` is not mounted; the required `POST /api/stream/sessions` handshake (Story 11.3 core) is never called. No HLS.js/Vidstack, no progress posting, no chapters/subs/speed/keyboard. Playback cannot work.
3. **Auth bootstrap probes a missing route.** `lib/auth.tsx:39` `GET /api/auth/me` has no handler; session restore on reload always fails → users bounced to `/login` on every refresh even with a valid cookie.
4. **WebSocket layer dead.** `lib/ws.ts:9` connects to `/ws/v1/events` (not mounted; real channels are `/ws/jobs`, `/ws/library/{id}`, `/ws/playback/{video_id}`) and `ProcessingQueue.tsx:39` expects `{type:"job.updated", job}` while the server emits `{type, at, payload}` (`ws.go:36-38`). No live updates anywhere.
5. **Service worker never registered.** `public/sw.js` is dead code — no `serviceWorker.register` in `src/`, `index.html`, or `vite.config.ts`. Story 11.10 effectively absent; PWA installability/offline = 0.
6. **Stories 11.7, 11.9, 11.13 entirely absent**; 11.14 unwired; 11.6 missing Libraries/STT/Search/Playback/About sections; pervasive i18n violation (hardcoded JSX strings in every page) breaks the RTL/Arabic parity goal.
