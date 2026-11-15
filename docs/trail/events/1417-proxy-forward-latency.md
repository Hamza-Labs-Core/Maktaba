---
schema: 1
kind: pr
pr: 1417
title: "The Apple stream proxy passes a request on at once: user-initiated priority, the token held now"
date: 2026-11-15
session: cd1328a6-61bf-4c4e-9a45-41166e522e8a
agent: claude-code
---

## What happened
- #1417 merged as 71d9639cc. It answers the open item in #1416 (the event for #1412). The proxy
  passed a segment request on about 3 s after AVPlayer sent it, on a loaded simulator
  (iphone shots/6 of #1412's proof run, head 683e2f7ed, run 43715426951). AVPlayer waits
  about 2 s, then drops the stream's only variant, and the playback stops for up to 30 s.
- Cause: every stage of the request path ran at utility QoS: the listener and connection
  queue, the serving task, the hop onto the `APIClient` actor for the token, and the
  upstream session's callback queue. The job's settle log shows `runner-idle=0.0%` with the
  simulator at 150–800 % CPU. Measured on a 28-core Mac with 4 user-initiated busy threads
  per core: the home saw requests 100 ms to 1 s late at utility, and 2–10 ms late at
  user-initiated.
- Second cause, not hit in CI: `fetch` awaited `playbackHeaders(minLife: 60)`. For the last
  minute of every 15-minute token, each request waited for a whole refresh. Now a request
  uses the held token (`heldPlaybackHeaders`, nonisolated) if it has at least 10 s left.
  Below 60 s left, one refresh runs apart from the requests.
- The new `Log.network` debug lines show each request's stages with the ms since it was
  accepted: accepted, head read, token held, passed on, and the home's answer.

- CI (run 43724539259): the only failure on the first head was
  `PlayerConnectProgressUITests.testSignInAndRestoreStatesEnglish` in iphone 2/6 ("the button
  says it is signing in"). It ran 1018 s, and then xcodebuild restarted the runner ("unexpected
  exit, crash, or test timeout"). It wasn't this change, and it wasn't a dropped tap. From the
  "With a code" tap on, every XCUITest "wait for app to idle" ran its full 60 s, and the app's
  pairing request to the mock home timed out (-1001). The held `/api/auth/login` then hit
  the app's own 60 s request timeout at 14:59:46.55. That ended "signing in" 0.3 s before
  the test's 10 s check began. The sign-in screen doesn't use the stream proxy. The failed
  jobs were re-run once, with no change to the code, and passed.

## What we learned
- Utility QoS isn't "a little lower". When the CPU is saturated, a utility task can wait
  hundreds of ms to seconds for a thread. Work on a deadline the player sets (a segment
  inside one target duration) belongs at user-initiated.
- An actor hop just to read a value that's already cached puts the request behind whatever
  else holds the actor, at the request's own (low) priority. Read immutable or lock-guarded
  state nonisolated.
- The CI settle log (`*-settle.log`, CPU per process and runner idle time) is the quickest
  way to tell a loaded host from a slow app.
- The "waits behind another request" test passes on the old code too. Requests were never
  serialized, so it only guards against that being added.

## Keep doing
- Before blaming a change for a red UI test, read the job's ` t = ` lines. Idle waits that
  each take 60 s mean the runner or the app stalled, not that a state came and went early.
- Hold the blocking thing (a request, a refresh) for the whole test. A request that waits
  behind it then never arrives, and the time bound only absorbs a loaded machine.

## Open
- `PlayerConnectProgressUITests.testSignInAndRestoreStatesEnglish` can lose its "signing in"
  check when the runner's idle waits stall for minutes: the held login reaches the app's
  60 s request timeout first. Why the app never went idle on the code sign-in screen in
  run 43724539259 (attempt 1, iphone 2/6) is not known.
