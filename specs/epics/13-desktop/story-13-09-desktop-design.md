# Story 13.9 — Design: Maktaba's own look on the desktop, as reviewed mockups

> Epic 13 · Design first · **Approved 2026-11-24** · Depends on: Epic 17 (tokens), Epic 60
> story 60.01 part B (the direction, Maktaba Display) · Gates: every story that changes a
> desktop surface's look (the build stories below)

## Why

Maktaba's desktop apps show the operating system's stock parts wherever the app's own pages
stop: the title bar, the menu bar, error message boxes, the tray menu and every installer.
The apps are to look like one designed Maktaba product instead, in English and Arabic, while
keeping what people expect of their platform. The look has to be decided and approved before
any app changes, or each app invents its own.

**The audit** (`docs/design/desktop/README.md`, "Audit", with three audit sheets of today's
reference images) found four desktop surfaces:

| Surface | Built with | Designed today | Stock today |
|---|---|---|---|
| Maktaba app, the server (Mac, Windows, Linux), `apps/desktop` | Tauri 2: WKWebView, WebView2, webkit2gtk; the window is the web app | the window's content (the web app), dark from the first frame | the OS title bar under the web app's own header; a native menu bar with English-only titles; the system's error message box; the tray's plain menu; the setup as a card inside the OS frame; NSIS, WiX, dmg, AppImage, deb and rpm with no branding |
| Maktaba Player for Windows and Linux, `apps/desktop-player` | Compose for Desktop 1.12.2 (Material 3, Swing host), JavaFX video | the content: Ink & Brass, Plex Sans Arabic and Amiri, RTL | the OS title bar; a Swing menu bar (Account, Big screen, Language, Help); Material's default dialogs; jpackage's stock WiX `.msi`; colours hand-copied, not from the token build |
| Maktaba Player for Mac, `apps/apple` | SwiftUI and AppKit | the content, on the generated tokens; snapshot tests | the split view's grey selection; system list rows in Profile; in Arabic the sidebar and window controls stay at the left |
| Tray and menu bar | the server app's Tauri tray (StatusNotifier on Linux) | the icon's status dot (Windows, Linux) | the menu itself on every platform |

No capture of a desktop frame, menu bar, tray, dialog or installer exists in the repository or
in CI; only the Mac Player's pages and the Compose Player's content are captured.

## Description

The deliverable is **a design document and high-fidelity mockups in the repo**
(`docs/design/desktop/`), reviewed and approved before any build story opens.

**The direction.** maktaba.tv's part B and the apps' Ink & Brass, carried into the window
itself: the window ground is ink (`#0B111C`); the title bar and the navigation rail share one
chrome colour (`#0E1624`, Mica's tint on Windows 11); pages sit in a panel with one rounded
start corner; brass marks only what matters (the one primary action, the current place, focus,
progress). Maktaba Display (Amiri Bold) sets page and window titles, Maktaba Text (IBM Plex
Sans Arabic, 400 and 600) everything else; Arabic is one step larger and looser. One icon style
(24-unit grid, 1.75 stroke, round). The arch from the app icon is the one decoration, on the
setup's and the installer's brand pane.

**What stays the platform's.** Window controls where and how each platform draws them (Windows'
caption buttons with Snap Layouts, GNOME's header-bar button order, the Mac's traffic lights),
the Mac's own menu bar and vibrancy, Mica on Windows 11 with a flat fallback on Windows 10, the
keyboard conventions, and the tray menus a Linux panel draws itself. Mirrored in Arabic as the
platform mirrors them.

**What goes.** Every stock-looking surface on the list above: the doubled title bar, the Swing
and English-only menu bars, system message boxes, plain tray menus as the main way in, and
unbranded installers.

**Mockups** (`docs/design/desktop/mockups/`, HTML with the real tokens
`web/packages/website/design/site-tokens.css` and the real self-hosted fonts, rendered by
`render.mjs` to PNG at each window's size, dark, English and Arabic):

| Mockup | Surface | Window |
|---|---|---|
| `foundations` | colour, type, controls, the three title bars, a menu | 1280 × 760 |
| `player-library`, `player-item`, `player-watch`, `player-settings` | the Player on Windows: a library, a video, watching, settings | 1280 × 800 |
| `setup-welcome`, `setup-check` | the server app's setup, its own window | 960 × 640 |
| `server-status` | the server app's status window | 960 × 640 |
| `dialog` | an error, in Maktaba's own dialog instead of the system's | 560 × 300 |
| `tray` | the tray flyout on Windows 11 | 960 × 640 (with the taskbar) |
| `installer-welcome`, `installer-progress` | the server app's `-setup.exe` | 720 × 460 |
| `installer-msi` | the Player's `.msi`, within what Windows Installer allows | 493 × 356 |
| `linux-player`, `linux-status`, `linux-tray` | the Linux equivalents (GNOME) | 1280 × 800, 960 × 640 |
| `mac-player`, `mac-menubar` | the Mac Player's Home; the server app's menu-bar panel | 1200 × 780, 960 × 640 |
| `audit-player`, `audit-server`, `audit-mac` | today's references, English | 1280 × 900 |

All copy is the apps' own strings where they exist (`*Strings.kt`, `lang.rs`, the web app's
`setup.*`) and the demo library's titles (`tests/fixtures/demo-library/`), never lorem ipsum.

**The technical approach per app, with effort and risk**, and **the quality bar** are in
`docs/design/desktop/README.md` ("How each app gets there", "Quality bar"); the decisions
they needed are D1–D5, below.

**Review.** The pull request lists every image. The owner approves the design (or asks for
changes) and the approval is recorded in `docs/design/desktop/README.md`, "Review", at a commit.
No build story opens its pull request before that.

## Decisions (approved 2026-11-24)

| | Decision |
|---|---|
| D1 | Windows frame: our own title bar, with Snap Layouts kept |
| D2 | Tray: a designed panel on left click, the native menu on right click |
| D3 | Installers: the branded two-page NSIS, themed `.msi`s and the designed `.dmg` background |
| D4 | The Player stays on Kotlin and Compose, on the JetBrains Runtime, and replaces JavaFX's media stack with libmpv through JNA, so MKV, HEVC, AC-3 and 5.1 play as they are (13.18). Rust, Java and .NET were weighed; Kotlin keeps `:client` shared with the Android apps |
| D5 | Mac: switching the language relaunches the Player, after a prompt in English and Arabic |

## Build stories (gated by this one)

| # | Story | App | Size |
|---|---|---|---|
| 13.10 | The server app's frame: own title bar (Windows, Linux), overlay title bar (Mac), no doubled header, the menu bar moved into the title bar's menu on Windows and Linux, localized on the Mac | `apps/desktop`, `web/` | M |
| 13.11 | Maktaba's own dialogs for the shell's errors and questions, the system's kept only as the fallback | `apps/desktop` | S |
| 13.12 | The tray flyout (Windows, Mac), the reordered native menu (all, and Linux's only menu) | `apps/desktop` | M |
| 13.13 | Setup and status as their own windows, restyled to the mockups | `web/`, `apps/desktop` | M |
| 13.14 | Installers: a two-page branded NSIS, themed `.msi` (Arabic too), a dmg with its background | `apps/desktop`, `apps/desktop-player`, `tools/release` | M |
| 13.15 | The Player's chrome on Windows and Linux: decorated window with its own title bar, no Swing menu bar, colours and type from the token build, one dialog style | `apps/desktop-player`, `web/design-system/build` | M |
| 13.16 | The Mac Player: brass selection, the apps' row style in Profile, the sidebar on the right in Arabic | `apps/apple` | S |
| 13.17 | Desktop visual references: the quality bar's captures and checks in CI | CI | M |
| 13.18 | [The Player plays with libmpv](story-13-18-player-libmpv.md): direct play of MKV, HEVC, AC-3 and 5.1, reported to the home; bundled per OS; a time-to-first-frame test | `apps/desktop-player`, `streaming/` | L |

Order: 13.10, 13.12, 13.11, 13.15, 13.18 (on 13.15's runtime), 13.13, 13.14, 13.16; 13.17
beside the first, so each lands with its capture.

## Acceptance criteria

- **Given** this story's pull request, **then** `docs/design/desktop/` holds the audit (with the
  three audit sheets), the direction, the desktop token group, the component specs, the
  technical approach per app with effort and risk, the quality bar, and mockups of every
  surface in the table above in English and Arabic, rendered from HTML with the real tokens
  and fonts.
- **Given** any mockup, **then** its Arabic render uses Arabic copy, mirrors as its platform
  mirrors (layout, window controls), keeps media timelines and transport left to right, and no
  PNG is over 1 MB.
- **Given** the owner's review, **then** `docs/design/desktop/README.md` records the outcome at
  a commit, with D1–D5 answered, before any of 13.10–13.18 opens a pull request (done:
  approved 2026-11-24 at `bead7a18c`).
- **Given** a build story, **then** its pull request shows its surface's capture beside the
  mockup, and the lead records each difference as fixed or accepted.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | script | `docs/design/desktop/mockups/` | `node docs/design/desktop/render.mjs` | one PNG per mockup and language (audit sheets English only); none over 1 MB; no failed request, no missing copy key, no sideways overflow |
| T02 | unit | the colour pairs in the README | contrast check | every text pair ≥ 4.5:1, every control edge and focus ring ≥ 3:1 on the surfaces it is drawn on |
| T03 | lint | `docs/design/desktop/**/*.css` and the mockups' styles | scan | logical properties only: no `left`, `right`, `margin-left` and the like |
| T04 | review | every PNG | the owner | approval recorded in the README before 13.10–13.18 open |

## Edge cases

- **Windows 10** has no Mica and square corners: the chrome is the flat chrome colour, and the
  mockups' 8 px corners become the system's.
- **Transparency off, high contrast on:** Mica falls back to the flat colour; `forced-colors`
  gives the system colours to text, edges and focus.
- **A Linux panel with no tray** (GNOME without AppIndicator): the status window is the way in,
  and the app says so once.
- **Long Arabic labels** in buttons and menus wrap or widen the menu; they never truncate. The
  tray flyout stacks its two quit buttons.
- **Latin inside Arabic** (a machine name, a path, a version): isolated (`<bdi>`, LTR runs), so
  punctuation lands on the right side.
- **WebView2 missing or broken** on Windows: the shell's own dialogs cannot draw, so the system
  message box stays as the fallback for that case only.

## Build plan

| File | Change |
|---|---|
| `docs/design/desktop/README.md` | the audit, the direction, tokens, components, the approach per app, the quality bar, the decisions, the review record |
| `docs/design/desktop/desktop.css` | the mockups' shared look, on the real tokens and fonts |
| `docs/design/desktop/mockups/*.html`, `kit.js`, `audit.css` | the mockups and audit sheets, both languages in one source |
| `docs/design/desktop/mockups/png/*.png` | their renders |
| `docs/design/desktop/render.mjs` | renders and checks them (T01) |
| `specs/epics/13-desktop/README.md` | this story in the table |
