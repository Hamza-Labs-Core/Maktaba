---
schema: 1
kind: pr
pr: 1634
title: "Story 13.14: the branded two-page NSIS installer, the Player's themed .msi with an Arabic transform, the laid-out .dmg"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The installers build story of the approved desktop design (D3).
- **Art:** `docs/design/desktop/installer-art.mjs` draws `installer-art/art.html` with Playwright on the real tokens, faces and arch. It writes 24-bit BMPs (the NSIS brand pane and titles at 100, 150 and 200%, the Player's WiX dialog and banner in English and mirrored Arabic) and the `.dmg` background as a 1x+2x TIFF. A re-run gives the same bytes.
- **`-setup.exe`:** a custom tauri template (`apps/desktop/src-tauri/windows/installer.nsi`, taken from tauri-bundler 2.10.3) with two Maktaba pages, welcome and installing, in English and Arabic. tauri's install, update, uninstall and command-line code is kept unchanged. The art folder reaches the rendered template through the hooks file (`installer-ui.nsh`).
- **Player `.msi`:** `tools/release/msi/theme_msi.py` themes the built `.msi` through msi.dll (ctypes): the bitmaps, light-on-ink text styles, and an embedded Arabic language transform (storage `1025`, Template `x64;1033,1025`) built from WiX 3.11.2's Arabic strings.
- **`.dmg`:** `tools/release/mac-app/dmg-layout.sh` writes the Finder window's `.DS_Store` (pinned `ds_store` and `mac_alias` wheels) after tauri's build, and `check-dmg.sh` checks it.
- **Built without a Windows machine.** The NSIS template was compile-checked with Homebrew's makensis 3.13 on macOS (`tools/release/win-app/nsis_template.py` renders it as tauri does). The `.dmg` layout was tested on a stand-in image. Windows behaviour is left to CI.
- An independent review of 843489ed2 found six issues, all fixed in 777646775:
  - an upgrade dropped the desktop shortcut;
  - a failed install never showed its details;
  - integer `.msi` keys were bound as strings;
  - the `.dmg` alias recorded a `/var` path, not `/private/var`;
  - hdiutil had no busy retry;
  - minor: Start menu in WiX mode, page order, unbuilt tauri `.msi` files.

## What we learned
- **Compose empties jpackage's resource folder before every run** (`AbstractJPackageTask.prepareWorkingDir`), so `--resource-dir` overrides cannot reach jpackage's WiX step. Theme the built `.msi` through msi.dll instead. A language transform embedded under the LANGID, and named in the Template summary, gives one `.msi` two languages.
- **tauri skips the `.dmg` Finder layout when `CI=true`.** It still copies the background into `.background/`; the `.DS_Store` can be written without Finder.
- **Mount the image at a real path** (`pwd -P` on a `mktemp` dir). `mac_alias` records the target's path relative to the volume's mount point, and a `/var/folders` path the kernel reports as `/private/var/folders` gives an alias out of the volume.
- **NSIS:**
  - A comment ending in a backslash continues onto the next line.
  - A new child window goes to the bottom of its siblings' z-order, so a background control must be made last.
  - Themed check boxes ignore SetCtlColors' text colour.
  - The instfiles page's leave function, called as the install ends, is where `IfAbort` works; `.onInstFailed` runs only as the window closes.
- **makensis runs on macOS and Linux**, and reads Windows plugin DLLs without running them. A tauri template can be compile-checked there, with a stand-in `powershell.exe` for the hooks' `!system`.
- **A transform that changes the code page must say so**: without `MSITRANSFORM_ERROR_CHANGECODEPAGE` in its error conditions, applying it fails with 1624.
- **A test folder named by the clock is not its own**: two crash tests in one tick of macOS' microsecond clock shared one, and one removed it under the other (EINVAL). It is counted now.
- **`GetInstDirError` answers only on NSIS's own directory page**: a custom page has no disk-space check at all until it asks Windows.
- **A solid NSIS installer unpacks its whole payload into `%TEMP%`**, so the room is needed twice on one drive.
- **A safety check can break what it protects**: asking for an update's whole size refused a home that updates fine today, because the old payload goes first. It counts as room now; CI installs an update with room for one payload and not two.
- **The installer's refusal comes after the app has stopped its server**: the app looks first, so too little room costs no outage.
- **Two processes settle one update**: the agent starts before the app, so a reason told to one of them is lost. It is left in a file both read, tied to the release and the attempt, and only the app, which tells the person, takes it away.
- **What an installer unpacks can be read from its version information**, uncompressed at the start of its file: the app asks for exactly what its installer will.

## Keep doing
- Compile-check the NSIS template locally (`nsis_template.py --makensis`) before pushing. It found a swallowed line that would have broken every Windows build.
- Keep the plain decisions (restyling, translating, mirroring, typed key binds) in functions tested on every OS, and only the database I/O Windows-only.
- When CI cannot reach a part, write a negative test for the bug class (the `/var` alias), not just a positive run.

## Stop doing
- Changing a default users rely on (the desktop shortcut) without asking what an upgrade loses. Trace the reinstall path's uninstall before changing a box's default.
- Keeping configuration for an artifact nothing builds (tauri's themed `.msi`): it is unchecked and drifts.

## Open
- The first real runs of the template, `theme_msi.py` and `dmg-layout.sh` are this PR's win-app, Desktop Player (Windows) and mac-app jobs.
- 13.17's Windows capture should show both pages, the failure path, the Back state, and the Player `.msi` in Arabic, applied by Windows Installer itself.
- A tauri CLI update fails `test_installers.py` until the template is taken again from its bundler.
- RadioButton positions in the Player's Arabic transform are not mirrored. WixUI_InstallDir has none today.
