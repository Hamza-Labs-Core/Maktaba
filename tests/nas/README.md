# nas-shapes: the server image in NAS shapes

Epic 59 story 59.15. `.github/workflows/nas.yml` builds the server image of the commit
(`deploy/docker/server/Dockerfile` around the Linux payload, `tools/release/linux-app/payload.sh`)
for amd64 and arm64, then runs each **cell** of [`cells.json`](cells.json) on a GitHub runner:
the image started in one shape a NAS gives it. What a container cannot imitate is in the device
checklists, [`docs/nas/checklists/`](../../docs/nas/checklists/README.md).

| File | What it is |
|---|---|
| `cells.json` | the matrix: each cell's shape and assertions, and `landed`, the stories merged on main |
| `cell.py` | runs one cell: volumes, `server.toml`, headless setup, the server started, ready, assertions, logs |
| `plan.py` | the workflow's matrix from `cells.json` (`--no-arm64`, `--only`) |
| `fixtures.sh` | the fixture library: 40 synthetic 4-second clips, 20 English and 20 Arabic (Arabic file names), one HEVC, one Arabic side-car subtitle; spoken sentences by espeak-ng, made in a `debian:12-slim` container, nothing downloaded or committed |
| `seccomp-no-landlock-{enosys,eperm}.json` | a kernel without Landlock (`landlock_*` answer ENOSYS, or EPERM as older Docker default profiles do) and without user namespaces (`unshare` and `clone(CLONE_NEWUSER)` answer EPERM) |
| `check_checklists.py` | the device checklists' lint: every step has its expected result and its screenshots in both languages |
| `test_harness.py` | this folder's own tests (the plan job runs them) |
| `stt-baseline.json` | each speech model and architecture's word error rate on the bundled clips (59.06) |

`tools/ci/tests/test_nas_workflow.py` checks the workflow's triggers: a change to the Dockerfile
runs every cell, a change to `web/` alone runs nothing.

## A cell, step by step

1. **Volumes**, made by a root helper (the image itself as uid 0, its entrypoint bypassed):
   the data volume (`/var/lib/maktaba`) with the cell's `data.owner` and `data.mode`, the library
   (the fixtures, at `/volume1/Media/{en,ar}`, the same path as on a Synology) with
   `media.owner`, `media.mode` and `media.ro`, and `/volume1/Private` (mode 000) when the cell has
   `noaccess_root`.
2. **server.toml** first, as the cell's user: the smallest Whisper, no second listen, no
   language model, no quiet hours (the Light and Standard tiers' default, 01:00-06:00, holds
   a library's first scan until then; `quiet-hours-hold` checks that hold).
3. **Setup, headless**: `docker run --rm -i <shape> IMAGE setup --answers /dev/stdin`, what
   `docker compose run --rm maktaba setup` runs, the admin's password only on stdin.
4. **The server**: `docker run -d <shape> --network host IMAGE`. Ready is the public health route,
   one sign-in (the login route allows 10 a minute), then every role (`postgres`, `api`,
   `streaming`, `pipeline`) running in `/api/system/processes`. One library per root is then made
   through the admin API (`POST /api/libraries`), one with an Arabic name, as the web app's first
   screen makes them.
5. **Assertions**, in the cell's order, each through the admin API or Docker; a failed one is
   reported and the rest still run.
6. **Logs, pass or fail**, in the job's artifact `nas-cell-<cell>-<arch>` (14 days), linked from
   the run's summary: `container.log` (`docker logs -t`), `inspect.json`, `processes-top.txt`,
   `cgroup-memory.txt`, `setup.log`, `state-logs/` (server.log, venv.log), `data-logs/` (every
   role's log segments: api, streaming, pipeline and its workers, the plugin runners, postgres,
   supervisor), `launcher.log`, the API's view (`api-processes.json`, `api-libraries.json`,
   `api-machine.json` (`GET /api/server/machine`, 59.04), `api-version.json`), `postgres-pss.txt` on the cells that check it, `report.json` and `summary.md`.

## Assertions

| Name | Passes when |
|---|---|
| `ready` | the health route, the sign-in and every role answered (step 4) |
| `only_uid` | every process in the container (`docker top`) runs as the cell's uid |
| `healthcheck` | the image's `maktaba-server healthcheck` (the TrueNAS app's healthcheck, run by `docker exec`) answers healthy: the web app's health route answered 200 (59.10) |
| `scan` | the libraries' roots are the fixtures' and a scan finds all 40 clips |
| `thumbnail` | an Arabic clip's poster is served |
| `transcode` | a forced software transcode of the HEVC clip serves its master playlist, a rendition and a first segment |
| `transcode_auto` | the same on the encoder the server picked (no `force_software`): libx264 once VA-API fell back (59.05) |
| `video_no_device`, `video_device_failed`, `video_group_add` | `GET /api/server/machine`'s `video` (the streaming role's verdict, 59.05) is software with that reason: no render node; one whose test encode failed (`/dev/null` as `renderD128`), its error kept; one the user may not open (the host's `/dev/kmsg`, 0644 root and read-only in the device cgroup: `/dev/null` is in Docker's default allow list, so `:r` does not narrow it), the uid named |
| `no_oom` | not OOM-killed, `memory.current` under `memory.max`, no `oom_kill` event |
| `media_untouched` | the library holds exactly the fixtures afterwards: nothing written in it |
| `logs_clean` | no permission error in `server.log` |
| `access_ro` | every library under the media mount shows Read only after Check access (`POST /api/libraries/{id}/check-access`), each root with its fix, a `:ro` mount named as one, and `GET /api/libraries` says `ro` (59.03) |
| `convert_409` | conversion of a read-only library (and its dry run) answers 409 `library_read_only` (59.03) |
| `access_none` | adding the mode-000 root is refused (422, `not-readable`); a library made while the folder was open shows No access once it is closed and checked again, with its fix, and its scan answers 409 `library_no_access` (59.03) |
| `tier_light`, `tier_standard` | `GET /api/server/machine` (59.04) reads the container's memory limit as Docker set it, and the tier and its defaults are that tier's |
| `quiet_hold` | outside the cell's quiet hours a new library's first scan waits: the scan queue (`GET /api/queues`) says `quiet_hours` and when the window opens, and no clip is found; the admin's `POST /api/queues/scan/run-now` then starts it: the held jobs run and none stays paused; every clip is then found as in `scan` (59.02) |
| `postgres_pss_512m` | the bundled Postgres's processes hold under 512 MiB together (the sum of `Pss` in each one's `smaps_rollup`, kept as `postgres-pss.txt`), the Light tier's tuning applied (59.02) |
| `isolation_container` | without Landlock and user namespaces the plugin isolation level is Container (`GET /api/server/machine`'s `plugin_isolation` and `GET /api/plugins/isolation`); the test plugin (`--plugin`, plugind's hello guest that the amd64 image job packs as the `nas-plugin` artifact), installed and turned on by the admin, stays off with `isolation_held`; `PUT /api/plugins/isolation` without the password is 401, with it 200; the plugin then runs, and `plugins.host` is Degraded with `sandbox_partial` (plugind's required layers on, its optional ones missing). The answers are kept as `api-plugin-isolation.json` (59.07) |
| `refused_write`, `refused_privileged` | the container exits 78 with its message (59.01) |
| `stt_light_off` | on the Light tier speech is off by default, its reason `light`, the model base, in the quiet hours only; on arm64 no second listen (59.06) |
| `stt_engine` | the speech engine (the stt extra's aarch64 pins, `pipeline/constraints-stt.txt`) goes into the pipeline's venv and imports, with no torch and no transformers: on arm64 from the payload's wheelhouse alone (`--no-index`), on amd64 from PyPI; its pip log is `stt-install.log` (59.06) |
| `stt_measure` | an admin's Measure again (`POST /api/server/speech/measure`): the server transcribes the bundled Arabic and English clips with the tier's model; the real-time factor and each clip's word error rate are reported (`stt:wer`, `api-speech.json`), and a rise of more than 5 points over [`stt-baseline.json`](stt-baseline.json) for that model and architecture fails (no entry yet: recorded only) (59.06) |

An assertion still waiting for its story is written `{"name": "tier_light", "needs": ["59.04"]}`:
it is reported as pending (⏳) until that story is in `landed`.

## Adding a cell

1. Add it to `cells.json`: `name`, `about`, `arches` (`amd64`, `arm64`), and its shape: `user`,
   `data`, `media`, `noaccess_root`, `docker` (extra `docker run` arguments such as `--memory=4g`
   or `--device /dev/dri`), `env`, `seccomp` (a profile in this folder), `expect: "refused"`,
   `kill_restart`, `quiet_hours_ahead` (a one-hour quiet window opening three hours from now,
   UTC; every other cell has none, `[schedule] quiet_hours = ""`), `speech` (story 59.06:
   `[transcription] speech = "on"` and the tier's model instead of the cells' tiny one); then
   `asserts`.
2. A new assertion is a method `assert_<name>` of `Cell` in `cell.py` returning a short detail,
   raising `AssertionError` to fail. Use `self.api` (signed in as the admin) and `docker(...)`.
3. A cell or assertion for a story not merged yet carries `needs: ["59.NN"]`; the story's pull
   request (or the next one) adds `59.NN` to `landed`, which turns it on. A stale assertion is
   not a flake: it ships with the code that changes it.
4. Run it locally (Docker Desktop, any architecture):

   ```bash
   tests/nas/fixtures.sh build/nas/media
   # an image: the release's payload around the Dockerfile, or the one nas.yml built
   python3 tests/nas/cell.py --image maktaba-server:nas --cell uid-1000 \
       --fixtures build/nas/media --out build/nas/out/uid-1000 --local 47088
   ```

   The no-Landlock cells also take `--plugin hello.mkplugin`: `make plugind-guests`, then
   `go run ./cmd/mkplugin <folder> hello.mkplugin` in `shared/go/componentpack`, the folder
   holding the guest as `plugin.wasm`, its `manifest.toml` and `i18n/` (what nas.yml's image
   job does).

   `--local PORT` runs the cell on a bridge network with a relay in its network namespace, so
   the server still sees a local client (Docker Desktop has no host network). Pick a port no
   other program on the machine listens on.

**Waiting for a fix** (found by these cells; each `needs` names it until its fix merges):

| Need | What the cells found |
|---|---|
| `poster-queue` | in a fresh container no `content_hash` job is queued for the scanned clips, and `thumbnail` (the poster) only follows `content_hash`: `thumb_small` runs and writes nothing, and no poster is served after 15 minutes |
| `plugin-cache-grant` | stages run in the plugin runners are refused `$HOME/.maktaba/cache` (`plugin_sandbox_refused`, `/var/lib/maktaba/.maktaba/cache`): the image's `HOME` is the volume, and the runners' grants do not cover it; they fall back to running in process |

**VA-API (59.05):** `vaapi-fallback` (VA-API asked for, no `/dev/dri`), `vaapi-fake-device`
(`/dev/null` as the render node: the test encode fails) and `vaapi-no-group` (a node the user may
not open): each ends in libx264 with its reason in Settings. Real VA-API is in the device
checklists (J4125, N100).

**The TrueNAS app (59.10):** `truenas_app.py` runs truenas/apps' own tooling (at the commit
`truenas/catalog.env` pins, cloned by `truenas/clone-catalog.sh`) on
`deploy/nas/truenas/ix-dev/community/maktaba`: nas.yml's `truenas-lint` (the catalog's metadata
check, port check and render of each test values file, then the shape the story asks of each
render, and `truenas/*-values.yaml`, renders a runner cannot start: an Intel/AMD GPU, an NVIDIA
card, host-path data) and `truenas-deploy` (the ixVolume render started with this commit's
image: the permissions step gives the volume to 568, setup from the app's Shell, ready as 568,
platform truenas and the app's memory limit read; then the catalog's own deploy of each test
values file). Both keep their logs as artifacts (`nas-truenas-lint`, `nas-truenas-deploy`).

**Later phases add cells:** `bridge` and `offline` (59.08, 59.01), and the update legs (`update-rehearsal-container`, 59.12).
