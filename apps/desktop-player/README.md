# `apps/desktop-player/`: Maktaba Player for Windows and Linux (Epic 42.4, Epic 55)

Compose for Desktop. Watch a home, with the Android and Apple Players' sections (Epic 55): no
server inside (that is `apps/desktop`, the Tauri server app) and no admin pages. On a Mac the
player is the Apple app (`apps/apple`).

## What it has

After sign-in, a rail at the start side (the right in Arabic) with five sections, each with its
own stack of pages (`shell/`: `Navigator`, `Shell`; Back, Alt+Left — Alt+Right in Arabic — and
the mouse's back button):

| Section | Package | What |
|---|---|---|
| Home | `home/` | hero, Continue Watching, On Deck, What's On Now, Favorites, Recently Added, Recently Watched, pinned libraries, For You (Not interested) |
| Libraries | `libraries/` | library cards with pinning, All videos, a library's hub rows, videos with sort and filters, series; the series page |
| Live TV | `live/` | Now, Guide (Earlier/Later), the channel player at home and through a remote session |
| Search | `search/` | suggestions, Titles and In the transcript (Play from), recent searches; Ctrl+F and `/` |
| Profile | `profile/` | account, history, favorites, playlists, sessions, approve a device, playback settings, language, about |

A video's page is `video/`; watching (resume, audio, subtitles, quality, Up next, the keys) is
`Playback.kt`, `Watch.kt` and `watch/`. Every page reads one `DesktopHome`
(`shell/DesktopHome.kt`): the client, the user, the navigation, the posters, the settings and
`play(PlayRequest)`. Every list a person changes (Not interested, a pin, favorite, watched,
forget, end a session, remove a recent search) runs its call in `ReadOrder.saving`: a read that
began before the change does not bring the old state back.

### What it shares with the Android Player (`:client`)

The rule: a class goes into `:client` when it has no Android or Compose import and both Players
need it. Moved there for Epic 55: `core.session.ReadOrder`, `core.browse.homeRows` and
`HomeCache`, `core.live.GuideWindow`, and `core.playback` (the playback settings and their
store, `TrackChoice` and its plan, `QualityOption`, the Up next rule). Compose state stays in
each app.

### Subtitles and audio tracks on JavaFX

JavaFX draws no subtitles and plays an HLS stream's default audio only. `StreamProxy` reads the
session's master playlist (`watch/Hls.kt`): the Audio menu lists its `EXT-X-MEDIA` audio
renditions, and choosing one makes the proxy serve a master where that rendition is the only
audio one and `DEFAULT=YES`; the same session reopens at the same moment. A direct MP4 with
several tracks opens as HLS (`audio_track`, `format=hls`) when another track is chosen. The
Subtitles menu lists the subtitle renditions; the proxy fetches the chosen playlist and its
WebVTT files with the token, `watch/Vtt.kt` reads the cues, and the cue for the current time is
drawn in a JavaFX `Text` over the `MediaView`, inside the JavaFX scene (`VideoSurface.kt`).

### The capabilities gate

`-Pmaktaba.capabilitiesGate=off` builds a Player that shows every feature (issue #638), as the
Android Player's switch: the property is written into `BuildInfo.CAPABILITIES_GATE` and read by
`buildCapabilitiesGate()` ("off", "no", "false" or "0" turn it off). Release builds pass
nothing, so the gate is on. While signed in, the window follows `GET /api/capabilities` (at
sign-in, then every five minutes): a feature that is off is hidden from a viewer and explained to
an admin with a link to the home's Components page (`shell/FeatureOff.kt`). The home's
`409 component_off` still answers either way.

It is built on the Android apps' portable client, `apps/android/client` (included as
`:client`, like the Android TV build includes `:core`): the API client, sign-in by password and
by code, token sealing, the home picker's rules (`foundHome`, `dedupeHomes`, `maktaba://connect`
links), approving a sign-in, and the stream's authorization. The fonts come from the Android
core's `res/font`.

## Build and run

JDK 17.

```bash
cd apps/desktop-player
./gradlew test                     # its tests (and :client:test for the shared client's)
./gradlew :test --tests '*CapabilitiesGateTest*' -Pmaktaba.capabilitiesGate=off   # the gate switched off
./gradlew run                      # the app, on this machine
./gradlew packageMsi               # Windows: build/compose/binaries/main/msi/
./gradlew packageDeb packageRpm    # Linux: .../deb/, .../rpm/ (needs rpm and fakeroot)
```

`-Pmaktaba.version=0.3.35` stamps the version (the installers take its numeric core);
`-Pmaktaba.updaterPubkey=<base64>` embeds the apps' updater key (the repository variable
`TAURI_UPDATER_PUBKEY`). Without a key the player still checks for updates but installs none.

## How it works

- **Video:** JavaFX's media stack (H.264 and AAC, as MP4 or HLS). Sessions open with the
  streaming service's `desktop-player` profile (1080p at most), so anything else (HEVC, mkv,
  4K) is remuxed or transcoded by the home. JavaFX sends no header, so `StreamProxy` serves the
  stream on `127.0.0.1` under a random path and adds the user's current token to each request
  to the home's `/stream/` (the client's `StreamAuthInterceptor`: refreshed before it runs out,
  once more on a 401). Only that home's `/stream/` ever gets the token; a playlist naming the
  home's address is rewritten to the proxy's.
- **Finding a home:** JmDNS on every interface (`_maktaba._tcp`, `role=home` only, one row per
  home), saved homes, an address or a `maktaba://connect` link (typed, pasted, or the first
  command-line argument, which is always confirmed first). Tokens stay bound to the exact
  address.
- **Tokens:** sealed per home: Windows' DPAPI (this Windows user), elsewhere AES-GCM with a key
  in a file only this user can read. Settings and homes are in `%APPDATA%\Maktaba Player` or
  `$XDG_CONFIG_HOME/maktaba-player`.
- **Big screen** (Epic 46's couch mode, natively): F11 or the menu; full screen, everything
  1.75× larger, the arrows move the focus, Enter opens, Escape closes the video. "Start in big
  screen" is remembered.
- **Updates:** `latest-player.json` on the latest release (written by
  `tools/release/cmd/updater-feed --family player`), the platform keys
  `windows-x86_64-player[-msi]`, `linux-<arch>-player[-deb|-rpm]`. The download is checked with
  the minisign signature the release made with the apps' key (`Minisign.kt`, as
  `tools/release/updaterfeed/verify.go`), version included, before anything runs. Windows runs
  the per-user .msi; Linux saves the checked package for the person's installer.
- **Crash reports and Report a problem (Epic 53, `diagnostics/Diagnostics.kt`):** an uncaught
  exception is kept under the config folder's `crashes/` (its deepest cause's class and frames,
  never its message; the newest five). After signing in to a home the player asks once, with
  two equal buttons, whether to send crash reports there; the **Account** menu keeps the
  switch, off until the person says yes. When on, a kept crash goes to the home's
  `POST /api/diagnostics/envelope` as a scrubbed Sentry envelope (the Android apps' `client`
  code: `Envelope`, `CrashOutbox`, the shared `Redact`). **Help → Report a problem…** shows
  what it will send first (the device line, the last requests, the kept crashes as a tick box)
  and says the report's number; **Help → My reports** shows the admin's reply. A desktop window
  cannot be captured without the system's screen recording, so no screenshot is offered (as the
  web app's form).
- **Languages:** English and Arabic, Arabic laid out right to left. `Strings.kt` holds the start
  screens' words and merges one table per area (`<area>/<Area>Strings.kt`); `StringsTest` fails
  when a key has no Arabic, the placeholders differ, or a key is defined twice.

## Tests

`./gradlew test` runs the unit tests (each state class against MockWebServer, with `:client`'s
test fixtures; `TestHome` is the mock home), the `ReadOrder` latch tests (a read held open, a
change, then the old answer), the `StreamProxy` rules, and the screen tests: Compose for
Desktop's `runComposeUiTest`, off screen (no window, no display), in English and Arabic. Nothing
calls a real host; nothing opens JavaFX.

## Releases

`.github/workflows/desktop-release.yml`, the `player` legs: Windows (.msi), Linux x86_64 and
aarch64 (.deb, .rpm), each with its own Java runtime, named `Maktaba-Player_<v>_x64.msi`,
`maktaba-player_<v>_<amd64|arm64>.deb`, `maktaba-player-<v>.<x86_64|aarch64>.rpm`, signed with
`tauri signer sign --app-version`. CI: `.github/workflows/_desktop-player.yml`.
