---
schema: 1
kind: pr
pr: 1276
title: "The pairing sign-in test reads a copy of the request log"
date: 2026-11-14
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1276 merged as e2a045a50, test-only. `PairingTest.theSessionSignsInWithACode` threw
  `ConcurrentModificationException` in the Windows Desktop Player job on #1268: it iterated its
  `Collections.synchronizedList` request log with `first { }`, which does not hold the list's
  lock, while mockwebserver's dispatcher thread could still append. The assertions now read one
  copy taken under the lock, and the token check names the `/api/auth/me` request instead of
  `last()`. 15 of 15 pass locally.

## What we learned
- A synchronized list only guards single calls: indexed reads are safe, but any iteration
  (`first { }`, `any`, `for`) must hold the lock or read a copy. A `ConcurrentModificationException`
  in a test is a real race in the test, not noise: rerun once to unblock an unrelated PR, and
  fix the test on main the same day.
- This Mac needs `JAVA_HOME=/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home` for
  `apps/android/gradlew`; stop the daemon (`./gradlew --stop`) afterwards.
