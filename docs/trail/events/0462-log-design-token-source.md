---
schema: 1
kind: pr
pr: 462
title: "48.2: one design token source"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`tokens.json` / `tokens.dark.json` now hold the shipped Ink & Brass values (copied from `web/src/styles/tokens.css`, which stays hand-written and is what the web ships), plus `accent-fg`, `accent-text`, `accent-soft`, `border`, `running`, `info`, `type.size.*`, `spacing.5`, and the apps-only `raised`, `nav`, `scrim`, `chosen`.
`build-tokens.mjs` also writes `MaktabaKit/Tokens.swift` (`MaktabaTokens.Colors.*` as 0xRRGGBBAA light/dark pairs with a dynamic SwiftUI `.color`) and core's `ui/Tokens.kt` (`MaktabaTokens.Light/Dark`, Compose `Color`/`dp`/`sp`).
CI's unit job runs `make test-tokens`: a `--check` stale test, CSS parity, contrast on 70 text/surface pairs, and self-tests that each check catches a bad input.

## What we learned
- Decided: generate, not replace, `tokens.css`. Its glass/art/shadow values are web-only and a swap would touch every screen.
- Light `raised` is `#FFFDF8`, because `#F8EFDB` put light `running` text at 4.38:1.
- `scrim` is not contrast-checked: its text sits on the library art, which 48.10 / 48.11 check in screenshots.
- A MaktabaKit test suite named `Tokens` clashes with an existing one (keychain tests); it is `DesignTokens`.
