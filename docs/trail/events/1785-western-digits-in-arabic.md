---
schema: 1
kind: pr
pr: 1785
title: Western digits in Arabic across clients
date: 2026-11-26
session: 01a21956-bf2a-7aa2-b815-9dc4f89b27ee
agent: codex
---

## What happened
- [PR #1785](https://github.com/Hamza-Labs-Core/Maktaba/pull/1785) merged to main as `c2a654b0c1971f5bd08f40c9d09a8c635cb3bf86`, closing [#1199](https://github.com/Hamza-Labs-Core/Maktaba/issues/1199).
- Arabic UI formatting uses Western digits (0–9). Web routes localized numbers and dates, including admin timestamps, through `intlLocale` and updates `ar.json`. Apple applies `AppLanguage.westernDigits` in `MaktabaThemeModifier` and its formatters.
- Android Phone, Remote, TV, and Fire TV use `WesternDigits.kt` and `WesternDigitsScope`, including the Media3 `PlayerView` path. The TV player demo uses a Curiosity Arabic subtitle cue, and 13 Roborazzi screenshots were refreshed.

## What we learned
- Set the BCP 47 numbering system structurally via `-u-nu-latn` and keep safe fallbacks for unsupported or malformed locale inputs. Android must preserve the separate `DISPLAY` and `FORMAT` locale categories and handle legacy variants when applying the numbering system.
