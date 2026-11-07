---
schema: 1
kind: pr
pr: 869
title: Plugins on Windows (the host in its AppContainer, the relay)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [863]
---

## What happened
- The supervisor starts maktaba-plugind inside its AppContainer and Job Object through a hidden `maktaba-server plugins-sandbox-run` (appcontainer.Launch, shared with the CI job's aclaunch).
  - It grants read on `plugins\` and read-write on the cache, the run folder and the short socket folder; the install's preload starts the same way.
  - It runs #863's relay and names its pipe to the pipeline; `PluginsHere` no longer excludes Windows.
- On Windows both sockets live in the host's run folder, never `run\` itself.
- Spec 54.03 §4 now says the host opens its own socket through Winsock before entering the AppContainer, and the guest cannot reach any socket; the developer guide's sandbox page says it in both languages.

## What we learned
- §4 described a Winsock self-check (`WSAStartup` refused, `winsock_loaded`) that no code ever had. The host's sandbox code already said Winsock is loaded by design.
- Granting the AppContainer the API socket's folder as it was (`<data>\run`) would have given it the supervisor's own run files, its local token among them. Moving both sockets into the host's own run folder keeps the grant to what the host needs.
- The supervisor builds each process's environment before it builds the plugins role, so the relay starts with the first pipeline environment rather than with the role.
- `TestRestoreAskedPutsTheBackupBackBeforeAnyRole` fails on this Mac on main as well; it is not this PR's.

## Keep doing
- Read the code a spec section describes before relying on the section: here the code was right and the spec stale.

## Open
- Maktaba's own first-party plugin runners (Python, 54.23) still run their stages in the worker on Windows; only third-party plugins (maktaba-plugind) run there now.
