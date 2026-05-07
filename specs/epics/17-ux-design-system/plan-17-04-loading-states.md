# Implementation Plan — Story 17.04 Loading states & skeleton screens

> Companion to [story-17-04-loading-states.md](story-17-04-loading-states.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Skeleton primitives | `design/components/src/Feedback/Skeleton.tsx` and shape variants per layout (Row, Card, Avatar, Text). |
| Spinner primitive | `design/components/src/Feedback/Spinner.tsx`. |
| Async wrappers | `design/components/src/Feedback/Async.tsx` — `<Async>` consumes a `useQuery`-like state object and renders skeleton/error/data with the timing rules. |
| Min-display + timeout | A `useDelayedSkeleton(load, opts)` hook that hides the skeleton if data resolves < 200 ms (no flash) and swaps to a generic spinner + retry after 5 s. |
| Player buffer spinner | Lives in player components (Epic 11/12); wrapper here. |
| Out of scope | Page-level loading at the route level (Epic 11 web, etc.); concrete error states ([Story 17.5](story-17-05-error-empty-states.md)). |

## 1. Min-display ≥ 200 ms (no flash)

The story AC: "Skeleton: shape-matches the final content; never shows for < 200 ms (avoid flash); maxes at 5 s before swapping to a generic spinner + retry."

```ts
// design/components/src/Feedback/useDelayedSkeleton.ts
//
// Earlier draft used `useState`+`useEffect` with `[state.kind]` in the
// dep array; the effect's `phase` reference closed over a stale value
// because `phase` was not in deps. The 200 ms minimum-hold branch
// could then misfire (the closure saw `phase === 'pending'` even
// though we'd already flipped to `'skeleton'`). Refactor as
// `useReducer` so transitions are based on the current state, not a
// closure capture.
type Phase = 'pending' | 'skeleton' | 'spinner' | 'done' | 'error';
type S = { phase: Phase; startedAt: number | null };
type A =
    | { type: 'load' }
    | { type: 'show-skeleton' }
    | { type: 'show-spinner' }
    | { type: 'success'; now: number }
    | { type: 'error' };

function reducer(s: S, a: A): S {
    switch (a.type) {
        case 'load':           return { phase: 'pending', startedAt: Date.now() };
        case 'show-skeleton':  return { ...s, phase: 'skeleton' };
        case 'show-spinner':   return { ...s, phase: 'spinner' };
        case 'success': {
            const since = a.now - (s.startedAt ?? a.now);
            if (s.phase === 'skeleton' && since < 200) {
                // Caller schedules the deferred 'done' transition; we
                // stay in skeleton until then.
                return s;
            }
            return { ...s, phase: 'done' };
        }
        case 'error':          return { ...s, phase: 'error' };
    }
}

export function useDelayedSkeleton<T>(state: AsyncState<T>): DelayedState<T> {
    const [s, dispatch] = useReducer(reducer, { phase: 'pending', startedAt: null });
    useEffect(() => {
        if (state.kind === 'loading' && s.startedAt == null) {
            dispatch({ type: 'load' });
            const t1 = setTimeout(() => dispatch({ type: 'show-skeleton' }), 100);
            const t2 = setTimeout(() => dispatch({ type: 'show-spinner' }), 5_000);
            return () => { clearTimeout(t1); clearTimeout(t2); };
        }
        if (state.kind === 'success') {
            const now = Date.now();
            const since = now - (s.startedAt ?? now);
            if (s.phase === 'skeleton' && since < 200) {
                const t = setTimeout(() => dispatch({ type: 'success', now: Date.now() }), 200 - since);
                return () => clearTimeout(t);
            }
            dispatch({ type: 'success', now });
        }
        if (state.kind === 'error') dispatch({ type: 'error' });
    }, [state.kind, s.phase, s.startedAt]);
    return { phase: s.phase, data: state.data, error: state.error };
}
```

- 100 ms initial delay before skeleton (handles the AC: "0-ms response: skeleton never shown" — if data arrives in < 100 ms, we never enter `skeleton`).
- 200 ms minimum skeleton hold once shown (AC: "do not flash for < 200 ms").
- 5 s upgrade to generic spinner + "Still loading" caption.
- 60 s upgrade to retry CTA.

## 2. Skeleton primitive

```tsx
// design/components/src/Feedback/Skeleton.tsx
export function Skeleton({ width, height, radius = 'sm', rounded, className }: SkeletonProps) {
    return (
        <div
            className={clsx('mk-skel', className)}
            style={{
                width: width ?? '100%',
                height: height ?? 'var(--space-4)',
                borderRadius: rounded ? '999px' : `var(--radius-${radius})`,
            }}
            aria-hidden
            role="presentation"
        />
    );
}

// Shape variants — shape-match real content
export const SkeletonText = (p) => <Skeleton height="1em" {...p} />;
export const SkeletonCard = () => (
    <div className="mk-skel-card">
        <Skeleton height="180px" />
        <SkeletonText width="70%" />
        <SkeletonText width="40%" />
    </div>
);
export const SkeletonRow = ({ count = 6 }) => (
    Array.from({length: count}).map((_, i) => <SkeletonText key={i} width={`${60 + (i*7)%30}%`} />)
);
```

`mk-skel` CSS:

```css
.mk-skel {
    background: linear-gradient(90deg,
        color-mix(in oklab, var(--color-fg) 8%, transparent),
        color-mix(in oklab, var(--color-fg) 18%, transparent),
        color-mix(in oklab, var(--color-fg) 8%, transparent));
    background-size: 200% 100%;
    animation: mk-shimmer 1.4s ease-in-out infinite;
}
@keyframes mk-shimmer { 0% { background-position: 100% 0 } 100% { background-position: -100% 0 } }
@media (prefers-reduced-motion: reduce) { .mk-skel { animation: none; } }
```

Shape-matching is the responsibility of the consumer: `<Async><SkeletonCard /></Async>` is wrong; `<Async><CardLoadingShape data={data} /></Async>` is right. We document the convention and lint by checking that `<Skeleton*>` only appears inside `*Loading*` files.

## 3. Spinner primitive

```tsx
export function Spinner({ size = 'md', label }: SpinnerProps) {
    return (
        <span className={`mk-spinner mk-spinner--${size}`} role="status" aria-label={label ?? 'Loading'}>
            <svg viewBox="0 0 24 24" aria-hidden>...</svg>
        </span>
    );
}
```

Reduced-motion fallback:

```css
@media (prefers-reduced-motion: reduce) {
    .mk-spinner svg { animation: none; }
    .mk-spinner::after { content: 'Loading…'; animation: mk-dots 1s steps(4) infinite; }
}
@keyframes mk-dots { 0% { content: 'Loading' } 25% { content: 'Loading.' } 50% { content: 'Loading..' } 75% { content: 'Loading...' } }
```

The dot animation is < 1 Hz to satisfy story EC: "the spinner becomes a static 'Loading…' text + dot animation kept under 1 Hz."

## 4. `<Async>` wrapper

```tsx
export function Async<T>({ state, children, errorFallback, skeleton }: {
    state: AsyncState<T>;
    children: (data: T) => React.ReactNode;
    errorFallback?: (e: Error, retry: () => void) => React.ReactNode;
    skeleton: React.ReactNode;
}) {
    const view = useDelayedSkeleton(state);
    switch (view.phase) {
        case 'pending':  return null;                     // < 100 ms: nothing
        case 'skeleton': return <>{skeleton}</>;
        case 'spinner':  return <Spinner label={t('loading.still')} />;
        case 'error':    return errorFallback?.(view.error!, state.retry) ?? <ErrorState onRetry={state.retry} />;
        case 'done':     return view.data ? <>{children(view.data)}</> : null;
    }
}
```

## 5. Layout-stable swap

The story TC: "Skeleton-to-content swap is layout-stable (no CLS)."

Implementation: skeleton variants take explicit `width` and `height`; the content variant uses the same dimensions. Visual regression assertion in CI compares before/after geometry.

For pagination empty placeholder (AC: "6 skeleton rows"), `SkeletonRow` exposes `count`.

## 6. Player buffer spinner

The player shows a centered spinner over the poster:

```tsx
{state === 'buffering' && (
    <Overlay>
        <Spinner size="lg" />
        <Caption>{t('player.buffering')}</Caption>
    </Overlay>
)}
```

The story EC: "Player buffer underrun mid-playback: spinner over the player center with a 'Buffering…' caption." Implementation: the player wires `state === 'buffering'` from the underlying HLS engine.

## 7. Test plan

### 7.1 Hook timing

| Test | What it pins |
|---|---|
| `testNoSkeletonUnder100ms` | Resolve at 50 ms → phase never reached `skeleton`. |
| `testSkeletonHeldFor200msMinimum` | Resolve at 150 ms after skeleton showed → phase stays `skeleton` until total ≥ 200 ms. |
| `testSpinnerAfter5s` | Stub a 6 s load → phase = `spinner` at 5 s. |
| `testRetryAfter60s` | Stub 65 s load → phase = `error` at 60 s. |
| `testReducedMotionDisablesShimmer` | Skeleton DOM has no animation under reduced. |

### 7.2 Component

| Test | What it pins |
|---|---|
| `testAsyncRendersSkeleton` | State=loading, > 100 ms → skeleton DOM. |
| `testAsyncSwapsLayoutStable` | Snapshot before/after; element rect unchanged. |
| `testButtonLoadingShowsSpinnerInline` | Button.loading wraps a Spinner; width preserved. |
| `testSearchDropdownShimmer` | Shimmer renders while suggestions load. |

### 7.3 Visual regression

| Test | What it pins |
|---|---|
| `chromaticSkeletonShapes` | All shape variants captured. |
| `chromaticAsyncStates` | Phase transitions captured at 50ms, 200ms, 5s, 60s. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| 0 ms response | Skeleton never shown. | `testNoSkeletonUnder100ms` |
| 30 s load | Spinner + "Still loading" at 5 s; retry CTA at 60 s. | `testSpinnerAfter5s` + `testRetryAfter60s` |
| Reduced motion | Shimmer disabled; spinner becomes text "Loading…"; dot anim < 1 Hz. | `testReducedMotionDisablesShimmer` |
| Player buffer underrun | Centered spinner + "Buffering…" caption. | `testBufferOverlay` |
| Skeleton-to-content shape mismatch | Visual regression catches; CI fails. | Chromatic |
| Async called with no skeleton prop | TS compiler error (skeleton required). | typecheck |
| Component lazy-loaded | React Suspense boundary uses an `Async` wrapper of the page. | `testLazyLoadedShowsSkeleton` |
| Slow first paint hides skeleton (FOIT) | Font tokens declare `font-display: swap` (Story 17.1). | n/a |
| Pagination next-page race | Old page content remains until new page resolves; skeleton overlay during load > 200 ms. | `testPaginationOverlay` |

## 9. Acceptance checklist

**Primitives**
- [ ] `Skeleton`, `Spinner`, `Async`, `useDelayedSkeleton` shipped.

**Timing**
- [ ] Min-display 200 ms; spinner at 5 s; retry at 60 s.

**Reduced motion**
- [ ] Shimmer disabled; spinner becomes text.

**Tests**
- [ ] All §7 tests pass.

**Docs**
- [ ] `design/docs/loading.md` documents the convention.
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.4.
