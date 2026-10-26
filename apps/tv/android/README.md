# Maktaba Android TV

The Android TV / Google TV app (minSdk 28, target 37): a 10-foot Compose-for-TV UI over the
same Maktaba API as every other client, built on the phone apps' shared **core**
([`apps/android/core`](../../android/core)): the API client with one token refresh at a time,
Keystore-sealed tokens, the player's per-request stream token, and finding homes on the
network. One set of versions: this build reads the phone apps' version catalog
(`apps/android/gradle/libs.versions.toml`) plus its own extras (`gradle/tv.versions.toml`:
TV Material, ZXing for the QR).

## What it does

| Screen | What | API |
|---|---|---|
| Choose a home | homes announcing `_maktaba._tcp` on the network (mDNS), saved homes, or a typed address | — |
| Sign in | **a code**: the TV shows `ABCD-1234` and a QR of `<home>/pair?code=…`; a person signed in on a phone or the web approves, and the TV is signed in as them. Fallback: username and password on the on-screen keyboard (and straight there on a home without sign-in by code) | `POST /api/pairing/device`, `POST /api/pairing/device/token` (approved with `POST /api/pairing/approve`, the web's `/pair`); `POST /api/auth/login` (`X-Maktaba-Client: native`) |
| Home | Continue Watching (where you stopped), Recently added, For you; the library scene behind | `GET /api/recommendations`, `GET /api/videos?sort=created_at` |
| Libraries | the libraries you can read, then a library's videos, a page at a time | `GET /api/libraries`, `GET /api/videos?library=` |
| Search | videos by name, as you type | `GET /api/videos?q=` |
| Video | picture, name, length, description; **Resume from 12:34** / Start over, or Play | `GET /api/videos/{id}` (`playback_state`) |
| Player | Media3 HLS / direct play with the system transport; progress every 10 s | `POST /api/stream/sessions` (`start_sec`), `…/progress`, `DELETE …` |
| Account | who, which home; switch home, sign out; English / العربية | — |

**Signing in.** Tokens: a 15-minute RS256 access token and a refresh token, sealed with an
Android Keystore key (no backup, no device transfer). Every request carries the current
token; a 401 takes the core's single refresh and retries once; a refused refresh signs out.

**Playback.** The home's web port answers `/stream/` only with `Authorization: Bearer`. ExoPlayer
reads every playlist and segment through the core's OkHttp stream client, whose interceptor
puts the token as it is *now* on each request (refreshed before it ends, once more on a 401),
so a two-hour film outlives many access tokens. A fresh transcode's rendition playlist answers
404 until it is written: the TV waits for it (`ApiClient.waitUntilPlayable`) before playing. A
resumed video opens its session at the position (`start_sec`; the stream keeps the video's
times) and the player seeks there.

**10-foot UI.** Dark "Ink & Brass" (`ui/theme`), Amiri headings and IBM Plex Sans Arabic text
(the core's fonts, SIL OFL, `assets/licenses/`), 56 dp overscan gutters, TV Material cards
that scale up with a 3 dp gold ring on focus, buttons that turn gold. English and Arabic
(`values-ar`, right to left; the system's per-app language setting lists both).

## Build and test

```bash
cd apps/tv/android
./gradlew :app:testDebugUnitTest :app:lintDebug :app:assembleDebug
```

CI runs the same on every change to this app or the shared core (`_android-tv.yml`); a `v*`
tag builds the release APK (`tv-release.yml`). JDK 17 and the Android SDK (platform 37).

On an emulator: create an Android TV or Google TV AVD (`system-images;android-36;google-tv;arm64-v8a`,
device `tv_1080p`), install `app/build/outputs/apk/debug/app-debug.apk`. The emulator reaches
a home on the LAN by its address, and its mDNS browse finds it too.

## Next steps (not built)

- Assistant voice search and a Home-screen recommendation channel (`androidx.tvprovider`).
- Live TV channels (see `../README.md`).
