# Story 17.4 — Loading states and skeleton screens

Every async surface has a defined loading state: skeleton for content,
spinner for actions, indeterminate progress for unknowns.

**Anchors:** [`architecture.md` §6](../../architecture.md).

## AC

- Skeleton: shape-matches the final content; never shows for < 200 ms
  (avoid flash); maxes at 5 s before swapping to a generic spinner +
  retry.
- Spinner: only for action-bound waits (button submitting, modal
  saving); never for page-level loads.
- Empty placeholder during pagination: 6 skeleton rows.
- Search dropdown: shimmer while suggestions load.
- Player initial buffer: a centered spinner over the poster, ≤ 2 s.

## TC

- Slow-network test (200 ms latency): skeletons appear; do not flash
  for < 200 ms.
- Skeleton-to-content swap is layout-stable (no CLS).
- Hold a button: spinner replaces the label; button width preserved.

## EC

- A 0-ms response: skeleton never shown (under the 200 ms minimum).
- A 30 s load: skeleton timeout → "Still loading" text → retry CTA at
  60 s.
- Player buffer underrun mid-playback: spinner over the player center
  with a "Buffering…" caption.
