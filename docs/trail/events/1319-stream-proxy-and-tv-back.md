---
schema: 1
kind: pr
pr: 1319
title: "The apps' stream proxy copies in chunks and bounds what it keeps; Back always leaves the Apple TV live player"
date: 2026-11-14
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
agent: claude-code
---

## What happened
- #1319 merged as 65b3b7fa8. Report: while a live channel played in the Apple TV app, the
  Apple TV stopped answering: Back did nothing, the app switcher could not be reached, sleep
  and wake came back to the same state, and only cutting the power cleared it. No device
  logs were available. **The cause was not established**; the change removes what was found
  and is hardening, not a proven fix.
- The session first read the wrong app: the iPhone, iPad and Mac Player under
  `apps/apple/Player`. The Apple TV app is its own (`apps/tv/tvos/MaktabaTV`) with its own
  live player; it shares MaktabaKit's `StreamProxy` and `LiveOverlays`.
- `StreamProxy` (every stream in the four Apple apps passes through it on the home network)
  copied each body one byte at a time from `URLSession.AsyncBytes`, at the priority it
  inherited. Measured on a Mac: 14.8 ms of CPU per MB against 3.5 ms in chunks. At the live
  default (one 1080p rendition, about 0.8 MB/s) that is about 1 % of a Mac core: waste, and
  by itself not enough to stop a device.
- Three rounds of independent review, each on a design the last round had changed:
  1. A 60 s "takes nothing" drop timed one whole write of up to several MB, so it cut slow
     readers mid-body and every paused film; a read-side FIN was taken for "the player left".
     The drop was removed: a connected player is never timed.
  2. CI then showed a no-reader holding 20.7 MB against an 8 MB claim: suspending a
     `URLSessionTask` does not bound what its delegate is handed (507 MB kept in one local
     run). The data callback is now held once 256 KB is kept, each request on its own session
     (idle ones reused), 16 requests at most. Review of that found the session's 60 s request
     timer still ran during a hold, cutting a pause of over a minute: the upstream request now
     has a long timeout and the proxy times the home's silence itself.
  3. That watchdog counted while the proxy was writing to a paused player; it now counts only
     while the head or the next bytes are awaited.
- An intermittent hang (about 1 run in 20) was the test's own client: a Network.framework
  connection stuck in `waiting(Address already in use)` under churn of loopback connections,
  while a plain `connect()` to the same listener worked. The test client is a plain socket
  now, with bounded reads that print the client's, the proxy's and the home's state.
- Apple TV live player: a second Back always leaves (cleared by the viewer's own tune, Select
  or info press), leaving stops picture, sound and proxy at once, one `LivePlayback` per
  screen (a new one, with a leaked observer, was made on every redraw of the tab behind it).
- #1318 merged as d07330b58: `docs/testflight.md` gains a section on TestFlight listing a
  build and refusing to install it. The session spent several turns on its own causes
  (agreements, processing, the tester group, Screen Time, the parental controls release), was
  told the problem predated that release by weeks, and had not searched the web for the
  message. It then recorded a pasted Stack Overflow answer as a fix already made, in memory
  and in the first version of the guide's section, and corrected both. The problem is open.

## What we learned
- `URLSessionTask.suspend()` is not backpressure: what the session has read keeps arriving.
  Holding the delegate callback is; it parks a thread per held request.
- While a data callback is held the session reads nothing, and its request-idle timer still
  runs. A proxy that holds must lift that timer and time the upstream's silence itself, only
  while it is the upstream being waited on.
- A timer on "one write" is not a timer on progress when writes are megabytes.
- An orderly end of the read side is "no more requests", not "the client left".
- A pause test that pauses less than every timer in the path proves nothing about pausing.
- A reviewer who did not write the code, run again after each redesign, found a real defect
  each time until the third pass; green checks found none of them.
- For a message that belongs to another company's product, search for its exact words first.
- Text a person pastes may be someone else's report. Ask before recording it as done.

## Keep doing
- Measure a suspect before calling it the cause.
- Reproduce an intermittent failure with the state of every party printed before choosing
  between "the code" and "the test".

## Stop doing
- Naming a cause for a device problem from reading alone.
- Tying a long-standing problem to the newest change without asking how long it has lasted.

## Open
- Nothing here ran on a real Apple TV, and nothing ran for hours. Whether the freeze is gone
  is not known.
- The memory figures are Mac loopback measurements; a real AirPlay receiver reading chunked
  responses, and keepalive finding a vanished receiver, were not tried.
- TestFlight still refuses to install builds it lists. The answers found point at the app
  record's sale state in App Store Connect, which only the owner can change.
- The iPhone, iPad and Mac Player's own live code was not audited.
