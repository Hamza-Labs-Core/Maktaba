---
schema: 1
kind: pr
pr: 1743
title: "Android: AI mode's refusal was read on the main thread, and a swallowed exception chose the reason"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- Issue #1741, found by reading the code while tracing #1738. `ApiClient.opened` read a refusal's
  body where the coroutine resumed, on Android the main thread. The exception was swallowed, the
  body was empty, and a home whose admin had turned AI mode off was shown as one without it set up.
- Writing the test found a second symptom: closing a 401's unread body reads the network too, so an
  ended sign-in showed as an answer that stopped.
- #1743 reads and closes an answer outside 2xx on OkHttp's thread.
- Issue #1687 (SRC-60) is #1683's race: the same assertion at heads without it, and 30 `unit` jobs
  with it passed, none failed. Closed.

## What we learned
- `getOrDefault` on a body that decides what is said turns any failure into one particular answer.
- A JVM test can hold Android's rule: a socket factory whose streams a named thread may not read.
- Asserting the platform's rule beside the fix, on CI's emulator, says whether the stand-in is true.

## Open
- The emulator could not be opened locally (no Java for `avdmanager`, no emulator port): CI's
  Android TV emulator is the only Android run.
- A stream's unread body is still closed by whichever thread cancels as the headers come in.
