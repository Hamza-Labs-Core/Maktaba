# Epic 54 — Plugins: new functions from the queue, with no internet, from a Maktaba store

> **Status:** spec (2026-11-03; flows, cross-plugin events, loop checks and joined machines added 2026-11-06). **Source:** [`specs/epics/54-plugins/`](../../../specs/epics/54-plugins/README.md).
> **Anchors:** [research](../../../specs/epics/54-plugins/research.md) · [Epic 51](epic-51-configurable-components.md) (components and lifecycle) · [Epic 53](epic-53-logs-errors-reports.md) (logs and health per plugin) · the store is independent of [Epic 50](epic-50-cloud-no-downtime.md)'s Maktaba Cloud (README §6.0).

## Goal

Requirement: a plugin system for third-party queue extenders that never call the internet and
add new functions by working with the queues and the Maktaba API, and a store for those
plugins.

Other people can add functions to Maktaba. A plugin adds queue stages that run after stable
events (a video was added, a transcript is ready…) or on a schedule, reads what the admin let it
read through a narrow plugin API, and writes tags, chapters, subtitles, artwork, suggestions and
its own data. It can never reach the internet or see the disk. Maktaba runs a store at
`plugins.maktaba.tv` (docs on maktaba.dev) that checks, reviews, signs and sells plugins, and can
switch a bad one off in every home. The store is independent of Maktaba Cloud: its own
accounts, database, deploy and payments, all on Cloudflare with no VPS.

Added on the same day: **all of Maktaba's own stages become first-party plugins** on
the same interface (README §15). They run as native Python in supervised runners, not as
WebAssembly, with a trusted capability set that only Maktaba's release signature unlocks, and
move stage by stage with a switch per stage and a CI proof that each writes exactly what it
wrote before.

Added on 2026-11-06 (README §4.12, §4.13): **the graph is the configuration.** Each set of
plugins that works together submits one signed flow (`flow.toml`) naming its plugins, their
stages, the core events they attach to, the events they publish and who subscribes to what;
each manifest matches it exactly. Third-party stages are leaves after core stages. Plugins
trigger each other only through published events, with an admin's grant. Cycles are refused
at the store's verification and again on the home at install, enable and update, by one
check built in Go and TypeScript on one corpus; a loop through data is stopped at run time
by each job's lineage (16 hops, no re-entry, cause `plugin_loop`). The flow is drawn as a
chart on the install screen, the Plugins page, the store listing and maktaba.dev. Plugins
can run on joined machines, installed and approved on the home.

## The design in one table

| | Choice |
|---|---|
| Plugin code | a WebAssembly component (WASI 0.2), from Rust, JS/TS, Python (componentize-py) or Go (TinyGo) |
| Where it runs | `maktaba-plugind`, one Rust process per machine running plugins (the home, and joined machines a plugin's `placement` allows) embedding Wasmtime, supervised as the Epic 51 component `plugins.host` (on demand: no process while no plugin is enabled); a fresh store per job |
| No internet | layer 1: sockets, HTTP and files are never linked into the component; layer 2: the host runs in Seatbelt (macOS), namespaces + seccomp + Landlock (Linux), AppContainer + Job Object (Windows); CI escape probes with a canary must log 0 connections |
| Queue | the pipeline claims plugin stages from a `plugins` pool and hands each job to the host; retries, drain and parking are Epic 40/51's |
| Plugin API | `maktaba:plugin@1.x` (WIT), forwarded by the host to the Go API over a unix socket with a per-job token the plugin never sees; every call checked for capability, subject, library and quota |
| Permissions | named capabilities granted by the admin at install, with library scope; updates asking for more wait |
| Lifecycle | each plugin is an Epic 51 component `plugin.<id>` of a new run-time kind `plugin` (54.37; Epic 51's catalog is fixed per release), switched by `PUT /api/components/plugin.<id>`, `/stop`, `/start` and `DELETE` or by `/api/plugins/*`: install, enable, disable, hold, side-by-side update, data-aware rollback, uninstall, purge; files under `<state>/plugins/` on Epic 51's packs code (54.38) |
| Store | independent of Maktaba Cloud, all on Cloudflare (Workers, D1, Durable Objects, R2; no VPS) at `plugins.maktaba.tv`, files on `plugins-cdn.maktaba.tv`; its own store accounts (email verification, MFA required for developers, passkeys for reviewers and store admins, re-auth for sensitive actions); review and store admin at `plugins.maktaba.tv/admin`; automated checks (import and capability audit, sandbox run, malware scan), human review, developer + Maktaba signatures, signed expiring catalog and revocation list |
| Linking a home | a device code like the TV pairing links a home to a store account, only to buy and rate; entitlements are store-signed and checked offline, never through the cloud |
| Offline | files install offline; store-signed files as from the store; others only with "Allow unreviewed plugins" and a red Unreviewed badge |
| Admin's switch | an admin's disable is a "keep off" that sticks through updates, restores and reinstalls; the store's malware switch-off is separate and can't be undone |
| Permissions dialog | library checkboxes start empty; the admin ticks each |
| Flows | a signed `flow.toml` per set of plugins is the only wiring; leaves after core; published events between plugins with `events.subscribe:<id>`; the cycle check at the store (54.16 C12) and on the home; run-time lineage capped at 16 hops; a chart wherever someone decides (54.39–54.41) |
| Joined machines | the home installs, approves and distributes the signed package; each machine verifies it and runs its own sandboxed host and plugin API (54.42) |
| Panels | read-only, data-only, shown to everyone in the home (viewers included) once installed; no separate viewer approval |
| Store listings | English and Arabic required (machine translation allowed, labelled); every version's source goes to reviewers, kept private |
| Maktaba's own stages | 15 first-party plugins, one per Epic 51 component that owns stages (`core.pipeline`, `media.thumbnails`, `library.convert`, `library.dedup`, `library.repair`, `transcription`, `subtitles`, `search.semantic`, `classify`, `enrich`, `youtube`, `livetv`, `livetv.slots`, `ext.plex`, `ext.dizquetv`), which keep their ids and kinds, covering all 56 stages; native Python runners; named SQL statements under per-plugin Postgres roles; exact stage names; lock-step with the server; nine migration waves |
| Paid | 85 % developer / 15 % Maktaba; $25 one-time fee to sell; the store's own payment provider and seller of record, separate from cloud billing, decided at Phase 3; payouts, tax and refunds owned by the store; a lapsed entitlement waits with `entitlement_missing` |
| Store cost (estimate) | about $5 a month at launch (Workers Paid base), about $60 a month at 50,000 homes |

## Stories

| # | Story | Phase |
|---|---|---|
| 54.01 | [The manifest and the package](../../../specs/epics/54-plugins/story-54-01-manifest-package.md) | 1 |
| 54.02 | [The plugin host: Wasmtime, the WIT world and limits](../../../specs/epics/54-plugins/story-54-02-plugin-host.md) | 1 |
| 54.03 | [The OS sandbox and the egress tests](../../../specs/epics/54-plugins/story-54-03-os-sandbox.md) | 1 |
| 54.04 | [The plugin API: tokens, capabilities and scope](../../../specs/epics/54-plugins/story-54-04-plugin-api.md) | 1 |
| 54.05 | [Media handles and host services](../../../specs/epics/54-plugins/story-54-05-media-handles.md) | 1 |
| 54.06 | [Plugin stages in the queue: events, schedules, dispatch](../../../specs/epics/54-plugins/story-54-06-queue-stages.md) | 1 |
| 54.07 | [Plugins are Epic 51 components](../../../specs/epics/54-plugins/story-54-07-lifecycle.md) | 1 |
| 54.08 | [Grants, ratings and roles](../../../specs/epics/54-plugins/story-54-08-grants-roles.md) | 1 |
| 54.09 | [Crash isolation, health, logs and quotas](../../../specs/epics/54-plugins/story-54-09-isolation-health.md) | 1 |
| 54.10 | [Install from a file and the unreviewed opt-in](../../../specs/epics/54-plugins/story-54-10-file-install.md) | 1 |
| 54.11 | [Settings pages declared as data](../../../specs/epics/54-plugins/story-54-11-settings.md) | 1 |
| 54.12 | [SDK, templates, test harness and docs](../../../specs/epics/54-plugins/story-54-12-sdk-harness.md) | 1 |
| 54.13 | [The admin Plugins page](../../../specs/epics/54-plugins/story-54-13-plugins-page.md) | 1 |
| 54.14 | [Signing, the trust root and the revocation list](../../../specs/epics/54-plugins/story-54-14-signing-trust.md) | 2 |
| 54.15 | [The store service, store accounts and linking a home](../../../specs/epics/54-plugins/story-54-15-store-service.md) | 2 |
| 54.16 | [Automated checks and human review](../../../specs/epics/54-plugins/story-54-16-review.md) | 2 |
| 54.17 | [The home's catalog, updates and the kill switch](../../../specs/epics/54-plugins/story-54-17-catalog-kill-switch.md) | 2 |
| 54.18 | [Store browser, listing pages, ratings and reports](../../../specs/epics/54-plugins/story-54-18-store-browser.md) | 2 |
| 54.19 | [Paid plugins](../../../specs/epics/54-plugins/story-54-19-paid-plugins.md) | 3 |
| 54.20 | [Panels declared as data, in every app](../../../specs/epics/54-plugins/story-54-20-panels.md) | 3 |
| 54.21 | [Local compute: embeddings and the language model](../../../specs/epics/54-plugins/story-54-21-local-compute.md) | 3 |
| 54.22 | [First-party plugins: the trusted manifest and release signing](../../../specs/epics/54-plugins/story-54-22-trusted-manifest.md) | 4 |
| 54.23 | [The native plugin runner](../../../specs/epics/54-plugins/story-54-23-native-runner.md) | 4 |
| 54.24 | [Named statements and per-plugin database roles](../../../specs/epics/54-plugins/story-54-24-statement-registry.md) | 4 |
| 54.25 | [The graph from manifests and the per-stage switch](../../../specs/epics/54-plugins/story-54-25-graph-and-switch.md) | 4 |
| 54.26 | [The parity proof](../../../specs/epics/54-plugins/story-54-26-parity.md) | 4 |
| 54.27 | [Repo layout, packaging, components and ownership](../../../specs/epics/54-plugins/story-54-27-layout-packaging.md) | 4 |
| 54.28 | [Wave 1: classify's leaf stages](../../../specs/epics/54-plugins/story-54-28-wave1-leaf-classify.md) | 4 |
| 54.29 | [Wave 2: thumbnails and subtitles](../../../specs/epics/54-plugins/story-54-29-wave2-thumbnails-subtitles.md) | 4 |
| 54.30 | [Wave 3: transcription and semantic search](../../../specs/epics/54-plugins/story-54-30-wave3-transcription-search.md) | 4 |
| 54.31 | [Wave 4: classify with the language model, and enrich](../../../specs/epics/54-plugins/story-54-31-wave4-classify-enrich.md) | 4 |
| 54.32 | [Wave 5: convert, duplicates and the file trash](../../../specs/epics/54-plugins/story-54-32-wave5-file-actions.md) | 4 |
| 54.33 | [Wave 6: YouTube and repair](../../../specs/epics/54-plugins/story-54-33-wave6-youtube-repair.md) | 4 |
| 54.34 | [Wave 7: live TV](../../../specs/epics/54-plugins/story-54-34-wave7-livetv.md) | 4 |
| 54.35 | [Wave 8: Plex and dizqueTV through the service door](../../../specs/epics/54-plugins/story-54-35-wave8-plex-dizquetv.md) | 4 |
| 54.36 | [Wave 9: the core stages, and removing the in-process path](../../../specs/epics/54-plugins/story-54-36-wave9-core.md) | 4 |
| 54.37 | [The component catalog takes plugin entries](../../../specs/epics/54-plugins/story-54-37-catalog-plugin-entries.md) | 1 |
| 54.38 | [Plugin files on disk, on Epic 51's packs code](../../../specs/epics/54-plugins/story-54-38-plugin-files.md) | 1 |
| 54.39 | [Flows: the declared graph, the leaf rule and the cycle check](../../../specs/epics/54-plugins/story-54-39-flows.md) | 1 |
| 54.40 | [Published events, cross-plugin triggers and run-time lineage](../../../specs/epics/54-plugins/story-54-40-events-lineage.md) | 1 |
| 54.41 | [The flow chart](../../../specs/epics/54-plugins/story-54-41-flow-chart.md) | 1 |
| 54.42 | [Third-party plugins on joined machines](../../../specs/epics/54-plugins/story-54-42-joined-machines.md) | 1 |

## Key numbers

| Measure | Target |
|---|---|
| Job start overhead (compiled, cached) | p95 ≤ 20 ms; instantiate ≤ 5 ms |
| Plugin API call | p95 ≤ 2 ms |
| Compute vs native | ≤ 2× |
| Runaway job stopped | ≤ deadline + 200 ms |
| Network connections from a plugin or its host | 0, on macOS, Linux and Windows, every CI run |
| Revocation reaches a home (linked or not) | ≤ 7 min (5-minute poll on the CDN) |
| Store checks / human review | ≤ 30 min / ≤ 5 business days (new), ≤ 2 (update) |
| A home with no plugins | 0 extra processes |
| First-party stage vs in-process | p50 ≤ 1.10×, p95 ≤ 1.15×; rows, files and fingerprints identical |
| Stage flip | 0 lost jobs, 0 reruns over 100 flips |
| Flow check over a home's plugins | ≤ 50 ms for 100 plugins of 20 stages |
| Lineage hops | at most 16 |

## Hard calls (details in the README §11)

Wasm components, not native code or in-process Python. One Rust host with Wasmtime, not
wasmtime-go (cgo) or wazero (no component model). Two layers: nothing networked is linked, and
an OS sandbox on the host. The pipeline keeps the queue; the API makes every decision. Stable
events, not internal stage names. No secrets in plugin settings. Store keys of its own (offline
root), not the release Sigstore identity. A `disable` revocation can't be undone, and an admin's
keep-off sticks until the admin turns it on; an expired list warns but never stops plugins.
85/15, and the payment provider is chosen at Phase 3. Third-party plugins are run-time catalog
entries of a new kind; first-party plugins implement existing components, which keep their
kinds. The store shares nothing
with Maktaba Cloud and runs on Cloudflare only. First-party plugins are
native and trusted behind the same interface, reach the database through named statements, move
with the server version, keep their stage names and prove identity, not similarity. Unreviewed installs allowed behind an opt-in. Third-party
plugins run on joined machines, approved on the home. Third-party stages are leaves after
core; the flow is the configuration; loops are refused statically and at run time. Heavy compute stays a host service.
