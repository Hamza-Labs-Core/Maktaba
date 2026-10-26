# `apps/apple/` — the native Apple apps (Epic 42)

SwiftUI apps that talk to a home's REST API, like the TV apps.

| Scheme | Platform | Bundle ID | What it is |
|---|---|---|---|
| Maktaba Player | iPhone (iPhone Duo included) | `com.hamzalabs.maktaba.player` | browse and watch |
| Maktaba Player iPad | iPad | `com.hamzalabs.maktaba.player.tablet` | browse and watch |
| Maktaba Player Mac | macOS 15+, Apple silicon | `com.hamzalabs.maktaba.player.mac` | browse and watch |
| Maktaba Remote | iPhone (iPhone Duo included) | `com.hamzalabs.maktaba.remote` | run a home: needs attention, retry (its admins only) |
| Maktaba Remote iPad | iPad | `com.hamzalabs.maktaba.remote.tablet` | the same, on iPad |
| Maktaba TV | Apple TV (tvOS 26) | `com.hamzalabs.maktaba.tv` | browse and watch on the television; sources in [`apps/tv/tvos`](../tv/tvos/) |

`MaktabaKit/` is the Swift package they share: the API client (Bearer tokens, one refresh on
a 401, `X-Maktaba-Client: native`), stream sessions (signed URLs, so the player needs no
header), the Keychain session per home, the home picker and sign-in screens.

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
