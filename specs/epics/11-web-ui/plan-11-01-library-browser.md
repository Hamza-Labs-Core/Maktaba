# Implementation Plan — Story 11.1 Library Browser

> Companion to [story-11-01-library-browser.md](story-11-01-library-browser.md).
> The story states *what* and *why*; this plan states *how*.
> Stack anchored by [architecture.md §6.2](../../architecture.md) (React 18 + Vite + Tailwind);
> data shapes anchored by Epic 7 Stories 7.2 (cursor pagination), 7.4 (videos list).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Framework | React 18, Vite, TypeScript, React Router v6, Tailwind CSS, TanStack Query v5, TanStack Virtual. |
| Placement | `web/src/routes/library/` (route + page), `web/src/features/library/` (hooks + components). |
| Data fetching | `useLibraryVideos(filters, cursor)` — wraps `GET /api/libraries/{id}/videos` (Epic 7 Story 7.4) via TanStack Query infinite query. |
| URL state | `?lang=ar&type=lecture&duration=10-30&speaker=…&tag=…&library=…&sort=recent&view=grid` parsed by `useLibraryFilters()` (custom hook over `useSearchParams`). |
| Local prefs | View mode (grid/list) and density persist in `localStorage` under `maktaba.library.view` and `maktaba.library.density`; per-user keying handled by Story 11.6. |
| Components | Token-driven primitives from Epic 17 Stories 17.1–17.2 (`<Card>`, `<Badge>`, `<FilterChip>`); no hard-coded color/spacing. |
| Out of scope | Library CRUD (Story 11.6), video detail (11.2), search (11.4). |

## 1. Architecture diagram

```
                ┌────────────────────────────────────────────────┐
                │ /library route (LibraryPage)                   │
                │   useLibraryFilters()  ◄── URL search params   │
                │   useLibraryVideos(filters)                    │
                │   useViewMode()  ◄── localStorage              │
                └───────────────┬────────────────────────────────┘
                                ▼
        ┌───────────────────────────────────────────────────────────┐
        │  <LibraryToolbar>      <FilterChips>   <SortMenu> <ViewToggle>│
        │  ───────────────────────────────────────────────────────── │
        │  <FilterSidebar> (lg+)  ──► language / type / duration /   │
        │                              speaker (typeahead) / tag      │
        │  ───────────────────────────────────────────────────────── │
        │  <VideoGrid> | <VideoList>  (virtualized, TanStack Virtual)│
        │     <VideoCard> | <VideoRow>                                │
        │     <Sentinel> (IntersectionObserver → fetchNextPage)       │
        │  ───────────────────────────────────────────────────────── │
        │  <EmptyState> | <FilterEmptyState>  (CTAs per role)         │
        └───────────────────────────────────────────────────────────┘
```

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/routes/library/index.tsx` | Route component; lazy-loaded by router. |
| `web/src/features/library/LibraryPage.tsx` | Top-level layout (toolbar + sidebar + grid/list + sentinel). |
| `web/src/features/library/useLibraryFilters.ts` | URL ↔ filter-state hook; emits `LibraryFilters` and `setFilter(name, value)`. |
| `web/src/features/library/useLibraryVideos.ts` | Infinite query hook over `GET /api/libraries/{id}/videos`. |
| `web/src/features/library/useViewMode.ts` | localStorage-backed view-mode hook (`grid` | `list`). |
| `web/src/features/library/components/VideoGrid.tsx` | Virtualized grid (column-aware, 1/2/3/4/6 cols by breakpoint). |
| `web/src/features/library/components/VideoList.tsx` | Virtualized list rows (filename, size, mtime, state). |
| `web/src/features/library/components/VideoCard.tsx` | Poster, title, duration badge, language flag, processing badge. |
| `web/src/features/library/components/FilterChips.tsx` | Active-filter pills with remove affordance. |
| `web/src/features/library/components/FilterSidebar.tsx` | Multi-select facets; speaker is a typeahead. |
| `web/src/features/library/components/SortMenu.tsx` | Sort dropdown wired to `?sort=`. |
| `web/src/features/library/components/EmptyState.tsx` | "Library empty" + admin "Scan now"; "No matches" + "Clear filters". |
| `web/src/lib/api/videos.ts` | Typed fetcher + `LibraryVideo` type from generated GraphQL types. |
| `web/src/test/library/LibraryPage.test.tsx` | Vitest + Testing Library suite. |
| `web/e2e/library.spec.ts` | Playwright e2e (visual regression on the matrix from Story 11.7). |

## 3. State model

```ts
type LibraryFilters = {
  libraryId?: string;
  lang: string[];          // multi
  type: string[];          // lecture, sermon, podcast, …
  duration: ('lt10'|'10-30'|'30-60'|'gt60')[];
  speaker: string[];
  tag: string[];
  sort: 'recent'|'title-asc'|'title-desc'|'duration-asc'|'duration-desc'|'recently-watched'|'language';
  view: 'grid'|'list';     // mirrored to localStorage but URL is authoritative when present
};

type VideoCardData = {
  id: string;
  title: string;
  posterUrl: string;
  durationSec: number;
  langs: string[];
  state: 'PROCESSING'|'READY'|'FAILED'|'MISSING'|'READY_NO_AUDIO'|'SUPERSEDED'|'CORRUPTED';
  size?: number;
  modifiedAt?: string;
  filename?: string;       // list view only
};
```

## 4. Implementation steps

### 4.1 Routing & data hook

```tsx
// web/src/routes/library/index.tsx
const LibraryPage = lazy(() => import('@/features/library/LibraryPage'));
export const libraryRoute = {
  path: '/library/:libraryId?',
  element: <Suspense fallback={<LibrarySkeleton/>}><LibraryPage/></Suspense>,
};
```

`useLibraryVideos` is a TanStack `useInfiniteQuery` keyed by serialized
filters. `getNextPageParam` returns `lastPage.next_cursor` (Epic 7
Story 7.2 contract). On filter change, the cursor is reset by changing
the query key — we never hand-clear the cache.

```ts
const queryKey = ['library', filtersKey(filters)];
useInfiniteQuery({
  queryKey,
  queryFn: ({ pageParam }) =>
    fetchLibraryVideos({ ...filters, cursor: pageParam, limit: 60 }),
  initialPageParam: null,
  getNextPageParam: (last) => last.next_cursor ?? undefined,
  staleTime: 30_000,
});
```

### 4.2 Filter URL plumbing

`useLibraryFilters` reads `useSearchParams` once; mutating any filter
calls `setSearchParams(next, { replace: false })` so links are
shareable. Multi-value chips are encoded as repeated keys
(`?lang=ar&lang=en`) and parsed by `searchParams.getAll('lang')`.

### 4.3 Virtualization

Grid uses `useVirtualizer` with column count derived from a CSS variable
populated by a `ResizeObserver` on the grid container. Row height is
fixed per breakpoint (poster is square or 16:9; same height for all
cards in a row). List uses a single-column virtualizer with row height
72 px.

### 4.4 Empty / loading / error states

| State | Visual |
|---|---|
| First load | `<LibrarySkeleton>` — 12 placeholder cards. |
| Filter empty | `<FilterEmptyState>` with "Clear filters" CTA. |
| Library empty | `<EmptyState>` with admin-only "Scan now" CTA (gated by `useFeatureFlag('admin')`). |
| Slow page (> 5 s) | Inline "Still loading…" under the sentinel (timer started on `isFetchingNextPage`). |
| Page error | Toast + "Retry" button on the sentinel. |

### 4.5 Cross-cutting

- **i18n.** No string in JSX. All via `useT()` from Story 11.12; namespaces `library.*`.
- **Bidi.** Titles wrap in `<bdi>` (Unicode bidi isolate); `dir="auto"` on the wrapper.
- **A11y.** Grid uses `role="list"`, cards `role="listitem"`. Filter chips are buttons with `aria-pressed`. Sentinel announces "Loading more videos…" via `aria-live="polite"`.
- **Telemetry.** Optional, off by default; emits `library.viewed`, `library.filtered`, `library.paged` (no titles or paths).

## 5. Test cases

### 5.1 Unit (Vitest + Testing Library)

| Test | Asserts |
|---|---|
| `parses url filters and round-trips through setSearchParams` | `?lang=ar&lang=en&type=lecture` → `{ lang:['ar','en'], type:['lecture'] }` and back. |
| `infinite query resets when filters change` | `queryKey` change drops old pages; first page refetches. |
| `view-mode persists to localStorage` | Toggle grid → list → grid; localStorage observed. |
| `chip remove emits correct setSearchParams` | Removing `lang=ar` updates URL to `?lang=en&type=lecture`. |
| `empty state CTA is admin-only` | With `useFeatureFlag('admin') === false`, "Scan now" not in DOM. |
| `mixed-direction title renders inside bdi` | RTL container + LTR title doesn't bleed direction. |

### 5.2 e2e (Playwright)

| Test | Asserts |
|---|---|
| `loads 1000 video fixture and scrolls smoothly` | First paint ≤ 1.5 s on cold cache; FPS > 55 during scroll. |
| `deep-link reproduces filtered view` | Visiting `?lang=ar&type=lecture` shows the same chips and result set as filtering manually. |
| `grid → list → grid preserves scroll` | Scroll to row 30; toggle list; toggle grid; scroll position restored. |
| `applying a filter mid-pagination resets cursor` | Page 2 loaded, then add `lang=ar` → grid empties and refetches page 0. |
| `slow network shows still-loading hint` | Inject 6 s delay on next page; "Still loading…" appears. |

### 5.3 Edge cases

- Server returns 0 results due to a deleted tag chip: toast fires once, chip removed from URL, query refetches.
- Poster URL 404: `<img onerror>` swaps to placeholder; row stays interactive.
- `state = MISSING/CORRUPTED/SUPERSEDED`: badge color and label per design tokens; row click still navigates to detail (Story 11.2 handles the detail-side behavior).

## 6. Performance budget

| Metric | Target | How measured |
|---|---|---|
| First paint (cold cache, 1k items) | ≤ 1.5 s | Lighthouse CI, LAN profile. |
| Steady-state scroll | ≥ 55 fps | Playwright trace + `web-vitals`. |
| Re-render on chip toggle | ≤ 16 ms | React Profiler in dev; assert `<= 1 commit` in test. |
| Bundle delta | ≤ 35 KB gz for the library route | `vite-bundle-visualizer` snapshot in CI. |

## 7. Dependencies and follow-ups

- Blocks **none**; consumed by Stories 11.2 (detail navigation) and 11.4 (search "Open in library" link).
- Depends on **Epic 7 Stories 7.2, 7.4** (cursor pagination + videos endpoint), **Epic 17 Stories 17.1, 17.2** (tokens + primitives).
- Follow-up: Story 11.10 wraps the library response in the SW cache (`stale-while-revalidate`).
