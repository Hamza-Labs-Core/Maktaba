# Implementation Plan — Story 17.03 Motion / animation guidelines

> Companion to [story-17-03-motion.md](story-17-03-motion.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Token additions | `motion` group in `design/tokens/tokens.json` — durations, easings, named patterns. |
| Web primitives | `design/components/src/motion/{useMotion.ts,withReducedMotion.tsx,patterns.ts}`. |
| Native primitives | `apps/tvos/Sources/UI/Motion/Motion.swift` and `apps/androidtv/.../ui/motion/Motion.kt`. |
| Reduced-motion detection | OS/browser preference + a global `MotionContext.override` for tests. |
| Documentation | `design/docs/motion.md`. |
| Out of scope | Page-level animation (lives in Epics 11–14); brand/marketing motion. |

## 1. Token additions

```jsonc
{
  "motion": {
    "duration": {
      "instant":     { "value": "100ms" },
      "quick":       { "value": "150ms" },
      "standard":    { "value": "250ms" },
      "relaxed":     { "value": "400ms" },
      "theatrical":  { "value": "600ms" }
    },
    "easing": {
      "ease-out":    { "value": "cubic-bezier(0,0,0.2,1)" },
      "ease-in":     { "value": "cubic-bezier(0.4,0,1,1)" },
      "ease-in-out": { "value": "cubic-bezier(0.4,0,0.2,1)" }
    },
    "pattern": {
      "page-transition": { "duration": { "value": "200ms" }, "easing": { "value": "{motion.easing.ease-in-out}" }, "kind": { "value": "cross-fade" } },
      "modal":           { "duration": { "value": "250ms" }, "easing": { "value": "{motion.easing.ease-out}" }, "kind": { "value": "scale+fade" } },
      "toast":           { "duration": { "value": "250ms" }, "easing": { "value": "{motion.easing.ease-out}" }, "kind": { "value": "slide-up+fade" } },
      "focus-ring":      { "duration": { "value": "0ms" }, "kind": { "value": "instant" } }
    }
  }
}
```

Style Dictionary outputs CSS custom props (`--motion-duration-quick: 150ms`), Swift constants (`Motion.duration.quick`), and Kotlin constants.

## 2. Reduced-motion handling

### 2.1 Web

```ts
// design/components/src/motion/useMotion.ts
import { useSyncExternalStore } from 'react';

export function useReducedMotion(): boolean {
    return useSyncExternalStore(
        (cb) => { const m = matchMedia('(prefers-reduced-motion: reduce)'); m.addEventListener('change', cb); return () => m.removeEventListener('change', cb); },
        () => matchMedia('(prefers-reduced-motion: reduce)').matches,
        () => false,
    );
}

export function applyMotion<T extends string>(token: T, reduced: boolean): string {
    if (reduced) return '0ms';
    return `var(--motion-duration-${token})`;
}
```

```css
/* fallback in CSS */
@media (prefers-reduced-motion: reduce) {
    * { animation-duration: 0ms !important; transition-duration: 0ms !important; }
}
```

We keep both because some animations are JS-driven (Framer Motion, Lottie) and the CSS rule alone won't catch them.

### 2.2 tvOS

```swift
@Environment(\.accessibilityReduceMotion) var reduceMotion
let dur = reduceMotion ? 0 : Motion.Duration.quick
view.animation(.easeOut(duration: dur), value: x)
```

### 2.3 AndroidTV

```kotlin
val accessibilityManager = LocalAccessibilityManager.current
val reduce = (accessibilityManager?.isReducedMotion ?: false)
val duration = if (reduce) 0 else MotionTokens.Duration.quick
```

## 3. Patterns

### 3.1 Page transition (web)

Cross-fade between routes:

```tsx
import { AnimatePresence, motion } from 'framer-motion';

export function PageWrapper({ children }: { children: React.ReactNode }) {
    const reduced = useReducedMotion();
    return (
        <AnimatePresence mode="wait">
            <motion.main
                initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
                transition={{ duration: reduced ? 0 : 0.2, ease: [0.4, 0, 0.2, 1] }}>
                {children}
            </motion.main>
        </AnimatePresence>
    );
}
```

### 3.2 Modal (scale + fade)

`@reach/dialog` overlay handles fade; we add a scale on the panel via `framer-motion`:

```tsx
<motion.div
    initial={{ opacity: 0, scale: 0.95 }}
    animate={{ opacity: 1, scale: 1 }}
    exit={{ opacity: 0, scale: 0.95 }}
    transition={{ duration: reduced ? 0 : 0.25 }}
/>
```

When `reduced=true`, scale is skipped *but the fade remains* (story TC: "Open a modal with `useReducedMotion`: the scale animation is skipped; fade remains").

### 3.3 Toast

Slide-up + fade; we render in a portal at bottom-right.

### 3.4 Focus ring

Instant (no transition); reduced motion irrelevant. Already 0 ms in tokens.

### 3.5 Player chrome

Player allows spring physics for affordances (the EC: "spring physics for layout (causes nausea on TV); allowed for player chrome"). The motion guideline restricts spring to player chrome only; CI lint catches `spring()` calls outside `apps/*/Player/*`.

## 4. Frame-rate adaptation

Story EC: "60 fps not achievable on a low-end Android: motion durations clamp to 150 ms regardless of token."

```kotlin
// AndroidTV — devices with < 60 Hz refresh or low-end class clamp duration.
fun clampDuration(d: Int): Int {
    val context = LocalContext.current
    val isLowRam = ActivityManager.isLowRamDevice(...)
    val refreshRate = display.refreshRate
    return if (isLowRam || refreshRate < 60f) min(d, 150) else d
}
```

Web: `navigator.deviceMemory < 4` → clamp at 150 ms.

## 5. No-blend rule

Two simultaneous animations on the same element: the later wins, never blend (story EC).

```ts
// framer-motion: cancel previous
<motion.div animate={state} initial={false} />
```

When state changes, framer-motion replaces the in-flight animation rather than blending. The rule is therefore mechanical, not stylistic — we just rely on the library's default.

## 6. Documentation

`design/docs/motion.md` covers:

1. Duration scale and when to use each.
2. Easings + when to use enter/exit/reposition.
3. Patterns table (page, modal, toast, focus, etc.).
4. Reduced motion guidance (what to skip vs. preserve).
5. Anti-patterns (spring physics for layout; > 600 ms; blending two animations on the same element).

## 7. Test plan

### 7.1 Unit (web)

| Test | What it pins |
|---|---|
| `testUseReducedMotionRespondsToMQChange` | Toggle `prefers-reduced-motion` → hook value flips. |
| `testApplyMotionReturnsZeroOnReduced` | `applyMotion('quick', true)` → `'0ms'`. |
| `testModalSkipsScaleWithReducedMotion` | Render modal under reduced; framer-motion scale animation is no-op; opacity transitions. |

### 7.2 Storybook

| Test | What it pins |
|---|---|
| `motionPatternsStories` | One story per pattern; visual regression captures each frame at 0, 50%, 100%. |

### 7.3 Native

| Test | What it pins |
|---|---|
| `tvosReduceMotionFlattens` | UI test toggles AX setting; modal scale skipped. |
| `androidTVLowEndClamps` | Mock low-end device class; duration ≤ 150 ms. |

### 7.4 Lint / CI

| Test | What it pins |
|---|---|
| `LintBlocksSpringOutsidePlayer` | A `spring()` call in `web/src/features/library/*` fails the lint step. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Reduced-motion toggled mid-session | All future animations honor; in-flight finish naturally. | `testUseReducedMotionRespondsToMQChange` |
| Loading spinner under reduced-motion | Becomes static "Loading…" text + dot kept under 1 Hz. | `testSpinnerStaticUnderReduced` |
| Spring used outside player | Lint error; build fails. | `LintBlocksSpringOutsidePlayer` |
| Two animations on the same element | Later replaces earlier (framer-motion default). | `testReplaceNotBlend` |
| Low-end Android clamp | All durations capped at 150 ms. | `androidTVLowEndClamps` |
| Browser doesn't support `prefers-reduced-motion` | `matchMedia` returns false; standard durations. | `testFallbackWhenMQUnsupported` |
| User in motion-sensitive accessibility category | Reduced-motion media query fires (OS-managed); rest follows. | n/a |
| 600 ms theatrical duration on a TV | Allowed only for full-screen transitions (player open). Lint warns elsewhere. | `LintWarnTheatricalNonPlayer` |

## 9. Acceptance checklist

**Tokens**
- [ ] Motion group in `tokens.json`; outputs to web / Swift / Kotlin.

**Primitives**
- [ ] `useReducedMotion`, `applyMotion`, framer-motion patterns wired.
- [ ] Native: `Motion.swift`, `Motion.kt`.

**Behavior**
- [ ] Reduced-motion toggles work on web, tvOS, AndroidTV.
- [ ] Spring restricted to player chrome.
- [ ] Low-end clamp.

**Tests**
- [ ] All §7 tests pass.

**Docs**
- [ ] `design/docs/motion.md` published.
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.3.
