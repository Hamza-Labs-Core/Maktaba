---
schema: 1
kind: incident
title: Title similarity ranks no Arabic title on the bundled server (migration 0098, C locale)
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
---

## What happened
- While building Epic 49 story 49.8's did-you-mean on pg_trgm, a check on a scratch
  PostgreSQL 18 showed `show_trgm('مدرسه')` empty and `similarity('الصبر', 'الصبير')` 0.
- The bundled server initialises its database with `--locale=C`
  (`cmd/maktaba-server/internal/pgbundle/pgbundle.go`). In the C locale, pg_trgm counts only
  ASCII letters and digits as word characters, so an Arabic word has no trigrams.
- The same holds for search's title matches since migration 0098 (`videos_title_trgm_idx`):
  `runTitles` orders by `similarity(v.title, q)`, which is 0 for every Arabic title, so
  Arabic title matches come newest first. The index also cannot serve `ILIKE '%q%'` for an
  Arabic query, so the match scans `videos`.
- 49.8 avoids the problem: its dictionary keeps the key with its 29 Arabic letters
  spelled in ASCII (`search_terms.trgm`, slot 0235), and pg_trgm reads that.
- The title ranking is recorded as Epic 49 story 49.15.

## What we learned
- On the bundled server, pg_trgm (and anything else that relies on the locale's notion of a
  letter) does not work on Arabic. Docker images default to `en_US.utf8`, so a test there
  does not show it. Check text functions on a `--locale=C` database.

## Keep doing
- Test search features on a `--locale=C` scratch database (the bundled server's setting),
  with Arabic data.

## Open
- Story 49.15: rank Arabic title matches by similarity (the ASCII spelling as 49.8 does, or
  the dictionary's keys), with a test on a C-locale Postgres.
