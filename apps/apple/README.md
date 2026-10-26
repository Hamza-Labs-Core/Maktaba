# `apps/apple/` — the native Apple apps (Epic 42)

SwiftUI apps that talk to a home's REST API, like the TV apps.

| Scheme | Platform | Bundle ID | What it is |
|---|---|---|---|
| Maktaba Player | iPhone (iPhone Duo included) | `com.hamzalabs.maktaba.player` | browse and watch |
| Maktaba Player iPad | iPad | `com.hamzalabs.maktaba.player.tablet` | browse and watch |
| Maktaba Player Mac | macOS 15+, Apple silicon | `com.hamzalabs.maktaba.player.mac` | browse and watch |
| Maktaba Remote | iPhone (iPhone Duo included) | `com.hamzalabs.maktaba.remote` | run a home (its admins only): needs attention, the queue, libraries, machines, the home's state |
| Maktaba Remote iPad | iPad | `com.hamzalabs.maktaba.remote.tablet` | the same, on iPad |
| Maktaba TV | Apple TV (tvOS 26) | `com.hamzalabs.maktaba.tv` | browse and watch on the television; sources in [`apps/tv/tvos`](../tv/tvos/) |

`MaktabaKit/` is the Swift package they share: the API client (Bearer tokens, one refresh on
a 401, `X-Maktaba-Client: native`), stream sessions (signed URLs; `StreamProxy` hands every `/stream/` request to
the home with a fresh token), the Keychain session per home, the home picker and sign-in screens, and the admin
calls the Remote makes (`Admin.swift`).

## What each app does

- **Player** (iPhone, iPad, Mac): libraries → videos → a video's page → play. Playback goes
  through `StreamProxy` (every `/stream/` request carries a fresh Bearer token). The layout is
  one `NavigationSplitView`: three columns on iPad and the Mac, one on an iPhone or an iPad in
  a narrow Split View / Slide Over. On the Mac the video fills the window; the **Playback**
  menu (also an iPad's menu bar with a keyboard) has space for play/pause, ← → to skip 10 s,
  ⌘. to stop, Esc to close; the **Account** menu has the theme, Switch Home (⇧⌘H), Sign Out.
- **Remote** (iPhone, iPad; home admins only): tabs on iPhone, a sidebar on iPad.
  *Needs attention* (retry a group, retry all), *Queue* (every stage; pause/resume one by a
  swipe or its page, pause/resume all; a stage's failures), *Libraries* (scan), *Machines*
  (let machines ask to join, approve one with the six-digit code it shows, turn one down,
  revoke an approved one), *Home* (name, release, queue state, machines online). The home
  enforces every one of these for its admins (403 to anyone else); a non-admin who signs in
  sees "For the home's admins".

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
keychain).

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
