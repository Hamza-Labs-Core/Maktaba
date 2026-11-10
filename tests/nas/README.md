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

`tools/ci/tests/test_nas_workflow.py` checks the workflow's triggers: a change to the Dockerfile
runs every cell, a change to `web/` alone runs nothing.

## A cell, step by step

1. **Volumes**, made by a root helper (the image itself as uid 0, its entrypoint bypassed):
   the data volume (`/var/lib/maktaba`) with the cell's `data.owner` and `data.mode`, the library
   (the fixtures, at `/volume1/Media/{en,ar}`, the same path as on a Synology) with
   `media.owner`, `media.mode` and `media.ro`, and `/volume1/Private` (mode 000) when the cell has
   `noaccess_root`.
2. **server.toml** first, as the cell's user: the smallest Whisper, no second listen, no
   language model.
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
   `api-machine.json` (`GET /api/server/machine`, 59.04), `api-version.json`), `report.json` and `summary.md`.

## Assertions

| Name | Passes when |
|---|---|
| `ready` | the health route, the sign-in and every role answered (step 4) |
| `only_uid` | every process in the container (`docker top`) runs as the cell's uid |
| `scan` | the libraries' roots are the fixtures' and a scan finds all 40 clips |
| `thumbnail` | an Arabic clip's poster is served |
| `transcode` | a forced software transcode of the HEVC clip serves its master playlist, a rendition and a first segment |
| `no_oom` | not OOM-killed, `memory.current` under `memory.max`, no `oom_kill` event |
| `media_untouched` | the library holds exactly the fixtures afterwards: nothing written in it |
| `logs_clean` | no permission error in `server.log` |
| `tier_light`, `tier_standard` | `GET /api/server/machine` (59.04) reads the container's memory limit as Docker set it, and the tier and its defaults are that tier's |
| `refused_write`, `refused_privileged` | the container exits 78 with its message (59.01) |

An assertion still waiting for its story is written `{"name": "tier_light", "needs": ["59.04"]}`:
it is reported as pending (⏳) until that story is in `landed`.

## Adding a cell

1. Add it to `cells.json`: `name`, `about`, `arches` (`amd64`, `arm64`), and its shape: `user`,
   `data`, `media`, `noaccess_root`, `docker` (extra `docker run` arguments such as `--memory=4g`
   or `--device /dev/dri`), `env`, `seccomp` (a profile in this folder), `expect: "refused"`,
   `kill_restart`; then `asserts`.
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

   `--local PORT` runs the cell on a bridge network with a relay in its network namespace, so
   the server still sees a local client (Docker Desktop has no host network). Pick a port no
   other program on the machine listens on.

**Waiting for a fix** (found by these cells; each `needs` names it until its fix merges):

| Need | What the cells found |
|---|---|
| `scan-grant-race` | a scan asked right after a library is made walks nothing: the `light` runner's sandbox has no grant for the new root yet (EACCES, `files_walked=0`); the runner restarts with it a moment later. `scan` asks again each minute; `first_scan` waits |
| `plugin-cache-grant` | stages run in the plugin runners are refused `$HOME/.maktaba/cache` (`plugin_sandbox_refused`, `/var/lib/maktaba/.maktaba/cache`): the image's `HOME` is the volume, and the runners' grants do not cover it; they fall back to running in process |

**Later phases add cells:** VA-API without a device (`vaapi-fallback`, 59.05), speech on arm64
(`arm64` with speech on, 59.06), the plugin isolation level on the no-Landlock cells (59.07),
`bridge` and `offline` (59.08, 59.01), and the update legs (`update-rehearsal-container`, 59.12).
