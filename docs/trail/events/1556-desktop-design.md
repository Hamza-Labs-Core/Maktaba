---
schema: 1
kind: pr
pr: 1556
title: "Story 13.9: the desktop apps' audit and design, mockups in both languages, awaiting the owner"
date: 2026-11-23
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The design gate for the desktop apps opened as #1556. It is design only, and no app changes until the owner approves. It holds story 13.9 in Epic 13 and `docs/design/desktop/`.

The audit:
- **Tauri server app:** the stock parts are the OS title bar above the web app's own header, a native menu bar whose titles are English literals (`src/lib.rs`), the system message box for errors (`src/shell.rs:386`), a plain tray menu, and unbranded NSIS, WiX, dmg, AppImage, deb and rpm installers.
- **Compose Player:** the OS frame, a Swing `JMenuBar`, Material's default dialogs and jpackage's stock `.msi`. Its colours are copied by hand rather than generated from the tokens.
- **SwiftUI Mac Player:** the sidebar's selection is grey, and in Arabic the sidebar and window controls stay on the left (CI run 45267535780, `product-shots-mac`).

The design adds:
- 18 HTML mockups on the real tokens and fonts, which render to 39 PNGs.
- A proposed `desktop` token group with its contrast table.
- The approach for each app, with effort and risk.
- The quality bar, eight proposed build stories (13.10–13.17), and five decisions for the owner (D1–D5).

## What we learned
- No capture of any desktop frame, menu, tray, dialog or installer exists, either in the repository or in any CI artifact. Only the Compose Player's content, the web app and the Mac Player are captured. An audit of the stock look therefore has to rest on the code.
- With Pillow, a 256-colour palette (website `quantize.py`) shifts small saturated areas. The brass and macOS's traffic lights came out wrong. Desktop renders at 1× stay under 1 MB in full colour (0.8 MB at most), so the palette step was dropped.
- In an LTR run, Arabic-Indic digits around a "/" reorder: "٦:٢٠ / ١٤:٤٨" displays reversed. Each number needs its own isolate. Version numbers stay in Latin digits, as `lang.rs` substitutes them.
- A mockup kit that sets copy with `textContent` silently prints `<bdi>` as text. The kit now refuses markup in `data-t`.

## Keep doing
- One HTML source holding both languages (`?lang=ar`), rendered at each window's real size. Render checks run in the same script: missing copy keys, failed requests, sideways overflow, contrast pairs, and logical CSS properties only.
- Verify each audit claim against a file and line before it goes into the sheet. A first draft said "white title bar on Windows 10", which the code cannot show.

## Stop doing
- Quoting a CI or reference image without saying where it came from (run id, artifact, path).

## Open
- The owner's review of #1556 and the decisions D1–D5. The outcome goes in `docs/design/desktop/README.md` under "Review".
- Build stories 13.10–13.17 open only after that approval. Story 13.17 adds the desktop visual references to CI.
