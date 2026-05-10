# Maktaba desktop (Tauri 2)

Epic 13 (Stories 13.1 / 13.2 / 13.3) ships macOS, Windows, and Linux
desktop apps via Tauri 2. The frontend bundle is the same `web/dist`
served by the SPA — the native shell only adds menu bar, tray, window
state, file dialogs, and the auto-update channel.

## Layout

```
apps/desktop/
├── package.json            ← Tauri JS bindings + scripts
├── src-tauri/
│   ├── Cargo.toml          ← Rust deps (tauri, plugins)
│   ├── build.rs            ← required by tauri-build
│   ├── tauri.conf.json     ← bundle id, window, CSP, plugins
│   ├── capabilities/       ← allowlists per window
│   ├── icons/              ← bundle icons (PNG / ICNS / ICO)
│   └── src/
│       ├── main.rs         ← thin entry → lib::run()
│       └── lib.rs          ← menu bar, tray, plugins
```

## Bootstrap

```bash
# from repo root
cd web && pnpm build           # one-time, also done by tauri build
cd ../apps/desktop
pnpm install
pnpm tauri dev                 # dev: opens a window pointing at vite
pnpm tauri build               # signed installers per the active platform
```

## Distribution

- macOS: `.dmg` (universal) — Story 13.1
- Windows: `.msi` / `.exe` — Story 13.2
- Linux: `.AppImage` / `.deb` — Story 13.3

The `updater` plugin endpoint in `tauri.conf.json` points at the
GitHub-Pages-published `latest.json`; signing key landing is a Story 13.8
follow-up.

## Out of scope for the scaffold

- Bundle icons (placeholder paths in tauri.conf.json point at empty
  files — `cargo tauri icon ./logo.png` regenerates them once the
  artwork lands)
- Apple Developer ID / Microsoft EV cert / Linux signing key
- mDNS discovery handler (Story 13.5)
