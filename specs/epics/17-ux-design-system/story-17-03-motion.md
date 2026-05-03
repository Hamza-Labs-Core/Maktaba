# Story 17.3 — Motion / animation guidelines

A documented motion system: durations, easings, and patterns for
common transitions.

**Anchors:** [`architecture.md` §6](../../architecture.md). Depends on
[Story 17.1](story-17-01-design-tokens.md).

## AC

- Durations: 100 ms (instant), 150 ms (quick), 250 ms (standard),
  400 ms (relaxed), 600 ms (theatrical, sparingly).
- Easings: `easeOut` for enter, `easeIn` for exit, `easeInOut` for
  reposition.
- Patterns: page transition (200 ms cross-fade), modal (250 ms scale +
  fade), toast (slide-up + fade), focus ring (instant).
- All motion respects `prefers-reduced-motion`.
- No spring physics for layout (causes nausea on TV); allowed for
  player chrome.

## TC

- Toggle reduced motion: every animated element falls to a 0 ms
  transition.
- Open a modal with `useReducedMotion`: the scale animation is skipped;
  fade remains.
- Player chrome reveal/hide: 150 ms; never blocks input.

## EC

- A device with `prefers-reduced-motion: reduce` and an essential
  animation (loading spinner): the spinner becomes a static "Loading…"
  text + dot animation kept under 1 Hz.
- 60 fps not achievable on a low-end Android: motion durations clamp to
  150 ms regardless of token.
- Conflict between two simultaneous animations on the same element: the
  later wins; we never blend.
