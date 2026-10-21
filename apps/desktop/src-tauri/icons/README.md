# Bundle icons

`tauri.conf.json > bundle.icon` references the icon set below. The files
committed here are **placeholders** (a plain square, Epic 36) so the shell
builds and its tests run in CI; real artwork is a follow-up. Replace them
by generating the full set from a single square source PNG
(≥ 1024×1024, transparent):

```bash
cd apps/desktop
pnpm exec tauri icon /path/to/logo.png
```

`tauri icon` writes every size + format the bundlers need into this
directory:

| File | Platform |
|---|---|
| `32x32.png`, `128x128.png`, `128x128@2x.png` | Linux / general |
| `icon.icns` | macOS |
| `icon.ico` | Windows |
| `Square*Logo.png`, `StoreLogo.png` | Windows Store (optional) |

Commit the generated icons — they are intentionally **not** gitignored.
