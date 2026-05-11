# Implementation Plan — Story 25.27 macOS installer

> Companion to [story-25-27-macos-installer.md](story-25-27-macos-installer.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Bundle | `desktop/macos/Maktaba/` Xcode project; SwiftUI shell `MaktabaApp.swift` supervising Go subprocesses. |
| Subprocesses | `api`, `streaming`, `pipeline-launcher`, plus `maktaba-cloudlink` (25.7). |
| Python venv | `uv`-built; copied into `Contents/Resources/python/`. |
| FFmpeg | Static build embedded at `Contents/Resources/ffmpeg`. |
| Auto-start | LaunchAgent `app.maktaba.server.plist` at `~/Library/LaunchAgents/`. |
| Updater | Sparkle 2; appcast at `https://releases.maktaba.app/macos/appcast.xml` (EdDSA-signed). |
| Distribution | DMG (drag-to-Applications) + Homebrew cask. |
| Signing | Apple Developer Team `HAMZALABS_TEAM_ID`; hardened runtime + notarization. |
| Out of scope | Mac App Store (sandbox conflicts). |

## 1. Xcode project structure

```
desktop/macos/Maktaba/
  Maktaba.xcodeproj
  Maktaba/
    MaktabaApp.swift                      # @main
    Supervisor.swift                      # spawns + monitors subprocesses
    MenuBarController.swift
    PreferencesView.swift
    OnboardingHandoff.swift               # hands web URL to default browser
    SparkleHandler.swift
    Helpers/
      Bookmarks.swift                     # security-scoped bookmarks
      PortFinder.swift                    # 8080 → 8081 fallback
    Resources/
      ServerBinaries/
        api
        streaming
        pipeline-launcher
        maktaba-cloudlink
        python/                           # full venv
        ffmpeg
      Info.plist
      Maktaba.entitlements
  scripts/
    build.sh                              # one-shot: vendor binaries, codesign, package, notarize
    sign-binaries.sh
    notarize.sh
    appcast-update.sh
```

## 2. Supervisor

```swift
final class Supervisor {
    var processes: [String: Process] = [:]
    func startAll() throws {
        let dataDir = appSupport.appendingPathComponent("Maktaba")
        try ensureDir(dataDir)
        try spawn(name: "api",     args: ["serve", "--config", config])
        try spawn(name: "streaming", args: ["serve", "--config", config])
        try spawn(name: "pipeline-launcher", args: ["serve", "--config", config])
        try spawn(name: "maktaba-cloudlink", args: ["serve", "--config", config])
    }
    func spawn(name: String, args: [String]) throws {
        let p = Process()
        p.executableURL = bundleBinary(name)
        p.arguments = args
        p.environment = baseEnv()
        p.terminationHandler = { [weak self] _ in self?.handleExit(name) }
        try p.run()
        processes[name] = p
    }
    func handleExit(_ name: String) {
        // Restart with backoff; 5 fails in 60s → red menubar + offer Quit.
    }
}
```

## 3. Code signing

`scripts/sign-binaries.sh`:

```bash
set -e
APP=build/Maktaba.app
codesign --force --options runtime --timestamp --deep \
    --entitlements desktop/macos/Maktaba/Maktaba/Resources/Maktaba.entitlements \
    --sign "Developer ID Application: HamzaLabs ($TEAM_ID)" \
    "$APP/Contents/Resources/ServerBinaries/api" \
    "$APP/Contents/Resources/ServerBinaries/streaming" \
    "$APP/Contents/Resources/ServerBinaries/pipeline-launcher" \
    "$APP/Contents/Resources/ServerBinaries/maktaba-cloudlink" \
    "$APP/Contents/Resources/ServerBinaries/ffmpeg" \
    "$APP/Contents/Resources/ServerBinaries/python/bin/python3"
# Sign the bundle last
codesign --force --options runtime --timestamp \
    --entitlements desktop/macos/Maktaba/Maktaba/Resources/Maktaba.entitlements \
    --sign "Developer ID Application: HamzaLabs ($TEAM_ID)" \
    "$APP"
```

`Maktaba.entitlements`:

```xml
<dict>
  <key>com.apple.security.cs.allow-jit</key><true/>
  <key>com.apple.security.cs.disable-library-validation</key><true/>
  <key>com.apple.security.network.server</key><true/>
  <key>com.apple.security.network.client</key><true/>
  <key>com.apple.security.files.user-selected.read-write</key><true/>
  <key>com.apple.security.files.bookmarks.app-scope</key><true/>
</dict>
```

`scripts/notarize.sh`:

```bash
xcrun notarytool submit "$DMG" \
    --apple-id "$NOTARY_USER" --team-id "$TEAM_ID" --password "$APP_PW" --wait
xcrun stapler staple "$DMG"
```

## 4. LaunchAgent

`~/Library/LaunchAgents/app.maktaba.server.plist`:

```xml
<plist version="1.0">
<dict>
  <key>Label</key><string>app.maktaba.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Applications/Maktaba.app/Contents/MacOS/Maktaba</string>
    <string>--background</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
</dict>
</plist>
```

Installed on first launch via `loadLaunchAgent()` helper. Removed on uninstall.

## 5. Sparkle 2

`appcast.xml` published at `https://releases.maktaba.app/macos/appcast.xml`; EdDSA key public-bundled in app. Sparkle reads, validates, applies on quit.

The app respects `--update-channel beta` flag (also written to UserDefaults) to swap appcast URL.

## 6. Homebrew cask

`homebrew-maktaba/Casks/maktaba.rb`:

```ruby
cask "maktaba" do
  version "1.0.0"
  sha256 "..."
  url "https://releases.maktaba.app/macos/Maktaba-#{version}.dmg"
  name "Maktaba"
  homepage "https://maktaba.app"
  app "Maktaba.app"
  zap trash: [
    "~/Library/Application Support/Maktaba",
    "~/Library/LaunchAgents/app.maktaba.server.plist",
    "~/Library/Logs/Maktaba",
  ]
end
```

## 7. First-launch UX

1. Gatekeeper accepts (notarized + stapled).
2. Menubar icon mints; Supervisor starts subprocesses.
3. Hardware probe (25.35) runs; wizard opens at `http://localhost:8080/setup`.
4. User picks library folder → security-scoped bookmark stored at `~/Library/Application Support/Maktaba/bookmarks/<sha>.data`.

## 8. Rollback path

On launch, if subprocesses fail to come up 3× in 60s, restore previous binary set from `~/Library/Application Support/Maktaba/.previous/` (preserved by Sparkle's pre-update hook) and log the failure.

## 9. Test plan

### 9.1 Manual (CI smoke records to runbook)

| Test | Pins |
|---|---|
| Apple Silicon DMG install | Gatekeeper accepts; menubar appears <5s. |
| Intel Mac DMG install | Same. |
| Reboot with autostart | LaunchAgent fires; healthz 200 in <10s. |
| Brew cask install | Same as DMG. |
| Sparkle update | Update dialog → apply → relaunch new version. |
| Privacy prompts | Only Documents prompt fires on first scan. |
| Multi-user Mac | Two macOS users → two servers on different ports. |
| Sparkle rollback | Inject post-update health failure → previous restored. |

### 9.2 Unit

| Test | Pins |
|---|---|
| `TestAppcastEdDSASignature` | Tampered → reject. |
| `TestPortFinderFallback` | 8080 taken → 8081 chosen. |
| `TestBookmarksResolveAfterReboot` | Saved bookmark survives reboot. |
| `TestSemverCompare` | 1.10.0 > 1.9.9. |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Quarantine bit | Notarization removes warning. | Spec. |
| App Translocation | Prompt user to move to /Applications on first run. | UX. |
| MLX vs CPU | Architecture-based selection at first run. | Cross-25.32. |
| External media volumes | Security-scoped bookmarks. | Spec. |
| Time Machine excludes | tmutil + xattr on cache+DB. | Implementation. |
| macOS Sequoia App Management | Bundle is self-contained; no other-app modification. | Spec. |
| Multi-user host | Per-user LaunchAgent + ports. | Spec. |
| Keychain elevation | Keys in Application Support; no keychain. | Spec. |
| Notarization outage | Releases pause; documented. | Doc. |
| Beta channel | Separate appcast URL. | Implementation. |

## 11. Dependencies

- Local API/Streaming/Pipeline binaries (Epics 07/08/03).
- 25.34 (appcast publishing).
- 25.35 (first-run wizard hosted by API).

## 12. Acceptance checklist

- [ ] Notarized signed DMG.
- [ ] Homebrew cask installs identical bits.
- [ ] LaunchAgent autostarts.
- [ ] Sparkle 2 EdDSA appcast wired.
- [ ] Rollback to `.previous` on health failure.
- [ ] Tests in §9 pass.
