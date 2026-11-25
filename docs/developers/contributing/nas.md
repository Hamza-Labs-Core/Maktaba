# Maktaba on a NAS: how it fits together

Epic 59 runs a Maktaba home on the NAS that keeps a household's media: Synology, TrueNAS,
Unraid, QNAP, UGREEN, ASUSTOR or any machine with Docker. This page is the developer's map: what
each part does and where its code is. The household's guides are on maktaba.tv/install/nas; the
TrueNAS app's catalog work has [its own page](nas-apps.md).

## One image, any user

Every NAS runs the same signed image, `ghcr.io/hamza-labs-core/maktaba-server`. The image is a
bootstrap: it puts its release's payload on the data volume once, and from then on the server
updates the volume itself, as a headless Linux install does (`cmd/maktaba-server/cmd_container.go`).

- **The user:** the container runs as whatever uid:gid it is given (TrueNAS 568, Unraid 99:100,
  a DSM user). There is no root entrypoint and no PUID/PGID. `nss_wrapper` names a uid that
  `/etc/passwd` lacks, for the bundled Postgres.
- **The preflight** (`container_preflight.go`) refuses root, a privileged container and the
  Docker socket, and checks that the data volume takes a write. A refusal exits 78 with its
  message on two lines, English then Arabic: a NAS's log viewer cannot switch language.
- **Not set up yet:** the container waits and says, in both languages and in the NAS's own
  words (`MAKTABA_PLATFORM`: `dsm`, `truenas`, `unraid`), how to run
  `maktaba-server container setup` in its terminal. Every guide starts the container before
  its setup, so the launcher starts only once `server.toml` is there, setup's pending file
  (`state/setup.json`) is gone and the database setup's finish step makes exists: setup writes
  `server.toml` at its first answer, before the pending file.

## What the server finds out for itself

| Part | Where | What it does |
|---|---|---|
| Hardware and tiers | `internal/hostinfo` | memory from the cgroup limit, ZFS's cache counted as available, CPU quota, AVX2, render devices; Light, Standard or Strong by memory alone (defaults, never limits) |
| Data folder | `setup_datadir.go`, `setup_storage.go` | refuses network file systems and Unraid's `/mnt/user`; in the image proposes the data volume, never a media mount, and never offers a read-only mount; notes spinning disks; Postgres tuned per tier |
| Media permissions | `shared/go/mediaaccess` | each library read and write, read only or no access, with the fix in the NAS's own screens (`fix.go`); read-only libraries keep their subtitles in the database |
| Video | the transcode test encode | VA-API beside NVENC and QSV; the render group named when the device is there but not openable |
| Speech | the pipeline | CTranslate2 on arm64; the model picked by a measured speed |
| Plugins | plugind | isolation levels Full and Container; third-party plugins on Container held until the admin allows them |
| Network | `serve_network.go`, `ports.go` | host network by default; on a bridge mDNS and SSDP are off and setup says why; a taken port moves into 8780 to 8790 and the move is printed in both languages |
| Ports | `internal/config/portenv.go`, `serve_postgres.go` | each port set from the container's environment (below); a port set there is never moved: the start stops, naming the port and the variable in both languages |

### Ports a household chooses

Another app on a NAS (Plex, another package, the NAS's own UI) may already use one of Maktaba's
ports. Each one has a variable, which every NAS file shows commented, beside its `server.toml`
key:

| Port | Variable | `server.toml` |
|---|---|---|
| 8088 web app | `MAKTABA_WEB_PORT` | `[server] web_listen` |
| 8080 API (apps, the home's machines) | `MAKTABA_API_PORT` | `[server] listen` |
| 8081 live TV | `MAKTABA_STREAM_PORT` | `[server] stream_addr` |
| 54329 Postgres (the home's machines) | `MAKTABA_POSTGRES_PORT` | `[postgres] port` |
| a published port on a bridge or a port forward | `MAKTABA_PUBLIC_WEB_PORT`, `MAKTABA_PUBLIC_API_PORT`, `MAKTABA_PUBLIC_STREAM_PORT` | `[server] public_web_port`, `public_api_port`, `public_stream_port` |

- `config.LoadFrom` applies the variables over `server.toml` on every load, so each program that
  reads the config uses the same port; a save writes the file's own values back, never the
  variables'. Code that needs a port reads it from the config (`cfg.Server.Listen`,
  `cfg.PublicWebPort()`), never a literal 8088.
- A default port another program holds moves (`ReconcilePorts`); a port a variable sets returns
  `config.ErrPortPinned` instead, and setup and serve say `setup.check.note.port_setting_taken`.
- The addresses handed out (`PublicWebURL`, `PublicAPIURL`, `PublicStreamURL`, mDNS's ports)
  use the published ports. A node learns its home's live TV port from `/api/discovery/home`'s
  `stream_port`.
- Tests: `portenv_test.go`, `ports_pinned_test.go`, and the `custom-ports`,
  `bridge-published-ports` and `port-taken` cells of `tests/nas/cells.json`.

## Updates on a NAS

The image has two triggers and one update path (`docs/updates.md`, "Updates on
a NAS"): the self-update in the household's window, and a newer image a catalog pulls, put on the
volume at the next start under the same guard. An older image never replaces a newer runtime.

When an update's migrations fail part way, the database step goes back the cheapest safe way
(`updater.FailedBack`, `migrationGate.backWithoutDump`) and halts with one of three codes, each in
every client's English and Arabic. No backup is ever put back by itself (since 2026-11-24):

| Code | When | What the household sees |
|---|---|---|
| `db_kept` | every migration since the release before is rollback-safe and that release runs on a newer schema | nothing put back; the release before runs on the database as it is, in seconds |
| `db_undone` | the recorded migrations are reversible and the one that failed part way left only additive work | `migrate down-to` ran their Down scripts, in seconds |
| `db_held` | anything else: the Down scripts failed, a migration that ran lost data, the way back cannot be told, or the launcher's guard gave up after the migrations ran | nothing put back; the update stops and asks. The record is `held`, every start (the release before too) stops on it, and the start page, the recovery page and the Updates page give the choices: put the pre-update backup back (**Restore** on `/recovery`), or install a newer release, which goes on from it |
| `db_restored` | a release from before 2026-11-24 only | the pre-update dump put back, hours for a large library |

The update's watch then rolls back to the release before and blocks the failed one. While
migrations run, any session one of them has waited on for more than 10 s (a client backend or a
joined machine's walsender) is ended (`pgbundle.EndBlockersSQL`), so a `CREATE INDEX
CONCURRENTLY` no longer meets its lock timeout behind a copy's long snapshot. The guides'
Troubleshooting page (`#update-failed`, `#update-disk`) says what each looks like.

## A NAS in a cluster (story 59.13)

- **Home**, the default on any tier: the guides set it up. Setup's share step works out each
  folder's canonical share (DSM `/volume<N>/<share>`, Unraid `/mnt/user/<share>`, QNAP
  `/share/<share>`; TrueNAS and other mounts by the name the household types) and keeps the NAS's
  own `storage_servers` record, so a node's `/Volumes/<share>` matches with no path map.
  **Machines → This NAS's shares** edits them; once settled, stored paths convert to
  `smb://<nas>/<share>/…` after a backup (**Revert last conversion** undoes it).
- **Full node of a home elsewhere:** Standard and Strong. A Light NAS gets `cluster.join.light_nas`
  and joins only with `setup --node --light-nas` (`setup_node_nas.go`). Nothing hard-blocks.
- **Storage only:** unchanged; a home on another computer reads the NAS's shares.

## The install files and the guides

- `cloud/installers/docker/docker-compose.yml` is the one compose file. The Synology project
  (`deploy/nas/synology/`) is generated from it, and the Unraid template
  (`deploy/nas/unraid/maktaba.xml`) and the TrueNAS app (`deploy/nas/truenas/`) are checked
  against it in CI (`tests/nas/install_files.py`).
- **maktaba.tv's install pages** are `web/packages/install` (`pnpm -C web build:install`):
  `/install/nas` (which guide, and the model table, `src/nas-models.json`), `/install/docker`
  and `/install/synology` (forms that fill the compose file in the page; no answer leaves it),
  the TrueNAS, Unraid, QNAP, UGREEN and ASUSTOR guides, After setup and Troubleshooting, each
  with its Arabic page under `/ar/`. Every platform guide has the same sections: before you start,
  the user and its uid:gid, the folders, the install in the NAS's own app, the setup and the role,
  ports, hardware video, updates and troubleshooting. The model table links each model to its
  maker's guide (`GUIDE` in `src/nasPage.ts`).
- **Platforms the code knows** (`hostinfo.Platform*`): `dsm`, `truenas`, `unraid`, `qts` (QNAP:
  the install page writes `MAKTABA_PLATFORM: qts` for it, so `/share/<name>` folders are known
  shares) and `omv`, beside `docker` and `linux`. UGREEN and ASUSTOR run as `docker`.
  OpenMediaVault is only a name in the code so far (no install file, CI cell or guide);
  TerraMaster and CasaOS are not in it at all.
- The build fails when an Arabic page's prose is mostly Latin: the same check as maktaba.dev's
  (`web/packages/devportal/scripts/parity.mjs`, through `src/latin.ts`). A NAS menu name that
  has no Arabic stays English in «quotes» after an Arabic description, and counts as a name.
- A screenshot of a NAS's own screens can only be taken on the device: until the device
  checklist (`docs/nas/checklists/`) takes it, a guide shows a marked placeholder
  (`figure.placeholder` with `data-shot`), the same one in both languages.

## Strings

Each Epic 59 story's spec has a Strings table, and every key in it ships in English and Arabic:

- in the web catalogs (`web/src/i18n/en.json`, `ar.json`);
- for what setup prints, in `cmd/maktaba-server/setup_texts_web.go`;
- for what the container prints before the web is up (`container.*`), in
  `container_preflight.go`.

`web/src/i18n/epic59.test.ts` reads the stories' tables and fails on a key missing from either
language. `container_texts_test.go` checks that each container message is two lines, English
then Arabic.

## Tests

- `.github/workflows/nas.yml`: the image started in NAS-shaped containers (the uid cells, the
  TrueNAS catalog's own lint and deploy, the install files).
- `web-ui.yml`'s `install` job (`pnpm -C web e2e:install`): the install page in Chromium sends no
  answer anywhere; every guide is mirrored in Arabic in the dark theme, photographed in both
  languages (the run's `install-guides-screenshots` artifact).
- The device checklists (`docs/nas/checklists/`): what only a real NAS can show, by hand,
  before each platform's release.
