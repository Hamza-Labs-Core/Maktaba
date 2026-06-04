# Bundle icons

`tauri.conf.json > bundle.icon` references the icon set below. They are
**not yet committed** — real artwork is a follow-up. The Tauri build
requires them, so generate the full set from a single square source PNG
(≥ 1024×1024, transparent) before the first release build:

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
