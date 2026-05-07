# Implementation Plan — Story 13.3 Linux App

> Companion to [story-13-03-linux.md](story-13-03-linux.md).
> .AppImage + .deb for x86_64; .rpm v1.1.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| WebView | WebKitGTK (Tauri default). |
| Distros | Ubuntu 22.04+, Debian 12+, Fedora 38+. |
| Targets | x86_64 (AppImage + .deb); aarch64 best-effort. |
| Display servers | Wayland + X11; fractional scaling honored. |
| MIME registration | `.maktaba` opened via Maktaba (`.deb` only). HLS playlists (`application/x-mpegurl`) intentionally omitted: HLS support is for cloud streams handled in-app, not local files we'd ingest. |
| Out of scope | Snap/Flatpak (post-v1); Mac App Store-style sandboxing. |

## 1. tauri.conf.json (Linux section)

```json
"bundle": {
  "targets": ["deb", "appimage"],
  "linux": {
    "deb": {
      "depends": ["libwebkit2gtk-4.1-0", "libgtk-3-0"],
      "section": "video",
      "files": {
        "/usr/share/applications/maktaba.desktop": "src-tauri/linux/maktaba.desktop",
        "/usr/share/icons/hicolor/512x512/apps/maktaba.png": "src-tauri/icons/512x512.png",
        "/usr/share/mime/packages/maktaba.xml": "src-tauri/linux/mime.xml"
      }
    },
    "appimage": { "bundleMediaFramework": true }
  }
}
```

## 2. .desktop file

```ini
# src-tauri/linux/maktaba.desktop
[Desktop Entry]
Type=Application
Name=Maktaba
GenericName=Personal media library
Comment=Self-hosted video library with full transcript search
Exec=maktaba %U
Icon=maktaba
Categories=AudioVideo;Player;Network;
MimeType=application/x-maktaba;
Terminal=false
StartupWMClass=maktaba
```

## 3. MIME XML

```xml
<!-- src-tauri/linux/mime.xml -->
<mime-info xmlns="http://www.freedesktop.org/standards/shared-mime-info">
  <mime-type type="application/x-maktaba">
    <comment>Maktaba shortcut</comment>
    <glob pattern="*.maktaba"/>
  </mime-type>
</mime-info>
```

## 4. Wayland & HiDPI

Tauri/WebKitGTK reads `GDK_SCALE` automatically. For fractional scaling on KDE/Wayland we test with `KDE_SESSION_VERSION=6` and `WAYLAND_DISPLAY` and document any quirks. No code changes expected.

## 5. AppImage portability

`bundleMediaFramework: true` packs gstreamer plugins so playback works without distro-installed media packages. Final AppImage size target ≤ 90 MB.

**Trade-off note (vs architecture §6.4 ~10 MB binary target).** Only the
AppImage carries the gstreamer payload; `.deb` and `.rpm` rely on the
system gstreamer (declared via `depends`) and stay near 10 MB. The
architecture's "~10 MB binary" promise applies to the macOS `.app`,
Windows installer payload, and Linux `.deb`/`.rpm`; the AppImage is the
deliberate exception because portability is its whole purpose.

## 5.1 mDNS / avahi runtime probe

Linux mDNS resolution depends on `avahi-daemon` being running (Story
13.5). At startup, probe for it and degrade gracefully if absent:

```rust
fn avahi_running() -> bool {
    std::process::Command::new("pgrep")
        .arg("-x").arg("avahi-daemon")
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

// In setup:
#[cfg(target_os = "linux")]
if !avahi_running() {
    log::warn!("avahi-daemon not detected; mDNS discovery disabled. Manual server entry remains available.");
    app.manage(MdnsDisabled);
}
```

The in-app server-URL onboarding (Story 13.5 §4 manual entry) still
works without avahi.

## 6. ARM64 (best-effort)

Build matrix adds `aarch64-unknown-linux-gnu`. Document GPU video decode caveats on Raspberry Pi 5 (V4L2 stateless decoder may not support HEVC).

## 7. Edge cases

| Case | Handling |
|---|---|
| WebKitGTK missing/too old | Installer surfaces apt/dnf hint with package name. |
| HiDPI on KDE | Honors `GDK_SCALE`; documented baseline. |
| AppArmor / SELinux blocks file dialog | Document workaround in release notes (`aa-complain`, `setenforce 0`). |
| AppImage on glibc-too-old distro | Document min glibc 2.31 (Ubuntu 20.04+). |

## 8. Test cases

### 8.1 CI

- Build matrix produces .deb and .AppImage on Ubuntu 22.04 runner.
- `dpkg-deb -I` + `lintian` clean.
- AppImage runs `--appimage-extract` and `chmod +x` boot test.

### 8.2 Manual

- AppImage on Ubuntu 22.04 Wayland: window opens; mDNS discovers server (Story 13.5).
- .deb on Debian 12: launcher appears; `.maktaba` MIME registered (`xdg-mime query default application/x-maktaba` returns `maktaba.desktop`).

## 9. Signing & checksums

- `.deb`: SHA256 + GPG sig published alongside artifacts.
- `.AppImage`: SHA256 + GPG sig.
- Public GPG key documented in `docs/security/keys.md`.

## 10. Performance

- Cold launch ≤ 3 s on Ubuntu 22.04 + i5.
- Idle RAM ~90 MB.

## 11. Dependencies

- Story 13.1 (shared Tauri base; single-instance lock §12 reused here).
- Story 13.5 (mDNS discovery; avahi probe §5.1 above).
