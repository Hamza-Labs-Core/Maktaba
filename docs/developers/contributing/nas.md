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
  `maktaba-server container setup` in its terminal.

## What the server finds out for itself

| Part | Where | What it does |
|---|---|---|
| Hardware and tiers | `internal/hostinfo` | memory from the cgroup limit, ZFS's cache counted as available, CPU quota, AVX2, render devices; Light, Standard or Strong by memory alone (defaults, never limits) |
| Data folder | `setup_datadir.go` | refuses network file systems and Unraid's `/mnt/user`; notes spinning disks; Postgres tuned per tier |
| Media permissions | `shared/go/mediaaccess` | each library read and write, read only or no access, with the fix in the NAS's own screens (`fix.go`); read-only libraries keep their subtitles in the database |
| Video | the transcode test encode | VA-API beside NVENC and QSV; the render group named when the device is there but not openable |
| Speech | the pipeline | CTranslate2 on arm64; the model picked by a measured speed |
| Plugins | plugind | isolation levels Full and Container; third-party plugins on Container held until the admin allows them |
| Network | `serve_network.go`, `ports.go` | host network by default; on a bridge mDNS and SSDP are off and setup says why; a taken port moves into 8780 to 8790 and the move is printed in both languages |

## The install files and the guides

- `cloud/installers/docker/docker-compose.yml` is the one compose file. The Synology project
  (`deploy/nas/synology/`) is generated from it, and the Unraid template
  (`deploy/nas/unraid/maktaba.xml`) and the TrueNAS app (`deploy/nas/truenas/`) are checked
  against it in CI (`tests/nas/install_files.py`).
- **maktaba.tv's install pages** are `web/packages/install` (`pnpm -C web build:install`):
  `/install/nas` (which guide, and the model table, `src/nas-models.json`), `/install/docker`
  and `/install/synology` (forms that fill the compose file in the page; no answer leaves it),
  the TrueNAS, Unraid, QNAP and UGREEN/ASUSTOR guides, After setup and Troubleshooting, each
  with its Arabic page under `/ar/`.
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
