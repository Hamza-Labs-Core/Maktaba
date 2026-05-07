# Story 13.2 — Windows app

A signed `.msi` and `.exe` installer for x64.

**Anchors:** [`architecture.md` §6.4](../../architecture.md).

## AC

- Targets Windows 10 1809+; ARM64 build optional v1.1.
- Native window chrome (title bar, snap zones); high-DPI aware.
- WebView2 runtime auto-installed via the bootstrapper if missing.
- Start Menu entry, file association for `.maktaba` shortcuts, taskbar
  pinning.
- Code-signed installer via EV cert; SmartScreen passes.

## TC

- Install on Windows 10 22H2 and Windows 11: succeeds without admin
  prompt for per-user install.
- Maximize / restore on a 4K monitor: scales correctly.
- Open a `.maktaba` shortcut from Explorer: launches app pointed at that
  server.

## EC

- WebView2 runtime missing and offline: bootstrapper offers an offline
  installer link.
- Antivirus quarantines the unsigned binary: we ship signed; document
  Defender false-positive process.
- ARM64 Windows running x64 emulator: works but documented as
  "experimental".
