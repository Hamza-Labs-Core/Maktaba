---
schema: 1
kind: pr
pr: 1450
title: "Story 54.45: Maktaba's plugins as store packages (partial)"
date: 2026-11-19
session: 9aa2417d-57dc-4c99-a4b6-462f5098d816
agent: claude-code
---

## What happened

Story 54.45 shipped partially in #1450 (merge 9e4fdcfbf), on the decision that a Maktaba plugin's statements change only with a server release.

- `shared/plugins/fppackage` holds the content and wheel checks. Both the release's check (`firstparty.VerifyReleasePackages`) and the home's store rule `CheckStore` use it. `CheckStore` trusts a package only when:
  - the store countersigned it;
  - its developer key is pinned in `shared/plugins/maktaba-keys.json` (empty until the key is made);
  - its manifest, strings and statements are exactly the server's;
  - its wheel is the plugin's code in the server's own pipeline wheel.
- Core (`tv.maktaba.core-media`) is not a plugin (54.44) and is no longer packaged, so a release carries 12 packages.
- `build.py --developer-key` and `fp-sign` sign without changing `files.json`.
- The store gained a Maktaba publisher (`tv.maktaba`, kind `maktaba`), `prefixCovers`, trusted-manifest checks in Rust and native-package checks.

Installing a store or file package waits for an install path (54.46/54.49) and Maktaba's key.

GLM-5.3's review found one major: `CheckStore` passed no pipeline RECORD, so a package rebuilt from other source, signed by Maktaba's key, would pass. It now takes the RECORD, which every home has until 54.49, and refuses without it. It also reads every file again against the signed `files.json`, through `pluginsig`'s `Open`. Minors are in #1458; #1456's items 2 and 3 were fixed here.

## What we learned

- The wheel code moved into `fppackage` on a stacked branch while #1440 kept fixing it in `firstparty`. Porting meant applying #1440's diff to the moved file with names rewritten (`git apply --recount` took it cleanly). Unexported test hooks (the wheel limits) could not follow across packages, so their tests moved with the code.
- A check that compares contents but takes a nil reference for the code compares nothing. Make the reference a required argument that refuses when absent.
- GLM-5.3 hit its 100-turn limit on a broad brief. A brief with a tool-call budget, the exact diff command and an ordered list of what to check came back in one run.

## Open

- #1456 item 1: a duplicated JSON key in a package's `statements.json` is not refused, until the reader of a package's statements lands.
- #1458: moving the Maktaba publisher prefix (and its set-up race), the bundle parser's duplicate-id guard, and `CheckStore` taking its member list from the caller.
