---
schema: 1
kind: pr
pr: 1042
title: An open screen keeps its feature gate in the render that sees the first answer
date: 2026-11-08
session: 97cbe796-cf29-4a37-835c-e765023cadb0
---

## What happened
- `capabilities.test.tsx` "a page already open when the feature goes off stays" failed once in `unit / unit` on #998 (run 42494590846). The DOM showed Home and the unavailable toast.
- The cause was in the code. `useOpenFeatureGate` (story 51.11) recorded the first answer's gate with `setKept` inside a `useEffect`. The capabilities store renders at sync priority, and the commit runs passive effects right away, so the `setKept` called there got default priority and waited for a later task. An answer turning the feature off, rendered at sync priority before that task, skipped the pending update. That render had nothing kept, so the open screen closed.
- #1042 keeps the gate with a render-phase state update in the render that first sees an answer. A new test pushes both answers with `flushSync` and `IS_REACT_ACT_ENVIRONMENT` off: it fails on the old code every time. The existing test now waits for `answered`, not just the request. Merged as aa8c245ef. `unit / unit` ran the file's 21 tests on head 29c4f2748.
- Failure rates. CI: a temporary loop workflow (run 42497328005, two rounds, 4 runners × 4 parallel loops × 50) failed 1/1600 on main's code with the same error and 0/1600 with the fix. Local: 0/200 unloaded on main; under 28 CPU hogs with `--sequence.shuffle`, 1 real failure in 22 valid runs on main and 0/200 with the fix.

## What we learned
- Don't copy the store into state from a `useEffect` when a newer store update could overtake it. The effect's `setState` has lower priority than an external store's sync render, so a newer store value can render first. To keep something from the first render that sees a value, use a render-phase `setState`, not an effect.
- `flushSync` with `IS_REACT_ACT_ENVIRONMENT` off reproduces a priority race deterministically in jsdom. `act` would batch both updates and hide it.
- A local loaded loop on this Mac hit ENOSPC in vitest's temp directory: the disk had under 1 GB free before the loop started. Check `df` before long loops, and stop a loop when space runs low.

## Open
- No one has audited the web app for other hooks that copy an external store's value into state from an effect; any such hook can lose to a newer store update the same way.
