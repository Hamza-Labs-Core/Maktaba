# Bundle icons

`tauri.conf.json > bundle.icon` references the icon set in this directory: the Maktaba
mark (gold on the library navy), generated on Magnific with the owner's account. The files
are cut from one master, `tools/icons/source/maktaba-icon.png`, by
`tools/icons/make-icons.py`, which also writes the web, TV and phone icons. To change the
icon, replace the master and run:

```bash
uv run --no-project --with pillow python tools/icons/make-icons.py
```

| File | Platform |
|---|---|
| `32x32.png`, `128x128.png`, `128x128@2x.png` | Linux / general |
| `icon.icns` | macOS (Apple's grid: an 824 px rounded square with margins on 1024) |
| `icon.ico` | Windows (16–256 px) |
