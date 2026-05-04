# Implementation Plan — Story 13.2 Windows App

> Companion to [story-13-02-windows.md](story-13-02-windows.md).
> Tauri 2 wrapper; .msi + .exe installer; signed with EV cert; SmartScreen-clean.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Targets | Windows 10 1809+, x64. ARM64 build optional v1.1. |
| WebView | WebView2 runtime auto-installed by bootstrapper. |
| Installer | WiX (`.msi`) + NSIS (`.exe`); both signed with EV cert. |
| Signing | `signtool` with EV cert; SHA-256. |
| File assoc | `.maktaba` URL shortcuts (custom file type). |
| Out of scope | Same shared concerns as Story 13.1. |

## 1. tauri.conf.json (Windows section)

```json
"bundle": {
  "targets": ["msi", "nsis"],
  "windows": {
    "wix": {
      "language": ["en-US", "ar-SA"],
      "fragmentPaths": ["src-tauri/wix/maktaba_file_assoc.wxs"]
    },
    "nsis": {
      "installerIcon": "src-tauri/icons/icon.ico",
      "headerImage": "src-tauri/icons/installer-header.bmp",
      "license": "../LICENSE",
      "installMode": "perMachine",
      "languages": ["English", "Arabic"]
    },
    "webviewInstallMode": { "type": "downloadBootstrapper", "silent": true },
    "certificateThumbprint": "${WIN_CERT_THUMBPRINT}",
    "digestAlgorithm": "sha256",
    "timestampUrl": "http://timestamp.digicert.com"
  }
}
```

## 2. Native window chrome

Tauri uses native title bar by default. High-DPI handled via `windows.high_dpi_aware = true`. Snap zones come for free with Win32 chrome.

## 3. File association

`src-tauri/wix/maktaba_file_assoc.wxs`:

```xml
<Wix>
  <Fragment>
    <ProgId Id="MaktabaShortcut" Description="Maktaba shortcut" Icon="MaktabaIcon">
      <Extension Id="maktaba" ContentType="application/x-maktaba">
        <Verb Id="open" Command="Open" TargetFile="MaktabaExe" Argument='"%1"'/>
      </Extension>
    </ProgId>
  </Fragment>
</Wix>
```

`MainActivity` equivalent on the Rust side parses `argv[1]` for `*.maktaba` files and emits `app://open-shortcut` to the React layer (which fans out to Story 13.5's server picker).

## 4. WebView2 runtime

Tauri's `downloadBootstrapper` mode auto-installs the runtime if missing. If the user is offline at install time, the bootstrapper offers a download URL; the React side surfaces a "WebView2 required" notice on first launch if the runtime check fails.

## 5. Per-user vs per-machine install

- WiX MSI is per-machine by default; we add a feature flag for per-user (no admin prompt).
- NSIS defaults to `installMode: perMachine`; users can run a per-user variant via `/CURRENTUSER`.

## 6. Code signing

`scripts/sign-windows.ps1`:

```powershell
$thumb = $env:WIN_CERT_THUMBPRINT
signtool sign /sha1 $thumb /tr http://timestamp.digicert.com /td sha256 /fd sha256 `
  "target\release\bundle\msi\Maktaba_*.msi" `
  "target\release\bundle\nsis\Maktaba_*-setup.exe"
```

## 7. ARM64 (v1.1 optional)

Build matrix in CI adds `aarch64-pc-windows-msvc`; we ship as "experimental" with a separate installer. x64 emulation works on ARM but documented as fallback.

## 8. Edge cases

| Case | Handling |
|---|---|
| WebView2 missing + offline | Bootstrapper offers offline installer URL. |
| Antivirus quarantines unsigned binary | We ship signed; document Defender false-positive flow. |
| Per-user install on locked-down corp machine | NSIS per-user path works without admin. |

## 9. Test cases

### 9.1 CI

- `cargo tauri build` produces both .msi and .exe.
- `signtool verify /pa` on both succeeds.
- VirusTotal scan in nightly job; alert if false-positive count rises.

### 9.2 Manual

- Install on Windows 10 22H2 + Windows 11: per-user path no admin prompt.
- Maximize/restore on 4K monitor: scales correctly.
- Open `.maktaba` shortcut from Explorer: launches app pointed at that server.

## 10. Performance

- Cold launch ≤ 3 s on Windows 11 + i5-1135G7.
- Idle RAM ~90 MB.

## 11. Dependencies

- Story 13.1 (shared Tauri base).
- Story 13.8 (auto-update updater is signed with the same cert).
