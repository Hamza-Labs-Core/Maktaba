# Maktaba on a NAS: the TrueNAS app

A NAS runs Maktaba as one Docker image (`ghcr.io/hamza-labs-core/maktaba-server`): any non-root
user, the home's data on one volume, media mounted at its host path, updating itself on its
volume (Epic 59). Each NAS gets it its own way. This page is about TrueNAS, whose catalog
([truenas/apps](https://github.com/truenas/apps)) takes community apps: how Maktaba's app is
built, tested and released. The household's guide is maktaba.tv/install/truenas.

## Where it lives

`deploy/nas/truenas/` ([README](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/deploy/nas/truenas)):

- `ix-dev/community/maktaba/`: the app in the catalog's own source layout (`app.yaml`,
  `item.yaml`, `ix_values.yaml`, `questions.yaml`, `README.md`,
  `templates/docker-compose.yaml` on the catalog's rendering library, `templates/test_values/`);
- `custom-app.yaml`: the same install as a TrueNAS Custom App (Install via YAML), for boxes while
  the app is not in the catalog.

The app runs as `apps` (568) by default, keeps its data on a host path (a dataset with the
**Apps** preset, on an SSD pool when there is one) or an ixVolume (the catalog's permissions step
owns it for the run-as user), mounts each media folder at its own path, uses the host network,
and offers the catalog's GPU question (Intel/AMD `/dev/dri` with the video and render groups;
NVIDIA). It sets `MAKTABA_PLATFORM=truenas`, so every fix text names TrueNAS's screens
(**Datasets → {dataset} → Permissions**), and never `MAKTABA_UPDATE_DISABLE`.

## Two small server commands it relies on

- `maktaba-server healthcheck` (hidden): the container's health, run by the app's healthcheck
  (`docker exec`; the image has no curl). Exit 0 while the container waits for its first setup
  (no `server.toml`), then only when `/api/system/health` answers 200 on `server.toml`'s
  listen; exit 1 with the reason otherwise.
- `maktaba-server container setup`: the setup, run from the app's Shell. A container that is not
  set up says so in its log, in TrueNAS's words when `MAKTABA_PLATFORM=truenas`. Its preflight's
  write-denied message adds "Edit the dataset's permissions and add an ACL entry for user 568
  with Modify, or use the Apps preset."

## How it is tested

`.github/workflows/nas.yml` runs the catalog's own tooling on the app, at a pinned truenas/apps
commit (`tests/nas/truenas/catalog.env`), whenever the app, its harness or the healthcheck
change (`tests/nas/truenas_app.py`):

- **truenas-lint:** the catalog's metadata check, port check and render of each test values
  file, with the shape the story asks of each render (568, host network, no D-Bus, nothing over
  `/proc`, media at its own path, the permissions step only for an ixVolume, GPU devices and
  groups), plus renders a runner cannot start (GPUs, host-path data) and the Custom App YAML;
- **truenas-deploy:** the image of the pull request as the app's pin; the ixVolume install
  started, the setup run as from the Shell, ready as 568, `GET /api/server/machine` reading
  `truenas` and the app's memory limit; then the catalog's own deploy of each test values file.

The `uid-568` cell also runs the healthcheck. The catalog's renderer needs `openat2`: run it on
Linux amd64, not under Docker Desktop's emulation on Apple silicon.

## How it is released

The source pins the release it was last changed for. A release never commits a new pin to main
(that commit would be the next release candidate, and every release would make another); it
renders its own copy with `tools/release/truenas_app.py render --tag vX.Y.Z`:

- the release page carries `maktaba-truenas-custom-app.yaml` and `maktaba-truenas-app-<version>.tar.gz`,
  both pinned to the image's `tag@digest` and signed with the rest by `checksums.txt`. The
  catalog's `version` is derived from Maktaba's (vX.Y.Z → (X+1).Y.Z), so it grows with every
  release;
- once the app is in the catalog, `packages.yml` opens the release's bump pull request to
  truenas/apps. That job runs on `workflow_run` with a token, so it checks out and runs no code:
  it verifies the release's app archive against the signed checksums and copies its files in
  as data.

The app goes into the catalog's community train under the project's GitHub account once the
device checklist (`docs/nas/checklists/truenas.md`) passes on TrueNAS 25.04 and 25.10; the
pull request's steps and text are in the folder's README.
