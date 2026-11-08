---
schema: 1
kind: pr
pr: 948
title: The service door reads dizqueTV programmes' playable fields from Plex itself (issue #827)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [942]
---

## What happened
- #942 left one low item: the door trusted the plugin's pairing of a Plex rating key with its file, part and duration. #948 closes it.
  - For a show or filler write, the door uses only each programme's rating key.
  - It reads the items from the home's Plex (`/library/metadata/<keys>`, at most 50 keys per request, the token in a header) and builds each programme the way the worker's `program_from_plex` does. Plex's answer never reaches the plugin.
  - A key Plex doesn't know, or an item it can't play, is refused, and nothing is written.
- The plugin's client now sends `{"ratingKey": ...}` alone. An older plugin's other fields are accepted and ignored. Parity was run once with the old client before the client change, to show the door tolerates it.
- The plugin parity and leak tests now run against a fake Plex that requires the token in a header. Before, no server stood behind the test's Plex address.
- `affected-tests` asked for `integrations/dizquetv.py`'s own tests. The client test had been written in the plugin service tests, so it moved into `tests/extensions/test_dizquetv.py`. The Epic 50 stream approved; merged at 67197d40d.

## What we learned
- When a door holds the upstream's credentials, it should read the facts from that upstream rather than check the caller's copy. That removed a whole class of pairing and path-validation code.
- A refusal test must check why it was refused. A new refusal case missing other required fields passes on the old code for the wrong reason.
- Parity fixtures need a real fake behind every upstream address the door may call; a placeholder address only works while nothing contacts it.
- The affected-tests gate maps a module to the test files that import it. Put a module's unit test in its own test file.

## Open
- Nothing; issue #827 is complete.
