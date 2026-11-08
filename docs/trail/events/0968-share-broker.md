---
schema: 1
kind: pr
pr: 968
title: A Windows runner's library on an SMB share through its launcher's handle broker (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [956]
---

## What happened
- An AppContainer with no capabilities reaches no SMB share, so before #968 a job on a share-hosted library ran in the worker.
- Capabilities (`privateNetworkClientServer` with `enterpriseAuthentication`) were refused: they open the whole LAN and every share the user's credentials reach.
- The launcher (outside the sandbox) now serves a per-runner pipe that only the runner's own process may use. For each file it confines the path to the group's brokered folders, opens it, refuses any reparse point on the way, and duplicates a read-only handle into the runner. No byte passes through the broker. Subtitles are written by the launcher from the bytes the runner sends, so the runner never holds a handle it could write with.
- Administrative shares are never brokered; a share this machine serves is mapped to its folder and refused like that folder when it holds or is inside a protected one.
- CI makes two real shares (`New-SmbShare`, `\\\\localhost\\mklibrary`) and probes from inside: a 256 MiB video read in any case, a subtitle written; traversal, a junction out, an 8.3 alias, a stream, the other share and a second process are refused. Reads through the handle ran no slower than the share directly.

## What we learned
- Compare share paths by ASCII case only: Go's Unicode folding and Windows' upcase table disagree (U+0130, the Kelvin sign), which let a request name a sibling folder as the root until an independent review caught it.
- Handing over a handle instead of carrying bytes keeps confinement without a throughput cost.

## Open
- A share the group writes in full (`files.write`) is not brokered; its jobs run in the worker.
