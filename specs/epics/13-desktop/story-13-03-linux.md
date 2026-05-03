# Story 13.3 — Linux app

`.AppImage` and `.deb` for x86_64; `.rpm` v1.1.

**Anchors:** [`architecture.md` §6.4](../../architecture.md).

## AC

- Built against WebKitGTK (Tauri default); compatible with Ubuntu 22.04+,
  Fedora 38+, Debian 12+.
- `.deb` installs a `.desktop` launcher and registers MIME types for
  `.maktaba` and `application/x-mpegurl` opened via the app.
- `.AppImage` is portable; runs on any compatible distro without install.
- Wayland and X11 supported; fractional scaling honored.

## TC

- Run `.AppImage` on Ubuntu 22.04 Wayland: window opens, mDNS discovers
  the server, video plays.
- Install `.deb` on Debian 12: launcher appears in menu, MIME registered.
- Run on a Raspberry Pi 5 (ARM64 — best-effort): document GPU video
  decode caveats.

## EC

- WebKitGTK missing or too old: installer surfaces apt/dnf hint with
  the package name.
- HiDPI scaling on KDE: window respects `GDK_SCALE` / Wayland scaling.
- AppArmor / SELinux blocks file dialog: documented workaround in
  release notes.
