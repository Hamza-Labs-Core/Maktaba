# Implementation Plan — Story 11.4 Search Interface

> Companion to [story-11-04-search-interface.md](story-11-04-search-interface.md).
> Backed by `POST /api/search` and `GET /api/search/suggest`
> (Epic 7 Stories 7.8, 7.9; Epic 5 indexing).
> Match coordinates are **segment-level** per REVIEW §4.2 resolution.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `/search?q=…&mode=…&lang=…&library=…&speaker=…&type=…&from=…&to=…`. |
| Header search | `<HeaderSearch>` lives in the app shell; opens results in `/search` on Enter. |
| Placement | `web/src/routes/search/`, `web/src/features/search/`. |
| Data | TanStack Query for `/search` (POST → cached by URL key); separate `/suggest` query, debounced 200 ms. |
| Highlighting | Server returns `<mark>` spans inside snippets; client trusts and renders via `dangerouslySetInnerHTML` after DOMPurify pass. |
| Out of scope | Indexing pipeline (Epic 5); saved-search admin UI (lives under Settings). |

## 1. Component layout

```
<SearchPage>
 ├─ <SearchHeader>   debounced input + mode toggle (FTS / Semantic / Hybrid)
 ├─ <SearchFacets>   collapsible groups: language / library / speaker / content type / date range
 ├─ <SavedSearches>  sidebar list (My Searches)
 └─ <ResultsList>    virtualized; grouped by video; per-hit snippet + timestamp deep-link
       <ResultGroup video>
         <HitRow segmentId start end snippetHtml>
```

Header search box (`<HeaderSearch>`) is rendered globally.

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/routes/search/index.tsx` | Lazy route. |
| `web/src/features/search/SearchPage.tsx` | Layout + filters + virtualized list. |
| `web/src/features/search/HeaderSearch.tsx` | Global search box + suggest dropdown. |
| `web/src/features/search/useSearch.ts` | TanStack Query over `POST /api/search`. |
| `web/src/features/search/useSuggest.ts` | Debounced `GET /api/search/suggest?q=`. |
| `web/src/features/search/useSavedSearches.ts` | List + save + delete via `/api/search/save`. |
| `web/src/features/search/components/Facets.tsx` | Faceted filters with counts from response. |
| `web/src/features/search/components/HitRow.tsx` | Snippet + `[mm:ss → mm:ss]` button → `/watch/{id}?t=`. |
| `web/src/features/search/components/ModeToggle.tsx` | FTS | Semantic | Hybrid; persists per user. |
| `web/src/features/search/components/SaveSearchButton.tsx` | POST current query+filters; idempotent via `Idempotency-Key`. |
| `web/src/features/search/sanitize.ts` | `sanitizeFtsQuery()` — escapes `"`, `(`, `)`, `*` for FTS5 safety. |
| `web/src/features/search/utils/timestamp.ts` | `mmss(sec)` helper. |

## 3. Data model

```ts
type SearchMode = 'fts' | 'semantic' | 'hybrid';

type SearchRequest = {
  q: string;
  mode: SearchMode;
  facets?: {
    lang?: string[]; library?: string[]; speaker?: string[];
    type?: string[]; from?: string; to?: string;
  };
  limit?: number;          // default 20
  cursor?: string;
};

type SearchResponse = {
  total: number;
  cursor?: string;         // next page
  facets: { lang: FacetCount[]; library: FacetCount[]; ... };
  groups: ResultGroup[];
  suggestions?: string[];  // when total === 0
  took_ms: number;
};

type ResultGroup = {
  video: { id: string; title: string; posterUrl: string; durationSec: number; lang: string; };
  matches: HitMatch[];     // segment-level
};

type HitMatch = {
  segmentId: string;
  start_sec: number;
  end_sec: number;
  snippetHtml: string;     // server-emitted <mark> spans, sanitized client-side
  score: number;
};
```

## 4. Implementation steps

### 4.1 Header search

- Controlled input; updates `q` on every keystroke.
- `useSuggest` debounces 200 ms; minimum length 2.
- Suggestions dropdown listbox; arrow keys navigate; Enter executes a navigate to `/search?q=`.

### 4.2 Search request

```ts
const search = useQuery({
  queryKey: ['search', q, mode, facets, cursor],
  queryFn: () => api.post('/search', { q, mode, facets, cursor, limit: 20 }),
  enabled: q.length >= 2,
  keepPreviousData: true,
  staleTime: 60_000,
});
```

Loading visual: spinner only after 500 ms; "Search took longer than expected" after 2 s; "Search took too long, retry?" on 5 s timeout (REVIEW §1.4.d).

### 4.3 Snippet rendering

```tsx
<span
  dir="auto"
  dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(hit.snippetHtml, {
    ALLOWED_TAGS: ['mark', 'br', 'span', 'bdi'],
    ALLOWED_ATTR: ['class', 'dir'],
  }) }}
/>
```

Bidi: every snippet wraps in `<bdi>` so RTL/LTR queries don't bleed. `<mark>` color comes from design tokens.

### 4.4 Facets

Counts come from the response. Each facet group is collapsible (`<details>`); clicking a checkbox updates the URL and refetches. Facet counts update with each response (no separate `/facets` endpoint).

### 4.5 Saved searches

```ts
async function saveCurrent(name: string) {
  await api.post('/search/save', { name, q, mode, facets },
    { headers: { 'Idempotency-Key': uuidv4() } });
}
```

Saved searches render in a sidebar; clicking pushes the URL.

### 4.6 Virtualization

Result rows are flattened from groups, with a sticky group header per video. TanStack Virtual estimates row size by snippet length (avg 80 px, header 56 px).

### 4.7 Edge cases

| Case | Handling |
|---|---|
| `total: 0` with suggestions | Empty state shows "No matches for «query»" + each suggestion as a chip. |
| Unbalanced quotes / FTS5 chars | `sanitizeFtsQuery` escapes; original query preserved in URL for shareability. |
| Backend 5xx or > 5 s | Toast + Retry; do not silently degrade to partial index. |
| Query > 1 KB | Inline error before submit. |
| Mixed-script query | Suggest list renders RTL/LTR correctly via `dir="auto"`. |

## 5. Test cases

### 5.1 Unit

| Test | Asserts |
|---|---|
| `header search debounce` | After 5 keystrokes within 200 ms, only one `/suggest` call fires. |
| `min length 2` | `/suggest` not called for length < 2. |
| `sanitizeFtsQuery escapes quotes` | `it"s` → `it""s`. |
| `mode persists per user` | Switching mode updates URL and writes `localStorage.search.mode`. |
| `snippet sanitization` | `<script>alert(1)</script>` is stripped; `<mark>` survives. |

### 5.2 e2e

| Test | Asserts |
|---|---|
| `Arabic query streams in within budget` | p95 ≤ 500 ms warm, ≤ 2 s cold against fixture. |
| `timestamp deep-link plays exact second` | Clicking a hit's `[06:12]` opens `/watch/{id}?t=372` and player starts at 06:12 ± 0.2 s. |
| `mode toggle changes URL and result set` | Switching FTS → Semantic updates `?mode=semantic` and triggers refetch. |
| `save search appears in sidebar` | After `Save`, the entry appears under "My Searches". |
| `1k hits virtualize` | DOM nodes ≤ 60; scroll FPS ≥ 55. |

## 6. Performance

- Suggest dropdown ≤ 50 ms typical (server cached).
- Search list keypress → repaint ≤ 16 ms (debounced).
- Bundle delta for `/search` route ≤ 25 KB gz.

## 7. Dependencies

- API: Epic 7 Stories 7.8, 7.9.
- Indexing: Epic 5 Stories 5.1–5.7 (chunking, FTS, vector, hybrid).
- Owner of segment-level coordinates: Epic 5 Story 5.2 indexer (REVIEW §4.2).
