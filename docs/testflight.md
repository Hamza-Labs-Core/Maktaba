# TestFlight: the iPhone, iPad, Mac and Apple TV apps

Every `v*` release tag uploads the native iPhone, iPad and Mac apps and the Apple TV app from
`apps/apple/`
to App Store Connect,
where they appear in TestFlight. The workflow is `.github/workflows/testflight.yml`. It can
also be run by hand from the Actions tab (*TestFlight* → *Run workflow*).

| App | Scheme | Bundle ID |
|---|---|---|
| Maktaba Player (iPhone) | `Maktaba Player` | `com.hamzalabs.maktaba.player` |
| Maktaba Player for iPad | `Maktaba Player iPad` | `com.hamzalabs.maktaba.player.tablet` |
| Maktaba Player for Mac (Mac App Store) | `Maktaba Player Mac` | `com.hamzalabs.maktaba.player.mac` |
| Maktaba Remote (iPhone) | `Maktaba Remote` | `com.hamzalabs.maktaba.remote` |
| Maktaba Remote iPad | `Maktaba Remote iPad` | `com.hamzalabs.maktaba.remote.tablet` |
| Maktaba TV (Apple TV) | `Maktaba TV` | `com.hamzalabs.maktaba.tv` |

Each app is its own job (`fail-fast: false`): if one upload fails, for example because its
App Store Connect record doesn't exist yet, the other apps still upload.

## How it works

1. The tag guard runs first, as in every tag workflow. A hand run skips it, and a re-run of a
   tag that is already published uploads nothing.
2. A `macos-26` runner uses the newest Xcode it has. Until the runner image has Xcode 27, the
   Player is built without the iPhone Duo's half-folded layout (that code needs the iOS 27.1
   SDK and sits behind `#if compiler(>=6.4)`). The build is otherwise the same. The TV app
   targets tvOS 26 and archives with any Xcode 26 or later (`generic/platform=tvOS`).
3. Versions: the tag's version is the app version (`v0.3.45` → `0.3.45`; a `-rc.N` suffix is
   dropped). A hand run uses the latest tag. The build number is the UTC time of the run
   (`YYYYMMDD.HHMMSS`), so every upload is new and higher than the last.
4. Each app is archived unsigned. The export then signs it and uploads it in one step
   (`xcodebuild -exportArchive`, ExportOptions `method` `app-store-connect`, `destination`
   `upload`). Signing is Xcode's automatic cloud signing, using the App Store Connect API key
   already in the repository secrets (`APPLE_API_KEY_P8`, `APPLE_API_KEY_ID`,
   `APPLE_API_ISSUER`) and `APPLE_TEAM_ID`. No certificate or provisioning profile is stored
   as a secret: Apple keeps the cloud-managed distribution certificate, and Xcode creates the
   App Store profile when it needs one. The Mac app's export builds the installer package
   (`.pkg`) the Mac App Store takes and signs it with the cloud-managed Mac Installer
   Distribution certificate.
5. The Mac app is sandboxed (`apps/apple/Player/Player-Mac.entitlements`). An unsigned archive
   carries no entitlements and the export signs with the archived app's, so before the export
   the Mac leg signs those entitlements into the archived app ad hoc (no certificate, the
   team's ID in place of `$(AppIdentifierPrefix)`). The export keeps them and adds the App
   Store profile's. The Mac app also declares its App Store category
   (`LSApplicationCategoryType`, Entertainment), which the Mac App Store requires.

6. Crash symbols (Epic 53 story 53.16): after its upload, each leg turns the archive's dSYMs
   into symbol tables (`cloud/cmd/maktaba-symbols`) and uploads the tables, not the dSYMs, to
   Maktaba Cloud (`POST /v1/symbols`) with the `MAKTABA_SYMBOLS_TOKEN` repository secret. A
   problem report's Apple crash frames then read as function, file and line in the operator
   console. Without the secret the leg logs one line (crash symbols not uploaded) and stays
   green. A failed upload warns and never fails the leg (the app is already in TestFlight):
   the tables are kept for 90 days as the run's `symbols-N` artifact, and
   `MAKTABA_SYMBOLS_TOKEN=… maktaba-symbols upload -version vX.Y.Z *.symbols` sends them later.
   See [Crash symbols](#crash-symbols-owner) for the one step that turns it on.

The Capacitor wrapper's iOS job in `mobile-release.yml` (`apps/mobile`) is a different app.
It still archives unsigned and is not uploaded.

## One-time setup in App Store Connect (owner)

App records can't be created through the API, so the first upload fails until they exist
(`Error Downloading App Information`, `missingApp(bundleId: …)` in the log).

1. In [App Store Connect](https://appstoreconnect.apple.com) → **Apps** → **+** → **New App**,
   create the six apps:

   | Platform | Name | Primary language | Bundle ID | SKU |
   |---|---|---|---|---|
   | iOS | Maktaba Player | English | `com.hamzalabs.maktaba.player` | `maktaba-player-ios` |
   | iOS | Maktaba Player for iPad | English | `com.hamzalabs.maktaba.player.tablet` | `maktaba-player-ipad` |
   | macOS | Maktaba Player for Mac | English | `com.hamzalabs.maktaba.player.mac` | `maktaba-player-mac` |
   | iOS | Maktaba Remote | English | `com.hamzalabs.maktaba.remote` | `maktaba-remote-ios` |
   | iOS | Maktaba Remote iPad | English (U.S.) | `com.hamzalabs.maktaba.remote.tablet` | `com.hamzalabs.maktaba.remote.tablet` |
   | tvOS | Maktaba TV | English | `com.hamzalabs.maktaba.tv` | `maktaba-tv-tvos` |

   For the Mac app pick the **macOS** platform, and for the Apple TV app **tvOS**, in the
   New App form. The iPad, Mac and Apple TV apps each have their own bundle ID, so each needs
   its own record. The Maktaba Remote iPad record already exists (made on 2026-11-03, with its
   first build, 0.3.73, uploaded from Xcode): its SKU is its bundle ID. If the name
   "Maktaba TV" is taken, pick another that starts with Maktaba.

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

## Age rating and parental controls (owner, Epic 57)

With the release that carries Epic 57 (parental controls), in App Store Connect for **Maktaba
Player** and **Maktaba Player for iPad** (the two apps that read Apple's signals), by hand:

- [ ] **Keep the current age rating.** An age-rating change counts as a significant change
      under the Texas law's tools (`AppStore.ageRatingCode`, PermissionKit's
      `SignificantAppUpdateTopic`), so the rating must not change with this release.
- [ ] In the age-rating questionnaire's **In-App Controls**, answer **Parental Controls: Yes**
      and **Age Assurance: Yes** (the Player reads Declared Age Range).
- [ ] **Declared Age Range capability.** The Player asks Apple for the age range only once the
      App IDs `com.hamzalabs.maktaba.player` and `com.hamzalabs.maktaba.player.tablet` have the
      **Declared Age Range** capability (Certificates, Identifiers & Profiles → Identifiers; no
      approval form) and the targets carry `com.apple.developer.declared-age-range` in an
      entitlements file (`apps/apple/project.yml`). Until then the system answers "not
      available", nothing is asked or sent, and the app builds and runs the same.
- [ ] **Family Controls is not used** in this release. Story 57.16 (the home's time rules
      through Screen Time) needs `com.apple.developer.family-controls`, which Apple approves
      per app and per extension; the owner files that request.
- [ ] **The device check of story 57.04** before the release tag: on a real iPad of a child in
      a test Family Sharing group, set Content & Privacy Restrictions → TV Shows to TV-PG
      (Ratings For: United States), open the Player's **Profile**, tap **About** five times and
      read the line that appears (`movie`, `tv`, `region`, `limit`). Write the values in the
      release's notes and append a dated line to
      `specs/epics/57-parental-controls/research.md`. If the values stay 1000, the account's
      limit and the age range are what limit Maktaba, as Help says.
- [ ] **Screen Time check** on the same device (story 57.09 T04): set Downtime to start in a
      minute, watch a video, and see that the shield comes up, the position is saved and
      Resume works after downtime.
- [ ] The Player sells nothing, so Ask to Buy covers only installing the free app. If the app
      ever sells anything, it must handle StoreKit's `Product.PurchaseResult.pending` and
      approvals arriving on `Transaction.updates`.

### Deviations from story 57.07 (Family on iPhone and iPad)

Built as the story says: the children, Requests (with the poster of a video the guardian may
see), the parent PIN, Make an account restricted and Add a guardian for an admin (the accounts
come from `GET /api/admin/library-acl`), rules for a library, collection, series, channel,
YouTube source, plugin or video, and Rate libraries with each library's stored tier. Not built
in this release:

- **Notifications.** The story's Notifications screen registers push categories
  (`parental_request`, `parental_pin_locked`). The Apple apps have no push capability (no APNs
  entitlement, no device registry), so there is no such screen; a guardian sees a waiting
  request the next time Family is opened or refreshed. The `family.notifications*` strings
  stay in the catalog unused.
- **The iPad split view.** Family on iPad is the same pushed list as on iPhone, in a sheet;
  the story's split view (children on the left, the child's page beside) is not built.
- **The unrated count per library.** Rate libraries shows each library's default tier, not
  how many of its videos have no rating: the home has no route that says that number
  (`GET /api/libraries/manage` carries `age_tier` only). `family.rateLibraries.unrated` stays
  in the catalog unused.

## CI's simulators when the runners get Xcode 27

`.github/workflows/_apple-apps.yml` makes a throwaway simulator for each UI-test job from the
**newest runtime** the runner's Xcode has and a device type picked by name. On the `macos-26`
runners (Xcode 26) the jobs make their simulators. On a Mac with **Xcode 27.1** (build
27A9275, found 2026-11-12) the same picks are refused, because the newest runtimes there do
not take those device types. The workflow's pick is unchanged for now; it needs a fallback **before CI moves
to Xcode 27**.

What each job picks:

- **Apple TV** (`tv-ui-tests`): the newest tvOS runtime, and the lexically last identifier
  among the device types whose name starts with "Apple TV 4K". That is
  `com.apple.CoreSimulator.SimDeviceType.Apple-TV-4K-4K`, the first-generation Apple TV 4K
  (`…-4K-4K` sorts after `…-4K-3rd-generation-4K`), not the newest one (the step's comment
  calls it the newest).
- **The Players and the Remote** (`player-ui-tests`, `remote-ui-tests`): the newest iOS
  runtime, with the highest-numbered "iPhone N" (iPhone 17) or the highest "iPad Pro 11-inch
  (MN)" (M5).

What Xcode 27.1 answers:

```
$ xcrun simctl create probe com.apple.CoreSimulator.SimDeviceType.Apple-TV-4K-4K com.apple.CoreSimulator.SimRuntime.tvOS-27-0
An error was encountered processing the command (domain=com.apple.CoreSimulator.SimError, code=403):
Incompatible device
Unable to create a device for device type: Apple TV 4K (com.apple.CoreSimulator.SimDeviceType.Apple-TV-4K-4K), runtime: tvOS 27.0 (27.0 - 24J360) - com.apple.CoreSimulator.SimRuntime.tvOS-27-0

$ xcrun simctl create probe com.apple.CoreSimulator.SimDeviceType.iPhone-17 com.apple.CoreSimulator.SimRuntime.iOS-27-1
An error was encountered processing the command (domain=com.apple.CoreSimulator.SimError, code=403):
Incompatible device
Unable to create a device for device type: iPhone 17 (com.apple.CoreSimulator.SimDeviceType.iPhone-17), runtime: iOS 27.1 (27.1 - 24A94425) - com.apple.CoreSimulator.SimRuntime.iOS-27-1
```

Both exit 147 and create nothing. The iPad pick fails the same way
(`iPad-Pro-11-inch-M5-12GB` with `iOS-27-1`: "Incompatible device").

What that Mac accepts, and where the UI tests ran:

| Job | Device type | Runtime |
| --- | --- | --- |
| Apple TV | `Apple-TV-4K-3rd-generation-4K` (Apple TV 4K (3rd generation)) | `tvOS-27-0` (tvOS 27.0) |
| iPhone | `iPhone-17` (iPhone 17) | `iOS-27-0` (iOS 27.0) |
| iPad | `iPad-Pro-11-inch-M5-12GB` (iPad Pro 11-inch (M5)) | `iOS-27-0` (iOS 27.0) |

So the first-generation Apple TV 4K has no tvOS 27, and iOS 27.1 (the runtime of the iPhone
Duo) does not take the iPhone 17 or the iPad Pro (M5).

The fallback the pick needs: try the pairs in order and keep the first that `simctl create`
accepts (it exits non-zero and creates nothing when it refuses, so trying costs nothing).
For Apple TV, the "Apple TV 4K" device types newest generation first; for iPhone and iPad,
the same device type with each runtime from the newest down. A job that finds no pair should
fail with the list it tried. The tests do not depend on which pair it is: they need a 4K
Apple TV, a plain iPhone, an 11-inch iPad.

## Crash symbols (owner)

One step turns symbol uploads on at both ends: add a repository secret **`MAKTABA_SYMBOLS_TOKEN`**
(GitHub → Settings → Secrets and variables → Actions → New repository secret), a long random
value, for example the output of `openssl rand -hex 32`. Keep it nowhere else.

- The next cloud deploy (`relay.yml`, the Phase 1 pair) writes it to `/etc/maktaba/symbols.token`
  on both nodes and names it in `/etc/maktaba/cloud.env` (`MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE`);
  the api reads it when the deploy restarts it. Until then `POST /v1/symbols` answers
  `503 symbols_off`.
- The next tag's TestFlight legs upload their tables with it. The operator console's Crash
  symbols page (admin.maktaba.tv/symbols) lists what arrived.

To rotate it, replace the secret and run a cloud deploy; uploads made with the old one are
refused from then on (401).

## Installing on the iPhone (iPad, Mac, Apple TV)

1. Install **TestFlight** from the App Store.
2. Open it with the Apple ID that is an internal tester. Maktaba Player and Maktaba Remote
   show up there (the iPad apps, Maktaba Player for iPad and Maktaba Remote iPad, in
   TestFlight on the iPad, the Mac app in TestFlight for Mac,
   Maktaba TV in the TestFlight app on the Apple TV) once App Store Connect has processed a
   build (usually 5–30 minutes after the upload). The apps declare that they use only standard encryption
   (`ITSAppUsesNonExemptEncryption` is `NO`), so App Store Connect doesn't ask the
   export-compliance question.
3. Tap **Install**. Each new tag's build arrives as an update in TestFlight.

Nothing here submits the apps for App Store review or adds external testers.

## When Install fails though the build is listed

Open as of 2026-11-14, from about 0.3.100: TestFlight lists the new build for an internal
tester, and Install or Update answers

> Could not install Maktaba Player. The requested app is not available or doesn't exist.

What is known:

- It is not the build. The uploads were all green, and nothing in what is built or uploaded
  changed around when it began (the same Xcode, export and app settings before and after).
- Apple's developer forums have many threads with this exact message and no answer from Apple.
  The one cause posters name is the app record's sale state: an app listed as "Removed from
  Sale", after which no build installs
  ([778597](https://developer.apple.com/forums/thread/778597),
  [837048](https://developer.apple.com/forums/thread/837048),
  [826537](https://developer.apple.com/forums/thread/826537)).
- A workaround posted on Stack Overflow, not tried here: make the app available for sale for a
  few minutes, then remove it from sale again; that poster's TestFlight installs worked
  afterwards.

App Store Connect is the owner's: the record's sale state, the workaround and a report to
Apple (Feedback Assistant, or a Developer Support case) are done there, by hand. When it is
settled, write here what the record's state was and what cleared it.
