# TestFlight: the iPhone apps

Every `v*` release tag uploads the native iPhone apps from `apps/apple/` to App Store Connect,
where they appear in TestFlight. The workflow is `.github/workflows/testflight.yml`. It can
also be run by hand from the Actions tab (*TestFlight* → *Run workflow*).

| App | Scheme | Bundle ID |
|---|---|---|
| Maktaba Player | `Maktaba Player` | `com.hamzalabs.maktaba.player` |
| Maktaba Remote | `Maktaba Remote` | `com.hamzalabs.maktaba.remote` |

## How it works

1. The tag guard runs first, as in every tag workflow. A hand run skips it, and a re-run of a
   tag that is already published uploads nothing.
2. A `macos-26` runner uses the newest Xcode it has. Until the runner image has Xcode 27, the
   Player is built without the iPhone Duo's half-folded layout (that code needs the iOS 27.1
   SDK and sits behind `#if compiler(>=6.4)`). The build is otherwise the same.
3. Versions: the tag's version is the app version (`v0.3.45` → `0.3.45`; a `-rc.N` suffix is
   dropped). A hand run uses the latest tag. The build number is the UTC time of the run
   (`YYYYMMDD.HHMMSS`), so every upload is new and higher than the last.
4. Each app is archived unsigned. The export then signs it and uploads it in one step
   (`xcodebuild -exportArchive`, ExportOptions `method` `app-store-connect`, `destination`
   `upload`). Signing is Xcode's automatic cloud signing, using the App Store Connect API key
   already in the repository secrets (`APPLE_API_KEY_P8`, `APPLE_API_KEY_ID`,
   `APPLE_API_ISSUER`) and `APPLE_TEAM_ID`. No certificate or provisioning profile is stored
   as a secret: Apple keeps the cloud-managed distribution certificate, and Xcode creates the
   App Store profile when it needs one.

The Capacitor wrapper's iOS job in `mobile-release.yml` (`apps/mobile`) is a different app.
It still archives unsigned and is not uploaded.

## One-time setup in App Store Connect (owner)

App records can't be created through the API, so the first upload fails until they exist
(`Error Downloading App Information`, `missingApp(bundleId: …)` in the log).

1. In [App Store Connect](https://appstoreconnect.apple.com) → **Apps** → **+** → **New App**,
   create both apps:

   | Platform | Name | Primary language | Bundle ID | SKU |
   |---|---|---|---|---|
   | iOS | Maktaba Player | English | `com.hamzalabs.maktaba.player` | `maktaba-player-ios` |
   | iOS | Maktaba Remote | English | `com.hamzalabs.maktaba.remote` | `maktaba-remote-ios` |

   If a bundle ID isn't in the list, register it first: Certificates, Identifiers & Profiles →
   Identifiers → **+** → App IDs. Opening the target once in Xcode with automatic signing
   also registers it. App names must be unique across the App Store; if one is taken, pick
   another name. The name can be changed until the app is first released.
   Instead of the web form, you can let Xcode create the record: Product → Archive, then
   **Distribute App** → **App Store Connect** in the Organizer.
2. The API key in the secrets needs the **Admin** role, or **App Manager** with access to
   certificates, so cloud signing can make the distribution certificate (Users and Access →
   Integrations → App Store Connect API).
3. TestFlight → **Internal Testing** → **+**: create a group (e.g. "Owner"), turn on
   automatic distribution, and add yourself. Internal testers must be users of the team.

Then run the workflow by hand, or push the next tag.

## Installing on the iPhone

1. Install **TestFlight** from the App Store.
2. Open it with the Apple ID that is an internal tester. Maktaba Player and Maktaba Remote
   show up there once App Store Connect has processed a build (usually 5–30 minutes after
   the upload). The apps declare that they use only standard encryption
   (`ITSAppUsesNonExemptEncryption` is `NO`), so App Store Connect doesn't ask the
   export-compliance question.
3. Tap **Install**. Each new tag's build arrives as an update in TestFlight.

Nothing here submits the apps for App Store review or adds external testers.
