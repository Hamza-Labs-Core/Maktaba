---
schema: 1
kind: pr
pr: 863
title: The pipeline's relay to the plugin host on Windows (#710, 1 of 2)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- On Windows the supervisor serves a named pipe for the pipeline and relays each connection, byte for byte, to the plugin host's AF_UNIX dispatch socket (`cmd/maktaba-server/internal/pluginrelay`).
  - The name is random per run; a protected DACL admits only its user; it rejects remote clients; the first instance takes `FILE_FLAG_FIRST_PIPE_INSTANCE` (no squatting).
  - It sends one Ready byte once the host answered.
- The pipeline's `pipe.py` opens it through the Proactor loop. A missing Ready becomes the `ConnectionRefusedError` a refused unix socket raises.
- Windows CI tests the relay: bytes, squatting, a down host, another local user refused, and the pipeline's own connect through it.

## What we learned
- A probe on `windows-2022` settled the design before any of it was written:
  - Python 3.12 there has no `socket.AF_UNIX`;
  - a process inside the host's AppContainer may not create a named pipe (`Access is denied`);
  - AF_UNIX crosses the AppContainer both ways for Go;
  - a pipe served outside reaches an AppContainer client only when its DACL admits AppContainers.
- GitHub's `shell: bash` runs with `-e`: a step meant to go past an expected failure needs `set +e`. The first probe run stopped at the first expected failure.
- Go 1.24+ wraps an overlapped Windows handle in `os.File` on the runtime's I/O completion port, so a full-duplex pipe relay needs only `golang.org/x/sys/windows` (no go-winio).
- x/sys/windows has no `LogonUser` or `ImpersonateLoggedOnUser`. The other-user test loads them from advapi32, with a network logon (no right to log on locally needed on Windows Server).

## Keep doing
- Probe the platform on a real runner, on a throwaway branch whose workflow runs only on its own pushes, before choosing a cross-process transport.

## Open
- PR 2 of #710: the supervisor starts the host through `appcontainer.Run`, runs the relay with `MAKTABA_PLUGIND_PIPE` in the pipeline's environment, and `PluginsHere` stops excluding Windows.
  - Spec 54.03 §4 says the host refuses to call `WSAStartup`, and AF_UNIX on Windows goes through Winsock: that PR must check how the dispatch socket squares with the self-check.
