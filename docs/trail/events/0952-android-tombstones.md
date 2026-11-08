---
schema: 1
kind: pr
pr: 952
title: Android native crashes keep the crashed thread's frames (story 53.14)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [745, 732, 907]
---

## What happened
- On Android 12 and later, a native crash in the Player, the Remote or the Android TV app is kept and sent with its crashed thread's backtrace. Before, only the reason was kept, because `ApplicationExitInfo.traceInputStream` is a binary tombstone (#745).
  - A small hand-written protobuf reader (`client/.../crash/Tombstone.kt`) reads only the needed fields of AOSP `tombstone.proto`: the crashed tid, the signal and code, the abort message, and the threads map's frames (pc offset, function and offset, library, build id).
  - Each frame keeps only its library's file name. The abort message has its paths replaced and is scrubbed. Thread names, memory maps, logs, open files and the command line are never read.
  - The envelope carries the frames oldest first, with the libraries and build ids for symbolication. The kept crash's type becomes the signal, e.g. `SIGSEGV`.
  - On Android 11, an unparseable stream or a missing crashed thread, the crash is kept with its reason alone; a stream cut at the 2 MB read limit keeps what came before the cut.
- Triage of the open items for the TV apps:
  - the Android TV app already shows the status strip: its screens sit in the shared HomeStatusHost, and #907 adds the test that drives it on the TV;
  - the Apple TV app already keeps PLCrashReporter crashes (#732), and the Android TV app already collected its exits (#745);
  - the native stack was the one gap, closed here for every Android app.

## What we learned
- A trail line can outlive its work: "native crash capture for the TV" stayed open after #732 and #745 had shipped it. Check the code before taking a trail line as work.
- A protobuf reader that tolerates a cut only at the top level still handles a truncated nested message, because the outer field's length then runs past the end.

## Open
- Symbolication of Android native frames on the home with the release's symbols (alongside 53.16's Apple dSYM item).
