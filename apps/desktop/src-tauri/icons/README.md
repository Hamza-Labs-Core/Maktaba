# Bundle icons

`tauri.conf.json > bundle.icon` references the icon set in this directory. The committed set
is a **placeholder**: the web app's mark (a #1E5AD8 rounded square, `web/public/manifest.webmanifest`)
at 1024×1024, so every desktop build works. Real artwork is a follow-up; regenerate the
set from a square source PNG (≥ 1024×1024, transparent) and keep these five files:

```bash
cd apps/desktop
pnpm exec tauri icon /path/to/logo.png -o /tmp/icons
cp /tmp/icons/{32x32.png,128x128.png,128x128@2x.png,icon.icns,icon.ico} src-tauri/icons/
```

| File | Platform |
|---|---|
| `32x32.png`, `128x128.png`, `128x128@2x.png` | Linux / general |
| `icon.icns` | macOS |
| `icon.ico` | Windows |
