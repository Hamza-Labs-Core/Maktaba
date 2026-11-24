# Apple App Review: what each Apple app needs for the App Store

An audit of every Apple-distributed Maktaba app against Apple's App Review Guidelines (last
updated 16 October 2026) and App Store Connect's requirements, as of 23 November 2026. It
covers public release on the App Store, not only TestFlight. What the code can fix is fixed
and checked by `tools/ci/tests/test_apple_app_review.py`. What only the owner can do is listed
under [Owner actions](#owner-actions). The App Store text drafts, in English and Arabic, are in
[`apps/apple/AppStore/`](../apps/apple/AppStore/README.md).

Status words used below:

- **done**: already true on main before this audit.
- **fixed**: made true by the pull request that added this file.
- **owner**: an action only the owner can take (App Store Connect, Apple requests, legal text, servers).
- **risk**: a likely reason for rejection that needs a decision first.

## The apps

| App Store Connect record | Scheme (`apps/apple/project.yml`) | Bundle ID | Platform | Channel |
|---|---|---|---|---|
| Maktaba Player | Maktaba Player | `com.hamzalabs.maktaba.player` | iPhone and iPad (iOS and iPadOS 27+), one universal app (#1616) | App Store |
| Maktaba Player for Mac | Maktaba Player Mac | `com.hamzalabs.maktaba.player.mac` | macOS 15+, Apple silicon | Mac App Store |
| Maktaba Remote | Maktaba Remote | `com.hamzalabs.maktaba.remote` | iPhone and iPad, one universal app (#1616) | App Store |
| *(retired)* Maktaba Player for iPad, Maktaba Remote iPad | — (removed in #1616) | `….player.tablet`, `….remote.tablet` | iPad | never submitted |
| Maktaba TV | Maktaba TV (sources in `apps/tv/tvos/`) | `com.hamzalabs.maktaba.tv` | Apple TV (tvOS 27+) | App Store |
| — | Maktaba.app (Tauri, `apps/desktop/`) | — | macOS | **Developer ID + notarization only** |

`testflight.yml` archives the App Store apps (four once #1616 merges) on the `xcode-27` runner and uploads them on
every tag (`.github/workflows/testflight.yml:87-103`).

**Maktaba.app, the Mac server, stays outside the Mac App Store.** The Mac App Store requires
the App Sandbox (https://developer.apple.com/documentation/security/app-sandbox). Guideline
2.4.5 also forbids several things the server must do:

- installing code in shared locations (2.4.5(ii));
- running at login or leaving processes running after quit without consent (2.4.5(iii)); the
  server's LaunchAgent and its Postgres and ffmpeg children do this;
- updating outside the Mac App Store (2.4.5(vii)); the server updates itself.

It ships through `desktop-release.yml`, signed with a Developer ID Application certificate and
notarized (`.github/workflows/desktop-release.yml:73-79`). Notarization needs the Hardened
Runtime and no App Review. Two dated changes affect that path:

- Apple's original Developer ID intermediate certificate expires on **1 February 2027**. "Pkg
  files signed with an affected certificate will no longer install"
  (https://developer.apple.com/news/?id=w4atic4c). See owner action 12.
- On macOS 15+, a launchd **agent** does not get the local-network exemption a daemon gets
  (TN3179). The server's LaunchAgent and its children are charged to the app, so the app needs
  `AssociatedBundleIdentifiers` in its LaunchAgent plist if it is not registered through
  `SMAppService`
  (https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy).

## Top rejection risks, ranked

1. **App Review cannot sign in (2.1, 4.2): no live review home and no demo mode.**
   - Every app does nothing without a Maktaba home. Guideline 2.1(a): "include demo account info
     (and turn on your back-end service!) if your app includes a login", or "a built-in demo
     mode in lieu of a demo account with prior approval by Apple"
     (https://developer.apple.com/app-store/review/guidelines/#2.1).
   - `review.maktaba.tv` is designed (`docs/stores/review-home.md`, `deploy/review-home/`) but
     **not running**: its name does not resolve, and the `Review home probe` workflow is skipped
     on every run (the `REVIEW_HOME_PROBE` variable is off).
   - It was designed for the TV stores. Its `reviewer` account is a viewer, so it cannot get
     through the Remote, which is for a home's admins only.
   - *Mitigation:*
     - (a) deploy the review home before the first submission;
     - (b) add a separate **admin** review account on it, or a second throwaway review home that
       is reset after each round, so the Remote can be reviewed;
     - (c) in the longer term, a built-in demo home inside the apps, built on the demo library
       the screenshots already use (`tests/fixtures/demo-library`). That needs Apple's prior
       approval, a design story first (it is a public surface), and media with no women or
       girls.
   - The review notes for (a) and (b) are drafted in
     `apps/apple/AppStore/*/metadata/review_information/notes.txt`.
2. **Maktaba Cloud is not live, but the apps offer it (2.1 incomplete, 2.5.19 dormant features).**
   - `api.maktaba.tv`, `watch.maktaba.tv`, `live.maktaba.tv` and `status.maktaba.tv` do not
     resolve in DNS today.
   - The Player and Remote start screen always offers **Through Maktaba Cloud**
     (`apps/apple/MaktabaKit/Sources/MaktabaKit/StartViews.swift:282-291`). The Player and the
     Apple TV app offer **Watch a broadcast** (`StartViews.swift:224-229`;
     `apps/tv/tvos/MaktabaTV/Views/StartViews.swift`). Both fail for a reviewer.
   - A feature that cannot work during review is a 2.1 rejection.
   - **Decided (2026-11-24, decision `apple-cloud-entries`):** Maktaba Cloud launches before
     the first submission, and **Through Maktaba Cloud** stays. The apps are submitted only once
     `api.maktaba.tv` and `status.maktaba.tv` answer (owner action 3). **Watch a broadcast** is
     off while broadcasting is off (risk 3).
3. **Watch a broadcast has no in-app Report (1.2 user-generated content).**
   - Viewers watch channels other people publish, which is user-generated content.
   - Guideline 1.2 requires "A mechanism to report offensive content and timely responses",
     "The ability to block abusive users" and "Published contact information". Since 8 June
     2026 it also requires the developer to remove violating content.
   - The cloud has the moderation routes (`cloud/internal/handlers/moderation/moderation.go:6`)
     and the watch pages have Report. The apps do not: `docs/broadcasting.md` lists "The Report
     button in the apps' player menu" as not yet built, because the route checks a Turnstile
     token the apps cannot show.
   - **Remove** on a saved broadcast (`GuestViews.swift:123`) serves as block.
   - **Decided (2026-11-24):** broadcasting is **off** for now: no broadcast entry shows in
     the Apple apps, and nothing about copyright shows anywhere (another change turns it off
     across the apps and the site). While it is off, risk 3 does not apply and the review notes
     need no broadcast text.
   - **If broadcasting comes back:** the block is **Leave channel**, which also stops that
     friend's invites, and the review notes explain that a channel is invite-only (only the
     people its publisher invites can watch it). Report still needs a route the apps can call
     (App Attest or a guest pass in place of Turnstile) before Watch a broadcast is visible on
     the App Store.
4. **YouTube and Plex names, and YouTube downloads, in the apps (5.2.1, 5.2.3).**
   - Guideline 5.2.3: "Apps should not facilitate illegal file sharing or include the ability to
     save, convert, or download media from third-party sources (such as Apple Music, YouTube …)
     without explicit authorization".
   - The home downloads YouTube channels into libraries. The apps show where that comes from:
     - the Remote's queue has a **YouTube** stage, with retry, pause and resume
       (`MaktabaKit/QueueWords.swift:19`);
     - the Player badges "YouTube playlist" and "YouTube channel"
       (`apps/apple/Player/Libraries/PlayerQueue.swift:129-130`);
     - the Apple TV app has **From YouTube** and **From Plex** tabs
       (`apps/tv/tvos/MaktabaTV/Views/LibraryTabsView.swift:287`);
     - parental rules can name a YouTube source (`Family.swift:640`).
   - Guideline 4.1(c) also says: "You cannot use another developer's icon, brand, or product
     name in your app's icon or name". Names in the UI are judged under 5.2.1.
   - **Decided (2026-11-24):** the YouTube and Plex collection badges and tabs are removed from
     every app (another change). This risk drops from the list once that change is on main.
   - Meanwhile: the review home has no YouTube or Plex collections and its YouTube component is
     off, and screenshots and metadata never show either name.
5. **Separate iPhone and iPad apps of the same app (4.3(a), 2.4.1).**
   - Guideline 4.3(a): "Don't create multiple Bundle IDs of the same app". Guideline 2.4.1:
     "iPhone apps should run on iPad whenever possible."
   - The Player and the Remote each have an iPhone-only app (`TARGETED_DEVICE_FAMILY: "1"`) and
     a separate iPad app (`"2"`), built from the same sources.
   - App Review may reject the second record of each pair as a duplicate. It also tests the
     iPhone app on an iPad, in compatibility mode.
   - **Decided (2026-11-24, decision `apple-universal-apps`), done in #1616:** one universal
     app per product (`TARGETED_DEVICE_FAMILY: "1,2"`). The iPhone bundle IDs and records are
     kept; the `.tablet` targets are gone, and their records are never submitted. The drafts
     here have one folder per universal app.
6. **The privacy policy does not cover Maktaba Cloud accounts or broadcast viewers (5.1.1(i)).**
   - https://maktaba.tv/legal/privacy/ (`web/packages/website/copy/legal/privacy.en.md`) says
     "The apps send nothing to us unless you choose to send us a problem report".
   - Signing in through Maktaba Cloud sends the account's email and password, and the cloud
     keeps the account and its sessions.
   - 5.1.1(i) requires the policy to "identify what data, if any, the app/service collects …
     and all uses of that data", the retention, and how to ask for deletion.
   - **fixed (text waits for the owner's read):** the site's privacy page gained the cloud
     sign-in (broadcast viewing came out again while broadcasting is off), in
     English and Arabic (`copy/legal/privacy.{en,ar}.md`, "The apps → To Maktaba"). The site's
     build ties that section to `docs/app-privacy.md` (`scripts/legal.mjs`, `apps-source`), so
     the page could not stay behind the label. The Arabic needs the owner's read before merge
     (owner action 9).
7. **The support URL has no contact information (App Store Connect).**
   - App Store Connect requires a support URL that "must lead to actual contact information"
     (https://developer.apple.com/help/app-store-connect/reference/app-information/platform-version-information).
   - https://maktaba.tv/help/ answers 200 but has no email or contact form.
   - `/support/` and `/contact/` answer 404. Only the privacy page names `privacy@maktaba.tv`.
   - *Mitigation:* a support contact on the help page, in English and Arabic (owner action 8).
8. **The age rating and the parental-controls claim (1.2.1(a), 2.3.8, the 2025 age ratings).**
   - The 2025 system adds 13+, 16+ and 18+. Its answers were due 31 January 2026
     (https://developer.apple.com/news/?id=ks775ehf).
   - Two more questions, Shared Location and Dating, must be answered by **12 January 2027**
     (https://developer.apple.com/news/?id=yxkh08us).
   - The Player and the TV app play anything a home holds. (Broadcasts, other people's
     channels, are off for now: risk 3.)
   - The description says parental controls exist, and the questionnaire asks about in-app
     controls. The Declared Age Range check (`apps/apple/Player/Parental/DeviceAgeSignal.swift`)
     needs its capability on the Player's App IDs (owner action 6).
   - *Recommendation* (the owner decides):

     | App | Suggested answers |
     |---|---|
     | Remote | No content questions; 4+. |
     | Player, TV | "User-generated content: no" while broadcasting is off (yes once it returns), "Parental controls: yes", "Age assurance: yes" once Declared Age Range ships, no ads, no messaging, no unrestricted web access. Expect review to ask; a higher override (16+) is safer but blocks children's devices, which parental controls exist for. |
9. **Dormant Google Cast code (2.5.19, new on 16 October 2026).**
   - Guideline 2.5.19: "Don't include any hidden, dormant, or undocumented features".
   - The Google Cast SDK is linked into the iPhone and iPad Player (`project.yml:28-30`) and
     turned off while no receiver is registered (`Config/Cast.xcconfig`:
     `MAKTABA_CAST_ENABLED = NO`).
   - Its privacy manifest declares Device ID, Product Interaction and Other Diagnostic Data for
     analytics. Xcode's privacy report adds those to the app's report even when the SDK never
     starts.
   - **Decided (2026-11-24, decision `apple-cast`):** Cast goes on and is tested for review.
     The Player's Cast code and its CI tests already exist (`apps/apple/Player/Cast/`,
     `UITests/PlayerCastUITests.swift` against a fake Cast context, since no runner has a Cast
     device). What turns it on is the receiver's application ID in `shared/cast/app.json`, and
     that ID exists only once the receiver is registered (owner action 6).
   - Google's pages say an unpublished receiver loads only on devices registered in the Cast
     console (https://developers.google.com/cast/docs/registration), so App Review's devices
     need the receiver **published**, after the real-device checklist in `docs/stores/cast.md`.
   - No multicast entitlement is needed: Google's iOS sender page asks only for the two
     Bonjour types (`_googlecast._tcp`, `_<app id>._googlecast._tcp`) and the local network
     prompt, which `Config/Cast.xcconfig` declares once the ID is set
     (https://developers.google.com/cast/docs/ios_sender).
10. **Name wording for the iPad Remote (App Store marketing guidelines).**
    - No longer applies: the iPad Remote is the universal Maktaba Remote (risk 5), and its
      "Maktaba Remote iPad" record is never submitted. "Maktaba Player for Mac" follows Apple's
      "app name for Mac" rule (https://developer.apple.com/app-store/marketing/guidelines/).
11. **No privacy policy link inside the apps (5.1.1(i)).**
    - 5.1.1(i): "All apps must include a link to their privacy policy in the App Store Connect
      metadata field and within the app."
    - No app links to https://maktaba.tv/legal/privacy/ today.
    - *Mitigation:* a Privacy policy row in the Player's Profile, the Remote's Home tab and the
      TV's Settings, in English and Arabic (owner action 10). The TV shows the address or a QR
      code, because it has no browser.

## Per-app checklist

### Every App Store app (Player iPhone, iPad and Mac; Remote iPhone and iPad; TV)

| Requirement | Status | Evidence |
|---|---|---|
| Built with Xcode 27 and the 27 SDKs. Apple requires the iOS 26 and tvOS 26 SDK since 28 April 2026, and the 27 SDK from April 2027 (https://developer.apple.com/news/?id=k1mtkt1k) | done | `.github/workflows/testflight.yml:87` (`runs-on: xcode-27`) and its SDK check at `:175`; #1486 |
| Privacy manifest with the required-reason APIs | **fixed** | `apps/apple/Player/PrivacyInfo.xcprivacy` (UserDefaults CA92.1; file timestamps C617.1 for `Player/Components/ImageCache.swift:260,284`), `apps/apple/Remote/PrivacyInfo.xcprivacy` and `apps/tv/tvos/MaktabaTV/PrivacyInfo.xcprivacy` (UserDefaults CA92.1). One in each of the six apps' Resources (`Maktaba.xcodeproj`). The test scans every app's Swift and MaktabaKit's for the five categories |
| Privacy manifest's collected data matches the App Privacy label | **fixed** | the manifests and `docs/app-privacy.md`'s App Store table (now with the cloud sign-in and broadcasts), checked against each other by the test |
| Third-party SDK manifests | done | PLCrashReporter 1.12.2 (TV) ships `Resources/PrivacyInfo.xcprivacy`; GoogleCast 4.8.6 ships one in each framework slice. Neither is on Apple's list of SDKs needing a signature (https://developer.apple.com/support/third-party-SDK-requirements/) |
| No tracking, no ATT prompt | done | no advertising identifier, no tracking SDK; `NSPrivacyTracking` false in every manifest |
| Export compliance (`ITSAppUsesNonExemptEncryption` NO: only system HTTPS and CryptoKit hashing) | done | `project.yml:95` (iPhone and iPad), `:187` (Mac), `:441` (TV); now checked by the test. The Annual Self Classification Report does not apply to exempt-only encryption; check it in App Store Connect when asked |
| App Transport Security: local network only | **fixed** | `NSAllowsArbitraryLoads` removed, `NSAllowsLocalNetworking` kept (`project.yml`, the six Info.plists). Apple: "In iOS 10 and later and macOS 10.12 and later, the value of the NSAllowsArbitraryLoads key is ignored … if … NSAllowsLocalNetworking" is present, and "You must supply a justification during App Store review if you set the key's value to YES" (https://developer.apple.com/documentation/bundleresources/information-property-list/nsapptransportsecurity/nsallowsarbitraryloads). No behaviour change; nothing left to justify. The review notes say why local http is needed |
| Local network prompt text, English and Arabic | done | `INFOPLIST_KEY_NSLocalNetworkUsageDescription` (`project.yml:61`); Arabic in each `InfoPlist.xcstrings`; `tools/ci/tests/test_apple_app_names.py` checks both |
| Bonjour services declared | done | `NSBonjourServices: ["_maktaba._tcp"]` (`project.yml:73`); browsed with `NWBrowser` (`MaktabaKit/HomeBrowser.swift`) |
| Multicast entitlement | not needed | only `NWBrowser` of a declared type; no raw UDP multicast. TN3179: the entitlement is for "Working with arbitrary Bonjour service types" and raw multicast. Request it only if the Google Cast SDK is turned on (Cast discovery) |
| Camera prompt (QR scan), English and Arabic | done | `project.yml:93`; `Player/InfoPlist.xcstrings`, `Remote/InfoPlist.xcstrings`. iPhone and iPad only; asked only when Scan is tapped |
| Microphone, photos, location, contacts, Bluetooth, Face ID prompts | not needed | none of these APIs is called (searched every app's sources and MaktabaKit) |
| Sign in with Apple (4.8) | not needed | 4.8 applies only to a third-party or social login for the primary account. The apps use a home's own username and password, and Maktaba Cloud's email and password. "Not required if: Your app exclusively uses your company's own account setup and sign-in systems." The cloud's Google and Apple sign-in is on the web only |
| Account deletion (5.1.1(v)) | done (not triggered) | the apps create no account: a home's accounts are made by its admin on the home, and cloud accounts on the web (`Cloud.swift` has sign-in only). When the cloud is live, a **Manage or delete your account** link in the cloud sheet is still wise (owner action 10) |
| In-app purchase (3.1) | done | nothing is sold and no price is shown. `FeatureOffNote` opens the home's own Components page for an admin (`MaktabaKit/Capabilities.swift:390-433`), not a purchase. The catalogs still hold unused "Open Subscription" and "Needs a plan" strings with no code using them; keep it that way, or the app becomes a 3.1.1/3.1.3 case |
| Downloaded executable code (2.5.2, 4.7) | done | plugins run on the home. A plugin's panel in the apps is data drawn with SwiftUI's own views, with no links and no code (`MaktabaKit/PluginPanels.swift:4-9`); no web view or JavaScriptCore anywhere in the apps |
| AI search and data sharing (5.1.2(i), wording of 13 November 2025) | done | search by meaning runs on the person's home; the apps send no data to a third-party AI |
| IPv6-only networks (2.5.5) | done | `URLSession` and `Network.framework` with host names; the only IPv4 literal is the loopback `StreamProxy` (`127.0.0.1`), which exists on IPv6-only networks too |
| Starts with no home and no network | done | the start screen is the home picker; `ConnectUITests.testTheCloudDownShowsTheSavedHomes` and `PermissionsUITests.testFirstRunAsksThenStaysClosed` start the apps with no home on CI |
| App icon: 1024 px, no alpha | **fixed** | `Shared/Assets.xcassets/RemoteIcon.appiconset/icon-1024.png` had an alpha channel (all opaque), now removed with identical pixels; `AppIcon` had none. Checked by the test |
| Privacy policy URL (5.1.1(i)) | done (URL) / **fixed** (text, owner's read) | https://maktaba.tv/legal/privacy/ and `/ar/legal/privacy/` answer 200. The text now covers the cloud account and broadcasts (risk 6). The apps link to it from the descriptions; an in-app link is also required ("within the app") and the apps have none yet (owner action 10) |
| Support URL with contact information | **owner** | https://maktaba.tv/help/ answers 200 but has no contact (risk 7) |
| Marketing URL | done | https://maktaba.tv/ and `/ar/` answer 200 |
| Name, subtitle, description, keywords, promotional text in English and Arabic | **fixed** (drafts) | `apps/apple/AppStore/<app>/metadata/{en-US,ar-SA}/`, within App Store Connect's limits (checked by the test); owner approval before use |
| Review notes and demo account | **fixed** (drafts) / **owner** (account) | `apps/apple/AppStore/*/metadata/review_information/notes.txt`: secrets named, never written (`tools/ci/review_credentials.py` now scans the folder) |
| Screenshots | done (made) / **owner** (upload) | `.github/workflows/store-gallery.yml` (#1566) composes every app's gallery as an artifact laid out for fastlane deliver (`apple/`); uploading it is an owner action. iPhone 6.9" (1320×2868) and 6.3", iPad 13" (2064×2752), Mac 16:10, Apple TV 1920×1080 (https://developer.apple.com/help/app-store-connect/reference/app-information/screenshot-specifications). Demo media shows no women or girls |
| Accurate metadata (2.3) | **fixed** (drafts) | the drafts say a Maktaba home is needed, name no other app or platform brand, and carry no price |
| Content rights | **owner** | answer "No, it does not contain, show, or access third-party content" only if the review home's content is all the app shows. Its content is public domain or CC BY (`deploy/review-home/content/manifest.json`), so say so in the notes |
| EU Digital Services Act trader status | **owner** | without it apps are removed from the EU store (https://developer.apple.com/news/?id=einwn76m) |

### Maktaba Player (iPhone, iPad)

| Requirement | Status | Evidence |
|---|---|---|
| Background audio for audiobooks, AirPlay and Picture in Picture (2.5.4) | done | `UIBackgroundModes: [audio]` (`project.yml:141,171`), used only for playback |
| iPad multitasking (Split View, Slide Over) | done | all four orientations (`project.yml:98-99`), no `UIRequiresFullScreen`; the narrow layout is one column (`apps/apple/README.md`) |
| Parental controls and Declared Age Range | **owner** | the code is behind `canImport(DeclaredAgeRange)` (`Player/Parental/DeviceAgeSignal.swift:5`); the capability must be on the Player App IDs (`docs/testflight.md`) |
| Watch a broadcast: report and block (1.2) | off | broadcasting is off for now (risk 3); "Leave channel" when it returns |
| Google Cast dormant (2.5.19) | **risk** | risk 9 |

### Maktaba Player for Mac

| Requirement | Status | Evidence |
|---|---|---|
| App Sandbox (required on the Mac App Store) | done | `Player/Player-Mac.entitlements`: sandbox, network client, network server (the loopback `StreamProxy`), its own keychain group |
| Hardened Runtime | done | `ENABLE_HARDENED_RUNTIME: YES` (`project.yml:189`) |
| App category | done | `LSApplicationCategoryType` entertainment (`project.yml:62`) |
| Local network privacy on macOS 15+ | done | the same prompt text and Bonjour list as iOS; no multicast entitlement is needed on macOS (TN3179) |
| Full icon set | done | `AppIcon.appiconset` has every macOS size (16 to 512 @2x) |
| Privacy manifest | **fixed** | the Player's manifest is in the Mac app too. Apple enforces required reasons on iOS, iPadOS, tvOS and visionOS, not macOS, so it is a declaration there |

### Maktaba Remote (iPhone, iPad)

| Requirement | Status | Evidence |
|---|---|---|
| A reviewer can get in (admin only) | **owner** / **risk** | risk 1 (b): the review home has no admin account for App Review |
| Minimum functionality (4.2) | done | a full admin console (queue, libraries, machines, updates, diagnostics, problem reports) |
| YouTube stage in the queue (5.2.3) | **risk** until removed | risk 4: being removed in every app |
| Separate iPad app (4.3(a)) | **risk** | risk 5 |
| iPad name wording | **owner** | risk 10 |

### Maktaba TV (Apple TV)

| Requirement | Status | Evidence |
|---|---|---|
| Layered app icon and App Store icon | done | `apps/tv/tvos/MaktabaTV/Resources/Assets.xcassets/AppIcon.brandassets`: App Icon and App Icon - App Store image stacks (1280×768) |
| Top Shelf (static fallback) | done | Top Shelf Image (1920×720) and Top Shelf Image Wide (2320×720) with @2x. A Top Shelf extension is optional (https://developer.apple.com/design/human-interface-guidelines/top-shelf) |
| Focus and the Siri Remote | done | `MaktabaTVUITests` drive the app with the Siri Remote's buttons (`XCUIRemote`) |
| Apple TV privacy policy text (required for tvOS) | **fixed** (draft) | `apps/apple/AppStore/tv/metadata/*/apple_tv_privacy_policy.txt` |
| "Available only on Apple TV" wording | **fixed** (draft) | the TV description says so, as the marketing guidelines ask |
| Local network prompt | done | no local network privacy on tvOS (TN3179); the key is harmless |
| PLCrashReporter | done | MIT, its own manifest; reports stay on the TV unless sent to the home (`docs/app-privacy.md`) |
| YouTube and Plex tabs (5.2.1) | **risk** until removed | risk 4: being removed in every app |
| Cloud code sign-in | **owner** | risk 2: the cloud launches before submission. Watch a broadcast is off (risk 3) |

## App Privacy ("nutrition label") answers

These come from what the code sends; `docs/app-privacy.md` is the source and now lists them.
Apple's definition of collected data is "transmitting data off the device in a way that allows
you and/or your third-party partners to access it"
(https://developer.apple.com/app-store/app-privacy-details/). What goes only to the person's
own home is therefore not collected: Maktaba cannot read it.

| Data type | Player | Remote | TV | Linked | Tracking | Purpose |
|---|---|---|---|---|---|---|
| Email Address | yes, optional | yes, optional | no | yes | no | App Functionality |
| User ID | yes, optional | yes, optional | no | yes | no | App Functionality |
| Customer Support | yes, optional | yes, optional | yes, optional | no | no | App Functionality |
| Crash Data | yes, optional | yes, optional | yes, optional | no | no | App Functionality |
| Other Diagnostic Data | yes, optional | yes, optional | yes, optional | no | no | App Functionality |

Where each comes from:

- **Email Address** and **User ID**: signing in through Maktaba Cloud.
- **Customer Support**: a problem report sent on to Maktaba.
- **Crash Data**: only with Send crash reports on, then only in a report sent to Maktaba.
- **Other Diagnostic Data**: a problem report's log.

If the Google Cast SDK is turned on (risk 9), add what its manifest declares for the iPhone
and iPad Player: Device ID, Product Interaction and Other Diagnostic Data, not linked, for
Analytics and App Functionality.

## The privacy policy's new parts (for owner approval)

What this pull request adds to https://maktaba.tv/legal/privacy/, under **The apps → To Maktaba**
(`web/packages/website/copy/legal/privacy.{en,ar}.md`). The Arabic follows the site's voice
(`web/packages/website/copy/voice.md`); the shipped wording is in those files, and the bullets
below summarise it.

- **English**
  - *Maktaba Cloud.* If you sign in through Maktaba Cloud in an app, the app sends your
    account's email and password to Maktaba Cloud. Maktaba Cloud keeps your account and the
    devices signed in to it until you delete the account on the web. Deleting the account
    removes them.
- **Arabic**
  - *سحابة مكتبة.* إن سجّلتم الدخول عبر سحابة مكتبة في أحد التطبيقات، يرسل التطبيق بريد
    حسابكم وكلمة مروره إلى سحابة مكتبة. وتحتفظ السحابة بحسابكم وبالأجهزة المسجَّل دخولها إليه
    حتى تحذفوا الحساب من الموقع، فيُحذف ذلك كله معه.

## Owner actions

Only the owner can do these. None of them is a code change. Never put a password in the
repository; the notes name the secrets.

1. **App Store Connect agreements, tax and banking.** Accept the latest Apple Developer Program
   License Agreement and the Free Apps agreement (Business → Agreements). Paid apps are not
   needed: everything is free.
2. **EU trader status** (Digital Services Act). Declare it in App Store Connect → Business:
   address, phone and email are verified and shown on the store. Without it the apps are not
   on sale in the EU (https://developer.apple.com/help/app-store-connect/manage-compliance-information/manage-european-union-digital-services-act-trader-requirements).
3. **Deploy the review home** (`deploy/review-home/README.md`), set the `REVIEW_HOME_PROBE`
   variable to `true`, and keep it green during each review window.
4. **Create an App Review admin account** for the Remote: on the review home, or on a second
   throwaway home reset after each round. Put its username and password in the release secrets
   as `REVIEW_HOME_ADMIN_USERNAME` and `REVIEW_HOME_ADMIN_PASSWORD`, and rotate them after each
   round like the viewer's.
5. **Approve the privacy wording** (asked again on the decisions page with the final text).
   Every other decision was made on 2026-11-24: launch the cloud first (risk 2), broadcasting
   off and "Leave channel" if it returns (risk 3), YouTube and Plex badges removed (risk 4),
   universal apps (#1616, risk 5), Cast on (risk 9).
6. **Capabilities and the Cast receiver.**
   - Declared Age Range on the Player's App ID (Certificates, Identifiers & Profiles).
   - Register the Cast receiver in the Google Cast SDK Developer Console (one-time $5), with
     `https://cast.maktaba.tv/r/` as its URL, and give the application ID it shows (public, not
     a secret) for `shared/cast/app.json`. Register the test devices, deploy the receiver
     (`CAST_RECEIVER_LIVE` and the `cast.maktaba.tv` name), pass the checklist in
     `docs/stores/cast.md`, then **publish** the receiver before submitting the Player.
   - No multicast entitlement is needed for Cast (risk 9).
7. **Leave the iPad-only records unsubmitted** (Maktaba Player for iPad, Maktaba Remote iPad),
   tell iPad testers to install Maktaba Player and Maktaba Remote, and set each submitted
   record's Arabic localization from `apps/apple/AppStore/`.
8. **A support contact on maktaba.tv/help/**, in English and Arabic: a mailbox (for example
   `support@maktaba.tv`) or a contact form. App Store Connect's Support URL must show it.
9. **Read and approve the privacy policy's new text** (English and Arabic, in this pull request;
   the site's copy needs the owner's read). It publishes when the pull request merges.
10. **In-app links** (a small app change, for a design story): a Privacy policy link in each
    app (5.1.1(i) asks for one "within the app"). Once the cloud is live, a Manage or delete
    your account link in the cloud sheet.
11. **App Privacy label**: fill in App Store Connect's App Privacy for each of the four records
    from the table above. **Age rating**: answer the questionnaire, including the two questions
    due 12 January 2027. **Content rights**: answer it. **Category**: Entertainment, with Photo
    & Video second (Utilities for the Remote). **Review contact**: name, email and phone in
    App Review Information.
12. **The Mac server app's Developer ID certificate**: make sure the `APPLE_CERTIFICATE` used
    by `desktop-release.yml` is from the current Developer ID intermediate before 1 February
    2027 (https://developer.apple.com/news/?id=w4atic4c).
13. **Screenshots**: run `store-gallery.yml` and upload its `apple/` images to each record (#1566).

## Sources

Every page was read on 23 November 2026:

- App Review Guidelines, last updated 16 October 2026: https://developer.apple.com/app-store/review/guidelines/
- What changed in 2025–2026: https://developer.apple.com/news/
  - 8 June 2026 (1.2, 4.3): https://developer.apple.com/news/?id=a233fmpw
  - 16 October 2026 (2.3.14, 2.5.19): https://developer.apple.com/news/?id=92th7e5i
- Privacy manifests and required reasons: https://developer.apple.com/documentation/bundleresources/privacy-manifest-files
  and https://developer.apple.com/documentation/bundleresources/describing-use-of-required-reason-api
- Local network privacy (TN3179): https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy
- App Transport Security: https://developer.apple.com/documentation/bundleresources/information-property-list/nsapptransportsecurity
- Export compliance: https://developer.apple.com/documentation/security/complying-with-encryption-export-regulations
- App Privacy details: https://developer.apple.com/app-store/app-privacy-details/
- Age ratings: https://developer.apple.com/help/app-store-connect/reference/app-information/age-ratings-values-and-definitions
- Account deletion: https://developer.apple.com/help/app-review/guideline-reference/5-1-1-account-deletion
- Marketing guidelines (app names): https://developer.apple.com/app-store/marketing/guidelines/
