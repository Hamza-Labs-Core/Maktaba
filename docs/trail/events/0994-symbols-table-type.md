---
schema: 1
kind: pr
pr: 994
title: "53.16: the Crash symbols page names each table's type and key"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [917, 961, 967]
---

## What happened
- `symbol_tables` has `kind` (`dsym`, `r8`, `elf`) and `code_id` (a native library's whole build id), written on upload (migration `00530003`). `GET /v1/admin/symbols` returns both; a table stored before them gets its kind filled from its stored table the first time the list is read.
- The console's Crash symbols page has a Type column (Apple dSYM, R8 mapping, Native (build ID)); the UUID column is now Key: a dSYM's UUID, an R8 mapping's app and version, a native library's build id shortened with the whole on hover and a Copy button. English and Arabic, right to left checked.

## What we learned
- A column that can only be filled from stored data is a nullable column filled the first time it is read: migrations stay DDL only, as their README asks.
- A vitest that checks a Copy button replaces `navigator.clipboard` after `userEvent.setup()`, which installs its own.

## Open
- The first list after the deploy unpacks each older table once, one by one.
