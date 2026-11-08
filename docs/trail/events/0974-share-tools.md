---
schema: 1
kind: pr
pr: 974
title: A Windows runner's ffmpeg and ffprobe read one share file through a brokered handle (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [968]
---

## What happened
- #968 left tools out: a tool in the AppContainer cannot use the runner's brokered handles, so a job handing ffmpeg a share path ran in the worker.
- The runner now asks the broker to open the tool's one share input (the same checks as its own reads) and starts ffmpeg or ffprobe with that read-only handle as standard input and the argument as `fd:`. ffmpeg seeks a file descriptor as it seeks a path, so an mp4 with its index at the end works; `pipe:` does not seek (the CI control logs "partial file").
- A second share file, a share path as an output or inside another argument, an unbrokered share, any other program, or a caller that needs the tool's stdin is refused up front and the job runs in the worker.
- CI on the real share: a thumbnail, a probe of an mp4 indexed at its end and the transcription audio decode run inside the AppContainer, about as fast as the worker reading the share directly.
- The watcher's merge of main stopped on a conflict in spec 54.23 after #966 merged; resolved by keeping both sides.

## What we learned
- The CI run that proved `fd:` also showed that asyncio's captured tool starts fail inside the AppContainer, share or not (a local-copy control): see 0976 and 0977.

## Open
- None for share inputs.
