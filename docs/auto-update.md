# Auto-Update (Epic 28)

Maktaba keeps itself current from **GitHub Releases**
(`Hamza-Labs-Core/Maktaba`). This page covers operator configuration, the
update channels, and the one-time signing-key setup the desktop updater
needs.

## How it works

| Surface | Mechanism |
|---|---|
| All binaries | Version/commit/build-date stamped at compile time; reported at `GET /api/system/version` (`{version, commit, build_date, channel}`). |
| Server | Background poller hits the GitHub Releases API, caches the result, serves `GET /api/system/updates`. |
| Server (automatic) | The home installs a new signed release in its quiet window, then every joined machine, one at a time (Epic 35: [the whole cluster](#automatic-updates-of-the-whole-cluster-epic-35), below); a desktop app that runs the server stages it and installs it when someone clicks there. |
| Server (admin) | Admin -> Updates (`/admin/updates`): Update now, Check now, Skip, pause; a run's steps on the home and on each machine, Cancel (before the home switches), Retry, Skip and Roll back a machine, Resume a held rollout. `POST /api/admin/system/update {"confirm":true}` is Update now. |
| CLI | `maktaba-server update` reads the same GitHub Releases feed and verifies with the same code (`--check`, `--version vX.Y.Z`, `--rollback`). |
| Desktop (Tauri) | Built-in updater reads the Release's `latest.json`, applies on relaunch (signature-verified). An app that runs the server stages the release the home's update orders and installs it on a click there (below); a client-only app keeps its own "restart to install" badge. |
| Mobile | In-app banner → App Store / Play Store / `.apk` (apps can't self-update; [below](#the-mobile-apps-update-banner-story-285)). |

## Channels

- **stable** (default) — ignores prereleases.
- **beta** — includes `-beta`/`-rc` prereleases.

A build's channel is derived from its version string (a prerelease
suffix ⇒ `beta`), overridable with `MAKTABA_UPDATE_CHANNEL`. Switching
`beta`→`stable` never downgrades a running server.

## The mobile app's update banner (story 28.5)

A store app cannot update itself, so the iOS and Android app (the web app in Capacitor) only says
when a newer release exists and where to get it. At launch, at most once a day (Settings → About →
App updates: daily, weekly or never), it asks the server it is connected to
(`GET /api/system/updates`, the server's channel), or GitHub's latest stable release when no server
answers (a phone never follows beta by itself; a server whose update checks are off is followed, and
then no banner shows). When the release is newer than the app, a banner says "New version available
— vX.Y.Z" with **Get update** (the App Store on iOS, the Play Store for the Play build, the `.apk` on
the release for the sideloaded build) and the release notes. **Dismiss** hides it until a newer
version ships. Offline the check is skipped and tried again at the next launch; GitHub's rate limit
backs it off until its reset. The app never downloads a binary itself.

The release's mobile builds carry the tag's version and their kind (`mobile-release.yml`: the APK's
web bundle is the sideload build, the AAB's the store build); the iOS App Store page comes from the
`IOS_APP_STORE_URL` repository variable (the release page while it is unset).

## Server configuration (env)

| Variable | Default | Meaning |
|---|---|---|
| `MAKTABA_UPDATE_CHANNEL` | derived from version | `stable` or `beta`. |
| `MAKTABA_UPDATE_INTERVAL_SEC` | `86400` | Background check cadence; `0` disables the poller. |
| `MAKTABA_UPDATE_DISABLE` | unset | `1`/`true` = no network checks at all. |
| `MAKTABA_UPDATE_REPO` | `Hamza-Labs-Core/Maktaba` | Retarget a fork's releases. It changes the expected signing identity too (the fork's own `release.yml`); it is a startup setting, no API field can change it. |
| `MAKTABA_DATA_DIR` | `maktaba-server`'s `data_dir`; run on its own, `/var/lib/maktaba` as root, else `$XDG_DATA_HOME/maktaba` (`~/.local/share/maktaba`) | The Sigstore trusted root refreshed through TUF is kept in `<data_dir>/sigstore/`. |
| `MAKTABA_GITHUB_TOKEN` | unset | Optional; raises the GitHub API rate limit. |

## Automatic updates of the whole cluster (Epic 35)

The home checks GitHub every 6 hours. A verified release newer than the cluster's
(`rules.updates`: channel, pin, skipped versions, deferral, release age, the quiet window,
default `03:00-05:00`) is installed with no one at a keyboard on headless and plain server
installs; a desktop app that runs the server (Maktaba.app, the Windows or Linux app) gets it
staged and installs it when someone clicks "Install" there (the steps below wait for that
click):

1. **The home prepares** it: downloads and verifies it (the signature, every file),
   stages it, runs it once (`--version`), and rehearses its migrations on a scratch copy
   of the database (a migration that fails on the real data stops here; nothing changed).
2. **It waits for its moment**: inside the window. A plain server install's TV keeps
   playing across its restart (the front door and the TV slots are handed over to the new
   supervisor), so it waits for nothing more; any other install restarts live TV with it, so
   it waits for nobody to be watching (`tv_staging.quiet_max_viewers`, 0). The cluster's jobs
   are drained.
3. **The home switches** to it and restarts. The new release's start dumps the database
   (`backups/pre-<old>-to-<new>.dump`, checked to read back), migrates it, and puts the
   dump back if the migrations fail. Its health is watched (every role running, the API
   ready and answering as the new version) for 10 minutes; then it is kept.
   Meanwhile the TV moves to the new release's streaming software the way any TV change
   goes on the air (Live TV → Changes): a change of the streaming binary alone, staged next
   to what airs, checked, and swapped at programme boundaries, without waiting for an
   approval (only this updater's binary change skips it; every other change still waits for
   one). The release is kept once that change is on the air. If its check or its swap
   fails, the release rolls back (blocked, the failing checks in the notice); if it waits
   behind a change awaiting your approval until the window ends, the next window tries
   again. A rollback for any reason takes the TV back too.
4. **Every machine follows**, after the home: they download and verify in parallel, then
   switch one at a time, each drained of its jobs for its own switch and watched before the
   next (`node_failure_policy`, default `hold_platform`: one that fails holds the others of its
   platform). A machine offline meanwhile is caught up when it comes back (`catch_up`:
   `immediate` any time, except a machine carrying TV streams; `window` only inside the
   window), and one whose part failed for a reason since mended is caught up too. A machine the
   new release's schema paused is caught up at once. A desktop app's machine
   waits for its click while the others go; its click takes the next turn. A run that waits
   only for clicks gives way to a newer release.

A release that does not stay healthy is **rolled back**: the previous release runs again.
When it had already migrated the database, the dump taken before its migrations is put
back first (every release refuses a database a newer one migrated); writes made between
that dump and the rollback are lost — minutes, inside the window, with the jobs drained —
and the failed state is dumped aside (`backups/failed-<version>-<time>.dump`). The release
is then blocked until a newer one appears or an admin unblocks it (Admin → Updates → "Try it
again", or "Update now" naming it). A power cut at any step is recovered at the next start
from the update's journal (`updates/journal.json`).

**Which release is newer.** One rule for every updater (the home's choice, the plain server,
the runtime, the apps' server payload, the `update` commands): a release numbered higher but
signed before the one running here, on the same major.minor line, is an older build (an rc of a
version already released, tagged after it) and is passed by: not a downgrade, not a replay
alarm, no notice (`older_build`). A higher major.minor always counts as newer, so a hotfix on an
older minor still moves up. The same version with other checksums stays a refused replay. A
release seen before its signed files were attached (they come a few minutes after it is
created) is verified as soon as GitHub lists them. A download that fails (HTTP, a timeout, the
disk) is `download_failed`: retried twice at once, then as any failure that may pass, never a
verification failure that blocks the release.

**When a run cannot finish:**
- **A failure that may pass** (a download, an install that did not take) is tried again at
  once, then 15 minutes later, then in the next window; each window counts one attempt.
- **A failure that needs a person** (no app in the release for that machine, not enough disk, an
  install that is not self-managed, an app that cannot change itself or was built locally, a
  trust root that expired, the Microsoft Store's `python3` alias where the pipeline needs a real
  Python) starts no new run until the next window or an admin acts, and is told once.
- **A release that bundles another PostgreSQL major** than the one a machine runs is never started
  on its data: the release says each app's major (`release.json`'s `app_postgres_major`, signed),
  so a runtime install and an app's install refuse it before anything is installed
  (`pg_major_change`, needs a person, the release not blocked). A release from before that field
  is refused by the start's database step, which halts and goes back.
- **Only a verification failure or a rehearsal that failed on the real data** blocks a release;
  a rehearsal that could not run (its dump, scratch database or restore) is an attempt.
- **A home whose window always has a viewer** (or that is always outside it) is told after three
  windows ("stuck", `update.stuck`), and the page offers "Install now".
- The page shows what the scheduler waits for ("Not started now: …"), and a machine its new
  release's schema pauses is told (`update.node_paused_schema`).
- The dumps are kept to `rules.updates.backups_keep`; scratch copies a crash left behind go
  after an hour.

How each kind of install takes a release
([install-kinds.md](../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md)):

| Install | Automatically | Still needs a person |
|---|---|---|
| **Maktaba.app** (Mac) | The server orders the release from the app (`updates/app-install.json`, or starts it with `maktaba://install-update`); the app downloads and checks exactly that release from its own updater feed and shows it ("Install Maktaba <v>"). After the click, the home's go: the app installs what it staged and restarts the server. Before, the server keeps a copy of the payload it ran, which the launcher runs if the new one cannot stay up. | **The click** in the app (owner decision D1). The first update from a release before automatic updates (the app's "Install update"). An app in a folder this user cannot change (`/Applications` for a standard user): reported as needing an administrator, nothing changed. |
| **Windows app** | The same; the installer stops and starts the server. | The click. The first update from a release before automatic updates. |
| **Linux app** (AppImage, `.deb`, `.rpm`) | The server installs the release's signed Linux payload as a new runtime directory, and switches to it after the click in the app (no root needed); `previous` is kept for the rollback. | The click. The first update from a release before automatic updates. The app's own window updates with its updater. |
| **Headless** (`install.sh --app`) | The same runtime switch, by itself in the window. | The first update from a release before automatic updates. |
| **Plain server** on the side-by-side layout (`setup --self-managed`, `.deb`/`.rpm` in the layout) | Staged in `bin/releases/<v>` with its migrations and its own pipeline environment (`venv`, built from the release's wheel with the machine's Python 3.12 and the extras its environment had), switched to under the launcher's guard; the pipeline runs the new environment. A release that needs a newer launcher replaces it after its commit. | A Python 3.12 for the pipeline (or `MAKTABA_PIPELINE_PYTHON`), and access to PyPI (or a mirror in `PIP_INDEX_URL`, or a proxy). |
| Plain binary copied by hand (no launcher) | — (reported: `not_self_managed`) | One `maktaba-server setup --service`, then automatic. |
| **Worker-only node** (the Epic 31 installer) | Made a full node by its next update: on its turn in the window the home sends its worker `update.convert`; the worker downloads the release, verifies it itself (the pinned identity, `checksums.txt`) and runs that release's `setup --node --converting`, which installs the side-by-side layout and the launcher's service and removes the old worker units once the node answers. Automatic from then on. | A worker whose pipeline predates the command ("worker-only — re-run the installer once"). |
| **Docker** (`ghcr.io/hamza-labs-core/maktaba-server`, [cloud/installers/docker](../cloud/installers/docker/docker-compose.yml)) | The headless runtime switch, on the image's volume, by itself in the window: the image only puts its payload there the first time (or when a newer image is pulled; never an older one), and the server updates it from then on. | Nothing (no image to pull, no Docker socket). |
| A container of the stack built from source (`deploy/compose`) | — | `docker compose pull && docker compose up -d` (Epic 28.3's `409`). |

Automatic installs are on by default (`rules.updates.auto_install`); with them off (Admin ->
Updates -> Pause) the home only checks and notifies. `MAKTABA_UPDATE_DISABLE=1` turns the
checks off altogether.

## When an update fails: where it says why

Every update attempt is kept with its reason
([update-attempts.md](../specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md)):
the desktop app's own updater (its badge, its menu, the home's order), a release's first start
(the database step, the pipeline's environment), and the cluster's runs. **Admin → Updates**
shows, for the home and each machine, the last attempt: when, from and to, the step it stopped
at, the reason in plain words, the raw error, and what to do next (Retry, the fix, or the logs).
"See the logs" opens the logs page on that update's time window with the machine's own files
beside the api's lines: `server.log` (the database step, every role), `app.log` (the desktop
app's updater) and `launcher.log`, a node's fetched over the machine channel
(`GET /api/admin/logs/files`). "Download the logs" is the diagnostics bundle of the same window
with those files in it (`files/*.log`, a node's under `node-<id>/`). The desktop app says the
same in its menu and an OS notification, which open the logs page, or the folder with `app.log`
and `server.log` when the server does not answer. A machine that cannot log in to the
home's database says so on the Updates and Machines pages; the home re-makes its access by
itself ([machine-database-access.md](../specs/epics/37-node-replica/machine-database-access.md)).

Where the evidence is on each machine:

| | macOS | Windows | Linux app |
|---|---|---|---|
| the attempts | `~/Library/Application Support/Maktaba/updates/app-attempts.json`, `server-attempts.json` | `%LOCALAPPDATA%\Maktaba\updates\` (the same two) | the app's data folder, `updates/` |
| the app's log | `~/Library/Logs/Maktaba/app.log` | `%LOCALAPPDATA%\Maktaba\logs\app.log` | the app's logs folder, `app.log` |
| the server's log | `~/Library/Logs/Maktaba/server.log` | `%LOCALAPPDATA%\Maktaba\logs\` | the app's logs folder |
| the database step | `~/Library/Application Support/Maktaba/state/database.json`; the dump before the update: `backups/pre-<from>-to-<to>.dump`; a failed state set aside: `backups/failed-*.dump` | the same under `%LOCALAPPDATA%\Maktaba` | the same under the data folder |
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

### The desktop apps' own updates

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

### A Mac or PC app built locally cannot update itself

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

## Install-type behaviour for the manual self-update (Epic 28)

- **Plain binary / manual archive** — binary swap + re-exec,
  with a `.bak` kept for rollback on a failed post-restart health check.
- **Homebrew formula** — self-managed, like the `.deb`/`.rpm`: its service runs the launcher
  over `$(brew --prefix)/var/maktaba/server`, and the server installs each release there itself
  (Epic 35); `brew upgrade` brings a new launcher.
- **`.deb` / `.rpm`** — the server invokes the package manager
  (`apt-get install --only-upgrade` / `dnf upgrade`) so package state
  stays consistent.
- **Docker** — the Docker image (`maktaba-server`) updates the runtime
  on its volume through Epic 35's orchestrator (Update now starts its
  run); a container of the stack built from source returns `409` with
  `docker compose pull && docker compose up -d` (it can't replace its own
  image).

## Package repositories and the Homebrew tap

Every stable release is published, once it is published on GitHub as the newest stable one,
to an apt and a dnf repository (`https://packages.maktaba.dev`: the server, `maktaba-server`,
and the Linux app, `maktaba`; signed indexes) and to the Homebrew tap (the formula, and the Mac
app's cask `maktaba-app`), by `packages.yml`. It publishes only the release's own files, checked
first against the release's signature (or, for the apps, their updater key). An installed server
still installs its updates itself, verified; the package manager keeps the package's own files
(the launcher) current. Setup and configuration:
[deploy/packaging](../deploy/packaging/README.md#package-repositories-and-the-homebrew-tap).

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

## How a release is verified (Epic 35 story 35.1)

Every release publishes, next to its archives:

- `release.json`: version, tag, commit, schema revisions (`schema_rev`,
  `min_node_schema`, `breaks_previous_home`), protocol numbers, the
  per-platform archive with the SHA-256 of every binary inside it, the
  pipeline wheel and constraints. It is listed in `checksums.txt`.
- `checksums.txt` and `checksums.txt.sigstore.json`, a Sigstore bundle
  (`cosign sign-blob --new-bundle-format`) holding the Fulcio certificate,
  the signature and the Rekor entry with its inclusion proof. The legacy
  `checksums.txt.sig` / `.pem` stay for humans.

The server, `maktaba-server update` and (later) every node verify with one
in-process package, `shared/go/releaseverify` (no cosign binary, no
network needed):

1. the bundle against the Sigstore trusted root, with at least one Rekor
   inclusion proof and a signed log time inside the certificate's validity
   (checked against that time, not the local clock);
2. the certificate identity, exactly: issuer
   `https://token.actions.githubusercontent.com`, SAN
   `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<tag>`,
   source repository `https://github.com/Hamza-Labs-Core/Maktaba`, ref
   `refs/tags/<tag>`, trigger `push`, build config (the workflow the tag
   push started) the same `release.yml@refs/tags/<tag>`, a GitHub-hosted
   runner, and GitHub's numeric repository and owner ids (so a repository
   re-registered under the same name after a rename is refused). A fork,
   another workflow (also one calling `release.yml`), a branch run, a
   self-hosted runner or another tag's signature is refused; `release.json`'s
   `commit` must be the commit the signing run was built from;
3. `checksums.txt` (duplicates or bad digests are refused), then
   `release.json` against it and against the tag, every downloaded file
   against `checksums.txt`, the extracted `maktaba-server` against
   `release.json`, and every constraints line has `--hash=sha256:`;
4. the watermark, the highest version this machine ever installed
   (`maktaba-server.watermark` next to the binary): a lower version is
   refused, the same version with other checksums is refused, and so is a
   release signed before the installed one. `maktaba-server update
   --rollback` (the kept `.bak`) is the only way to run a lower version;
   it does not lower the watermark.

Releases published before story 35.1 have no bundle and no `release.json`:
they are never installed automatically (install them by hand).

**Trusted root.** The public-good Sigstore `trusted_root.json` is compiled
into the binary (`shared/go/releaseverify/trusted_root.json`, refreshed by
`go generate` there; a release refuses to build with one older than 90
days). The server refreshes it daily through Sigstore's TUF repository,
anchored on the TUF root compiled into sigstore-go, into
`<data_dir>/sigstore/`; the CLI refreshes into the user cache directory
before it verifies. A failed refresh keeps the last good root; a root not
refreshed for 180 days is treated as expired and verification fails closed.
The cache is trusted only while no other user can write it (the directory
and its parents owned by the running user or root and not group/other
writable, no symlinked files) and its `fetched_at` is not in the future;
otherwise the embedded root is used. So `sudo maktaba-server update` with
the invoking user's `$HOME` never trusts a root that user could have planted.

`install.sh` fails closed as well: a release missing `checksums.txt`, its
Sigstore bundle or the archive's entry is refused (`MAKTABA_NO_VERIFY=1`
overrides). Without `cosign` installed it can only check the SHA-256 (it
warns); `MAKTABA_REQUIRE_COSIGN=1` makes a missing cosign fatal.

## Verifying server artifacts manually

```bash
v=1.5.0
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

Use the exact `--certificate-identity`, never a
`--certificate-identity-regexp` over the repository name: that accepts a
fork's release workflow too. `--certificate checksums.txt.pem --signature
checksums.txt.sig` in place of `--bundle` checks the same identity. A fork
(`MAKTABA_UPDATE_REPO`) uses its own `owner/name` in the identity.
