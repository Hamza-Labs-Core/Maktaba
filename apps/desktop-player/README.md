# `apps/desktop-player/`: Maktaba Player for Windows and Linux (Epic 42.4)

Compose for Desktop. Browse and watch a home, nothing else: no server inside (that is
`apps/desktop`, the Tauri server app) and no admin pages. On a Mac the player is the Apple app
(`apps/apple`).

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
- **Languages:** English and Arabic (`Strings.kt`), Arabic laid out right to left.

## Releases

`.github/workflows/desktop-release.yml`, the `player` legs: Windows (.msi), Linux x86_64 and
aarch64 (.deb, .rpm), each with its own Java runtime, named `Maktaba-Player_<v>_x64.msi`,
`maktaba-player_<v>_<amd64|arm64>.deb`, `maktaba-player-<v>.<x86_64|aarch64>.rpm`, signed with
`tauri signer sign --app-version`. CI: `.github/workflows/_desktop-player.yml`.
