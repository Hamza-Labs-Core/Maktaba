# Implementation Plan — Story 17.11 Subtitle and transcript presentation

> Companion to [story-17-11-transcript-presentation.md](story-17-11-transcript-presentation.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web component | `web/src/features/transcript/{TranscriptSidebar.tsx,Segment.tsx,FindBar.tsx,useCurrentSegment.ts}`. |
| Inline subtitle layer | `web/src/features/player/SubtitleLayer.tsx` consumes the same styles as the sidebar (Story 17.8 §6). |
| Virtualization | `react-window`'s `FixedSizeList`/`VariableSizeList` for the sidebar; auto-scroll uses virtual list index. |
| Auto-scroll toggle | Setting persisted per user (`transcript.auto_scroll`); default true. |
| Find bar | `Cmd/Ctrl+F` keyboard handler scoped to the sidebar container; `n` / `Shift+n` cycle. |
| Out of scope | Transcript ingest / segments table (Epic 3); player engine (Story 17.8); subtitle styling controls (Story 17.8 §6). |

## 1. Composition

```tsx
// TranscriptSidebar.tsx
export function TranscriptSidebar({ videoId }: { videoId: string }) {
    const { data: segments } = useTranscript(videoId);
    const current = useCurrentSegment(segments);
    const [autoScroll, setAutoScroll] = useTranscriptPreference('auto_scroll');
    const listRef = useRef<VariableSizeList>(null);

    useEffect(() => {
        if (!autoScroll || !listRef.current || current.index < 0) return;
        listRef.current.scrollToItem(current.index, 'center');
    }, [current.index, autoScroll]);

    return (
        <aside className="mk-transcript">
            <header>
                <Toggle label={t('transcript.auto_scroll')} checked={autoScroll} onChange={setAutoScroll} />
                <CopyMenu />
            </header>
            <FindBar list={listRef} />
            <VariableSizeList
                ref={listRef}
                height={containerHeight} itemCount={segments.length}
                itemSize={(i) => estimateSegmentHeight(segments[i])}>
                {({ index, style }) => (
                    <Segment
                        style={style}
                        segment={segments[index]}
                        isCurrent={index === current.index}
                        onClick={() => seekTo(segments[index].startSec)}
                    />
                )}
            </VariableSizeList>
        </aside>
    );
}
```

### 1.1 `estimateSegmentHeight`

The `VariableSizeList` needs a deterministic per-row height. We don't
do real text measurement at scroll-time (too expensive at 50k rows);
instead we estimate from text length, line-height, and chrome height.

```ts
// Average chars per line at the transcript's typography scale.
const CHARS_PER_LINE = 64;          // 18px Inter at the transcript width
const LINE_HEIGHT_PX = 28;          // 1.55 × 18 (matches CSS)
const CHROME_PX      = 32;          // timestamp + speaker badge + paddings

export function estimateSegmentHeight(seg: Segment): number {
    const text = seg.text ?? '';
    const lines = Math.max(1, Math.ceil(text.length / CHARS_PER_LINE));
    return CHROME_PX + lines * LINE_HEIGHT_PX;
}
```

The estimate is intentionally over-conservative (CHARS_PER_LINE is the
*upper* bound for typical content); the auto-scroll precision target
(±1 segment) is met because `scrollToItem('center')` from
`react-window` snaps to the row regardless of estimate accuracy.
Dynamic measurement via `react-window`'s `resetAfterIndex` is reserved
for v2 if real-world content drifts the estimate by > 20%.

## 2. Segment component

```tsx
function Segment({ segment, isCurrent, onClick, style }: SegmentProps) {
    const isSilence = !segment.text || segment.text.trim().length === 0;
    return (
        <button
            style={style}
            className={clsx('mk-segment', isCurrent && 'is-current', isSilence && 'is-silence')}
            onClick={onClick}>
            <TimestampChip start={segment.startSec} />
            {segment.speakerLabel && <SpeakerBadge label={segment.speakerLabel} />}
            <span className="mk-segment__text">
                <Bidi>{segment.text || <em>{t('transcript.silence')}</em>}</Bidi>
            </span>
        </button>
    );
}
```

`<Bidi>` (from [Story 17.7](story-17-07-rtl-layout.md)) isolates direction so a Latin name in an Arabic segment stays LTR (story EC).

## 3. Current-segment tracking

```ts
// useCurrentSegment.ts — picks the segment containing the player's current time.
export function useCurrentSegment(segments: Segment[]) {
    const t = usePlayerTime();
    return useMemo(() => {
        if (!segments?.length) return { index: -1 };
        const idx = binarySearchSegment(segments, t);
        return { index: idx };
    }, [segments, t]);
}

function binarySearchSegment(segs: Segment[], t: number): number {
    let lo = 0, hi = segs.length - 1;
    while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (t < segs[mid].startSec)        hi = mid - 1;
        else if (t > segs[mid].endSec)     lo = mid + 1;
        else                                return mid;
    }
    // No exact-containing segment. If the player is past the last
    // segment's end (typical for the credits/silence at the end of a
    // video), return -1 so the UI shows "no current segment" rather
    // than highlighting the last segment forever. Earlier draft
    // returned `Math.max(0, lo - 1)` which made the last segment
    // sticky for the rest of playback.
    if (lo > segs.length - 1) return -1;
    // Player is in a gap between segments — caller may want the
    // *previous* segment as "the most recent context".
    return Math.max(0, lo - 1);
}
```

`useCurrentSegment` honors the `-1` return: a `< 0` index renders no
highlight in the sidebar and `scrollToItem` is skipped (the early
return at the top of the auto-scroll effect already gates on
`current.index < 0`).

The 200 ms TC ("sidebar's current segment highlight tracks the player within 200 ms") is satisfied because `usePlayerTime` ticks at the player's `timeupdate` event, which fires ~4× per second on Vidstack.

## 4. Find bar

```tsx
function FindBar({ list }: { list: RefObject<VariableSizeList> }) {
    const [q, setQ] = useState('');
    const [matches, setMatches] = useState<number[]>([]);
    const [cursor, setCursor] = useState(0);

    useEffect(() => {
        if (!q) { setMatches([]); return; }
        const lower = q.toLowerCase();
        const ids = segmentTexts
            .map((t, i) => (t.toLowerCase().includes(lower) ? i : -1))
            .filter(i => i >= 0);
        setMatches(ids);
        setCursor(0);
        if (ids[0] != null) list.current?.scrollToItem(ids[0], 'center');
    }, [q]);

    useHotkey('mod+f', (e) => { e.preventDefault(); setOpen(true); });
    useHotkey('n', () => move(+1));
    useHotkey('shift+n', () => move(-1));

    function move(delta: number) {
        if (!matches.length) return;
        const next = (cursor + delta + matches.length) % matches.length;
        setCursor(next);
        list.current?.scrollToItem(matches[next], 'center');
    }

    return (
        <div className="mk-find" hidden={!open}>
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('find.placeholder')} />
            <span>{matches.length ? `${cursor+1}/${matches.length}` : '0/0'}</span>
            <button onClick={() => move(-1)} aria-label={t('find.prev')}>↑</button>
            <button onClick={() => move(+1)} aria-label={t('find.next')}>↓</button>
            <button onClick={() => setOpen(false)} aria-label={t('action.close')}>×</button>
        </div>
    );
}
```

The find bar highlights matches by wrapping matched substrings in a `<mark>` tag inside the segment's text.

## 5. Inline subtitle layer

```tsx
// SubtitleLayer.tsx — sits inside the player overlay
export function SubtitleLayer() {
    const segs = useCurrentCaptionsSegments();
    return (
        <div className="mk-subtitle-layer" aria-live="polite">
            {segs.map(s => <p key={s.id} className="mk-subtitle"><Bidi>{s.text}</Bidi></p>)}
        </div>
    );
}
```

The font, size, color come from CSS custom properties set by [Story 17.8](story-17-08-player-controls.md)'s `applySubtitleStyle`. The styling is **shared** between the inline layer and sidebar via CSS variables, which is what gives the "user's eye doesn't have to re-learn either" behavior.

## 6. Copy actions

```tsx
function CopyMenu() {
    const segs = useTranscript(...).data ?? [];
    return (
        <Menu trigger={<IconButton icon="copy" />}>
            <MenuItem onClick={() => navigator.clipboard.writeText(segs.map(s => s.text).join(' '))}>
                {t('transcript.copy_plain')}
            </MenuItem>
            <MenuItem onClick={() => navigator.clipboard.writeText(
                segs.map(s => `[${fmtTime(s.startSec)}] ${s.text}`).join('\n')
            )}>
                {t('transcript.copy_timestamped')}
            </MenuItem>
        </Menu>
    );
}
```

## 7. Test plan

### 7.1 Component

| Test | What it pins |
|---|---|
| `testCurrentSegmentTracksWithin200ms` | Drive player time; sidebar's `is-current` class matches within one render frame. |
| `testClickSeeksAndCenters` | Click segment 100; player seeks to its start; list scrolls to center the segment. |
| `testSilenceSegmentRenderedDimmed` | Empty-text segment → `<em>` placeholder + `is-silence` class. |
| `testBidiIsolatePreservesDirection` | Arabic segment with Latin name → name renders LTR. |
| `testAutoScrollToggleHonored` | Toggle off; player advances; list does not scroll. |

### 7.2 Find

| Test | What it pins |
|---|---|
| `testFindBarOpensOnCmdF` | Press Cmd+F → bar visible. |
| `testFindMatchesHighlighted` | Query "gratitude" → all segments with that token wrapped in `<mark>`. |
| `testNCyclesNext` | Press n → cursor advances; Shift+n decrements; wraps around. |
| `testEmptyQueryNoHighlights` | Empty input → no marks. |

### 7.3 Performance

| Test | What it pins |
|---|---|
| `testVirtualized50KSegments` | Render 50,000 segments; first paint < 100 ms; scroll uses virtual indices. |
| `testAutoScrollSeeksByIndexNotPixel` | Trigger seek; list `scrollToItem(idx)` called; no pixel arithmetic. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Silence segment | Dim placeholder; preserves timing context. | `testSilenceSegmentRenderedDimmed` |
| Bidi-mixed text | `<Bidi>` isolates. | `testBidiIsolatePreservesDirection` |
| 50,000 segments | Virtualized; auto-scroll by index. | `testVirtualized50KSegments` |
| Auto-scroll toggle off | List does not move on time updates. | `testAutoScrollToggleHonored` |
| User scrolling manually while playing | Auto-scroll yields temporarily; resumes 4 s after last user scroll. | `testManualScrollPausesAutoScroll` |
| Find query with regex special chars | Escape on entry. | `testFindEscapesRegex` |
| Inline subtitle layer with two simultaneous tracks (commentary + dialogue) | Both render stacked; aria-live polite. | `testTwoTracksRender` |
| Subtitle style toggled mid-play | CSS vars update; both layers re-style without source reload. | `testStyleLiveAppliesToBoth` |
| Copy timestamped while no transcript loaded | Menu items disabled. | `testCopyDisabledWithoutTranscript` |
| Right-to-left transcript with timestamps | Timestamps stay LTR via `<Bidi dir="ltr">` inside `<TimestampChip>`. | `testRTLTimestamps` |

## 9. Acceptance checklist

**Sidebar**
- [ ] Virtualized; current segment highlighted within 200 ms.
- [ ] Click seeks; auto-scroll toggle.

**Find**
- [ ] Cmd/Ctrl+F bar; n / Shift+n cycle; matches highlighted.

**Inline**
- [ ] Subtitle layer shares styles with sidebar.

**Copy**
- [ ] Plain + timestamped variants.

**Tests**
- [ ] All §7 tests pass.

**Docs**
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.11.
