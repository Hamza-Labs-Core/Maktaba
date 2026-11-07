---
schema: 1
kind: pr
pr: 717
title: Plugins part 4, the Plugins page, settings as data and the SDK (Epic 54, 54.10-54.13)
date: 2026-11-06
session: 08ec1b23-2735-4eb1-952d-c4e10425aeba
prs: [707]
---

## What happened
- Last PR of Epic 54 Phase 1: Settings → Plugins (cards, a plugin's page with permissions, status, settings, what it wrote, versions, turn off and remove), install from a file with the typed name and the password to allow unreviewed plugins, settings checked by `shared/plugins/pluginsettings` at install and on save, the Queues page's Plugins lane, plugin names on the Components page, and `mkp` (new, check, test on plugind-core with fixtures, pack). With #707 an admin installs a plugin from a file on a Postgres home, grants it, turns it on, its jobs run and show in the queue, its health and logs are on its page, and it is turned off and removed; `tests/components/test_plugins_live.py` runs that path with the hello guest.

## What we learned
- A browser cannot read a `.mkplugin` before upload (zstd tar), so the typed-name confirmation is a second upload after the server names the plugin (`plugin_name_mismatch` carries `name` and `name_ar`).
- Testing Library's accessible names keep the bidi isolates (`U+2068 … U+2069`) that `bidiIsolate` adds: match a plugin's name with a regular expression, not a string. The design system's Toggle is `role="switch"`.
- `rustup`'s cargo builds the template's guest with `--target wasm32-wasip2`; Homebrew's has no such target (#683's note holds for the SDK too).
- Switching branches in the same worktree while a background test run reads it is a race: run the long suites before switching, or in a separate worktree.

## Open
- #718 (TypeScript, Python and Go templates), #719 (signing, `mkp run`, developer mode, the `maktaba-server plugins` CLI).
