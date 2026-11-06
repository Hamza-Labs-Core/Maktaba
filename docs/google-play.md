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
   versionCode is `(MAJOR*1000000 + MINOR*1000 + PATCH)*100 + STEP`. STEP is 99 for a stable
   tag, and lower for its pre-releases, in semver order: `-alpha.N` is N (1 to 32), `-beta.N`
   is 32+N (1 to 32), `-rc.N` is 64+N (1 to 34). So `v0.4.0-rc.1` → `400065`, `v0.4.0-rc.2` →
   `400066`, `v0.4.0` → `400099`, `v1.2.3` → `100200399`. No two tags get the same code (Play
   refuses a code it already has), and every tag's code is higher than the one cut before it.
   All three apps use the same code. MINOR and PATCH must stay under 1000, MAJOR at or under 20
   (Play's limit is 2 100 000 000). Any other pre-release name is refused before the build.
   Until v0.3.54 the code was `MAJOR*1000000 + MINOR*1000 + PATCH` (`v0.3.54` → `3054`, a
   pre-release sharing its version's code). Every new code is above those, so bundles uploaded
   by hand before the change don't block the next one.
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

## Automatic upload to internal testing

Every release tag uploads each signed bundle to its app's **internal** testing track once the
repository variables `PLAY_WIF_PROVIDER` and `PLAY_SERVICE_ACCOUNT` are both set. Nothing else
turns it on (the old `PLAY_UPLOAD` and `PLAY_RELEASE_STATUS` variables are no longer read). The
`play` job of `play-bundles.yml` runs once per app and:

- signs in to Google keylessly, with Workload Identity Federation: GitHub gives the job a
  short-lived OIDC token (the job alone has `id-token: write`), Google's Workload Identity
  provider accepts it only from this repository and trades it for a short-lived token of the
  service account (`google-github-actions/auth`, pinned to a commit). There is no
  service-account key, in the repository or anywhere else;
- reads the app's package name from its Gradle build file (`applicationId`, through
  `tools/ci/play_release.py package <app>`), so it uploads to the package the bundle was built as;
- uploads `maktaba-<app>-<version>.aab` with its R8 `mapping.txt` to the `internal` track,
  release status `completed`, release name the version. It never writes the alpha, beta or
  production tracks. It uses `r0adkll/upload-google-play` (the Play Developer API,
  androidpublisher v3), pinned to a commit like every third-party action here: a maintained,
  widely used action, so the repository carries no API client of its own;
- sets the testers' release notes (whatsnew) in `en-US` and `ar` from the tag's message (below).

Pre-releases are uploaded too, since each tag has its own versionCode. Nothing is uploaded, and
the run still succeeds with one notice, when:

| Notice | Why |
|---|---|
| `Play upload skipped: PLAY_WIF_PROVIDER or PLAY_SERVICE_ACCOUNT not set` | a variable is missing |
| `Play upload skipped: the bundles are unsigned` | the upload key secrets are missing (Play refuses unsigned bundles) |
| `Play upload skipped: vX.Y.Z is published already, ...` | a re-run of a published tag (Play already has its code) |

A real upload error (Play refusing a bundle, a missing permission) fails that app's `play` job,
so it is seen. The two variables are not secret (a resource name and an email); what grants
access is the provider's condition that the token comes from `Hamza-Labs-Core/Maktaba`, and the
service account's Play Console permissions.

### Release notes

The notes come from the annotated tag's message: English first, then a line that is exactly
`--- ar ---`, then the Arabic. Each is cut to Play's 500 characters. An automatic tag
(`auto-tag.yml`, [updates.md](updates.md#how-a-release-is-made)) has only its name as message,
so it gives no notes; a release with notes is tagged by hand, as below.

```bash
git tag -a v0.3.55 -F - <<'NOTES'
Faster search. The TV remote reconnects by itself.
--- ar ---
بحث أسرع. جهاز التحكم في التلفاز يعيد الاتصال وحده.
NOTES
```

A lightweight tag, a message that is only the tag's name, or a missing half gets a standard
line in that language ("Maktaba 0.3.55 for testing. What changed is on the v0.3.55 release
page on GitHub.", and the same in Arabic).

### One-time setup (by the owner)

Each app must already exist in Play Console, with one release uploaded by hand (above). No
service-account key is ever created or downloaded (Google recommends against keys): GitHub
Actions signs in with Workload Identity Federation. Then, once:

1. **Google Cloud project.** In the [Google Cloud console](https://console.cloud.google.com),
   pick or create the project for Maktaba's Play publishing. Note its project ID and project
   **number** (Dashboard → Project info).
2. **Turn on two APIs** in that project: APIs & Services → Library → **IAM Service Account
   Credentials API** → Enable, and **Google Play Android Developer API** → Enable.
3. **Service account, with no key.** IAM & Admin → Service accounts → **Create service
   account** (for example `maktaba-play-upload`). It needs no Google Cloud role. Do not open
   its Keys tab: it never gets a key.
4. **Workload Identity pool and provider.** IAM & Admin → Workload Identity Federation →
   **Create pool**: ID `github`. Add a provider to it: **OpenID Connect (OIDC)**, ID `github`,
   issuer `https://token.actions.githubusercontent.com`, default audience. Attribute mapping
   `google.subject` = `assertion.sub` and `attribute.repository` = `assertion.repository`.
   Attribute condition `assertion.repository=='Hamza-Labs-Core/Maktaba'`, so no other
   repository's token is accepted.
5. **Let this repository act as the service account.** Service accounts →
   `maktaba-play-upload` → Permissions (Principals with access) → **Grant access**: principal
   `principalSet://iam.googleapis.com/projects/<NUMBER>/locations/global/workloadIdentityPools/github/attribute.repository/Hamza-Labs-Core/Maktaba`,
   role **Workload Identity User** (`roles/iam.workloadIdentityUser`).

   Steps 2 to 5 with `gcloud` instead of the console (`PROJECT` is the project ID, `NUMBER`
   its number):

   ```bash
   gcloud services enable iamcredentials.googleapis.com androidpublisher.googleapis.com --project "$PROJECT"
   gcloud iam service-accounts create maktaba-play-upload --project "$PROJECT"
   gcloud iam workload-identity-pools create github --project "$PROJECT" --location global
   gcloud iam workload-identity-pools providers create-oidc github --project "$PROJECT" \
     --location global --workload-identity-pool github \
     --issuer-uri https://token.actions.githubusercontent.com \
     --attribute-mapping 'google.subject=assertion.sub,attribute.repository=assertion.repository' \
     --attribute-condition "assertion.repository=='Hamza-Labs-Core/Maktaba'"
   gcloud iam service-accounts add-iam-policy-binding \
     "maktaba-play-upload@${PROJECT}.iam.gserviceaccount.com" --project "$PROJECT" \
     --role roles/iam.workloadIdentityUser \
     --member "principalSet://iam.googleapis.com/projects/${NUMBER}/locations/global/workloadIdentityPools/github/attribute.repository/Hamza-Labs-Core/Maktaba"
   ```

6. **Play Console access.** [Play Console](https://play.google.com/console) → **Users and
   permissions** → **Invite new users** → the service account's email
   (`maktaba-play-upload@<project>.iam.gserviceaccount.com`). Under **App permissions** add the
   three apps (Maktaba Player, Maktaba Remote, Maktaba TV) and give each **Release to testing
   tracks**, nothing for production. Send the invite.
7. **The two repository variables** (variables, not secrets: neither is secret):

   ```bash
   gh variable set PLAY_WIF_PROVIDER -R Hamza-Labs-Core/Maktaba \
     --body "projects/${NUMBER}/locations/global/workloadIdentityPools/github/providers/github"
   gh variable set PLAY_SERVICE_ACCOUNT -R Hamza-Labs-Core/Maktaba \
     --body "maktaba-play-upload@${PROJECT}.iam.gserviceaccount.com"
   ```

   `PLAY_WIF_PROVIDER` uses the project **number**, not its ID.

The next tag uploads all three apps. Check the run's `google play internal (...)` jobs, then
Play Console → each app → Testing → Internal testing. If Play answers "Only releases with
status draft may be created on draft app", that app has had no release rolled out yet: roll out
one internal release by hand (above), then re-run the failed job. A sign-in error naming the
attribute condition or `iam.serviceAccounts.getAccessToken` means step 4 or 5 does not match
this repository; "The caller does not have permission" from Play means step 6.

To stop the uploads, delete a variable (`gh variable delete PLAY_WIF_PROVIDER -R
Hamza-Labs-Core/Maktaba`). To take the access away for good, remove the Workload Identity User
grant (step 5) or disable the provider, and remove the service account in Play Console → Users
and permissions.

## Build a bundle locally

```bash
cd apps/android            # or apps/tv/android
./gradlew :player:bundleRelease -Pmaktaba.versionName=0.3.55 -Pmaktaba.versionCode=305599
```

Without `ANDROID_UPLOAD_KEYSTORE_FILE` (and the alias and password variables) the bundle is
unsigned. To try a release build on an emulator, sign it with a throwaway key, never the upload
key, and install it with bundletool
(`build-apks --connected-device --ks <throwaway>`, then `install-apks`).
