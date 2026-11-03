# Epic 54 — Plugins: new functions from the queue, with no internet, from a Maktaba store

> **Status:** spec (2026-11-03). **Source:** [`specs/epics/54-plugins/`](../../../specs/epics/54-plugins/README.md).
> **Anchors:** [research](../../../specs/epics/54-plugins/research.md) · [Epic 51](epic-51-configurable-components.md) (components and lifecycle) · [Epic 53](epic-53-logs-errors-reports.md) (logs and health per plugin) · [Epic 50](epic-50-cloud-no-downtime.md) (the cloud the store runs on).

## Goal

The owner, 2026-11-03: *"Spec a third-party no internet calling plugging queue extender that
allows new functions by interacting with queues and maktaba API and a store for such
plugins/jobs"*.

Other people can add functions to Maktaba. A plugin adds queue stages that run after stable
events (a video was added, a transcript is ready…) or on a schedule, reads what the admin let it
read through a narrow plugin API, and writes tags, chapters, subtitles, artwork, suggestions and
its own data. It can never reach the internet or see the disk. Maktaba runs a store on
maktaba.tv (docs on maktaba.dev) that checks, reviews, signs and sells plugins, and can switch a
bad one off in every home.

## The design in one table

| | Choice |
|---|---|
| Plugin code | a WebAssembly component (WASI 0.2), from Rust, JS/TS, Python (componentize-py) or Go (TinyGo) |
| Where it runs | `maktaba-plugind`, one Rust process per home embedding Wasmtime, supervised as the Epic 51 component `plugins.host`; a fresh store per job |
| No internet | layer 1: sockets, HTTP and files are never linked into the component; layer 2: the host runs in Seatbelt (macOS), namespaces + seccomp + Landlock (Linux), AppContainer + Job Object (Windows); CI escape probes with a canary must log 0 connections |
| Queue | the pipeline claims plugin stages from a `plugins` pool and hands each job to the host; retries, drain and parking are Epic 40/51's |
| Plugin API | `maktaba:plugin@1.x` (WIT), forwarded by the host to the Go API over a unix socket with a per-job token the plugin never sees; every call checked for capability, subject, library and quota |
| Permissions | named capabilities granted by the admin at install, with library scope; updates asking for more wait |
| Lifecycle | each plugin is an Epic 51 component (kind `plugin`): install, enable, disable, hold, side-by-side update, data-aware rollback, uninstall, purge |
| Store | developer accounts with 2FA, automated checks (import and capability audit, sandbox run, malware scan), human review, developer + Maktaba signatures, signed expiring catalog and revocation list |
| Offline | files install offline; store-signed files as from the store; others only with "Allow unreviewed plugins" and a red Unreviewed badge |
| Paid | Stripe Connect, 85/15 proposed, signed entitlement checked offline |

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
| 54.15 | [The store service and developer accounts](../../../specs/epics/54-plugins/story-54-15-store-service.md) | 2 |
| 54.16 | [Automated checks and human review](../../../specs/epics/54-plugins/story-54-16-review.md) | 2 |
| 54.17 | [The home's catalog, updates and the kill switch](../../../specs/epics/54-plugins/story-54-17-catalog-kill-switch.md) | 2 |
| 54.18 | [Store browser, listing pages, ratings and reports](../../../specs/epics/54-plugins/story-54-18-store-browser.md) | 2 |
| 54.19 | [Paid plugins](../../../specs/epics/54-plugins/story-54-19-paid-plugins.md) | 3 |
| 54.20 | [Panels declared as data, in every app](../../../specs/epics/54-plugins/story-54-20-panels.md) | 3 |
| 54.21 | [Local compute: embeddings and the language model](../../../specs/epics/54-plugins/story-54-21-local-compute.md) | 3 |

## Key numbers

| Measure | Target |
|---|---|
| Job start overhead (compiled, cached) | p95 ≤ 20 ms; instantiate ≤ 5 ms |
| Plugin API call | p95 ≤ 2 ms |
| Compute vs native | ≤ 2× |
| Runaway job stopped | ≤ deadline + 200 ms |
| Network connections from a plugin or its host | 0, on macOS, Linux and Windows, every CI run |
| Revocation reaches a linked home | ≤ 5 min (≤ 65 min unlinked) |
| Store checks / human review | ≤ 30 min / ≤ 5 business days (new), ≤ 2 (update) |
| A home with no plugins | 0 extra processes |

## Hard calls (details in the README §11)

Wasm components, not native code or in-process Python. One Rust host with Wasmtime, not
wasmtime-go (cgo) or wazero (no component model). Two layers: nothing networked is linked, and
an OS sandbox on the host. The pipeline keeps the queue; the API makes every decision. Stable
events, not internal stage names. No secrets in plugin settings. Store keys of its own (offline
root), not the release Sigstore identity. A `disable` revocation can't be overridden; an expired
list warns but never stops plugins. Unreviewed installs allowed behind an opt-in. Home-only
placement for now. Heavy compute stays a host service.
