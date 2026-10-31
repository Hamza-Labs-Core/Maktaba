# `apps/apple/` — the native Apple apps (Epic 42)

SwiftUI apps that talk to a home's REST API, like the TV apps.

| Scheme | Platform | Bundle ID | What it is |
|---|---|---|---|
| Maktaba Player | iPhone (iPhone Duo included) | `com.hamzalabs.maktaba.player` | browse and watch |
| Maktaba Player iPad | iPad | `com.hamzalabs.maktaba.player.tablet` | browse and watch |
| Maktaba Player Mac | macOS 15+, Apple silicon | `com.hamzalabs.maktaba.player.mac` | browse and watch |
| Maktaba Remote | iPhone (iPhone Duo included) | `com.hamzalabs.maktaba.remote` | run a home (its admins only): needs attention, the queue and its running jobs, libraries, machines, the home's state, its updates and processes |
| Maktaba Remote iPad | iPad | `com.hamzalabs.maktaba.remote.tablet` | the same, on iPad |
| Maktaba TV | Apple TV (tvOS 26) | `com.hamzalabs.maktaba.tv` | browse and watch on the television; sources in [`apps/tv/tvos`](../tv/tvos/) |

`MaktabaKit/` is the Swift package they share: the API client (Bearer tokens, one refresh on
a 401, `X-Maktaba-Client: native`), stream sessions (signed URLs; `StreamProxy` hands every `/stream/` request to
the home with a fresh token), the Keychain session per home, the home picker and sign-in screens, and the admin
calls the Remote makes (`Admin.swift`).

## What each app does

- **Starting** (Player and Remote, from `MaktabaKit`): the home picker lists the homes *on this
  network* (Bonjour `_maktaba._tcp`, one row per home `id`), the *recent homes* (rename and
  remove from a row's menu or swipe), a home *by address* (also takes a `maktaba://connect?…`
  link, the Mac's way to "scan") and, on iPhone and iPad, *Scan a QR code* (VisionKit; the
  camera is asked for then, and listed as optional in the permissions walkthrough). A typed or
  scanned address must answer `/api/discovery/home` (or `/api/system/version` on an older
  home); a link's addresses are tried in order. Opening a `maktaba://connect` link from
  outside the app asks the person first. A home's tokens stay with the exact address
  (origin) they were issued at: the same home id announced at another address is another
  entry, signed in to afresh, and a redirect to another origin is followed without the
  token (`Redirects.swift`; a request with a password, refresh token or assertion in its body
  does not follow it at all). **Through Maktaba Cloud** (story 42.7, `Cloud.swift`,
  `CloudViews.swift`): sign in to the cloud (email, password; its API address under
  *Advanced*, `https://api.maktaba.app` by default, remembered in the defaults), see the homes
  the account reaches (online or not; one not linked is shown with how to link it, never
  offered), pick a linked one: the app reaches it at the cloud's `direct_url` or `relay_url`,
  asks the cloud for a one-use assertion and trades it at that address (`POST
  /api/auth/cloud`) for the home's own tokens, kept as after a password sign-in. The cloud's
  token is kept in memory only (signed in to the cloud again after the app quits). A home
  keeps all its addresses (LAN and relay) and uses whichever answers; another address is
  signed in to afresh, silently with a new assertion when the person is signed in to the
  cloud and the cloud itself gives that address for the home (an address the home announced
  is never handed an assertion), else on the sign-in screen. Signing in: a username and password, or **with a code** the app
  shows (large, with a QR code) until a signed-in device approves it. Every account menu has
  **Approve a sign-in**: type or scan another device's code, see what asks, approve it.
  English and Arabic (right to left) after the person's first language.
- **Player** (iPhone, iPad, Mac): libraries → videos → a video's page → play. Playback goes
  through `StreamProxy` (every `/stream/` request carries a fresh Bearer token). The layout is
  one `NavigationSplitView`: three columns on iPad and the Mac, one on an iPhone or an iPad in
  a narrow Split View / Slide Over. On the Mac the video fills the window; the **Playback**
  menu (also an iPad's menu bar with a keyboard) has space for play/pause, ← → to skip 10 s,
  ⌘. to stop, Esc to close; the **Account** menu has the theme, Switch Home (⇧⌘H), Sign Out.
- **Remote** (iPhone, iPad; home admins only): tabs on iPhone, a sidebar on iPad.
  *Needs attention* (retry a group, retry all), *Queue* (every stage; pause/resume one by a
  swipe or its page, pause/resume all; a stage's failures; on a wide window, an iPad full
  screen or in a wide Split View, the stages beside the chosen stage's page, story 42.8), *Libraries* (scan), *Machines*
  (let machines ask to join, approve one with the six-digit code it shows, turn one down,
  revoke an approved one), *Home* (name, release, queue state, machines online, and two
  read-only pages: *Updates*, the plan and what holds it, blocked releases, a TV held, the run
  under way or the last with its steps and machines, every machine's release; *Processes*,
  the supervised processes and their recent events). The queue also lists the jobs running
  now and how long each has run. The home
  enforces every one of these for its admins (403 to anyone else); a non-admin who signs in
  sees "For the home's admins".

## English and Arabic

Every app ships in English (the development language) and Arabic, right to left. Each has a
String Catalog (`Remote/`, `Player/` and `../tv/tvos/MaktabaTV/` `Localizable.xcstrings`,
and `InfoPlist.xcstrings` for the permission prompts); MaktabaKit has its own for the words
it makes (`Sources/MaktabaKit/Localizable.xcstrings`, read from `Bundle.module`): the plan's
sentence and the processes' and queue's words, and the home's values (states, steps, codes)
under the web's keys (`updates.state.done`). The Arabic is the web's (`web/src/i18n/ar.json`)
wherever the web says the same thing. A SwiftUI `Text("…")` is localized by itself; a
sentence built in code uses `String(localized:)` (MaktabaKit: `kit("…")`). A new string needs
its Arabic, state *translated*, or CI fails:
`tools/apple/check-string-catalogs.py [--stringsdata <DerivedData>]` (the second form reads
the strings Xcode extracted as it built, `SWIFT_EMIT_LOC_STRINGS`). The connect, sign-in,
permission and error screens keep their own English and Arabic tables in MaktabaKit
(`ConnectStrings`, `PermissionsStrings`, `ErrorStrings`, `TVConnectStrings`).

## Open and run

```bash
cd apps/apple
xcodegen            # makes Maktaba.xcodeproj from project.yml (commit both)
open Maktaba.xcodeproj
```

Pick a scheme and a simulator (the **iPhone Duo** simulator is in Xcode 27.1), then Run.
`swift test` in `MaktabaKit/` runs the kit's tests without a simulator.

## Signing (Xcode does it)

Every target uses **automatic signing** with team `G58UVTKQPB` (Mahmoud Darwish):

1. Xcode → Settings → Accounts: the Apple ID of that team is signed in.
2. Open a target's Signing & Capabilities tab once: Xcode registers its App ID and makes the
   development certificate and profile.
3. Archive (Product → Archive) and distribute from the Organizer: Xcode makes the
   distribution certificate and the App Store or Developer ID profile.

The Mac Player keeps its tokens in the data-protection keychain (the login keychain asks the
person again after every rebuild), which needs the `keychain-access-groups` entitlement and so
a provisioning profile: the Mac you build on must be registered with the team (Xcode offers
"Register Device"; `xcodebuild … -allowProvisioningDeviceRegistration` does it once).
`xcodebuild test -scheme "Maktaba Player Mac"` runs its hosted tests (the player surface, the
keychain). The iPhone and iPad schemes run the UI tests (`UITests/`, against `MockHome` on
127.0.0.1, no real home): `-scheme "Maktaba Remote iPad"` on an iPad simulator covers the
queue's two panes.

From the command line Xcode's account is used with `-allowProvisioningUpdates`:

```bash
xcodebuild -project Maktaba.xcodeproj -scheme "Maktaba Player" -destination generic/platform=iOS \
  -allowProvisioningUpdates archive -archivePath build/Player.xcarchive
```

## iPhone Duo

Built to Apple's "Preparing your app for iPhone Duo":

- `NavigationSplitView` and `NavigationStack` with standard toolbars: they adapt to every
  pose, and the system moves the bars to the vertical side bar when it should (every item
  has an icon and a title).
- The watch screen is an `ArrangementView` with the `.overlay` style (iOS 27.1): controls
  over the video on a flat display; on a half-folded Duo the video on one half and the
  controls on the other.
- Layout from the view's size, never the screen's.

## Xcode and agents

Xcode 27 gives agents its tools through MCP: turn on Xcode → Settings → Intelligence →
"Allow external agents to use Xcode tools", then `claude mcp add --transport stdio xcode --
xcrun mcpbridge`. The project can move to Xcode's JSON project format (`project.xcproj`) from
the File inspector (Project Format: JSON) once everyone is on Xcode 27.
