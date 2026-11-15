---
schema: 1
kind: pr
pr: 1370
title: "Apple stream proxy: a connection takes a place only after its secret checks (security)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1370 merged as 5d89940ff. MaktabaKit's `StreamProxy` (the loopback proxy, and the LAN proxy
  an AirPlay session opens) counted every accepted socket against its 16 places before reading
  anything, and evicted only abandoned requests. Sixteen silent sockets from any process or LAN
  device kept real playback at 503 until the 30 s head timeout, and a client could reconnect.
- A connection now waits outside the counted set until its header names a GET or HEAD under the
  secret; only then does `admit()` give it a place. A bad header gets 400, a wrong secret 404,
  both while waiting. At most 32 may wait; one more closes the oldest waiting one, never a
  request being served.

## What we learned
- A security review run during a merge can flag code the branch only carried in from main; fix
  it on main in its own PR, never inside an unrelated story PR.
- For slot exhaustion, a shorter header timeout only narrows the window (a reconnecting client
  keeps the slots full) and can cut off a slow AirPlay receiver; admit after authentication and
  bound the waiting set separately.
- Apple UI tests on the first run failed on timing (a banner checked after it auto-hid, a
  double tap and PIN keys mis-registered) with no stream request involved; one rerun passed.
