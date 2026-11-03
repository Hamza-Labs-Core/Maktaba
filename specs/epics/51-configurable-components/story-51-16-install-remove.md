# Story 51.16 — Install, remove and offline import

> Epic 51 · Phase 3 · Depends on: 51.04, 51.15 · Needed by: 51.17, 51.18

## Why

51.15 publishes packs and a signed index. Nothing on a home can use them yet. An admin on a
Core home who ticks Transcription needs the machine to fetch `runtime.ml` and a Whisper model,
check them, put them where the pipeline finds them, and report progress. An admin who unticks
it for good wants the gigabytes back. A home with no internet needs the same from a file.

The supervisor already owns every file on a machine (payload, venv, runtime copies), and 51.04
gives it a reconciler for `process` components. This story adds the **asset** half of that
reconciler: install, remove, import.

## Description

### 1. Install flow

Triggered when a component's desired `want` becomes `installed` or `enabled` and its
`Installed` condition is false on this machine (51.02, 51.04). For each `requires` asset first,
in order:

1. **Index.** Fetch `components-<running version>.json` from the release (`release.json`'s
   `components.index`); a node asks its home's mirror first (`internal/homemirror`, as for the
   wheelhouse). Verify it (`releaseverify.VerifyComponentsIndex`, 51.15).
2. **Room.** Free disk ≥ pack `size` + `unpacked_bytes` + 10 %. Otherwise `Failed`
   (`disk_space`), nothing downloaded.
3. **Download** to `<data>/components/.partial/<name>`, resumable (HTTP `Range`), stopping at
   the index's `size`. Progress every 1 s.
4. **Verify** size, then sha256, against the index. A mismatch deletes the file:
   `Failed (verify_failed)`.
5. **Unpack** into `<data>/components/<id>/.staging-<version>_<digest12>/` with the safe
   unpacker (§3), checking every file against the pack's `files.sha256` as it is written.
6. **Prepare** (asset-specific, §4), still in staging.
7. **Rename** staging to `<data>/components/<id>/<version>_<digest12>/` and write the
   `current` pointer (§2).
8. Report `Installed = true`. If `want = enabled`, the reconciler goes on to start it (51.04).

Every step writes `component_status` (phase `Installing`, `progress` 0–100, a detail code from
`detail_codes.json`: `pack_index`, `pack_downloading`, `pack_verifying`, `pack_unpacking`,
`pack_preparing`). A restart mid-install resumes at the download (the `.partial` file) and
redoes steps 4–7; staging folders left by a crash are deleted at boot.

### 2. Where things go

```
<data>/components/
  runtime.ml/
    0.4.2_3f9a1c2b7d10/      pack.json  files.sha256  wheels/  constraints.txt
    0.4.1_a01b55e9c3d4/      kept for a rollback (51.18)
    current                  "0.4.2_3f9a1c2b7d10\n"
  model.whisper-turbo/
    rev_<revision12>/        model.json (revision, files, sha256s); the files live in the HF cache
    current
  .partial/                  downloads in progress
```

`current` is a **pointer file** written atomically with `layout.WriteFileAtomic`
(`shared/go/layout/atomic.go`), the same kind as the launcher layout's `current`. It is not a
symlink: a symlink needs an elevated token on Windows. The running release reads
`<id>/<its version>_*` first and rewrites `current` to it at boot, so a rollback to a kept
release finds its own packs with no extra step (51.18).

### 3. Safe unpack

`internal/packs/unpack.go` (new) reads the zstd stream (window ≤ 128 MB) and the tar entries:

| Entry | Rule |
|---|---|
| absolute path, a `..` element, a path with `\` or a NUL, a Windows drive or UNC prefix | refused |
| a name that differs only by case from an earlier one | refused (macOS and Windows disks) |
| regular file | written with mode `0644`, or `0755` if any execute bit was set; setuid, setgid, sticky dropped |
| directory | `0755` |
| symlink | allowed only when its target, resolved inside the staging folder, stays inside it and is listed in `files.sha256` |
| hard link, character or block device, FIFO, socket | refused |
| more bytes than `unpacked_bytes`, or more entries than `files.sha256` lists | refused (zip-bomb guard) |
| a file not in `files.sha256`, or one whose size or sha256 differs | refused |

A refusal deletes the staging folder and sets `Failed (unpack_refused)` with the entry's name.
There are **no install scripts**: the unpacker only writes files.

### 4. What each asset kind maps to

- **`runtime.ml` (and `tool.yt-dlp`).** The pack is a wheelhouse. *Prepare* rebuilds the venv
  through the existing gate (`internal/appvenv`, `Spec.Wheelhouse = <pack>/wheels`,
  `Spec.Extras = "stt,search"`, plus `mediaintel` when `tool.yt-dlp` is installed). The venv
  stays where it is (`<state>/venv`) and pip installs only from the pack, each wheel checked
  against its hash in the pack's constraints. `Installed` is true when the pack is verified
  **and** the venv's stamp (`maktaba-venv.json`) is `ready` with those extras. A Full home that
  built its venv from PyPI before this epic has the stamp and no pack: it counts as installed,
  nothing is downloaded (F15). The venv rebuild is reported apart, like a process's warm-up:
  N10 measures verify + unpack + pointer only.
- **Models.** Downloaded with the pipeline's own fetcher (`maktaba-pipeline models download
  --only <name> --revision <rev> --json`, revision flag new) into the HF cache that
  `MAKTABA_MODEL_CACHE_DIR` places (`<data>/models/huggingface`, `model_dirs.py`), so every
  loader keeps working. Then the supervisor checks each file's sha256 and size against the
  index and writes `model.json` in `rev_<revision12>/`. A mismatch removes that revision from
  the cache (`maktaba-pipeline models remove`, new).
- **`runtime.ollama`.** The supervisor's ollama role (`internal/supervisor/ollama.go`) takes
  the payload's `ollama/` when there is one, else `<data>/components/runtime.ollama/current/
  ollama/`. `model.llm.*` are pulled through ollama (`internal/llmserver`) and checked against
  the pinned manifest digest.

### 5. Remove

`want = absent` (refused with `409 component_required_by` if an enabled component requires it,
§4.6). The desired row stays in phase `Removing`, a finalizer: the reconciler first stops the
component (51.04, `StopGrace`), then deletes, then reports `Installed = false`, and only then
is the row's removal final. Deleting:

- packs: every `<version>_*` folder of the running version (kept folders of other versions stay
  for rollback until 51.18 prunes them), then `current`;
- `runtime.ml`: rebuild the venv with `Extras = ""` from the payload's core constraints;
- models: `maktaba-pipeline models remove --only <name>` (the HF snapshot and blobs nothing
  else uses).

Settings and data stay (§4.7). `purge = true` also deletes the component's `rules.*` sections
and its caches; the UI asks first.

### 6. Download for offline, and import

- **Make the file.** `POST /api/components/offline {"bundle":"full","platform":"darwin-arm64"}`
  (or `{"components":[…]}`) builds, on the home, `maktaba-offline-<version>-<platform>-<bundle>.tar`:

  ```
  offline.json                  {"schema":1,"version","platform","bundle","items":[ids]}
  checksums.txt  checksums.txt.sigstore.json  release.json  components-<version>.json
  packs/<name>.tar.zst
  models/<id>/<path>            each file exactly as the index lists it
  ```

  Uncompressed tar (the packs are compressed already). Progress like an install; the file is
  served by `GET /api/components/offline/<name>` and deleted after 24 h.
- **Import.** Settings → Components → *Import a file*: `POST /api/components/import`
  (streamed, up to 64 GB) or `maktaba-server components import <file>`. The supervisor verifies
  `checksums.txt` and its bundle, the index (§4 of 51.15), then every pack and model file
  against the index, and installs as §1 from the file instead of the network. A file for
  another version or platform is refused (`offline_wrong_version`, `offline_wrong_platform`).
  The import sets `want = installed` for what it brought; the admin enables it.

### 7. Routes and roles

Admin routes in the API (`api/internal/handlers/components/`, new), each proxied to the
supervisor's loopback API (`127.0.0.1:9103`, its per-boot token) for this machine; other
machines act on desired state alone:

| API route | Supervisor route | What |
|---|---|---|
| `POST /api/components/{id}/install` | `POST /components/{id}/install` | `want = installed`, start now, 202 + status URL |
| `POST /api/components/{id}/remove?purge=true` | `POST /components/{id}/remove` | `want = absent` (same as 51.08's `DELETE`) |
| `GET /api/components/{id}/progress` | `GET /components/{id}/progress` | phase, %, detail code, bytes |
| `POST /api/components/offline`, `GET /api/components/offline/{name}` | `POST /components/offline`, `GET …` | make / fetch the file |
| `POST /api/components/import` | `POST /components/import` | import a file |

Each is admin-only (`403` before the supervisor is called) and audited. Role tests per route:
**no one** → 401, **viewer** → 403, **cloud-linked play sign-in** (a `scp: play` token with
`is_admin`) → 403, **admin** → 2xx. The supervisor's routes refuse a request without the token.

### 8. Words (English + Arabic, same PR)

| Key | English | Arabic |
|---|---|---|
| `components.phase.installing` | Installing ({percent}%) | جارٍ التثبيت ({percent}%) |
| `components.detail.pack_downloading` | Downloading {name}: {done} of {total} | جارٍ تنزيل {name}: {done} من {total} |
| `components.detail.pack_verifying` | Checking the download | جارٍ التحقق من التنزيل |
| `components.reason.verify_failed` | The download did not match this release. It was deleted. | لم يطابق التنزيل هذا الإصدار، فحُذف. |
| `components.reason.unpack_refused` | The file holds something unsafe ({entry}). Nothing was installed. | يحتوي الملف على شيء غير آمن ({entry})، ولم يُثبَّت شيء. |
| `components.reason.disk_space` | Not enough free space: {need} needed. | لا توجد مساحة كافية: يلزم {need}. |
| `components.offline.import` | Import a file | استيراد ملف |
| `components.offline.download` | Download for offline | تنزيل للاستخدام دون اتصال |
| `components.reason.offline_wrong_version` | This file is for Maktaba {version}. | هذا الملف لإصدار مكتبة {version}. |

## Acceptance criteria

- **Given** a Core home with no `runtime.ml`,
  **when** an admin installs `runtime.ml`,
  **then** the pack is downloaded, verified, unpacked under `<data>/components/runtime.ml/
  <version>_<digest>/`, `current` points at it, the venv is rebuilt from it with no PyPI access,
  and `Installed` becomes true.
- **Given** a pack whose sha256 differs from the index,
  **when** it is installed,
  **then** the file is deleted, the component is `Failed (verify_failed)`, nothing is unpacked.
- **Given** a pack with a `../escape` entry, a link pointing outside, or a device file,
  **when** it is unpacked,
  **then** it is refused, staging is deleted, and no file exists outside staging.
- **Given** `runtime.ml` verified and unpacked on CI hardware,
  **when** the install is timed from verify start to `current` written,
  **then** it takes ≤ 30 s (N10).
- **Given** transcription enabled,
  **when** an admin removes `runtime.ml`,
  **then** the API answers `409 component_required_by` listing `transcription`.
- **Given** transcription disabled and `runtime.ml` removed,
  **when** the reconciler finishes,
  **then** the pack folders of this version are gone, the venv has no `stt` extra, settings
  remain, and the desired row's removal is final only after that.
- **Given** an offline file made on an online home,
  **when** it is imported on a home with no network,
  **then** every item installs and verifies exactly as online.
- **Given** a viewer, a play sign-in or no sign-in,
  **when** any route of §7 is called,
  **then** it answers 403 / 403 / 401 and the supervisor is never called.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | unpacker, crafted tars | `/abs`, `a/../../x`, `C:\x`, case clash, hard link, device, FIFO, symlink out | each refused, nothing outside staging |
| T02 | unit | unpacker | entries past `unpacked_bytes` | refused |
| T03 | unit | unpacker | file not in `files.sha256` / wrong sha256 | refused |
| T04 | unit | installer, fake HTTP | download cut at 40 %, restart | resumes with `Range`, verifies, installs |
| T05 | unit | installer | pack with wrong sha256 | `verify_failed`, `.partial` deleted |
| T06 | unit | installer, fake disk | free space too small | `disk_space` before any download |
| T07 | unit | `current` pointer | crash between rename and pointer write | boot finds the folder, writes `current`, deletes staging |
| T08 | unit | remove | enabled dependent | `409 component_required_by` |
| T09 | unit | Full home, venv stamp with `stt,search`, no pack | reconcile | `Installed = true`, no download |
| T10 | integration | scratch Postgres, fake GitHub + fake Sigstore (`-tags e2etrust`) | install `runtime.ml` test pack, then remove | phases and progress rows in order; files gone |
| T11 | integration | offline file of a test bundle | import with the network off | installs; wrong version refused |
| T12 | role | API handlers | each route as no one / viewer / play sign-in / admin | 401 / 403 / 403 / 2xx; supervisor called only for admin |
| T13 | unit (web) | Components page | progress and each reason code | en and ar words present (`i18n` key test) |
| T14 | CI | `components-packs` | real `runtime.ml` pack for linux-amd64 | N10 ≤ 30 s; bad signature, bad sha256, traversal, expired index refused |

## Edge cases

- **Two installs of the same pack at once** (two clicks, or a node and the home sharing a disk):
  a lock file per component (`layout` lock helpers); the second waits and finds it done.
- **An update starts during an install.** The updater waits for running installs (up to 10 min)
  before staging, so 51.18 sees a settled set.
- **The gated Cohere model in an offline file.** Included only if this home could download it
  (its Hugging Face token). The file is for the owner's own machines, like `model_share`.
- **Windows.** Pack paths stay under 200 characters from `<data>` (the build checks); a file
  held by an antivirus during the rename is retried for 30 s, then `Failed (busy)`.
- **A component removed on one machine** while another still needs it: removal is per machine;
  the home's desired `absent` applies to every machine without an override (51.12).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/packs/` (new) | `fetch.go` (index, resumable download), `unpack.go` (safe unpacker), `store.go` (folders, `current`, lock, prune helper), `offline.go` (make / import), tests. |
| `cmd/maktaba-server/internal/supervisor/components.go` (from 51.04) | asset kinds: install, remove finalizer, status rows, progress. |
| `cmd/maktaba-server/go.mod` | adds `github.com/klauspost/compress` (zstd; not a dependency today). |
| `cmd/maktaba-server/serve_components.go` (new) | loopback routes of §7 behind the per-boot token, wired next to `newConfigAdmin` in `cmd_serve.go`. |
| `cmd/maktaba-server/cmd_serve.go` | wire the handler. |
| `cmd/maktaba-server/internal/appvenv/appvenv.go` | install from a pack wheelhouse with given extras; rebuild with `""`. |
| `cmd/maktaba-server/internal/supervisor/ollama.go` | fall back to the pack's `ollama/`. |
| `cmd/maktaba-server/detail_codes.json` | `pack_*` detail codes. |
| `pipeline/src/maktaba_pipeline/models/fetch_cli.py` | `--revision`, `remove`. |
| `api/internal/handlers/components/` (new) | the routes of §7, admin wrapper, audit, supervisor client (as `serversettings`). |
| `api/internal/router/components.go` (new) | mount. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | the words of §8. |
| `shared/api/openapi.yaml` | the new routes. |
| `.github/workflows/_components-packs.yml` (from 51.15) | the install timing (N10) and the refusal cases. |

### Tests

- T01–T09 Go unit (`internal/packs`, supervisor); T12 API unit; T13 web unit.
- T10–T11 Go integration job (scratch Postgres, `-tags e2etrust`).
- **CI only:** `components-packs` (T14, N10).

### Rollout

Nothing installs unless an admin asks: Full homes have every asset (F15). The routes ship with
the Components page (51.10) buttons hidden until this story merges.

### Rollback

`[components] packs = off` in `server.toml`: the reconciler reports `Installed` from what is on
disk and refuses new installs with `packs_disabled`. Installed packs keep working.

### Dependencies

51.04 (the supervisor reconciler and `component_status`), 51.15 (the index and its verify).
