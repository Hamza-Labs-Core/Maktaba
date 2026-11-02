# Google Play: the native Android apps

Every `v*` release tag builds the three native Android apps as signed App Bundles (`.aab`) and
attaches them to the tag's GitHub Release. The workflow is `.github/workflows/play-bundles.yml`.
Google Play's internal testing track is Play's TestFlight (see [testflight.md](testflight.md)
for the iPhone apps).

| App | Release file | Package | Source |
|---|---|---|---|
| Maktaba Player | `maktaba-player-<version>.aab` | `com.hamzalabs.maktaba.player` | `apps/android/player` |
| Maktaba Remote | `maktaba-remote-<version>.aab` | `com.hamzalabs.maktaba.remote` | `apps/android/remote` |
| Maktaba TV | `maktaba-tv-<version>.aab` | `com.hamzalabs.maktaba.tv` | `apps/tv/android/app` |

Each app is **one package for every screen**. Play treats each package name as a separate app,
so the Player and the Remote are no longer split into `.foldable` and `.tablet` packages
(removed 2026-11-01). The layout follows the window on phones, foldables and tablets, as it
always did.

The Capacitor wrapper's `maktaba-<version>.aab` (`mobile-release.yml`, `apps/mobile`) is a
different, older app. It is not one of these.

## How it works

1. The tag guard runs first, as in every tag workflow.
2. Version: `tools/release/play-version.sh`. The versionName is the tag without its `v`. The
   versionCode is `MAJOR*1000000 + MINOR*1000 + PATCH` (`v0.3.45` → `3045`, `v1.2.3` →
   `1002003`). All three apps use the same code, and every tag's code is higher than the one
   before it. MINOR and PATCH must stay under 1000. The highest code allowed is Play's
   limit, 2 100 000 000 (`v2100.0.0`). A pre-release such as `v0.4.0-rc.1` gets the same code
   as `v0.4.0`, so it is never uploaded to Play automatically.
3. Gradle builds `bundleRelease` for each app, with R8 on. CI's Android gates build the same
   bundles, unsigned, on every pull request that touches the apps, so an R8 break shows up there
   and not on the tag.
4. Signing: Gradle signs with the **upload key** when the environment names it (the apps'
   `signingConfigs { "upload" }` in `build.gradle.kts`). No key is in the repository. The
   workflow writes the keystore to the runner's temp folder for the build and deletes it
   afterwards.
5. Signed bundles are checked with `jarsigner -verify`, then attached to the Release with a
   `maktaba-play-<version>.sha256` file. Without the secrets the bundles are unsigned. They are
   named `-unsigned.aab`, kept only as the `maktaba-play-bundles` workflow artifact (with R8's
   `mapping.txt` files), and the run shows a warning. Unsigned bundles are never attached to the
   Release.

Google Play App Signing re-signs what reaches users with Google's app signing key. The upload
key only proves that an upload came from us. If the upload key is lost, Play Console can reset
it.

## Secrets

| Secret | What it is |
|---|---|
| `ANDROID_UPLOAD_KEYSTORE_BASE64` | the upload keystore, base64 |
| `ANDROID_UPLOAD_KEY_ALIAS` | the key's alias in it (`upload` below) |
| `ANDROID_UPLOAD_KEY_PASSWORD` | the key's password |
| `ANDROID_UPLOAD_STORE_PASSWORD` | optional: the keystore's password, if it is not the key's |

These are not the `ANDROID_KEYSTORE_*` secrets that `tv-release.yml` and `mobile-release.yml`
use to sign sideloaded APKs.

## Make the upload key (once, by the owner)

On your own Mac, outside the repository. Keep the file and its password in your password
manager; they never go in the repository or in chat.

```bash
keytool -genkeypair -v -storetype PKCS12 -keystore maktaba-upload.p12 \
  -alias upload -keyalg RSA -keysize 4096 -validity 10000 \
  -dname "CN=Maktaba, O=Hamza Labs"
```

A PKCS12 keystore has one password for the store and the key, so
`ANDROID_UPLOAD_STORE_PASSWORD` can stay unset. Then add the secrets (each command asks for
the value, or reads it from the file):

```bash
base64 -i maktaba-upload.p12 | gh secret set ANDROID_UPLOAD_KEYSTORE_BASE64 -R Hamza-Labs-Core/Maktaba
gh secret set ANDROID_UPLOAD_KEY_ALIAS -R Hamza-Labs-Core/Maktaba --body upload
gh secret set ANDROID_UPLOAD_KEY_PASSWORD -R Hamza-Labs-Core/Maktaba
```

The next tag attaches signed bundles.

## The first upload of each app (by hand)

Play Console creates an app from its first bundle, and the API can't do that step. So each app's
first upload is done by hand:

1. In [Play Console](https://play.google.com/console), **Create app** for each of the three
   apps (name, default language, app or game, free).
2. Download its `.aab` from the release page
   (`https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/vX.Y.Z`).
3. **Testing → Internal testing → Create new release**. Accept Play App Signing (Google
   manages the app signing key), upload the `.aab`, name the release with its version, and
   **Save**, then **Review release** and **Start rollout to Internal testing**.
4. **Testers**: add a list of testers' Google accounts and share the opt-in link. Testers
   install the app from the Play Store after they opt in.

The TV app must use the **Android TV** form factor (Release → Setup → Advanced settings → Form
factors) and needs a TV banner and screenshots before Play will review it for TVs. Internal
testing works without that review.

## Turn on automatic upload to internal testing

Once each app exists in Play Console, a stable tag (no `-rc`) can upload all three apps to
internal testing by itself:

1. Google Cloud: create a service account and a JSON key for it, and turn on the **Google Play
   Android Developer API** in its project.
2. Play Console → **Users and permissions** → invite the service account's email, with
   access to the three apps and the **Release to testing tracks** permission.
3. Add the secret and turn the switch on:

   ```bash
   gh secret set PLAY_SERVICE_ACCOUNT_JSON -R Hamza-Labs-Core/Maktaba < service-account.json
   gh variable set PLAY_UPLOAD -R Hamza-Labs-Core/Maktaba --body true
   ```

The `play` job then uploads each signed bundle with its `mapping.txt` to the `internal` track
(`r0adkll/upload-google-play`, pinned to a commit). If Play says the app is still a **draft
app** ("Only releases with status draft may be created on draft app"), set
`gh variable set PLAY_RELEASE_STATUS --body draft` and roll each release out by hand until the
app is out of draft. Then delete the variable. The job never runs for a pre-release, for a
re-run of a published tag, or for unsigned bundles. If `PLAY_SERVICE_ACCOUNT_JSON` is missing,
it warns and uploads nothing.

To turn it off: `gh variable set PLAY_UPLOAD --body false`.

## Build a bundle locally

```bash
cd apps/android            # or apps/tv/android
./gradlew :player:bundleRelease -Pmaktaba.versionName=0.3.45 -Pmaktaba.versionCode=3045
```

Without `ANDROID_UPLOAD_KEYSTORE_FILE` (and the alias and password variables) the bundle is
unsigned. To try a release build on an emulator, sign it with a throwaway key, never the upload
key, and install it with bundletool
(`build-apks --connected-device --ks <throwaway>`, then `install-apks`).
