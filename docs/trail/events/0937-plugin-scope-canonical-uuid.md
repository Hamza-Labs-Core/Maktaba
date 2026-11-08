---
schema: 1
kind: pr
pr: 937
title: A plugin could write another library's video by spelling its id differently
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- The statements executor checked a `subject`-scoped statement from a library job by looking the video id up as text (`CAST(id AS text) = $1`). An id upper-cased, without hyphens or in braces missed that lookup and was allowed as "a video that is gone". The statement's `WHERE id = $1` on the uuid column still found the other library's video.
- The scope argument is now parsed as a uuid and compared in canonical form. A non-id is refused `plugin_scope`. A test covers all three forms and the job's own video in upper case.
- An independent review of wave 9f (the move into `tv.maktaba.core-media`) found it. The code was already on main.

## What we learned
- A fail-open branch ("not found, so allow") is only safe when "not found" really means no row. Compare ids in the form the database compares them, never as free text.
- Ask for an independent review of every relay and scope change. The writer of a check tests the inputs it expects.

## Open
- The same "not found, so allow" shape exists for an id that names no video at all. It writes nothing for an UPDATE or DELETE by id, but an INSERT could reference a missing video. Worth a follow-up check per statement mode.
