# Story 57.16 — The home's time rules through Screen Time

> Epic 57 · Phase 3b · Depends on: 57.14, 57.09 · Needed by: —
> **Status:** not built (2026-11-12). It needs Apple to grant the Family Controls entitlement
> (`com.apple.developer.family-controls`). The home's time rules (57.14, 57.15) hold without it.

## Why

On Apple devices a parent already uses Screen Time. Where Apple allows it, Maktaba should work
with it and not beside it: the rules a guardian sets once in Maktaba should also hold at the
system level on the child's iPhone or iPad. This story is built only if it proves doable; the
home's own enforcement (57.14) does not depend on it.

## Description

### 1. The feasibility check, first

Before any build, a written check answers, on a real child device in a test family:

1. Does Apple grant `com.apple.developer.family-controls` for distribution to this app? The
   owner files the request (README Q6).
2. With Family Controls authorization on the child's device, can the Player schedule a
   DeviceActivity for the home's slots and daily limit, and shield itself when one ends?
3. Does that hold when the Player is not running?
4. What does a parent see and approve, and can a child remove it?

The answers go in `research.md`. If 1 or 2 is no, this story ends there and Help says that
Screen Time is set by hand (57.09).

### 2. If it is doable

- On a restricted account's device, the Player asks for Family Controls authorization once, with
  the guardian's approval.
- The Player turns the account's time rules (57.14) into a DeviceActivity schedule and a shield
  for Maktaba, and refreshes them when the rules change.
- The home stays the judge. The shield is a second lock on that device, never a replacement: a
  device without it is still stopped by the home.
- Nothing about other apps is read or shielded. Maktaba touches only itself.

### 3. What stays as it is

Screen Time's own limits, set by a parent in Settings, keep working with no change (57.09). A
parent may use them, Maktaba's rules, or both; the strictest wins because each is enforced
separately.

## Acceptance criteria

- **Given** the feasibility check, **then** `research.md` records each answer with the iOS
  version and device.
- **Given** the entitlement is refused, **then** nothing is built and Help says so.
- **Given** it is granted and a slot ends at 20:00, **then** the child's iPad shields Maktaba at
  20:00 even if the Player was closed.
- **Given** a guardian changes the slot, **then** the device follows at the Player's next
  foreground.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | manual, real iPad in a test family | the four questions | run | answers written down |
| T02 | MaktabaKit XCTest (only if built) | time rules | map to schedules | one schedule per rule, midnight slots split |
| T03 | manual (only if built) | slot ends in 2 minutes, Player closed | wait | shield |

## Edge cases

- **Authorization revoked** by a parent: the Player notes it and the home's rules go on.
- **Apple TV**: no Family Controls there; the home's rules apply.

## Build plan

| File | Change |
|---|---|
| `specs/epics/57-parental-controls/research.md` | the feasibility answers |
| `apps/apple/Player/…` (only if built) | authorization, schedules, shield, entitlement |
| `web/src/help/content/{en,ar}/apple-apps.md` | what the Player does with Screen Time |
