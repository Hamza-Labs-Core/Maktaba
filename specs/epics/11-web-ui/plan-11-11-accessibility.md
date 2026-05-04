# Implementation Plan — Story 11.11 Accessibility (WCAG 2.1 AA)

> Companion to [story-11-11-accessibility.md](story-11-11-accessibility.md).
> CI gate: 0 axe-core serious/critical violations on every route.
> Manual VoiceOver / NVDA pass per release.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Tooling | `axe-core` via `@axe-core/playwright` for e2e; `vitest-axe` for unit on critical primitives. |
| Lint | `eslint-plugin-jsx-a11y` (recommended preset + `no-autofocus: error`). |
| Placement | A11y helpers in `web/src/a11y/`; checklist in `docs/a11y.md`. |
| Reduced motion | All non-essential animation respects `prefers-reduced-motion`. |
| Out of scope | Player vendor's internal a11y (Vidstack); we wrap with our ARIA + document deviations. |

## 1. Cross-cutting requirements

| Requirement | Implementation |
|---|---|
| Visible focus | `:focus-visible` ring uses `--color-focus` token at 3:1 contrast min. |
| Alt text | `<img>` requires `alt`; ESLint enforces; decorative use `alt=""`. |
| Color not sole carrier | Every state badge pairs color + icon + text. |
| Form labels | RHF wrappers always render `<label>`; ESLint rule blocks unlabeled inputs. |
| Live regions | Toasts/errors render in `aria-live="polite"`; alert dialogs use `aria-live="assertive"`. |
| Skip link | `<SkipToContent>` is the first focusable element on every page. |
| Tab order | Logical: header → nav → main → footer. |
| Reduced motion | `@media (prefers-reduced-motion: reduce) { transition: none; animation-duration: 0ms !important; }` |

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/a11y/SkipToContent.tsx` | Visually hidden until focused. |
| `web/src/a11y/LiveRegion.tsx` | Polite + assertive containers; `useAnnounce()` hook. |
| `web/src/a11y/useFocusTrap.ts` | Focus trap for dialogs. |
| `web/src/a11y/announceTime.ts` | `aria-valuetext` formatter for player scrubber. |
| `web/src/a11y/visuallyHidden.css` | Standard sr-only utility. |
| `docs/a11y.md` | Manual checklist (per release). |
| `web/e2e/a11y/*.spec.ts` | One spec per route. |

## 3. Axe integration

### 3.1 Unit

```ts
import { axe, toHaveNoViolations } from 'vitest-axe';
expect.extend(toHaveNoViolations);

it('FilterChips passes axe', async () => {
  const { container } = render(<FilterChips chips={...}/>);
  expect(await axe(container)).toHaveNoViolations();
});
```

### 3.2 e2e

```ts
import AxeBuilder from '@axe-core/playwright';
test('library page is a11y-clean', async ({ page }) => {
  await page.goto('/library');
  const results = await new AxeBuilder({ page }).withTags(['wcag2a','wcag2aa']).analyze();
  expect(results.violations.filter(v => ['serious','critical'].includes(v.impact!))).toEqual([]);
});
```

CI fails on any serious/critical hit.

## 4. Player a11y wrapping

Vidstack ships ARIA on its primitives, but we add:

- `role="region"` `aria-label="Video player"` around `<MediaPlayer>`.
- `aria-valuetext` on the time slider via `announceTime(currentSec, durationSec)` → "12 minutes 5 seconds of 1 hour".
- Caption toggle button with `aria-pressed`.
- Subtitle on/off announces "Subtitles on/off" via `useAnnounce('polite')`.

Document any vendor deviation in `docs/a11y.md`.

## 5. Transcript long-list

Use `role="feed"` with `aria-busy` while fetching, and per-row `aria-posinset`/`aria-setsize`. Virtualizer renders ±20 items at any moment; SR reads ranges sequentially without trapping.

## 6. Color tokens

Every token comes from Story 17.1; the suite snapshot-tests contrast pairs:

```ts
import { getContrast } from 'polished';
const PAIRS = [
  ['--color-bg', '--color-fg'],
  ['--color-card', '--color-fg-muted'],
  ['--color-focus', '--color-bg'],
  // ...
];
for (const [bg, fg] of PAIRS) {
  expect(getContrast(read(bg), read(fg))).toBeGreaterThanOrEqual(4.5);
}
```

Run for both `light` and `dark` themes.

## 7. Color-blind suite

Playwright captures each route with `forced-colors: active` and with color-blind matrix (`Protanopia`/`Deuteranopia`/`Tritanopia` via SVG filters); image diffs ensure no UI relies solely on hue.

## 8. Reduced-motion suite

For each route:

```ts
test('reduced motion route', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/library');
  // assert no transition durations > 0
});
```

## 9. Edge cases

| Case | Handling |
|---|---|
| Browser zoom 400% | Single-column reflow; horizontal scroll only on charts. |
| Vendor a11y bug (player) | Wrap with ARIA scaffolding + log deviation in `docs/a11y.md`. |
| Long transcript SR navigation | `role="feed"`; SR reads ranges as virtualizer mounts. |
| Forced colors | Don't override system colors; `forced-color-adjust: auto`. |

## 10. Test matrix

| Surface | Tooling | Frequency |
|---|---|---|
| Per-component | vitest-axe | every PR |
| Per-route | axe + Playwright | every PR |
| Color contrast tokens | unit (snapshot) | every PR |
| Color-blind matrix | Playwright + SVG filter | nightly |
| VoiceOver / NVDA pass | Manual checklist `docs/a11y.md` | per release |

## 11. CI

- `npm run a11y:unit` runs vitest-axe on primitives.
- `npm run a11y:e2e` runs axe on Playwright routes.
- Both gate the PR.

## 12. Dependencies

- Tokens: Story 17.1.
- Primitives: Story 17.2.
- Theme: Story 11.8.
- Player: Story 11.3 (we wrap; vendor doesn't change).
