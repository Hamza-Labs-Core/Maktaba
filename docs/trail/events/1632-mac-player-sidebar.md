---
schema: 1
kind: pr
pr: 1632
title: "Story 13.16: the Mac Player's brass sidebar, Profile rows, and the sidebar on the right in Arabic"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The Mac row of the approved desktop design (13.16), with decision D5, which the README's per-app table gives to this story.
- **Sidebar:** the Mac's sections are a split view with the Player's own sidebar (`MacSidebar`) in place of `TabView`'s stock one. It has brass symbols, puts the chosen section on the `chosen` fill, and keeps Profile at the bottom.
  - Drag a row to reorder it, or hide it from its menu (`SidebarArrangement`, in the defaults); ⌘1 to ⌘5 follow the order.
  - ↑/↓ move through it, and VoiceOver names it "Sections".
- **Profile:** the apps' row style on the Mac.
- **Language (D5):** Profile's Language picks English, العربية or System in the Player. It asks in a dialog where each line carries its own language and direction, then stops playback (waiting for the last position to reach the home) and starts a new instance.
- **Layout at launch:** the Player sets AppKit's layout direction from its starting language before any window opens.
- **A failed restart:** the defaults go back exactly as they were, and a two-language message says so.
- **Independent review** of `6c46ce67b`, fixed in `b27940cfb`:
  - state lost on every section switch;
  - reorder and hide gone on the Mac;
  - `AppleTextDirection` outliving Arabic, and no System choice;
  - a silent, sticky failed restart;
  - no keyboard focus or VoiceOver name on the sidebar;
  - missing snapshot references, now taken from CI with `tools/apple/snapshots.py`.

## What we learned
- **AppKit does not mirror an Arabic app by itself.** A throwaway bundle localized in Arabic and started with `-AppleLanguages (ar)` reported `userInterfaceLayoutDirection` left to right. `AppleTextDirection = YES` flips it, and writing that key after `NSApplication.shared` exists still applies to the same launch, because the direction is read lazily. CI's Arabic Mac captures stayed left to right for this reason.
- **The split view does follow the view's direction.** A `NavigationSplitView`'s sidebar is an `AXOutline` that moves to the right when the hosting view is right to left. A SwiftUI `List` with `.sidebar` style keeps the `outlines.firstMatch` the UI tests use.
- **Replacing `TabView` loses its per-tab life.** A split view's detail builds only the chosen section, so the state has to live in the shell (models, stacks) rather than in hidden views. A request counter such as ⌘F's has to be consumed, or a rebuilt screen replays it.
- **Some APIs are missing from macOS.** The macOS SDK has no speech-language attribute for `AttributedString`, so use `languageIdentifier` and the line's locale.
- **The hosted Mac tests cannot run in this agent session.** `xcodebuild test` on the app-hosted bundle hangs with "the test runner hung before establishing connection", MaktabaKit's `xcodebuild test` too. `swift test`, `build-for-testing` and off-screen `swiftc` probes work.
- **The worktree guard refuses some commands.** Long heredocs and compound commands with Arabic text or `git` are refused, and one such Python edit silently did not run. Only `check-string-catalogs.py --stringsdata` caught the missing Arabic.
- **A `NavigationSplitView` inside another's detail insets its own detail by the outer sidebar's width again**: Libraries' page was 160 points of a 1024-point window. No test saw it; CI's product shots (`product-shots-mac`) did. On the Mac the list and the page are now set side by side, and a test holds the page's width.
- **A task group is no time limit**: it waits for every child, so one that does not hear cancellation holds it. A timer and the work race outside one.
- **A view's `.locale` does not reach words a package reads for the app's language**: an Arabic picture is taken in a host started in Arabic (`-testLanguage ar`).

## Keep doing
- Probe AppKit behaviour with a tiny off-screen bundle before building on an assumption about it.
- Check the catalogs with `--stringsdata` after every build; it finds what an editor's catalog view would.
- Write scripts to files in the scratchpad and run them with plain arguments.

## Stop doing
- Replacing a container view (`TabView`) without listing the state it kept alive.
- Combining file writes and `git` in one shell command in an isolated worktree.

## Open
- The four `PageSnapshots` references (`sidebar-{en,ar}`, `profile-{en,ar}`) come from this PR's first CI run.
- Check the Arabic product shot (`mac-library-ar`) is mirrored now that only the launch logic sets the layout.
- Search's Results/AI tab, scroll positions and the open Profile sub-page are still lost on a section switch.
- The sidebar has no "Pinned" group yet: the Player has no pinned-libraries data for it.
