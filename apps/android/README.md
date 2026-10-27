# `apps/android/`: the native Android apps (Epic 42.3)

Jetpack Compose apps that talk to a home's REST API, like the Apple apps in `apps/apple`.

| Module | Application IDs (phone, foldable, tablet) | What it is |
|---|---|---|
| `player` | `com.hamzalabs.maktaba.player`, `.player.foldable`, `.player.tablet` | **Maktaba Player**: browse and watch |
| `remote` | `com.hamzalabs.maktaba.remote`, `.remote.foldable`, `.remote.tablet` | **Maktaba Remote**: run a home (its admins only) |
| `client` | (a JVM library) | the API client, sign-in, token sealing, the home picker's rules, the stream's authorization: plain Kotlin, shared with the desktop Player (`apps/desktop-player`) |
| `core` | (an Android library, on `client`) | Android's side: the Keystore and preferences, NSD discovery, the Ink & Brass theme, the fold-aware layout, the home picker and sign-in screens |

Each app has three product flavours, `phone`, `foldable` and `tablet`: the three store listings
the epic decided. They are one code base. The layout comes from the window, not from the flavour.

## Build and test

JDK 17 and the Android SDK (platform 37) are needed; `local.properties` names the SDK
(`sdk.dir=/Users/<you>/Library/Android/sdk`), or set `ANDROID_HOME`.

```bash
cd apps/android
./gradlew :client:test                 # the portable client's tests (JVM)
./gradlew :core:testDebugUnitTest      # the core's unit tests (JVM, no device)
./gradlew assembleDebug                # both apps, every form factor
./gradlew :player:installFoldableDebug # onto a running emulator or device
```

CI runs the tests, lint and the debug builds (`.github/workflows/_android-apps.yml`) when
`apps/android/` changes.

## An emulator

```bash
sdkmanager "emulator" "system-images;android-35;google_apis;arm64-v8a" "platforms;android-37.0"
avdmanager create avd -n Maktaba_Fold -k "system-images;android-35;google_apis;arm64-v8a" -d pixel_9_pro_fold
emulator -avd Maktaba_Fold
```

The Pixel 9 Pro Fold profile has the fold postures: the emulator's Extended controls → Virtual
sensors → Posture (closed, half open, open), or `adb emu fold` / `adb emu unfold`. An emulator
reaches a home on the Mac's network by its address (`192.168.50.177:8088`).

## How it works

- **Finding a home:** a home announces itself over mDNS as `_maktaba._tcp`
  (`cmd/maktaba-server/internal/mdns`). The home picker lists every `role=home` announcement
  (a cluster's worker machines say `role=node` and are left out) at its web port (`web=` in
  the TXT record; the service's own port is the API's). Saved homes come next, and typing an
  address is the fallback. Before sign-in a home shows the name its machine announces; once
  signed in, it shows the home's own name from `GET /api/auth/me`.
- **Home picker (story 42.2):** Your homes (every home this app knows: rename on this device,
  remove), On this network (NSD; `tls=1` means https, `path=` a web base path; one row per
  home by its id, else host and port), Add a home: by address (checked to be a Maktaba home by
  `GET /api/discovery/home`, or `GET /api/system/version` on an older home; a typed path is
  tried, then the root) or by scanning a QR (CameraX and ZXing, no Play services). A
  `maktaba://connect?id=…&n=…&u=…[&u=…]` link (QR or intent) tries its addresses in order and
  keeps the first that answers as the home it names; one opened from outside always asks
  first. **Security:** a home's id is self-reported; tokens are bound to the exact address
  (`SealedTokenStore` per URL) and never move to another address with the same id: that is a
  new entry and a fresh sign-in. `SameOriginCredentials` drops `Authorization` on any redirect
  that leaves the origin.
- **Sign-in by code (story 42.6):** the sign-in screen has Password and With a code. With a
  code: `POST /api/pairing/device` (this device's name as `device_label`), the code shown
  large with a QR of `verify_url` (else the home's `verify_path`), polling
  `/api/pairing/device/token` until approved, a new code when one runs out (`DeviceSignIn`).
  **Approve a sign-in** is in the account menu of both apps: type or scan the code,
  `GET /api/pairing/device/{code}` shows what is asking (app, platform, device, its name), then
  an explicit Approve (`POST /api/pairing/approve`). An older home without the lookup is
  approved only after a warning to check the code on one's own device.
- **Through Maktaba Cloud (story 42.7):** the picker's Through Maktaba Cloud signs in to the
  cloud (the client's `CloudSession`: `POST /v1/auth/login`, its token in memory only, no
  redirect followed with it), lists the account's homes (`GET /v1/homes`) and opens a linked one
  at its own address, else the relay, signing in with a one-use assertion
  (`POST /v1/homes/{id}/assertion`, then the home's `POST /api/auth/cloud`). A home keeps every
  address it or the cloud gave (`Home.addresses`) and moves to another when the one in use does
  not answer and the same home id answers there; a cloud sign-in is sent only to an address the
  cloud gave (`Home.cloudAddresses`).
- **Camera permission:** asked when the person taps Scan (optional; typing always works), with
  the walkthrough's rules (Android's dialog while it will, else Settings).
- **Local network permission:** on Android 17 (API 37) and later, reaching a home on the
  local network, by mDNS or by address, needs `ACCESS_LOCAL_NETWORK`, a runtime permission
  (Android shows it as «Nearby devices»). Without it, only an internet address works.
- **Permissions walkthrough** (`core/permissions`): the apps' only runtime permission is the
  local network one (no notifications, no background playback or work, no foreground
  service; NSD needs no Wi-Fi permission). `PermissionsGate` shows the walkthrough over the
  app on the first run and again whenever a required permission is missing, read again on
  every resume; «Not now» puts it away until the app next comes back to the foreground. Each
  row asks (`RequestPermission`) while Android still will, else opens the app's settings
  page. On an Android with none of the permissions (before 17) it never shows. The home
  picker keeps an Allow button for the same permission. Help: `/help/android-apps#permissions`.
  `./gradlew :core:connectedDebugAndroidTest` runs its Compose UI tests on a device.
- **Start screens:** Ink & Brass with the logo and the web's library art. Text is set in IBM
  Plex Sans Arabic and headings in Amiri, as on the web. They are in English and Arabic (the
  web's terms, `web/src/i18n`), and Android's per-app language setting lists both. The
  screens after sign-in are still English only.
- **Signing in** as a native client (`X-Maktaba-Client: native`): the home answers with an
  access token (RS256, 15 minutes) and a refresh token in the body. On a 401 the client
  refreshes once and retries; requests that 401 together share one refresh (`ApiClient.refreshAfter`).
  A refresh the home refuses signs out; a home that cannot be reached does not.
- **Token storage:** sealed with AES-GCM under a key in the Android Keystore that never leaves
  it (`KeystoreSecretBox`), one entry per app and home; no backup or device transfer.
- **Playback:** a stream session (`POST /api/stream/sessions`), played by Media3 ExoPlayer. The
  home's web port serves `/stream/` only to a signed-in caller, so ExoPlayer reads through
  OkHttp with `StreamAuthInterceptor`. Each playlist, segment and range request carries the
  access token as it is now, refreshed before it ends, and only this home's `/stream/` gets it.
  The Apple apps need a loopback proxy for this; ExoPlayer does not. A transcode's first
  rendition playlist answers 404 until it is written, so the player waits for it first
  (`ApiClient.waitUntilPlayable`). Progress goes out every 10 s and the session closes when the
  player stops.
- **Layouts** (`core/layout/Panes.kt`, from Jetpack WindowManager's fold through Material's
  adaptive info):
  - one pane: a phone, a foldable folded;
  - the list beside the page: 600 dp and wider (a tablet, a foldable open flat);
  - half folded: split at the hinge. Tabletop puts the upright half on top; book puts the
    halves side by side. The video (or a video's page, or the libraries) goes on the first
    half and the list on the second, as the iPhone Duo Player does. Right to left, the book's
    first half is the right one, cut at the hinge all the same (`bookCut`; the instrumented
    `HingeSplitTest` checks both directions with the hinge off centre).

  The activity handles folding and turning itself, so a playing video keeps playing.
- **Theme:** Ink & Brass from `web/src/styles/tokens.css`: light, dark or the system's (per
  device), and dark while a video plays.
- **Remote:** opens on Needs attention (Retry, Retry all), then Queue (pause and resume a
  stage or everything), Libraries (scan) and Machines (drain). A user who is not the home's
  admin is told the app is for admins. The home enforces every rule; the app's checks are not
  the rule.

## Icons

`core/src/main/res/mipmap-*` hold the adaptive icon's layers from `tools/icons/make-icons.py`,
which copies them here when it runs. The logo (`drawable-nodpi/maktaba_logo.png`) is the web's
`icons/icon-512.png`, and the library art is `web/public/art/library/`.
