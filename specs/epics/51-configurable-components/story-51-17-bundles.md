# Story 51.17 — Bundles: the Core payload and the setup choice

> Epic 51 · Phase 3 · Depends on: 51.10, 51.16 · Needed by: —

## Why

A home that only wants to play and organise its media still gets everything: the Mac app
carries ollama, setup's install screen downloads "about 2 GB" of transcription tools
(`setup.install.venv`), then the models, and the server starts the stt workers, the index
worker and ollama. Setup never asks what the owner wants (`cmd_setup_json.go` steps: check,
storage, admin, libraries, models, plex, venv, llm, adopt, finish).

This story adds the one question, "Choose what to run", with four answers (Full, Core, Worker
machine, Custom), a smaller Mac app for Core, and a way to change the answer later. Full stays
exactly today's Maktaba.

## Description

### 1. What the payloads really hold today (measured 2026-11-03, v0.3.62)

| Asset | Bytes | What makes it big |
|---|---|---|
| `Maktaba_0.3.62_aarch64.dmg` | 188,301,220 | Go binaries, Postgres, ffmpeg, Python, **ollama + llama-server** |
| `Maktaba_0.3.62_x64-setup.exe` | 138,720,101 | same, no ollama, plus the VC++ runtime |
| `maktaba-linux-payload-0.3.62-x86_64.tar.gz` | 111,972,001 | no ollama, no ffmpeg |
| `maktaba-linux-payload-0.3.62-aarch64.tar.gz` | 102,783,605 | same |
| upstream `ollama-darwin.tgz` 0.40.2 (universal, before thinning) | 167,473,220 | |

**No payload carries the ML wheels.** They come from PyPI when the venv is first built
(`venv_bootstrap.py`, `DEFAULT_EXTRAS = "stt,search"`), and the bootstrap installs **every pin
of `constraints.txt`**, so every payload's constraints pull torch and the rest. The models are
downloaded after that. So the weight of Full is mostly *after* the installer: ~2 GB of wheels
and a few GB of models.

### 2. The flavours

| Platform | Full | Core |
|---|---|---|
| macOS arm64 | `Maktaba_<v>_aarch64.dmg` (as today) | `Maktaba-Core_<v>_aarch64.dmg`: no `ollama/` |
| Windows x64 | one `-setup.exe` for both | the same installer: there is no ollama to leave out |
| Linux (AppImage, `.deb`, `.rpm`, payload tarball) | `maktaba-full`: `Depends: maktaba`, ships the `runtime.ml` and `tool.yt-dlp` packs of its arch in `/usr/share/maktaba/packs/` | `maktaba`: the app as today |

Every payload adds **`pipeline/constraints-core.txt`**: `uv export` of the runtime with no
extras (`deploy/packaging/node/build-release.sh` writes it next to the full set). A Core venv is
built from it with `Extras = ""` (`internal/appvenv`): no torch, no MLX. The full constraints
stay in the payload, so Full keeps building its venv from PyPI exactly as today.

`payload.json` gains `"flavour": "full" | "core"`. On Linux, `maktaba-full`'s packs are imported
at first start through 51.16's offline import (same verification), so a Linux Full install
needs no wheel download. Windows and Linux could have had a second installer each, but it would
differ by a few kilobytes of constraints: the setup choice does the work instead (H9 asks for
two flavours where they differ; they differ on the Mac).

The Core dmg updates to Core dmgs: `tools/release/updaterfeed` gets the keys
`darwin-aarch64-core-app` / `darwin-aarch64-core`, and the app picks its key from its payload's
`flavour` (`updater/appbundle.go` `feedPlatform`). Adding LLM to a Core Mac installs the
`runtime.ollama` pack (51.16); the payload flavour never changes by itself.

### 3. The bundles (catalog `bundles = […]`, README §4.8, §5)

| Bundle | Enabled at setup | Downloads at setup |
|---|---|---|
| **Full** | every component marked F | the venv's wheels (PyPI) and the models, as today |
| **Core** | core, thumbnails, chapters, convert, dedup, keyword search, analytics, backups, updates, mDNS, queue health | the core venv (~150 MB of base wheels) |
| **Worker machine** | what the home chooses for it (51.12); offered on the *join a home* path | what those components require |
| **Custom** | starts from Core or Full, admin ticks; `requires` ticked with it and shown | what the ticks require |

The choice is written by setup to `<data>/state/components.json` (`{"bundle":"core",
"custom":{…}}`), the cache the supervisor reads before the database is up (README §4.4). At
the database's first creation, 51.03's seed reads it instead of "today's switches"; no file
means Full (H8). The N9 promise (4 processes idle) follows: Core enables no `process` component.

### 4. The setup step "Choose what to run"

A new step `bundle` in `cmd_setup_json.go` `setupSteps`, between `libraries` and `models`:

- input `{"check_only":true}` → result: the four bundles, for each the components it enables
  (catalog `title_key`), the download size (from the index and `constraints*.txt`), the disk
  it needs, and whether this machine can run it (RAM and GPU from `stepCheck`'s facts; Full on
  an 8 GB machine is allowed with a warning).
- input `{"bundle":"core"}` or `{"bundle":"custom","enable":["transcription"]}` → writes the
  cache, result `{"bundle", "enabled":[…], "download_bytes"}`. Errors: `bad_input`,
  `unknown_component`, `platform_unsupported`.

After it, the `models` screen is shown only when the bundle needs a model, the `llm` step only
when it enables `llm`, and the `venv` step builds with the bundle's extras.

Callers:
- the desktop app's step list (`apps/desktop/src-tauri/src/setup.rs` `STEPS`), its screen in
  `web/src/pages/Setup/SetupWizard.tsx` (`Screen` gains `"bundle"`, in `ORDER` and `MAC_ORDER`
  after `libraries`), `web/src/lib/desktopSetup.ts` `StepName`;
- the Core dmg preselects Core, the Full one Full; the owner can still pick either (a Full
  choice on a Core dmg installs `runtime.ollama` as a pack);
- headless: `maktaba-server setup --bundle core` and `maktaba-server components bundle core`.

Words (English + Arabic, same PR):

| Key | English | Arabic |
|---|---|---|
| `setup.bundle.title` | Choose what to run | اختر ما يعمل |
| `setup.bundle.intro` | You can add or remove parts later in Settings → Components. | يمكنك إضافة الأجزاء أو إزالتها لاحقًا من الإعدادات ← المكوّنات. |
| `setup.bundle.full` | Full: everything, with transcription, search by meaning, Live TV and the language model | الكامل: كل شيء، مع التفريغ والبحث بالمعنى والبث المباشر والنموذج اللغوي |
| `setup.bundle.core` | Core: play and organise your library. Smaller and lighter. | الأساسي: تشغيل مكتبتك وتنظيمها. أصغر وأخف. |
| `setup.bundle.worker` | Worker machine: takes jobs for another Maktaba | جهاز عامل: ينفّذ مهام لمكتبة أخرى |
| `setup.bundle.custom` | Custom: pick each part | مخصّص: اختر كل جزء |
| `setup.bundle.download` | Downloads about {size} | ينزّل نحو {size} |
| `setup.bundle.lowMemory` | This computer has {ram}. Full works best with 16 GB or more. | في هذا الحاسوب {ram}. يعمل الكامل أفضل مع 16 غيغابايت أو أكثر. |

### 5. Changing bundle later

`GET /api/components/bundles` lists the bundles and which one the home is "based on".
`POST /api/components/bundle {"bundle":"full","dry_run":true}` answers the difference:

```json
{ "based_on": "core", "to": "full",
  "enable": ["transcription","search.semantic","llm","livetv", …],
  "install": [{"id":"runtime.ml","bytes":812000000}, {"id":"model.whisper-turbo","bytes":1617000000}],
  "disable": [], "remove": [], "download_bytes": 3100000000 }
```

The same call without `dry_run` applies it as **one** transaction of desired-state changes
(one audit row with before and after, one `NOTIFY`). Going to a smaller bundle disables, and
removes files only with `"remove_unused": true`. The Components page (51.10) shows "Based on
Core, plus Transcription" and a *Change bundle* dialog with the difference. Admin-only; role
tests as no one (401), viewer (403), cloud-linked play sign-in (403), admin (200).

### 6. The `bundle-size` CI job (N7–N9)

`.github/workflows/_bundle-size.yml` (new), on `macos-15`, called from `_mac-app.yml` on PRs
that touch `tools/release/*/payload.sh`, the constraints or the catalog, and from
`desktop-release.yml`'s Mac leg on every tag:

1. Build both payloads (`payload.sh --flavour full|core`) and both dmgs, unsigned.
2. **Footprint after setup** (N7): run setup's `venv` step on each payload in a scratch
   `HOME` with a local wheel cache; Full adds the model bytes from the index (summed, not
   downloaded). Core ≤ **45 %** of Full and ≤ **400 MB**. The dmg sizes are recorded too, and
   the Core dmg must be smaller than the Full one by at least the ollama files' size.
3. **Idle memory** (N8): start the Core payload (`maktaba-launcher serve`, scratch data dir),
   wait for `/healthz`, idle 120 s, sum RSS of every child: ≤ **700 MB**.
4. **Processes** (N9): exactly postgres, api, streaming, pipeline besides supervisor and
   launcher. The supervisor's own mDNS loop announces through the system's responder
   (`dns-sd` through a small `sh` on macOS, `avahi-publish-service` on Linux): a helper of the
   supervisor, listed apart and not counted; its memory counts in N8.
5. Write `bundle-size.json` as an artifact and in the run summary; compare with
   `tools/release/bundle-size-baseline.json` (new, seeded with the v0.3.62 numbers of §1) and
   fail on a budget breach or a growth of more than 10 % over the baseline without the file
   being updated in the same PR.

## Acceptance criteria

- **Given** a tag,
  **when** `desktop-release.yml` finishes,
  **then** there are two Mac dmgs whose `payload.json` say `full` and `core`, and the Core one
  has no `ollama/`.
- **Given** the desktop app's first run,
  **when** the owner reaches "Choose what to run" and picks Core,
  **then** no model screen is shown, the venv is built from `constraints-core.txt`, and the
  server starts with 4 processes besides supervisor and launcher.
- **Given** a fresh install with no `components.json`,
  **when** the database is first created,
  **then** desired state is Full (today's behaviour).
- **Given** a Core home,
  **when** an admin applies Full with `dry_run`,
  **then** the answer lists every component to enable and every asset to install with bytes,
  and nothing changes; without `dry_run` all of it changes in one generation.
- **Given** the `bundle-size` job,
  **when** it runs,
  **then** Core's footprint after setup is ≤ 45 % of Full's and ≤ 400 MB, idle RSS ≤ 700 MB.
- **Given** a viewer or a play sign-in,
  **when** it calls `POST /api/components/bundle`,
  **then** it gets 403 and nothing changes.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | `stepBundle` | `check_only` on an 8 GB fake host | four bundles, sizes, Full warns `low_memory` |
| T02 | unit (Go) | `stepBundle` | `custom` with `subtitles` only | `transcription`, `runtime.ml` and a model added as requires |
| T03 | unit (Go) | 51.03 seed | `components.json` = core | desired = Core set |
| T04 | unit (Go) | bundle diff | core → full, full → core | expected lists; `remove` empty unless `remove_unused` |
| T05 | role | API | `GET/POST /api/components/bundle*` as no one / viewer / play / admin | 401 / 403 / 403 / 200 |
| T06 | unit (web) | SetupWizard | Core chosen | models screen skipped; en + ar keys exist |
| T07 | unit (Rust) | `setup.rs` | `STEPS` order | `bundle` after `libraries` |
| T08 | unit | updaterfeed | a release with both dmgs | `darwin-aarch64-core-app` present; Full keys unchanged |
| T09 | script | `payload.sh --flavour core` (shellcheck + `check.sh`) | build | no `ollama/`; `payload.json` flavour core |
| T10 | Linux pkg | `check-app.sh` | install `maktaba-full` | packs imported at first start, verified |
| T11 | CI | `bundle-size` | both flavours | N7, N8, N9 |

## Edge cases

- **A Core dmg owner picks Full.** Allowed; `runtime.ollama` is installed as a pack. The app
  keeps updating from the Core feed; the pack follows each update (51.18).
- **Worker machine on a home that offers nothing yet.** The machine runs core only and shows
  "Waiting for the home to give this machine work".
- **Custom with a component this platform can't run** (`runtime.ollama` on Windows): shown
  disabled with `platform_unsupported` words.
- **Offline first run on Core:** the core wheels come from PyPI like today's venv; an offline
  file (51.16) can carry them as a `runtime.core` wheelhouse in a later release if owners ask.
- **Telemetry** (opt-in only) reports the bundle name, never more.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `tools/release/mac-app/payload.sh` | `--flavour full|core`; core skips `ollama/`; `flavour` in `payload.json`; `constraints-core.txt`. |
| `tools/release/mac-app/build-app.sh`, `tools/release/mac-app/check.sh` | build and check both; product name `Maktaba-Core` for the core dmg file. |
| `tools/release/win-app/payload.sh`, `tools/release/linux-app/payload.sh` | `constraints-core.txt`; `flavour: full`. |
| `tools/release/linux-app/bundle.sh`, `tools/release/linux-app/check-app.sh` | the `maktaba-full` package with the packs. |
| `deploy/packaging/node/build-release.sh` | writes `constraints-core.txt`. |
| `tools/release/updaterfeed/updaterfeed.go` | core feed keys. |
| `cmd/maktaba-server/internal/updater/appbundle.go` | feed key by payload flavour, falling back to the Full key. |
| `cmd/maktaba-server/cmd_setup_json.go` | step `bundle`; `venv` and `llm` follow the choice. |
| `cmd/maktaba-server/internal/appvenv/appvenv.go` | core constraints with `Extras = ""`. |
| `apps/desktop/src-tauri/src/setup.rs` | `STEPS` gains `bundle`. |
| `apps/desktop/src-tauri/src/shell.rs` | `updater_builder().target("darwin-aarch64-core")` when the bundled payload's flavour is core, with the Full key as fallback. |
| `web/src/pages/Setup/SetupWizard.tsx`, `web/src/lib/desktopSetup.ts` | the screen; `StepName`. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | §4 words, and the Change bundle dialog's. |
| `api/internal/handlers/components/bundle.go` (new) | `GET /api/components/bundles`, `POST /api/components/bundle`. |
| `web/src/pages/Admin/` Components page (51.10) | "Based on …" line and the dialog. |
| `.github/workflows/_bundle-size.yml` (new), `.github/workflows/_mac-app.yml`, `.github/workflows/desktop-release.yml` | the job. |
| `tools/release/bundle-size-baseline.json` (new) | v0.3.62 baseline. |

### Tests

- T01–T04 Go unit; T05 API role tests; T06 web unit; T07 Rust unit (`cargo test`); T08 updaterfeed unit.
- T09 in `mac-app`, T10 in `linux-app`.
- **CI only:** `bundle-size` (T11); Mac UI is never tested locally (standing rule).

### Rollout

The setup step ships first with Full preselected; the Core dmg is attached from the next tag.
Existing installs see nothing: they are Full (H8) and keep their feed key.

### Rollback

Stop attaching the Core dmg (one matrix entry). A Core app whose release has no core feed key
falls back to the Full key (`feedPlatform`), so Core homes already installed keep updating, onto
Full payloads, which are a superset.

### Dependencies

51.10 (the Components page for the dialog), 51.16 (packs for anything Core leaves out).
