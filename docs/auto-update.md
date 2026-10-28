# Auto-update: the desktop and mobile apps (Epic 28)

Maktaba keeps itself current from **GitHub Releases** (`Hamza-Labs-Core/Maktaba`). This page
covers the apps: the desktop app's own updater, the mobile apps' update banner, the evidence a
desktop app keeps when an update fails, and the one-time signing-key setup the desktop updater
needs. **The server and the cluster** — the quiet window, Update now, what is verified, TV during
an update, the machines, restore, the settings — are in [updates.md](updates.md).

## How it works

| Surface | Mechanism |
|---|---|
| All binaries | Version/commit/build-date stamped at compile time; reported at `GET /api/system/version` (`{version, commit, build_date, channel}`). |
| Server | Checks GitHub Releases and serves `GET /api/system/updates`; installs each release by itself in the quiet window, the home first, then every machine ([updates.md](updates.md)). |
| Server (admin) | Admin -> Updates (`/admin/updates`): Update now, Check now, Skip, Pause, a run's steps on the home and on each machine, Retry, Skip, Roll back, the backups and Restore ([updates.md](updates.md#update-now-skip-defer-pin)). |
| CLI | `maktaba-server update` reads the same feed and verifies with the same code (`--check`, `--version vX.Y.Z`, `--rollback`). |
| Desktop (Tauri) | Built-in updater reads the Release's `latest.json`, applies on relaunch (signature-verified). An app that runs the server stages the release the home's update orders and installs it by itself at the home's go (no click there); a client-only app keeps its own "restart to install" badge. |
| Mobile | The sideloaded Android app: an in-app banner → the release's `.apk`; store builds are updated by their store ([below](#the-mobile-apps-update-banner-story-285)). |

## Channels

- **stable** (default) — ignores prereleases.
- **beta** — includes `-beta`/`-rc` prereleases.

A build's channel is derived from its version string (a prerelease suffix ⇒ `beta`), overridable
with `MAKTABA_UPDATE_CHANNEL` on the server ([updates.md](updates.md#channels)). Switching
`beta`→`stable` never downgrades.

## The mobile app's update banner (story 28.5)

The sideloaded Android app (the web app in Capacitor, installed from the release's `.apk`) cannot
update itself, so it says when a newer release exists and where to get it. At launch, at most once a
day (Settings → About → App updates: daily, weekly or never), it asks the server it is connected to
(`GET /api/system/updates`, the server's channel), or GitHub's latest stable release when no server
answers (a phone never follows beta by itself; a server whose update checks are off is followed, and
then no banner shows). When the release is newer than the app, a banner says "New version available
— vX.Y.Z" with **Get update** (the release's own `.apk`, at GitHub's download path for that tag;
the release page when the release lists none, as an unsigned build attaches none) and the release
notes. **Dismiss** hides it until a newer version ships. Offline the check is skipped and tried again
at the next launch; GitHub's rate limit backs it off until its reset (a stored time that is not a
number, in the future, or a backoff over a day never stops the checks). The app never downloads a
binary itself.

A store build shows no banner: the Play Store updates the AAB's build and the App Store every iOS
build, each only once its review is through, and no store says cheaply and reliably which version it
carries, so a banner could point at a version the store does not offer yet. The release's mobile
builds carry the tag's version and their kind (`mobile-release.yml`: the APK's web bundle is the
sideload build, the AAB's the store build).

## The desktop apps' own updates

- **Every app shows its update**, the ones that run no server too (the Intel Mac app, an app
  connected to another Maktaba): the menu's update item and its badge. The Windows portable
  (no installer beside it) offers "download the new zip" and opens the release page.
- **One install at a time**, with a 45-minute download limit, a stall watchdog (reported as
  `no_network`) and the progress in the update item.
- **On a Mac**, an ordered install checks, without prompting, that the app can change itself:
  `translocated` first, then a read-only volume (`not_writable`), `needs_admin`, and
  `app_management` (App Management refused, or the bundle locked). It never reaches a password
  prompt. The server's login item needs a signed bundle (ad hoc is enough) and is bound to the
  build that registered it, so each new build registers it again at its first start.
- **On a Mac**, the server is stopped before the app's bundle is swapped (no old process reads
  the new files), and started again on the release it had when the install fails.
- **On Windows**, an installer that does not finish after the app closed is recorded at the next
  start (`installer_not_finished`); an unattended home's server is started again on the release
  it had once the installer is gone (15 minutes at the soonest, 2 hours at most), and the home's
  run is told at once. The installer removes the old payload only when the server stopped.
- **On Linux**, an app whose server an updater will order never switches the server to its own
  newer payload (a newer AppImage, an apt upgrade): a node's home orders it, and so does a home
  whose automatic updates are on (its `/api/cluster/self` says `updates_auto`, to callers on
  the same machine only). A standalone server with automatic updates off takes the app's newer
  payload as before. A server that does not say (it is not answering yet when the app starts,
  or it is older) goes by its last word, kept in `updates/app-updates-auto.json`; with none
  kept, the payload is left to the server's updater when it has run a release there
  (`updates/journal.json`).
- **A release becomes latest** only once every desktop platform is in its verified update feed
  (`updater-feed --require desktop`, each signature checked against the public key): a release
  missing one is published but not offered as latest.

## A Mac or PC app built locally cannot update itself

An app built on the machine (`make mac-app`, a local `tauri build`) carries the placeholder
updater key and the version 0.1.0: it can verify no release, so it cannot take one. It says so
instead of failing: its menu, the home's Updates page and an ordered run read `local_build`,
"this app was built locally and can't update itself: install the release by hand once". Such a
run is no failure of the release: it counts no attempt and blocks nothing, and the rest of the
cluster updates as usual. To bring that machine onto the release track, install the next
release's app by hand once (on a Mac, the `.dmg` from the GitHub release, dragged over the app in
Applications; its data and settings stay). From then on it updates itself like any other.

Note for the Mac home that reports 0.3.0-rc.7: no Apple-Silicon app was built for rc.7 or 0.3.1
(their release failed to notarize the ad-hoc app), so an "rc.7" Mac home is most likely such a
local build: install the next release's `.dmg` once.

## When a desktop app's update fails

Admin → Updates shows each machine's last attempt with its reason, and the app says the same in its menu and an OS notification, which open the logs page, or the folder with `app.log` and `server.log` when the server does not answer ([updates.md](updates.md#when-an-update-fails-where-it-says-why)). The reasons are listed in [update-attempts.md](../specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md), and what to do for each in the [runbook](cluster/runbook-updates.md).

### Where the evidence is on each machine

The attempts, the logs and the database step of a desktop app's server:

| | macOS | Windows | Linux app |
|---|---|---|---|
| the attempts | `~/Library/Application Support/Maktaba/updates/app-attempts.json`, `server-attempts.json` | `%LOCALAPPDATA%\Maktaba\updates\` (the same two) | the app's data folder, `updates/` |
| the app's log | `~/Library/Logs/Maktaba/app.log` | `%LOCALAPPDATA%\Maktaba\logs\app.log` | the app's logs folder, `app.log` |
| the server's log | `~/Library/Logs/Maktaba/server.log` | `%LOCALAPPDATA%\Maktaba\logs\` | the app's logs folder |
| the database step | `~/Library/Application Support/Maktaba/state/database.json`; the backup before an update that migrates: `backups/pre-<from>-to-<to>.dump` (a folder: one file per table) | the same under `%LOCALAPPDATA%\Maktaba` | the same under the data folder |
| the launcher | `~/Library/Application Support/Maktaba/updates/launcher.log` | `%LOCALAPPDATA%\Maktaba\updates\launcher.log` | the runtime's `updates/launcher.log` |

On a Mac, the server's login item and code signing show in the system log:
`log show --last 2d --style compact --predicate 'process == "Maktaba" OR eventMessage CONTAINS[c] "com.hamzalabs.maktaba"'`,
`launchctl print gui/$(id -u)/com.hamzalabs.maktaba.server`, and
`codesign -dv --verbose=2 /Applications/Maktaba.app`. An app with no code signature at all
cannot start its server's login item (`login_item_unsigned`): 0.3.0's Mac app is such an app
(built from the 0.3.0-rc.6 commit, before the ad-hoc signing of the bundle). 0.3.0-rc.7 and
0.3.1 have no Apple-Silicon app at all (their release could not build it: tauri tried to notarize
the ad-hoc app with empty Apple settings), so their update feeds offer a Mac nothing. Until the
next release, only
`codesign --force --deep -s - --identifier com.hamzalabs.maktaba /Applications/Maktaba.app`
mends an installed 0.3.0; the next release's app is signed ad hoc and updates as usual. Also
worth a look on the Mac: `~/Library/Application Support/com.hamzalabs.maktaba/mode.json` (server
or client), `sfltool dumpbtm` (the login items macOS knows),
`log show --last 2d --predicate 'eventMessage CONTAINS "Codesigning failure"'`,
`xattr -l /Applications/Maktaba.app`, and a leftover `$TMPDIR/tauri_current_app*` or
`tauri_updated_app*` (an update the updater left half done).

## Desktop updater signing (one-time)

Tauri's updater requires a key pair. Both halves live in the repository
settings, never in a commit: the **private** key and its passphrase are
the secrets `TAURI_SIGNING_PRIVATE_KEY` and
`TAURI_SIGNING_PRIVATE_KEY_PASSWORD`, the **public** key is the variable
`TAURI_UPDATER_PUBKEY`. `desktop-release.yml` stamps the variable into
`plugins.updater.pubkey` at build time, so the committed
`apps/desktop/src-tauri/tauri.conf.json` keeps the placeholder
`REPLACE_WITH_PUBKEY`.

```bash
# On a trusted machine, generate the key pair:
cd apps/desktop
pnpm exec tauri signer generate -w ~/.tauri/maktaba.key

#  → prints the PUBLIC key  → repository variable TAURI_UPDATER_PUBKEY
#  → writes the PRIVATE key → repository secret TAURI_SIGNING_PRIVATE_KEY
#    and its passphrase as:   TAURI_SIGNING_PRIVATE_KEY_PASSWORD
```

`desktop-release.yml` reads those secrets so `tauri build` signs each
updater bundle (`.sig`). Its `publish` job, after every platform's build
and checks, attaches the bundles that passed to the Release and writes
`latest.json` (the updater feed, `tools/release/cmd/updater-feed`) over
them. A fork **without** the secrets still produces unsigned installers
and no `latest.json` — the build stays green.

The private key without the variable fails the build, since no app could
verify its updates. An app checks each update against the public key it
was built with. So apps update themselves from the first tagged release
built with the key on; an app installed from an earlier release carries
the placeholder, can verify no update, and moves to a signed release by
one download by hand.

**Key rotation:** build one release with the new public key in
`TAURI_UPDATER_PUBKEY` while the secrets still hold the old private key.
Installed apps verify that release with the old key, and once installed
it verifies the next ones with the new key. Then put the new private key
in the secrets.

> Note: the updater signature is independent of OS code-signing
> (Apple notarisation / Windows Authenticode), which remains the deferred
> TODO in `desktop-release.yml`.
