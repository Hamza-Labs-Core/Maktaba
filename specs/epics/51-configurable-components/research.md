# Epic 51 research: what runs today, and how others switch parts on and off

> Checked 2026-11-03 against `origin/main` 7aa1af482. Paths are from the repo root. Line
> numbers are where the code was read on that day; they drift.

## 1. What runs today, and where it is switched

### 1.1 Processes the supervisor runs (`cmd/maktaba-server serve`)

| Part | Started at | Switch today (where stored) | Hard-wired? | Needs |
|---|---|---|---|---|
| Postgres (bundled) | `cmd_serve.go:219,260-266`; `internal/supervisor/postgres.go:27`, runs `First` | `[postgres] enabled = auto\|on\|off` (`internal/config/config.go:84`) | no | payload `postgres/` + pgvector |
| Nightly backups | `cmd_serve.go:266` (`pg.RunBackups`) | `[backups]` (`internal/config/backups.go:21-41`) | runs with bundled PG | `pg_dump` |
| Node DB copy (full node) | `cmd_serve.go:227,268-274` | implied by a joined `node.json` | yes | postgres |
| ollama (LLM server) | `cmd_serve.go:234,275`; `internal/supervisor/ollama.go:27` | `[llm] enabled`, `url`, `pull`, `models` (`config.go:135-151`) | no | payload `ollama/`, models pulled at runtime |
| Relay agent `maktaba-cloudlink` | `cmd_serve.go:280-291`; `RelayWanted` `internal/supervisor/relay.go:52-62` | `[cloud] enabled` + claim/slug (`config.go:370-379`); `OnDemand` | no | `bin/maktaba-cloudlink` |
| API | `cmd_serve.go:304`; `supervisor.go:207-217` | `--role` only | yes | — |
| Streaming | `supervisor.go:219-224` | `--role` | yes | ffmpeg |
| TV slots blue/green/amber | `cmd_serve.go:332-338`; `/slots/{i}/start\|stop` (`internal/supervisor/state.go:247-277`) | `[tv] slots = on\|off` (`config.go:208`, `slots.go:67-76`) | `Want` toggles at runtime | ffmpeg, yt-dlp |
| Pipeline (main worker) | `supervisor.go:226-237` | `--role`; env `MAKTABA_PIPELINE_STAGES` / `_EXCLUDE_STAGES` | yes | venv, ffmpeg |
| Transcription workers `stt-1..N` | `cmd_serve.go:311-315`; `internal/supervisor/instances.go:31` | `[pipeline] instances`, `[transcription] workers` (`config.go:162,329`); planned from RAM/GPU (`stt.go:296-353`) | no | Whisper models, refine model |
| Index worker | `cmd_serve.go:317-321`; `index.go:72` (stages `index,chapter_infer`) | `[pipeline] index_worker` (`config.go:171`) | no | embedder |
| Chroma → pgvector copy | `cmd_serve.go:323-327`; `chromacopy.go:43` | automatic, once | yes | — |
| Web UI + `/api` proxy (in process) | `cmd_serve.go:201-209` | `--no-web`, `--role web` | mostly | embedded SPA |
| Supervisor mDNS | `cmd_serve.go:213-215` | `[server] mdns`, `--no-mdns` | no | — |
| Update machinery | `cmd_serve.go:242-256` | `MAKTABA_UPDATE_DISABLE`; `rules.updates` | no | — |
| ffmpeg watcher | `cmd_serve.go:404-405` | — | yes | ffmpeg |
| venv build gate | `serve_appbundle.go:131-160` | skipped when `MAKTABA_PYTHON` / `[pipeline] python` is set | yes | ML wheels |

The supervisor's `Process` struct (`internal/supervisor/supervisor.go:52-120`) has `Probe`,
`OnDemand`, `Once`/`Tries`, `Prepare`, `Survives`, `Binds`, `StopGrace` and `First`. Restart
policy (`internal/supervisor/policy.go:11-122`, defaults `internal/config/supervisor.go:37-52`):
`restart = always|on-failure|never`, backoff 1–60 s, `max_restarts` 5 in 10 min, then failed and
retried after 5 min. **`Want(role, instance, on)` (`run.go:1024`) and `Restart` (`run.go:988`)
already start and stop a process at runtime**; only TV slots and the relay use them.

The supervisor's loopback API on 127.0.0.1:9103 (per-boot token, `cmd_serve.go:416-440`):
`/processes`, `/llm`, `/healthz`, `/slots/{i}/start|stop` (`state.go:233-283`),
`/config/settings` + `/config/restart` (`serve_config.go:68-83`), `/config/backups`,
`/config/storage*`, `/update/*`, `/switchover/*`, `/logs/files`, `/metrics`, `/agent/*` on nodes.

`server.toml` sections: `server, database, media, transcription, cloud, supervisor, tv,
pipeline, postgres, llm, telemetry, backups` (`config.go:35-46`). Only listen addresses, mDNS,
media roots and path maps are editable from the web (`internal/config/editable.go:175-201`);
none of the component switches are.

Ports registry: `shared/go/ports/registry.go:25-110`, checked at start (`cmd_serve.go:381`).

### 1.2 Loops inside the API (`api/main.go`)

| Loop | Started at | Switch today |
|---|---|---|
| idempotency sweep | `api/main.go:321` | — |
| process recorder | `:378` | needs `MAKTABA_SUPERVISOR_URL` |
| update checker | `:390-395` | `MAKTABA_UPDATE_DISABLE` (`:1004-1009`); off on a node |
| sharepath reload | `:457` | — |
| licence refresher | `:577-584` | when a verifier / cloud is set |
| cloudlink reporter | `:610-614` | `MAKTABA_CLOUDLINK_ADMIN` |
| tvremote controller, publisher, sampler | `router/p32_remote.go:88-92` | — |
| tvchange controller | `router/p33.go:62` | — |
| updates discoverer, scheduler, driver, health sampler | `router/p35.go:269-277` | loops always run; `rules.updates` decides what they do |
| watch reaper | `:678-683` | `MAKTABA_ANALYTICS_RETENTION_DAYS` |
| usage counter | `:685` | — |
| telemetry sender | `:692-699` | `MAKTABA_TELEMETRY_ENABLED` (from `[telemetry] enabled`) + `telemetry.share_server_data` |
| cluster housekeeping, hub, home link, push DB, ensure access, switchover | `api/cluster_bootstrap.go:111-216` | — |
| API mDNS | `cluster_bootstrap.go:58` | `MAKTABA_DISCOVERY_MDNS`; skipped when supervised |
| pairing sweep, JWT key reaper | `:717`, `:788` | — |
| event bus + pruner | `router/p6_eventbus.go:36,42` | — |
| enrichment auto-accept | `router/p6.go:300` | — |
| queue snapshots | `router/p6.go:358` | — |
| node-mode loops | `api/node_bootstrap.go:315-370,807` | `MAKTABA_NODE_MODE` |

### 1.3 Loops inside streaming

| Loop | Started at | Switch today |
|---|---|---|
| watchdog | `streaming/main.go:294` | — |
| cache GC, session reaper, VOD purge | `streaming/main.go:557-575` | `MAKTABA_CACHE_*` |
| hwaccel watcher | `:469` | `MAKTABA_HWACCEL_PREFER` |
| HDHomeRun + SSDP | `streaming/tvcluster_wire.go:291-302`, `slots_wire.go:198` | `hdhr_device.enabled` (DB, 0084) |
| channel engine, fences, sweeper, push relay | `tvcluster_wire.go:357-377`, `slots_wire.go:130-173` | `[tv] slots` picks slot or in-process |
| full-node VOD / offline TV | `streaming/main.go:272-275`, `offline.go:135-141` | `MAKTABA_FULL_NODE` |

### 1.4 The pipeline

- **Stages.** About 55 `StageDef`s in `pipeline/src/maktaba_pipeline/engine/graph.py:319-985`,
  each with a `pool`, a `placement` (`any`/`home`, `:267`) and an optional `extension`
  (`:260`). Pools include `network_yt`, `gpu` (transcribe), `nas_write`, `cpu`, `lookup`,
  `convert`, `nas_read`.
- **Start** (`runtime.py:586-691`): discover extensions → `ensure_rows` → `sync_graph` writes
  `job_stages` (home only) → `StageSelector` → `register_worker`.
- **Why a stage is off for a worker** (`cluster/stages.py:1-25,95-135`), first match wins,
  stored in `workers.stages_off`: `not_selected`, `version_ahead`, `unavailable`, `home_only`,
  `storage`, `switched_off` (`cluster_node_stages.enabled`, 0103), `draining`
  (`cluster_nodes.drain`), `paused: <why>`. Re-read at every refresh: **live**.
- **Global stage enable**: `job_stages.enabled` + `disabled_reason` (`not_configured`,
  `extension_disabled`, set by the worker in `engine/sync.py:36-91`; `operator`, set by
  `PATCH /api/queues/{stage}` in `api/internal/handlers/queues/control.go:150-171`).
- **Background tasks** (`runtime.py:452,523-531,723-769`): index-close, heartbeat,
  stage-selection refresher, reconciler, pruner, audio-cache sweep, channel top-up (all
  hard-wired); queue-health watchers (home with a share; `rules.queue_health`); network probes
  and cluster agent (`MAKTABA_CLUSTER_AGENT=0`); reaper.
- **Whole-process switches** in `__main__.py`: `MAKTABA_PIPELINE_NO_ENGINE` (`:401`),
  `MAKTABA_PIPELINE_SHARE=off` (`:407`), `MAKTABA_PIPELINE_SEMANTIC=off` (`:415`),
  `MAKTABA_PIPELINE_DRAIN_SEC` (default 15, `:423`).
- **Extensions**: `BUILTIN` = plex, dizquetv, cloudflare_stream (`extensions/__init__.py:22-26`),
  plus Python entry points (`:48-49`). State in `extensions` (`enabled` default false,
  `0089_queue_control_and_logs.sql:70-72`; `extensions/store.py:76-117`). Routes
  `GET/PUT /api/extensions/{name}` and `/test`.
- **Settings**: `rules.py` `SECTIONS` (`:77`) maps each section to `app_settings` key
  `rules.<section>` (read at `:186-192`). Sections: planner, groups, detect, tag, tagcheck,
  books, dizquetv, tv, tv_output, tv_cluster, tv_remote, tv_simulcast, tv_staging, tv_strip,
  enrich, display_titles, exactdates, audit, plexlive, repair, relay, storage, updates,
  queue_health, cluster, transcription, system_health, search. `search_settings.py`,
  `system_health_settings.py`, `updates_settings.py` and `cluster/machines_settings.py` are
  schema-only modules that generate `schema.json`. Existing `enabled` fields include
  `tv_settings.py:263,300,374,388,432`, `tvrelease/config.py:40`, `relay.py:64`,
  `classify/books.py:59`, `classify/display_title.py:86`, `classify/tag_check.py:75`,
  `enrich/providers.py:69`, `channels/base.py:161`.
- **Binaries and models**: `binaries.py:15-22` (ffmpeg from `MAKTABA_FFMPEG` or PATH);
  `model_dirs.py` (`MAKTABA_MODEL_CACHE_DIR` → `HF_HOME`, `CAMELTOOLS_DATA`);
  `venv_bootstrap.py:1-40` builds the venv from the payload's wheel with hash-pinned
  constraints (CUDA constraints on Windows with an NVIDIA GPU). Extras `mediaintel` (yt-dlp)
  and `stt` (mlx-whisper, faster-whisper, torch, transformers, camel-tools) in
  `pipeline/pyproject.toml:27-50`.

### 1.5 The queue: what already pauses without losing work

- Job states `pending, claimed, running, paused, resuming, done, failed, cancelled`
  (`0002_processing_jobs.sql:75-80`); per job `pause_requested`, `cancel_requested`,
  `not_before`, `parked_on`, `failure_class` (`0143_queue_health.sql:25-29`).
- Claim (`engine/claim.py:99-133`): `FOR UPDATE SKIP LOCKED`, stride fair-share over
  `claim_shares` (0172), requires `s.enabled AND NOT s.paused AND NOT queue_control.paused`,
  respects `worker_pools`.
- Pause layers: `queue_control` (global), `job_stages.paused` with an owner trigger (0172),
  `job_stages.enabled`, `cluster_node_stages.enabled`, `cluster_nodes.drain`, pool size.
- Shutdown drain 15 s; the index worker has a longer `StopGrace` (`supervisor.go:72-75`).

### 1.6 Payload, models, launcher, desktop app

- Mac payload: `bin/` (server, api, streaming, launcher, cloudlink), `postgres/`+pgvector,
  `ffmpeg/`, `ollama/`, `python/`, `pipeline/*.whl` + constraints, `migrations/`,
  `licenses/`, `payload.json` (`tools/release/mac-app/payload.sh:2-17`, schema `:222-235`).
  Windows adds CUDA constraints and has no ollama (`tools/release/win-app/payload.sh:5-18`).
  Linux has no ffmpeg and adds `files.sha256` (`tools/release/linux-app/payload.sh:6-20`).
- Downloaded after install: ML wheels (venv), Whisper models (`maktaba-server models download`,
  catalog `cmd/maktaba-server/internal/models/models.go:35-41`, through the HF CLI), LLM models (`models llm`, or
  `[llm] pull = auto`), the refine model (`setup_models.go:22-28`).
- Setup steps: check, storage, admin, libraries, models, plex, venv, llm, adopt, finish
  (`cmd_setup_json.go:96-105`).
- Launcher (`cmd/maktaba-launcher/run.go:31-63`): runs `releases/<current>/maktaba-server
  serve`, restarts on exit 75, rolls back a pending release after 3 exits in 10 min or no
  confirm in 15 min; app-bundle mode keeps a payload to roll back to (`run.go:340,422`);
  Linux runtime copies under `<data>/runtime/<v>_<digest>` (`runtime.go`, `cmd_runtime.go:87-229`).
- Desktop app (Tauri) is **not** the supervisor: a LaunchAgent / sign-in task runs
  `--server-agent`, which execs `maktaba-launcher serve` (`apps/desktop/src-tauri/src/agent.rs`).
  It polls `/processes` (`status.rs`), runs setup steps (`setup.rs`) and installs ordered
  updates (`autoinstall.rs`).
- Release signing: GitHub releases signed by Sigstore; the update rehearsal uses a fake
  Sigstore with `-tags e2etrust` (`docs/update-rehearsal.md`).

### 1.7 What clients can learn today

- `GET /api/me/flags` (+ `/keys`): signed per-user flags (`api/internal/handlers/flags/flags.go:66-67`);
  declarations `cloud_relay, multi_user, cloud_metadata_backup, advanced_analytics, federation,
  flags_admin, early_access` (`api/internal/flags/declarations.go:71-79`).
- `GET /api/discovery/home`: id, name, version, URLs, sign-in methods only.
- `GET /api/entitlements`, `/api/admin/license`, `/api/stream/capabilities` (codecs only),
  `/api/admin/system/status`, `/api/system/processes`.
- Web: `useFlag` / `<Flag>` exist (`web/src/lib/flags.tsx`) but **no page uses them** except
  the Experiments section. **No client hides a feature because a part of the server is off.**

### 1.8 Editions and licences

- Licence tiers (Epic 16): free (1 seat), home (4 seats; relay, multi-user, cloud backup,
  advanced analytics), pro (unlimited; federation) (`api/internal/subscriptions/subscriptions.go:36-114`).
- Web editions at build time: `VITE_MAKTABA_EDITION = full|player|remote`
  (`web/src/lib/edition.ts`; Epic 42 §1). Editions hide UI; rights stay on the server.

## 2. How others do it

### Plex Media Server

- Codecs are fetched the first time content needs them, then kept. There is no supported way
  to fetch them all in advance, which hurts offline installs. Some components update apart
  from the server. ([support article][plex-components]; forum threads [1][plex-forum-1],
  [2][plex-forum-2])
- Metadata agents are chosen per library. Legacy agents are hidden since 1.43.0 and are to be
  replaced by Custom Metadata Providers. ([agents][plex-agents],
  [announcement][plex-providers])
- Scheduled tasks run in a maintenance window, each with its own checkbox.
  ([scheduled tasks][plex-tasks])

### Jellyfin

- Plugin repositories are `manifest.json` URLs. A plugin installed by hand appears after a
  restart. Plugin settings live in XML under `configurations/` and survive reinstalls.
  ([plugins][jf-plugins])
- Plugin metadata: `guid`, 4-part `version`, `targetAbi`, `framework`, `artifacts`,
  `changelog`. ([template build.yaml][jf-template])
- `PluginStatus`: `Active`, `Restart`, `Disabled`, `NotSupported` (ABI mismatch),
  `Malfunctioned`, `Superseded`, `Deleted`. Enable, disable and install take effect at the next
  restart. **That is the pitfall this epic avoids.** ([PluginStatus.cs][jf-status])
- Scheduled task triggers: daily, weekly, interval, startup; any task can run by hand.
  ([triggers][jf-triggers], [tasks][jf-tasks])
- Per-library options carry feature toggles, each with two switches: whether it is on, and
  whether it runs during a scan. ([LibraryOptions.cs][jf-libopts])

### Home Assistant

- Config entry states: `NOT_LOADED`, `SETUP_IN_PROGRESS`, `LOADED`, `SETUP_ERROR`,
  `SETUP_RETRY` (backoff up to 10 min), `MIGRATION_ERROR`, `UNLOAD_IN_PROGRESS`,
  `FAILED_UNLOAD`. Integrations implement unload, so an entry can be reloaded or disabled
  without a restart. Entries have `VERSION` and `MINOR_VERSION`: a minor bump stays compatible,
  a major bump runs a migration, and a failed migration needs a repair.
  ([config entries][ha-entries])
- Add-on `config.yaml`: `boot: auto|manual`, `startup` order, a `watchdog` health URL,
  `options` (defaults) + `schema` (types). The Supervisor validates against the schema and
  builds the form from it. `stage: stable|experimental|deprecated`.
  ([add-on configuration][ha-addon])

### Nextcloud apps

- `info.xml` declares the server version range plus PHP, database, library and command
  dependencies, repair steps, background jobs and settings classes. ([info.xml][nc-info])
- `occ app:install --keep-disabled`, `app:enable --groups`, `app:disable`,
  `app:remove --keep-data`, `app:update`. ([occ apps][nc-occ])
- Code signing: one root CA signs per-app certificates whose CN is the app id;
  `signature.json` holds SHA-512 of every file. App-store apps must be signed, and an app
  signed once must stay signed. ([code signing][nc-signing])

### Kubernetes

- Controllers are loops that move the current state toward the desired `spec`.
  ([controllers][k8s-controller])
- `spec` vs `status`. Status is written through its own subresource. Conditions have `type`,
  `status: True|False|Unknown`, `reason`, `message`, `lastTransitionTime` and
  `observedGeneration`, are level-based, and replace the deprecated single `phase`.
  ([API conventions][k8s-conventions])
- Finalizers hold an object in `Terminating` until each cleanup removes its key.
  ([finalizers][k8s-finalizers])

### systemd

- `enable` (start at boot) is separate from `start` (run now). `Wants=` is weak, `Requires=`
  passes stop and restart on, `BindsTo=` also stops on death, `Conflicts=` stops the other,
  and `After=`/`Before=` only set order. ([systemd.unit][sd-unit])
- `ExecStop`, then `TimeoutStopSec`, then SIGTERM and SIGKILL. `Restart=` policies, backoff,
  start rate limits, `WatchdogSec`. A stop asked through systemd never triggers a restart.
  ([systemd.service][sd-service])

### Feature flags

- Toggle categories by lifetime and how dynamic they are: release, experiment, ops / kill
  switch, permission. Put each decision in one place and inject the result, show operators
  the current state, and treat toggles as inventory with expiry. ([Feature Toggles][fowler])
- OpenFeature: an evaluation returns `value`, `variant`, `reason` (`STATIC`, `DISABLED`,
  `ERROR`…), `errorCode`. When the provider isn't ready, the caller's default is returned.
  ([providers][openfeature])
- Unleash: strategies per flag per environment; the SDK evaluates locally from a downloaded
  set. ([activation strategies][unleash])

### Signed package indexes

- TUF roles (root, targets, snapshot, timestamp). All metadata expires, and clients refuse
  versions older than what they have seen (anti-rollback, anti-freeze). ([TUF metadata][tuf])
- Sigstore/cosign: keyless signing tied to an OIDC identity, logged in Rekor.
  ([cosign][cosign])
- apt: signed `InRelease` with SHA256 of each index. An older `Date` is ignored, plus
  `Valid-Until` and `Signed-By`. ([repository format][apt-format], [SecureApt][apt-secure])
- Homebrew bottles: sha256 per OS and architecture. ([bottles][brew])
- minisign: Ed25519, with a signed trusted comment that can carry the version.
  ([minisign][minisign])
- Grafana refuses to load unsigned plugins unless allowed by config. ([plugin signing][grafana])
- VS Code activation events: load an extension only when it is needed. ([activation events][vscode])

### What we take, and what we avoid

1. Desired and observed state kept apart, with observed state as conditions with reasons
   (Kubernetes) → §4.3.
2. Three separate controls: installed, enabled, running now (systemd enable vs start;
   Nextcloud `--keep-disabled`) → §4.3, H2.
3. Live unload and reload (Home Assistant), **not** "takes effect after restart" (Jellyfin) →
   §4.4, F4.
4. A named state for each failure (Jellyfin `NotSupported`, `Malfunctioned`) → phase *Failed*
   and *Waiting* with reason codes.
5. Weak and hard dependencies are different things (`Wants=` vs `Requires=`) → `wants` /
   `requires`.
6. A capped retry for missing prerequisites; bad settings stay failed until fixed (Home
   Assistant `SETUP_RETRY` vs `SETUP_ERROR`) → §4.7.
7. Removal as a finalizer; keep data by default (Nextcloud `--keep-data`) → §4.7, §4.9.
8. One schema that validates and builds the form, versioned with major and minor (Home
   Assistant) → §4.7.
9. A signed index with per-file hashes, expiry and anti-rollback (TUF, apt) → §4.9, §6.4.
10. Never load unsigned (Grafana, Nextcloud) → §4.9.
11. Download on demand **and** prefetch for offline (Plex's lesson) → §4.9.
12. Show the effective state and why (OpenFeature `reason`) → §4.3 conditions, §4.12.

[plex-components]: https://support.plex.tv/articles/203088737-dynamically-updated-server-components/
[plex-forum-1]: https://forums.plex.tv/t/force-plex-to-download-all-codecs-no-internet-connection/202654
[plex-forum-2]: https://forums.plex.tv/discussion/comment/1644446
[plex-agents]: https://support.plex.tv/articles/200241558-agents/
[plex-providers]: https://forums.plex.tv/t/announcement-custom-metadata-providers/934384
[plex-tasks]: https://support.plex.tv/articles/201553286-plex-media-server-scheduled-tasks/
[jf-plugins]: https://jellyfin.org/docs/general/server/plugins/
[jf-template]: https://github.com/jellyfin/jellyfin-plugin-template/blob/master/build.yaml
[jf-status]: https://github.com/jellyfin/jellyfin/blob/master/MediaBrowser.Model/Plugins/PluginStatus.cs
[jf-triggers]: https://github.com/jellyfin/jellyfin/blob/master/MediaBrowser.Model/Tasks/TaskTriggerInfoType.cs
[jf-tasks]: https://jellyfin.org/docs/general/server/tasks/
[jf-libopts]: https://github.com/jellyfin/jellyfin/blob/master/MediaBrowser.Model/Configuration/LibraryOptions.cs
[ha-entries]: https://contributors.home-assistant.io/core/integrations/config-entries/
[ha-addon]: https://contributors.home-assistant.io/apps/reference/configuration/
[nc-info]: https://docs.nextcloud.com/server/latest/developer_manual/app_development/info.html
[nc-occ]: https://docs.nextcloud.com/server/latest/admin_manual/occ_apps.html
[nc-signing]: https://docs.nextcloud.com/server/latest/developer_manual/app_publishing_maintenance/code_signing.html
[k8s-controller]: https://kubernetes.io/docs/concepts/architecture/controller/
[k8s-conventions]: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md
[k8s-finalizers]: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/
[sd-unit]: https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html
[sd-service]: https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html
[fowler]: https://martinfowler.com/articles/feature-toggles.html
[openfeature]: https://openfeature.dev/specification/sections/providers
[unleash]: https://docs.getunleash.io/reference/activation-strategies
[tuf]: https://theupdateframework.io/docs/metadata/
[cosign]: https://docs.sigstore.dev/cosign/signing/overview/
[apt-format]: https://wiki.debian.org/DebianRepository/Format
[apt-secure]: https://wiki.debian.org/SecureApt
[brew]: https://docs.brew.sh/Bottles
[minisign]: https://jedisct1.github.io/minisign/
[grafana]: https://grafana.com/docs/grafana/latest/administration/plugin-management/plugin-sign/
[vscode]: https://code.visualstudio.com/api/references/activation-events
