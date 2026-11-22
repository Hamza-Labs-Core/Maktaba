---
schema: 1
kind: incident
title: "Recent and saved searches lost their order and filters; the web's Save had never worked"
date: 2026-11-22
session: 159f0411-a9b0-4b31-acb4-1b3821caed04
prs: [1513, 1511]
---

## What happened
- Story 49.10 (#1511) listed two gaps in its As built section:
  - a recent search kept its order and filters only in the browser that ran it;
  - a shared filtered link showed a library chip by its id, and a page opened by sid alone
    showed no facets.
  #1513 closes both.
- Reading the saved-search path turned up two older faults:
  - The web posted `{ q, mode }` to `/api/search/save`, which requires `{ name, query }`, so
    every Save from the web got a 422. Its tests mocked the server with the web's own shape,
    so nothing failed.
  - `ListSaved` scanned SQLite's `created_at` text into a `time.Time`. The scan error was
    skipped row by row, so the saved list was always empty on SQLite.
- `search_runs.filters` holds the filters after a restricted account's libraries were added.
  Showing it as the user's filters would have listed every library of such an account.
  Slot 0236 adds `asked_filters` and `sort_order`: nullable, no rewrite, rollback-safe.
- The GLM review found that facets read by sid counted the kept set without checking the
  account's current libraries. A library the account had lost was still counted and named.
  The pages were already checked again (`cards`); the facets now follow the same check.
- A second worktree for this trail PR filled the shared disk part way through its checkout and
  vanished. A sparse checkout of `docs/trail/` was enough for the trail PR.

## What we learned
- A web test that mocks the API with the shape the web sends proves nothing about the
  contract. The new saved-search tests (SRC-55) answer in the server's shape, and the Go
  test saves and lists through the real handler.
- A handler that skips rows on a scan error (`continue`) hides type mismatches between
  SQLite and Postgres. Scan times through a type that takes both forms (`dbTime`).
- Anything computed again from a kept search (facets, counts) must apply the caller's
  current scope, as its pages do: the kept set was made under the old scope.

## Keep doing
- A Go round-trip test for each endpoint the web writes to, on SQLite and Postgres.
- A sparse worktree (`git worktree add --no-checkout`, then `sparse-checkout set
  /docs/trail/`) for a trail-only PR on a machine short of disk.

## Stop doing
- Reading a stored, already-scoped column (`filters`) back as the user's choice.

## Open
- The Android and Apple apps do not restore a recent search's order and filters; their
  search pages have neither.
- A results page by sid carries the order but not the filters, so a link stripped of its
  filter parameters shows no chips.
- No local Postgres was available, so the Postgres tests of #1513 ran only in CI.
