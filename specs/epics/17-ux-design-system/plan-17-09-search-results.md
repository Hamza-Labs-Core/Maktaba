# Implementation Plan — Story 17.09 Search results presentation

> Companion to [story-17-09-search-results.md](story-17-09-search-results.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web result list | `web/src/features/search/{ResultList.tsx,ResultGroup.tsx,Snippet.tsx,FacetSidebar.tsx,WhyThisResult.tsx}`. |
| Snippet renderer | Server returns `snippet` strings with `<mark>` tags from FTS (Epic 5 Story 5.4); client trusts the existing markup but sanitizes via `DOMPurify` allow-listed to `mark` only. |
| Timestamp chips | `<TimestampChip>` consumes `(video_id, start_sec)` and routes to `/watch/{id}?t={start_sec}` on click. |
| Facet sidebar | Collapsible by category; multi-select within a category, AND across categories. |
| Sort menu | Best match (default), Most recent, Most matches. |
| Out of scope | Server-side search execution (Epic 5 Story 5.4, Epic 7 Story 7.8); cross-language semantic ranking (Epic 5 Story 5.3). |

## 1. Architecture

```
   <SearchPage>
     ┌──────────────┐  ┌──────────────────────────────┐
     │ FacetSidebar │  │ ResultList                   │
     │  - language  │  │  - ResultGroup × N           │
     │  - library   │  │     - Header (poster, title) │
     │  - speaker   │  │     - Snippet × ≤3           │
     │  - type      │  │     - "more (k more hits)"   │
     │  - duration  │  │  - Pagination                │
     └──────────────┘  └──────────────────────────────┘
```

Results are grouped by `video_id` server-side; the client renders the first 3 snippets per group and shows "+ k more" if there are more. The server-side aggregator is owned by Epic 5/7.

## 2. Components

### 2.1 ResultGroup

```tsx
function ResultGroup({ group, onSeek }: ResultGroupProps) {
    return (
        <article className="mk-result-group">
            <Link to={`/watch/${group.videoId}`} className="mk-result-group__head">
                <Poster src={group.posterURL} ratio="16/9" />
                <div>
                    <h3>{group.title}</h3>
                    <Meta lang={group.languageCode} duration={group.durationSec} />
                </div>
                <Badge>{group.hitCount} {t('search.hits')}</Badge>
            </Link>
            <ul className="mk-result-group__snippets">
                {group.snippets.slice(0, 3).map((s, i) => (
                    <li key={i}>
                        <Snippet text={s.html} />
                        <TimestampChip onClick={() => onSeek(group.videoId, s.startSec)}>
                            {fmtTime(s.startSec)}
                        </TimestampChip>
                    </li>
                ))}
            </ul>
            {group.hitCount > group.snippets.length && (
                <Disclosure label={t('search.more_hits', { n: group.hitCount - group.snippets.length })}>
                    {/* lazy-load extra snippets */}
                </Disclosure>
            )}
        </article>
    );
}
```

### 2.2 Snippet

```tsx
function Snippet({ text }: { text: string }) {
    const safe = DOMPurify.sanitize(text, { ALLOWED_TAGS: ['mark', 'span'], ALLOWED_ATTR: [] });
    // 160-char ellipsize (server-side already does this; client guards).
    return <p className="mk-snippet" dangerouslySetInnerHTML={{ __html: safe }} />;
}
```

The snippet is pre-marked by FTS5; we only re-sanitize and constrain.

### 2.3 TimestampChip

```tsx
function TimestampChip({ children, onClick }: TimestampChipProps) {
    return (
        <button className="mk-timestamp-chip" onClick={onClick}>
            <Bidi dir="ltr">[{children}]</Bidi>
        </button>
    );
}
```

`<Bidi>` keeps timestamps LTR within Arabic snippets.

### 2.4 FacetSidebar

```tsx
function FacetSidebar({ facets, selected, onChange }: FacetSidebarProps) {
    return (
        <aside className="mk-facets">
            {FACET_GROUPS.map(group => (
                <Collapsible key={group.key} title={t(`facet.${group.key}`)}>
                    {facets[group.key]
                        .filter(f => f.count > 0)                                  // hide 0-count
                        .sort((a, b) => b.count - a.count)
                        .map(f => (
                            <FacetCheckbox
                                key={f.value}
                                checked={selected[group.key].includes(f.value)}
                                onChange={(v) => onChange(group.key, f.value, v)}
                                label={`${f.label} (${f.count})`}
                                hidden={f.count === 0}                              // assertion
                            />
                        ))}
                </Collapsible>
            ))}
        </aside>
    );
}
```

The story AC: "Facet count drops to 0: facet entry hidden, not greyed."

### 2.5 WhyThisResult

```tsx
function WhyThisResult({ scores }: { scores: { bm25: number; semantic: number } }) {
    const isAdmin = useFlag('admin_diagnostics');
    if (!isAdmin) return null;
    return (
        <details className="mk-why">
            <summary>{t('search.why')}</summary>
            <table>
                <tr><th>BM25</th><td>{scores.bm25.toFixed(3)}</td></tr>
                <tr><th>Semantic</th><td>{scores.semantic.toFixed(3)}</td></tr>
            </table>
        </details>
    );
}
```

Admin-only by default per the story AC.

## 3. Sort menu

```tsx
function SortMenu({ value, onChange }: SortMenuProps) {
    return (
        <Select value={value} onChange={onChange}
                options={[
                    { value: 'best',   label: t('search.sort.best') },
                    { value: 'recent', label: t('search.sort.recent') },
                    { value: 'most',   label: t('search.sort.most_matches') },
                ]}/>
    );
}
```

The selected sort is added as a query param (`?sort=best`) and forwarded server-side; this story is the UI surface, the server (Epic 7 Story 7.8) handles the actual ordering.

## 4. Pagination

20 video groups per page; server returns `total_groups` and `next_cursor`. We use cursor-based pagination via `<Pagination>` from [Story 17.2](story-17-02-component-library.md). The TC: "A query with 1,200 hits across 80 videos: render the first 20 video groups; pagination for the rest" maps to a 4-page paginator.

## 5. Test plan

### 5.1 Components

| Test | What it pins |
|---|---|
| `testResultGroupRendersFirst3Snippets` | Group with 5 snippets; only 3 rendered, "+2 more" disclosure. |
| `testTimestampChipNavigates` | Click chip → router push to `/watch/{id}?t=123`. |
| `testZeroFacetsHidden` | Facets list with 0-count entries → those entries absent. |
| `testSnippetSanitizesNonMark` | Server returned `<script>x</script>foo<mark>bar</mark>` → DOM has `foo<mark>bar</mark>`. |
| `testWhyThisResultAdminOnly` | Without admin_diagnostics flag → not rendered. |

### 5.2 Pagination

| Test | What it pins |
|---|---|
| `testPaginationFirstPageNoPrev` | First page → Prev disabled. |
| `testPaginationLastPageNoNext` | Last → Next disabled. |
| `testCursorAdvances` | Click Next → request includes `?cursor=...`. |

### 5.3 RTL

| Test | What it pins |
|---|---|
| `testTimestampChipBidiIsolate` | Snapshot of Arabic snippet with `[06:12]` → bracket sequence isolated. |

### 5.4 Edge

| Test | What it pins |
|---|---|
| `testDeletedVideoSurfacesInline` | Group's video is 404 on play → "Video no longer available" inline message replaces the row. |
| `testUnbalancedBidiInSnippet` | Server returns a snippet with unbalanced bidi controls → DOMPurify strips; bidi isolate prevents bleed. |
| `testSpeakerFacetWithOneHitSorted` | Speaker facet entries with `count=1` rendered last. |

## 6. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| 1,200 hits across 80 videos | First 20 groups; pagination for rest. | `testPaginationFirstPageNoPrev` |
| Click timestamp on deleted video | Inline "Video no longer available"; no nav. | `testDeletedVideoSurfacesInline` |
| Snippet with unbalanced bidi span | DOMPurify strips; `<Bidi>` isolates the chip. | `testUnbalancedBidiInSnippet` |
| Speaker facet with 1 hit | Sorted to bottom of its group. | `testSpeakerFacetWithOneHitSorted` |
| Facet count is 0 on first render | Entry hidden (filter at `> 0`). | `testZeroFacetsHidden` |
| Sort change | Re-issues query with `?sort=...`; preserves filters. | `testSortChangePreservesFilters` |
| Admin diagnostics flag flips off mid-session | `WhyThisResult` disappears on next render. | `testWhyDisappearsOnFlagFlip` |
| Snippet with `<script>` injection attempt | DOMPurify drops; only `mark`/`span` allowed. | `testSnippetSanitizesNonMark` |
| Pagination "Page 1 of 1" with 0 results | Renders `EmptyState kind="filtered_out"` (Story 17.5). | `testEmptyResultsRendersEmptyState` |
| Result group title contains LTR + RTL substrings | Bidi-isolated head; visual snapshot. | `testMixedDirectionTitle` |

## 7. Acceptance checklist

**Composition**
- [ ] Result groups; up to 3 snippets; "+k more"; timestamp chips.
- [ ] Facet sidebar with 0-count entries hidden.
- [ ] Sort menu wired to query.

**Security**
- [ ] DOMPurify constrains snippet markup.

**RTL**
- [ ] Bidi isolates around timestamps.

**Admin**
- [ ] WhyThisResult gated by flag.

**Tests**
- [ ] All §5 tests pass.

**Docs**
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.9.
