# Plan 27.8 — Channel management admin UI — implementation

> Implementation plan for [story-27-08-channel-admin-ui.md](story-27-08-channel-admin-ui.md).
> Self-contained. Cross-links: drives the CRUD API
> ([Plan 27.1](plan-27-01-channel-definition.md)), the schedule preview
> ([Plan 27.2](plan-27-02-program-scheduler.md)), and filler management
> ([Plan 27.10](plan-27-10-filler-bumper-system.md)); reuses the
> smart-query filter UI (Story 7.14) + the admin surface + design system.
> **Adds no migration, no new API** (composes 27.1/27.2/27.10 endpoints).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **One admin page with a mode-adaptive rule builder**; the builder swaps panels by `mode`. | The four modes have disjoint configs; a single page that adapts keeps the mental model simple. |
| D2 | **Reuse the existing smart-query filter UI** for shuffle/source filters. | Story AC2 — don't reinvent the filter builder; it already exists for collections. |
| D3 | **Schedule mode = a visual day×time grid with drag**; overlaps flagged with the resolution rule. | Story AC2/EC3 — the daypart grid is the one genuinely new builder; surface 27.2's overlap semantics. |
| D4 | **Preview calls the dry-run endpoint and renders a timeline**, clearly labelled "preview, not yet committed." | Story AC3/EC1 — show padding/filler/boundaries before commit. |
| D5 | **Rule changes warn "applies from next program boundary"** and refresh the preview. | Story AC7 — consistent with 27.1 D5; no surprise live jumps. |
| D6 | **Draft = save-as-disabled**; enabling requires a valid rule. | Story AC9 — forgiving authoring. |

---

## 1. Files (web)

```
web/src/pages/Admin/Channels/
├── ChannelsAdmin.tsx          # list + reorder (drag) + enable/disable (AC4/AC5)
├── ChannelForm.tsx            # CRUD form: name/number/logo/category/mode/transition (AC1)
├── rulebuilder/
│   ├── RuleBuilder.tsx        # mode-adaptive switch (D1)
│   ├── ShuffleRule.tsx        # smart-query filter UI + live match count (D2/AC2)
│   ├── MarathonRule.tsx       # series picker + order + loop (AC2)
│   ├── ScheduleGrid.tsx       # day×time drag grid + fill + overlap flags (D3)
│   └── SmartMixRule.tsx       # daypart profile + genre sliders + fallback note (AC2)
├── SchedulePreview.tsx        # 48h dry-run timeline (D4)
├── FillerManager.tsx          # pools + items + per-channel assignment (AC6 → 27.10)
└── __tests__/
```

## 2. Mode-adaptive builder (`RuleBuilder.tsx`, D1)

```tsx
switch (mode) {
  case "shuffle":   return <ShuffleRule .../>    // reuses SmartQueryFilter (D2)
  case "marathon":  return <MarathonRule .../>   // series picker (from 26.3)
  case "schedule":  return <ScheduleGrid .../>   // visual daypart grid (D3)
  case "smart_mix": return <SmartMixRule .../>   // daypart profile + weights
}
```

The form serialises each builder to the `mode_config` shape
[27.1](plan-27-01-channel-definition.md) validates; client validation
mirrors the server schema for instant feedback (AC1).

## 3. Schedule grid (`ScheduleGrid.tsx`, D3) — the one new builder

A week × 24 h grid; the editor drags a collection/filter/series chip into
a daypart to create a `{days, start, end, source}` slot; overlapping
slots are flagged with the resolution rule shown
([27.2](plan-27-02-program-scheduler.md) AC5); an out-of-slot **fill**
selector defines the rest of the day. A huge filter result shows only a
count (Story EC2), never the enumerated set.

## 4. Preview (`SchedulePreview.tsx`, D4)

Calls `GET /api/channels/{id}/schedule/preview?hours=48`, renders the
returned blocks (program + filler + slate) on a horizontal timeline with
boundaries and padding visible, labelled as a dry-run (EC1). Refreshes on
rule change (D5) with the "applies from next boundary" note.

## 5. List, reorder, enable/disable (`ChannelsAdmin.tsx`, AC4/AC5)

Drag-reorder posts to `/api/channels/reorder` (all-or-nothing; collisions
flagged, EC); per-row enable/disable toggles `enabled` immediately;
optimistic concurrency on `updated_at` surfaces the 409 "reload" prompt
(EC5).

## 6. Files to create / modify

**Create:** everything under `web/src/pages/Admin/Channels/`, tests.

**Modify:**
- `web/src/pages/Admin` index/nav — add the Channels admin entry (editors
  only; ACL-gated route, AC8).
- Reuse `SmartQueryFilter` (extract/share if currently collection-local).

## 7. Dependencies

- **27.1** (CRUD + reorder + logo), **27.2** (preview endpoint), **27.10**
  (filler endpoints), Story 7.14 (smart-query filter UI), **26.3** (series
  picker for marathon — soft; marathon builder degrades to a source
  filter without it). No migration, no new API.

## 8. Test strategy

Inline validation (dup number / invalid config), shuffle match-count,
marathon picker, schedule-grid drag + overlap flag, smart-mix fallback
note, 48 h preview render, reorder + collision, enable/disable immediacy,
filler pool assignment, "applies from next boundary" warning, ACL-gated
route, draft-disabled save, RTL.
