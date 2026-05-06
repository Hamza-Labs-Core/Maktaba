# Story 25.28 — Windows installer

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

Windows distribution as a signed MSI (primary) and NSIS EXE (small,
fallback for users who can't install MSIs because of group policy).
The install registers Maktaba as a Windows Service so it runs
without anyone logged in.

Components:

- **MSI built with WiX 4.** Outputs `Maktaba-<version>-x64.msi`
  and `Maktaba-<version>-arm64.msi`. Per-machine install (writes
  to `Program Files\Maktaba`) requires UAC elevation; per-user
  install (writes to `%LOCALAPPDATA%\Programs\Maktaba`) does not.
- **NSIS EXE** mirrors the MSI for environments where MSI
  install is blocked. Same install paths, same service registration.
- **Code signing.** EV certificate (we lose smart-screen reputation
  for the first ~weeks until reputation builds). Sign all `.exe`,
  `.dll`, MSI, EXE installers. Sign with `signtool` and
  `/td sha256 /fd sha256 /tr http://timestamp.digicert.com`.

Service:

- Service name: `MaktabaServer`. Display: "Maktaba Media Server".
- Runs as `LocalService` by default for least-privilege; user
  can re-target to a service account that has access to
  network shares for libraries on SMB.
- Recovery: restart on failure (delay 60s, 120s, then "take no
  action" after 3 fails to avoid restart loops).

Tray icon (optional, runs in user session):

- A small WPF/WinUI app `MaktabaTray.exe` runs from the
  Startup folder; communicates with the service over local
  named pipe (`\\.\pipe\maktaba`) for status. Read-only;
  service controls itself.
- Menu: status, "Open in browser", "Pause indexing", "Open
  config folder", "Show logs", "Help".

Firewall:

- During install, we add Windows Defender Firewall rules:
  - Inbound TCP 8080 (API), 8081 (Streaming), allow on
    "Private" networks only by default. Public network
    requires user opt-in via Preferences.
  - Outbound: all (default Windows policy).
- Implemented via `netsh advfirewall` invoked from the
  WiX custom action; logged for transparency.

Storage:

```
%PROGRAMDATA%\Maktaba\        # service-owned data
    config.toml
    db\maktaba.db
    cache\
    logs\
%PROGRAMFILES%\Maktaba\       # binaries (read-only after install)
    api.exe
    streaming.exe
    pipeline-launcher.exe
    ffmpeg\
    python\                   # bundled venv
%LOCALAPPDATA%\Maktaba\       # tray app per-user state
```

## Acceptance criteria

- **Given** an admin double-clicks the MSI,
  **when** UAC elevates and install completes,
  **then** the `MaktabaServer` service is registered, set
  to auto-start, and started; firewall rules added; tray
  app added to Startup folder.
- **Given** the system reboots,
  **when** Windows starts,
  **then** the service starts before user login and
  responds to `GET http://localhost:8080/healthz` with 200
  within 30s of boot.
- **Given** a non-admin user double-clicks the MSI,
  **when** UAC denies elevation,
  **then** the installer offers per-user install (no
  service; runs in user session via Startup folder); the
  user can complete install without admin.
- **Given** the user uninstalls via "Add or remove programs",
  **when** they confirm,
  **then** the service stops, binaries are removed,
  firewall rules removed, tray app entry removed; the
  user is asked whether to keep `%PROGRAMDATA%\Maktaba`.
- **Given** SmartScreen flags the unsigned/cold-reputation
  installer,
  **when** the user clicks "More info" → "Run anyway",
  **then** the install proceeds normally.
- **Given** a Windows Defender quarantine triggers on
  FFmpeg (rare false positive),
  **when** the operator submits the binary to MS for
  analysis,
  **then** documentation in our support page guides them.
- **Given** the service restarts unexpectedly,
  **when** Windows recovery policy fires,
  **then** the service comes back within 60s.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | manual      | Win 11 fresh | run MSI | service registered, healthz 200 |
| T02 | manual      | Win 10 22H2 | run MSI | identical |
| T03 | manual      | Windows on ARM | run ARM64 MSI | works on Surface Pro X |
| T04 | manual      | Group Policy: deny MSI | run NSIS | works |
| T05 | regression  | Windows Defender real-time | scan binaries | clean |
| T06 | regression  | uninstall path | run | clean (registry, fw, services) |
| T07 | integration | reboot + autostart | observe | service up |
| T08 | manual      | per-user install on a non-admin | run | works without elevation |
| T09 | regression  | named pipe ACLs | test | only `LocalService` and Authenticated Users can write |
| T10 | manual      | firewall rule presence | inspect | only "Private" network allowed by default |

## Edge cases

- **Antivirus quarantining FFmpeg.** Defender, Norton,
  Kaspersky periodically false-positive on packed FFmpeg.
  We submit binaries to vendors and ship our own
  not-packed build to dodge it.
- **Locked binaries on update.** Upgrading via MSI
  requires the service to be stopped. The MSI custom
  action stops the service before file replacement; if
  files are locked (rare), we ask the user to reboot
  and the upgrade completes via "RunOnce".
- **Long-path support.** Windows long paths (>260 chars)
  enabled in `manifest.xml`; required because the
  Python venv has deep folders.
- **PATH pollution.** We don't add to system `PATH`; we
  invoke binaries with absolute paths.
- **Locale.** Installer is English in v1; localizable
  via WiX MUI in v2.
- **Group Policy software restrictions.** Some enterprises
  block unsigned MSIs; ours is signed. AppLocker policies
  vary; we document the path-rules to allow.
- **Non-admin firewall rules.** Per-user installs cannot
  add firewall rules; we surface a dialog "to allow remote
  access on this network, ask an admin to run the
  firewall script".
- **Service account file access.** Libraries on SMB shares
  require `MaktabaServer` to run as a domain service
  account. Documented; we don't auto-configure.
- **Pause / resume on sleep.** Service survives sleep;
  the watchdog handles network IP change after wake.
- **64-bit-only.** No x86 build; Windows 11 + Win 10
  x64 + ARM64 only.

## Files / packages

- `desktop/windows/installer/` — WiX project.
- `desktop/windows/installer/CustomActions.cs` — service
  + firewall installation.
- `desktop/windows/MaktabaTray/` — WinUI 3 tray app.
- `desktop/windows/scripts/sign.ps1`.
- Release pipeline: builds two MSIs (x64, ARM64), one
  NSIS EXE (x64).

## Open questions

- **Microsoft Store distribution.** MSIX is feasible but
  introduces sandboxing similar to App Store. Out for v1.
- **Winget manifest.** Add a `winget install Maktaba`
  manifest in v1.1.
