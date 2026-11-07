---
schema: 1
kind: pr
pr: 774
title: Epic 53 on the desktop — crash reports to the home and Report a problem (Maktaba's shell, the desktop Player)
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- Maktaba's shell keeps its own panics, scrubbed. The web app in its window sends them to the home only when crash reports are on there, through two new commands in the `local-server` capability.
- The tray and Help menu open the web's Report a problem.
- The desktop Player keeps its uncaught exceptions without their messages. It asks once per home, sends with the Android `client`'s envelope and outbox, and has Report a problem and My reports in its Help menu.
- `Redact` moved from the Android core to the portable `client`.

## What we learned
- The shell has no user token, and the API takes envelopes only from a signed-in device with that person's consent. The web app in the shell's window has both: the session cookie and the switch. So the shell hands its crashes to the web app rather than sending them itself. The consent is then the same switch, and the shell needs no new route at the home.
- `File.renameTo` does not replace an existing file on Windows. The outbox's state was saved only the first time, and only the desktop Player's Windows job showed it (#745). `Files.move(REPLACE_EXISTING)` fixed it.
- Two Gradle builds started at once in one checkout can fail one of them with nothing in its output. Run them one after another.

## Open
- The desktop Player offers no screenshot: capturing a window needs the system's screen recording.
- 53.16's "Also send to Maktaba" box needs the home's sharing switch.
