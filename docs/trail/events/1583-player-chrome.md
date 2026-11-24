---
schema: 1
kind: pr
pr: 1583
title: "Story 13.15: the Windows and Linux Player draws its own title bar and dialogs, on a pinned JetBrains Runtime, with colours from the token build"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The fourth build story of the approved desktop design (#1556), D4: the Player stays Compose for Desktop on the JetBrains Runtime.
- **Its own title bar:** `shell/PlayerFrame.kt` uses Jewel's `DecoratedWindow` and `TitleBar` (jewel-int-ui-decorated-window 0.42.0-263.7307.353) when the runtime is JBR, with the menu button, icon, name, Big screen and Help; the Swing `MenuBar` is gone. On another JDK the system's frame stays and the same bar is the page's first row. F10 opens the menu.
- **One dialog:** `MaktabaAlertDialog` replaces every `AlertDialog`.
- **JBR in the installers:** `apps/desktop-player/jbr.sh` pins JBR 25.0.4.1 b635.79 per platform through `fetch-pinned.sh`; `-Pmaktaba.jbr` sets compose's `javaHome`, so the runtime is jlinked from JBR while Gradle still runs on Temurin 17. CI and the release's player legs check the runtime, and CI's Linux leg starts the packaged Player under `xvfb-run`.
- **Tokens:** `tokens.json` gains `color.desktop`; `build-tokens.mjs` emits the Player's `Tokens.kt`; `make test-tokens` checks the group's contrast.
- **References:** `FrameReferencesTest` renders the bar, the open menu and a dialog (en, ar) with `runDesktopComposeUiTest`, compared on Linux only; CI wrote the six and they were committed from its artifact.

An independent review of 742dcc01a found one blocking bug and six smaller issues, all fixed in 5e7a34768: the JBR check could never pass; the bar was drawn outside the Player's theme; a tall dialog could push its buttons off a short window; the title bar's insets flipped on an Arabic system locale; the menu had no keyboard route; the launch check rejected a slow stop (137); a frame test failed on a JBR-run Gradle.

CI's start of the packaged Player then threw `UnsupportedClassVersionError` on `JewelTheme` under JBR 21: Jewel 0.42 is Java 25 bytecode. 66fe8ef39 pins JBR 25 and has `decoratedFrameAvailable` load Jewel's classes first, so a runtime too old for them falls back to the system's frame.

## What we learned
- **compose's jlink writes no IMPLEMENTOR** to the runtime's `release` file (only `JAVA_VERSION` and `MODULES`), so "is this JBR" is checked by its Java version (25, where the build JDK is 17).
- **Jewel's major follows IntelliJ's platform, and so does its bytecode**: 0.42 (2026.3) is class file 69, Java 25. Kotlin compiles against it on Java 17 without a word, and tests that never load its classes pass, so only starting the packaged app shows it. Pick the JBR from Jewel's class files, not from what compiles.
- **`ComposeUiTest.captureToImage()` on the desktop test captures the whole scene**, popups included; `onRoot().captureToImage()` fails once a menu or dialog is open (two roots).
- **Material3's `DropdownMenuItem` has a 48 dp minimum height**; `heightIn(min)` does not lower it, a fixed `height` does.
- **Jewel's `DecoratedWindow` sits outside the app's theme**: anything Material in the title bar needs its own `MaterialTheme`, and its layout direction comes from the system's locale unless provided.
- **A Compose test's local `var` is not state**: a test that flips a language must hold it in `mutableStateOf`.

## Keep doing
- An independent review before calling a story done: it found the blocking JBR check before CI got that far (the first run stops at the missing references).
- References written by CI on Linux and committed after looking at each one.
- Starting the packaged app in CI and failing on any exception in its output: it caught the runtime mismatch no test could.

## Stop doing
- Checking a packaged runtime's vendor from its `release` file without first reading what the packager writes there.

## Open
- The decorated window has only been started on Linux under Xvfb; Windows (Snap Layouts on the native buttons, an Arabic system locale) needs a look on a real machine.
- 13.18 (libmpv) builds on this runtime; JavaFX stays in the installers until then.
- The CI run of #1568 at ff6361b8e failed once in `cloud/internal/ratelimit` `TestPGConcurrentTakesExactlyTheBurst` (31 of 90 concurrent requests allowed, want 30), a cloud test this work does not touch; it may be the node bucket's fallback under a busy pool and needs its own look.
