# Maktaba on TrueNAS (Epic 59 story 59.10)

TrueNAS Community Edition (25.04 Fangtooth, 25.10 Goldeye) runs apps as Docker Compose from its
catalog, [truenas/apps](https://github.com/truenas/apps). This folder holds Maktaba's app in the
catalog's own source layout, ready to go into its **community** train, and the same install as a
**Custom App** YAML for TrueNAS boxes until the app is in the catalog.

| Path | What it is |
|---|---|
| `ix-dev/community/maktaba/` | the app, as `truenas/apps` keeps it: `app.yaml` (metadata), `item.yaml` (its catalog entry, generated from `app.yaml`), `ix_values.yaml` (the image and constants), `questions.yaml` (the install form), `README.md`, `templates/docker-compose.yaml` (the template, on the catalog's rendering library) and `templates/test_values/` (what the catalog's CI renders and starts) |
| `custom-app.yaml` | the Custom App (Apps → Discover → ⋮ → Install via YAML), with the questions as comments |

The household's guide is maktaba.tv/install/truenas (its source:
[docs/nas/guides/truenas.md](../../../docs/nas/guides/truenas.md), in Arabic
[truenas.ar.md](../../../docs/nas/guides/truenas.ar.md)). The device checklist is
[docs/nas/checklists/truenas.md](../../../docs/nas/checklists/truenas.md).

## The app

- **User:** `apps` (568:568) by default, any non-root user in the form. The image chowns nothing.
- **Data** (`/var/lib/maktaba`): a host path by default, a dataset on an SSD pool with the
  **Apps** preset; ixVolume offered too, which the catalog's permissions step (its
  `container-utils` image, as root, not Maktaba's) hands to the run-as user before Maktaba
  starts.
- **Media:** a list of host paths, each mounted at its own path (`/mnt/tank/media` stays
  `/mnt/tank/media`, story 59.13), with a read-only toggle each. Never chowned.
- **Network:** host network by default (mDNS by Maktaba's own responder; no D-Bus socket). Off:
  8088, 8080 and 8081 published (never 54329) and the Host IP question sets `MAKTABA_HOST_IP`.
- **Resources:** 4 CPUs and 8192 MB by default; Maktaba's tier follows the memory limit
  (story 59.04). The ARC at `/proc/spl/kstat/zfs/arcstats` stays readable: nothing masks it.
- **GPU:** the catalog's own GPU question: Intel/AMD "allocate GPU" passes `/dev/dri` with the
  video (44) and render (107) groups (VA-API, story 59.05); an NVIDIA card is the catalog's
  NVIDIA reservation.
- **Environment:** `MAKTABA_PLATFORM=truenas` (TrueNAS words in every fix text), `TZ`. Never
  `MAKTABA_UPDATE_DISABLE`: the self-update stays on beside the catalog's updates.
- **Healthcheck:** `maktaba-server healthcheck` (the image has no curl): healthy while the
  container waits for its first setup, then when the web app answers. The setup runs from the
  app's Shell: `/opt/maktaba/server/bin/maktaba-server container setup`.

## CI

`nas.yml` runs truenas/apps' own tooling on the app, at the commit
[`tests/nas/truenas/catalog.env`](../../../tests/nas/truenas/catalog.env) pins, whenever the app,
its harness or the healthcheck change (`tests/nas/truenas_app.py`):

- **truenas-lint:** the catalog's metadata check (`app.yaml` and `item.yaml` exactly as its
  generator writes them: a difference is printed and kept as `metadata.diff`), its port check,
  and its render (`ci.py --render-only`) of each test values file, with the shape the story asks
  of each render checked; then renders a GitHub runner cannot start
  (`tests/nas/truenas/*-values.yaml`: an Intel/AMD GPU, an NVIDIA card, host-path data); and
  `docker compose config` of `custom-app.yaml`.
- **truenas-deploy:** this commit's image tagged as the app's pin; the ixVolume render started,
  the permissions step's result checked (568:568), the setup run from the "Shell" (`docker
  exec`), then ready, every process as 568, `GET /api/server/machine` reading platform truenas
  and the app's memory limit; then the catalog's own deploy (`ci.py`) of each test values file.

The catalog's renderer needs `openat2`, which Docker Desktop on Apple silicon does not emulate:
run it on Linux amd64 (CI does).

**Moving the pin:** set `TRUENAS_APPS_COMMIT` to a newer truenas/apps commit, set `lib_version`
in `app.yaml` to its newest `library/2.x.y`, and let truenas-lint print the metadata the
catalog then generates (`metadata.diff` in its artifact) to copy back here.

## Releases

The source pins the release it was last changed for: CI's checks need a real tag. A release does
not commit a new pin to main (that commit would be the next release candidate, and every release
would make another). Each release renders its own copy instead, as it renders the published
Docker compose file ([`tools/release/truenas_app.py`](../../../tools/release/truenas_app.py)):

- `release.yml` (on the tag) renders both, pinned to the server image's `tag@digest`, and
  publishes them as release assets, signed with the rest by `checksums.txt`:
  `maktaba-truenas-custom-app.yaml` (beside `maktaba-docker-compose.yml`) and
  `maktaba-truenas-app-<version>.tar.gz` (the app's folder), with `app_version` the release and
  the catalog's `version` derived from it (vX.Y.Z → (X+1).Y.Z, so it grows with every release).
- `packages.yml`'s `truenas-catalog` job, once the app is in the catalog
  (`vars.TRUENAS_APP_IN_CATALOG`), opens the release's bump pull request to truenas/apps from the
  project's fork. It runs on `workflow_run` with a token, so it checks out and runs no code of the
  repository: it verifies the release's app archive against the Sigstore-signed `checksums.txt`
  (release.yml at the tag), checks that it holds only the app's own files, and copies them in as
  data.

`python3 tools/release/truenas_app.py check` (run by `tools/ci/tests/test_truenas_app.py`) keeps
`ix_values.yaml`, `app.yaml` and `custom-app.yaml` on the same release.

## Getting into the catalog

The app goes to truenas/apps' community train under the project's GitHub account once the device
checklist passes on TrueNAS 25.04 and 25.10. The pull request:

1. Fork truenas/apps under the project account; branch `maktaba` from `master`.
2. Render the newest release's copy: `python3 tools/release/truenas_app.py render --tag vX.Y.Z
   --digest <the image's digest> --out /tmp/truenas`, and copy
   `/tmp/truenas/ix-dev/community/maktaba` into the fork's `ix-dev/community/maktaba`.
3. In the fork: `python3 .github/scripts/ci.py --app maktaba --train community --test-file
   basic-values.yaml` (it fills `lib_version_hash` and the metadata), then commit.
4. Open the pull request with the text below, the icon (`maktaba.svg`) and two screenshots
   attached for the reviewers to host on their CDN (the app's `icon` and `screenshots` then take
   their URLs).

> **New app: Maktaba**
>
> [Maktaba](https://maktaba.tv/) is a home media server (films, series, recordings) in Arabic and
> English, with live TV, subtitles and transcripts made on the home, and apps for phones and
> TVs. One container runs the server and what it runs (web app, API, live TV, the pipeline, its
> own Postgres), as any non-root user, and updates itself on its data storage inside the
> household's update window, beside this catalog's updates.
>
> - Runs as 568:568 by default (any non-root user); chowns nothing. Data on a host path (a
>   dataset with the Apps preset) or an ixVolume (the permissions step).
> - Host network by default (mDNS discovery for the apps and TVs); bridge with three published
>   ports.
> - The catalog's GPU question: `/dev/dri` with video and render for VA-API; NVIDIA for NVENC
>   and faster speech to text.
> - Healthy while waiting for its first setup (run from the app's Shell, as the app's notes
>   say), then when the web app answers.
>
> The app is developed in https://github.com/Hamza-Labs-Core/Maktaba/tree/main/deploy/nas/truenas,
> where this catalog's own tooling (metadata check, port check, render, and deploy of each test
> values file) runs on every change, and has been run on TrueNAS 25.04 and 25.10 (device
> checklist results: docs/nas/checklists/results/). Each Maktaba release opens the bump pull
> request here.

5. Once it merges: set `vars.TRUENAS_APP_IN_CATALOG` to `true` (with `vars.TRUENAS_APPS_FORK` and
   `secrets.TRUENAS_APPS_TOKEN` in the `packages` environment), and point the guide's install
   step at the catalog.
