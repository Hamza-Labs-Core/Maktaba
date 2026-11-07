---
schema: 1
kind: pr
pr: 777
title: The Mac app counts a held process as off, not one that keeps stopping
date: 2026-11-07
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- After the live home updated to 0.3.90, the Mac app said "Some parts keep stopping: plugins". The supervisor's `/processes` showed the plugin host `state: "held"`, `blocked: true`, `wanted: false`: no plugin installed, its component's gate closed. `apps/desktop/src-tauri/src/status.rs` (`derive`, `Child::failing`) treated any state it did not list as a problem. Held now counts as off.
- The same morning's 0.3.90 update sat in its watch for hours. The cause was not the held process: the watch skips `blocked` processes. It was pipeline `/healthz` timeouts while the pipeline was busy at boot, plus an app restart at 11:49 that started the watch again.

## What we learned
- The server already called held off (`supervisor.procStatus`), so the bug lived only in the client that kept its own allowlist of fine states. A state added on the server (held, Epic 51) only shows up in a client once some process actually holds: the plugin host (Epic 54) was the first held process on the live home.
- The Apple and Android apps flag an explicit list of trouble states, so a new state is quiet there. The desktop shell did the opposite (unknown means a problem). Both are choices; for a status line, quiet on unknown with the server's own summary is the safer one.
- `plugind` started before the migrations that create `plugin_installs` and retried until they ran: noisy in the log (`relation "plugin_installs" does not exist`), but it recovered on its own.

## Open
- The 0.3.90 run was still `home_applying` / `watching` when this was written, waiting for the pipeline's `/healthz` to answer within the watch's timeout.
