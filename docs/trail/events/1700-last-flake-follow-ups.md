---
schema: 1
kind: pr
pr: 1700
title: "An import asks again in the answered order; a file whose loop is closed ends no transcription thread"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
prs: [1697]
---

## What happened
- #1697's check that an answer is the same search's compared the order asked, and an import asks
  again with the order first asked, where the URL has the one answered. The URL kept the old
  search's `sid`. #1700 keeps the answered order.
- `batched.py` gives a file its result or failure through one helper at all five places, so a
  closed event loop no longer ends the thread with other files in flight.

## What we learned
- A test of "the next file" passed on the old code: the thread that died was replaced when the next
  file came. Only a file already beside it was stranded.

## Stop doing
- Cleaning `/tmp` by a pattern on a shared machine: `rm -f /tmp/w*.log` removed other sessions'
  logs. Temporary files go in the session's own scratchpad, removed by path. The same goes for
  `pkill -f` on a command other sessions run.
