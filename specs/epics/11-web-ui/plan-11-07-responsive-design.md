# Implementation Plan — Story 11.7 Responsive Design

> Companion to [story-11-07-responsive-design.md](story-11-07-responsive-design.md).
> Tailwind defaults: `sm 640`, `md 768`, `lg 1024`, `xl 1280`, `2xl 1536`.
> Visual regression suite gates merges.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Strategy | Mobile-first; layouts use Tailwind utility classes + container queries (`@container`) where the parent's width matters more than the viewport. |
| Placement | Layout primitives in `web/src/layout/`; navigation chrome in `web/src/components/nav/`. |
| Visual regression | Playwright + `pixelmatch`-based snapshot diffs; baselines in `web/visual-snapshots/`. |
| Touch targets | Tailwind plugin enforces `min-h-touch min-w-touch` (44 px) on `data-touch` elements. |
| Out of scope | Component primitives themselves (Epic 17); RTL flip (Story 11.12); a11y (Story 11.11). |

## 1. Layout primitives

| Primitive | Role |
|---|---|
| `<AppShell>` | Header + navigation rail/bottom-tab + main + (optional) right rail. Picks layout per breakpoint. |
| `<Sidebar>` | `lg+` permanent (`w-60`); `md` icon-only (`w-16`); hidden on `< md`. |
| `<BottomTabs>` | Visible only `< sm` (`<640 px`). 4 tabs: Library / Search / Queue / Settings. |
| `<Header>` | Always visible; contains app title + search affordance + user menu. |
| `<MainContent>` | `flex-1 min-w-0`; uses container queries for inner layout. |
| `<TouchTarget>` | Wraps icon-only buttons to enforce 44×44 px on touch. |

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/layout/AppShell.tsx` | Root layout per route. |
| `web/src/layout/Sidebar.tsx` | Collapsible / icon-only / hidden. |
| `web/src/layout/BottomTabs.tsx` | Mobile nav bar; safe-area-inset aware. |
| `web/src/layout/Header.tsx` | App bar with overlay search trigger on small screens. |
| `web/src/layout/SearchOverlay.tsx` | Full-screen search overlay (`< sm`). |
| `web/src/layout/breakpoints.ts` | `useBreakpoint()` hook (matchMedia). |
| `web/src/components/TouchTarget.tsx` | Min-size enforcement helper. |
| `web/src/styles/tailwind.config.ts` | Adds `min-touch` size, `safe-area` insets, `dvh` aliases. |
| `web/visual-snapshots/` | Playwright baselines. |
| `web/e2e/responsive.spec.ts` | Visual matrix runs. |

## 3. Tailwind configuration

```ts
// tailwind.config.ts
export default {
  theme: {
    extend: {
      minHeight: { touch: '44px' }, minWidth: { touch: '44px' },
      spacing: {
        'safe-t': 'env(safe-area-inset-top)',
        'safe-b': 'env(safe-area-inset-bottom)',
        'safe-l': 'env(safe-area-inset-left)',
        'safe-r': 'env(safe-area-inset-right)',
      },
    },
  },
  plugins: [require('@tailwindcss/container-queries')],
};
```

## 4. Layout matrix

| Breakpoint | Header | Nav | Main | Right rail |
|---|---|---|---|---|
| `< 640` | sticky, search→overlay | bottom tabs | full-width | hidden |
| `640–767` | sticky | bottom tabs | full-width | hidden |
| `768–1023` | sticky | sidebar (icons) | flex-1 | optional |
| `1024–1535` | sticky | sidebar (full) | flex-1 | optional |
| `≥ 1536` | sticky, max-content-width capped | sidebar (full) | flex-1 | optional |

### Player route

- `< 768`: full-width 16:9 player; transcript stacks below.
- `≥ 1024`: 16:9 player + side-by-side transcript (`grid-cols-[1fr,360px]`).

## 5. Container queries

`<MainContent>` declares `@container main`; inner grids use `@md/main:grid-cols-2`, `@lg/main:grid-cols-3`. This means a sidebar-collapsed state at 1024 px gives the grid more room than a sidebar-open state at the same viewport.

Fallback: in browsers without container query support, Tailwind already emits viewport-based breakpoints; no extra polyfill.

## 6. Touch & zoom

- Every icon-only button uses `<TouchTarget>` (min 44×44, padding compensates if the visual is smaller).
- Forms use `text-base` (16 px) on touch to avoid iOS zoom-on-focus.
- All horizontal-scroll containers must have `scrollbar-thin` or `overflow-x-hidden` outside of opt-in horizontal lists; CI lint fails on stray `overflow-x-scroll` at the page level.
- Browser zoom 200% uses `min-content` on tables; charts get a horizontal scroll fallback.

## 7. Test cases

### 7.1 Visual regression matrix (Playwright)

For routes `/library`, `/watch/{id}`, `/search`, `/queue`, `/settings`:
- `iPhone SE 375×667`
- `Pixel 7 412×915`
- `iPad 1024×768` (portrait) and `1366×1024` (landscape)
- `MBP 14 1512×982`
- `4K 2560×1440`

Each viewport produces one PNG; `pixelmatch` threshold ≤ 0.5%.

### 7.2 Behavioral

| Test | Asserts |
|---|---|
| `bottom tabs visible at 360px` | `<BottomTabs>` rendered, `<Sidebar>` not. |
| `sidebar icons-only at 800px` | Sidebar width 64 px; tooltips on hover. |
| `sidebar full at 1280px` | Sidebar width 240 px. |
| `player + transcript side-by-side at 1024px` | `grid-cols-[1fr,360px]` applied. |
| `iPad rotation does not pause video` | Playwright + media events → no pause emitted. |
| `200% zoom no horizontal overflow` | `document.scrollingElement.scrollWidth === clientWidth`. |
| `touch target ≥ 44px` | Computed style on every `<TouchTarget>`. |

### 7.3 Edge cases

- 280 px split-mode foldable: layout downgrades to single column with a soft "narrow display" hint banner.
- Container-query-less browser: viewport-based breakpoints still ship; Playwright runs an extra suite with `@container` polyfill stripped.
- 21:9 ultra-wide: player letterboxed inside 16:9 frame; sidebar fills the rest.

## 8. CI integration

- `web/scripts/visual-test.ts` runs the matrix on PRs; baseline updates require explicit `npm run snapshot:update`.
- Bundle-size budget: any layout primitive change > 5 KB triggers PR comment.

## 9. Dependencies

- Stories 17.1 (tokens) and 17.2 (primitives).
- The `<SearchOverlay>` reuses `<HeaderSearch>` from Story 11.4.
- Bottom-tab badge counts pull from `useQueueStats()` (Story 11.5) for the Queue tab.
