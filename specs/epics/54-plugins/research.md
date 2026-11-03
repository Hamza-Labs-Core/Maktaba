# Epic 54 research: plugins, sandboxes and stores

> Companion to [README.md](README.md). Checked 2026-11-03. §1 is what Maktaba has today, read
> from the code. §2 is how other products do plugins. §3 is how to run untrusted code safely.
> §4 is store economics and moderation. Each section ends with what we take and what we leave.

## 1. What Maktaba has today

### 1.1 The queue engine

- **One stage graph.** `pipeline/src/maktaba_pipeline/engine/graph.py` describes every stage
  once as a `StageDef` (name, subject kinds, pool, lane, version, handler, input and output
  fingerprints, `edges`, `may_request`, `placement`, `every_minutes_rule`, `extension`,
  `max_attempts`). At worker start `engine/sync.py` upserts the graph into `job_stages`; Go and
  the web only read that table.
- **Edges are the event system.** An `Edge(to, map, when)` on a stage says "when my output
  changes, enqueue `to`". `when="always"` fires on every run. There is no separate event bus a
  third party could subscribe to; subscribing means adding an edge from a core stage.
- **Handlers** are `async (DBConn, Job, StageContext) -> StageResult`. A `StageResult` carries
  an output hash, an outcome, `changed_subjects`, explicit `requests` (only to stages named in
  `may_request`, otherwise `GraphViolation`), a summary and log lines.
- **Pools** (`POOLS` in `graph.py`) are resource classes: global pools (`nas_read`,
  `network_plex`, `lookup`, `convert`…) are enforced in the claim SQL under an advisory lock;
  per-worker pools (`cpu`, `gpu`) by in-process semaphores (`engine/claim.py`).
- **Scheduled stages** use `every_minutes_rule = (section, field)`: the reconciler enqueues a
  catalog-subject job every N minutes from a `rules.*` value (`engine/reconcile.py`).
- **Failure handling** is Epic 40's: failure classes (`engine/failure_classes.py`), the
  failure policy, parking, and the shutdown drain that force-pauses running jobs
  (`engine/claim.py`). Epic 51 §4.5 builds its no-lost-work disable on the same path.
- **State changes** of a video go only through `orchestrator/advance.py`
  (`advance_after_stage`), enforced by a lint quarantine. A plugin must never be a second
  writer of `videos.state`.

### 1.2 The extension mechanism

- `pipeline/src/maktaba_pipeline/extensions/__init__.py`: built-in extensions in `BUILTIN`
  (Plex, dizqueTV, Cloudflare Stream) plus **any Python entry point in the group
  `maktaba.extensions`**, loaded with `ep.load()` into the worker process.
- `extensions/base.py`: an `Extension` has a `Manifest` (name, version, description, settings
  fields with `secret`, stage names), `register(graph: GraphBuilder)` to add stages and edges
  from core stages, `impls()` for handlers, and `is_configured(settings)`.
- `extensions/store.py`: one `extensions` row per extension (enabled, settings JSON); secrets
  sealed in `home_secrets` (slot 0120).
- The pattern is right (core never imports an extension; the extension adds stages and edges
  through `GraphBuilder`). The trust model is not: an entry-point extension is arbitrary Python
  in the worker, with the worker's database login, the home secrets key path, the network and
  the whole disk. Epic 51 H5 keeps entry points "a developer feature" and left a store to a
  later epic. This is that epic.
- Epic 52 (story 52.03) uses the same entry-point idea for third-party language packs
  (`maktaba.language_packs`). Language packs are data-only by contract.

### 1.3 Epic 51, which this epic builds on

Epic 51 (spec, 2026-11-03) gives every part of Maktaba a component id in
`shared/components/catalog.toml`, desired state in Postgres (`want` absent / installed /
enabled, `hold`), actual state per machine (phase + conditions with reason codes), runners in
every process, a drain that never loses a job (§4.5), packs signed by the release Sigstore
identity with an expiring index (§4.9, H6), offline import from a file, licence gating by
existing flags (§4.10), admin-only routes with role tests (§4.11) and capabilities for apps
(§4.12). Its five kinds are `process`, `stages`, `loop`, `extension`, `asset`. It has no kind
for code that is not built from Maktaba's own commit.

### 1.4 The API, roles and the cloud

- Roles: `principal.Principal{UserID, IsAdmin, Libraries, AccessAllLibraries, Source}`
  (`api/internal/auth/principal/principal.go`); sources `admin_token`, `jwt`, `cookie`, `pat`.
  `RequireAdmin` lives in `api/internal/auth/middleware/middleware.go`. Library access is an
  ACL (`api/internal/auth/authz/acl.go`). The standing rule (2026-10-25): every role rule is
  enforced by the API with a role test (no one, viewer, cloud-linked play sign-in, admin);
  `TestAdminOnly` in `api/internal/handlers/cluster/cluster_test.go` is the model.
- Machines authenticate as the cluster role and the `maktaba_worker` database login.
- Maktaba Cloud (`cloud/`) already has accounts, a billing package (`cloud/internal/billing/`),
  a webhook handler, plans, metering, a licence issuer (`cloud/internal/license/issuer.go`) and entitlements. Homes reach
  it through the cloud link (`maktaba-cloudlink`). Epic 50 makes it highly available.
  **The store uses none of it** (decided 2026-11-03, README §6.0): it has its own accounts,
  database, deploy, entitlement signing and payments on Cloudflare, so cloud accounts,
  billing, the licence issuer and the cloud link are listed here only as what the store
  stops depending on.
- Domains (owner, standing): **maktaba.tv** is everything users touch; **maktaba.dev** is the
  developer wiki, source and security.txt; maktaba.app is not ours.

### 1.5 Constraints from the trail

- `CGO_ENABLED=0` builds on the owner's Mac (the API does not link with cgo). A Go host that
  needs cgo (wasmtime-go) is out.
- Rust is already built in CI (the Tauri desktop app, `apps/desktop/src-tauri`).
- Update, rollback and restore paths are proven in the update rehearsal before a tag.
- Arabic ships with English everywhere, in the same PR.
- Logs and health per component come from Epic 53 (segments, `service` = component name,
  `/healthz`, status mapping).

## 2. How others do plugins

### 2.1 Jellyfin

- Plugins are .NET assemblies loaded **into the server process**, with the server's rights.
  Repositories are JSON manifests; each plugin has a `versions` array with `version`,
  `targetAbi` (the minimum server ABI), `sourceUrl` (a zip) and a `checksum` (an MD5 in the
  examples). Anyone can add a repository URL. There is no signature: the checksum only detects
  a broken download.
  Sources: [intro-skipper manifest](https://cdn.jsdelivr.net/gh/intro-skipper/manifest@master/10.10/manifest.json),
  [targetAbi issue #11331](https://github.com/jellyfin/jellyfin/issues/11331),
  [forum: catalog and targetAbi](https://forum.jellyfin.org/t-solved-plugin-catalog-does-not-update-with-new-repository).
- Jellyfin 12.0 (2026) changed plugin interfaces and moved to .NET 10, so 10.11 plugins do not
  load, and it started rejecting plugin packages with unsafe names.
  Source: [Help Net Security, 2026-09-08](https://www.helpnetsecurity.com/2026/09/08/jellyfin-12-0-security-fixes/).
- **Take:** a manifest with a server compatibility range, a per-version entry, one catalog.
  **Leave:** in-process code, unsigned repositories, ABI that breaks every major.

### 2.2 Plex: plugins retired in 2018

- In September 2018 Plex announced the end of the Plugin Directory (removed from Plex Web on
  9 October 2018), saying fewer than 2 % of people used plugins and that they were built on an
  outdated protocol that was hard to support. It told developers to turn utility plugins into
  stand-alone apps; scanners and metadata agents were left alone at first, and later posts
  said there were no plans to support third-party scanners once plug-ins are removed.
  Sources: [Plex forum announcement](https://forums.plex.tv/t/discontinuation-of-plugins-watch-later-recommended-and-cloud-sync/312312),
  [TechCrunch, 2018-09-26](https://techcrunch.com/2018/09/26/plex-kills-off-support-for-cloud-sync-plugins-and-bookmarking-features/),
  [Plex forum, 2024](https://forums.plex.tv/t/missing-agents/889547/6).
- **Lesson for Maktaba:** Plex's plugins were mostly *content channels* (scraping web video),
  which needed the internet, broke whenever a site changed and carried legal risk. They also
  ran in a Python 2 framework inside the server. A plugin system survives only if (a) it has
  a small, versioned contract that can be kept for years, (b) plugins add *functions to the
  owner's own library*, not content from the web, and (c) the cost of supporting it is low.
  Our no-internet rule removes the class of plugin that killed Plex's.

### 2.3 Home Assistant: custom integrations, add-ons and HACS

- **Custom integrations** are Python packages in `custom_components/` loaded into Home
  Assistant's process. HA logs a warning that they have not been tested by Home Assistant;
  Nabu Casa does not review them. A 2021 security bulletin found custom integrations that could
  leak data and told users to rename the folder to disable them all.
  Sources: [community: risks of HACS](https://community.home-assistant.io/t/risks-involved-with-hacs/671049),
  [security bulletin](https://community.home-assistant.io/t/security-bulletin/268456).
- **HACS** is a community store for them: a default repository list with CI checks (manifest
  keys, brand icon in `home-assistant/brands`, one integration per repo), submitted by the
  owner. It checks structure, not behaviour.
  Sources: [HACS publishing](https://hacs.xyz/docs/publish/integration), [HACS: start](https://hacs.xyz/docs/publish/start).
- **Add-ons** are containers run by the Supervisor, with a **security rating from 1 to 6**
  computed from the rights they ask for (host network, privileged, full access…), AppArmor by
  default, and "protection mode" on unless the add-on needs rights.
  Source: [HA developer docs: add-on security](https://git.sudo.is/home-assistant/developers.home-assistant/src/branch/master/docs/add-ons/security.md).
- **Take:** a visible rating from declared rights; a clear "not reviewed" warning; a single
  switch to disable all third-party code. **Leave:** containers (Mac and Windows homes do not
  run Docker), in-process Python.

### 2.4 Nextcloud app store

- Each app id has a **developer certificate** issued by Nextcloud (a CSR submitted as a pull
  request to the certificate repository); every release is signed with it, and the store and
  the server check the signature. The store also validates `appinfo/info.xml` against a
  schema; the `<nextcloud min-version max-version>` range is required in practice, so the
  store hides apps that do not support the running server.
  Sources: [Nextcloud app store developer guide](https://nextcloudappstore.readthedocs.io/en/stable/developer.html),
  [app metadata](https://docs.nextcloud.com/server/19/developer_manual/app/info.html),
  [store improvements](https://nextcloud.com/de/blog/nextcloud-appstore-improvements-2),
  [max-version issue](https://github.com/nextcloud/documentation/issues/13892).
- Apps are PHP in the server process with full access. Review is light; apps are tagged
  "featured" or "supported" by Nextcloud GmbH.
- **Take:** a developer key bound to an app id; a min and max server range; the store filters
  by compatibility. **Leave:** in-process code.

### 2.5 VS Code extensions

- Extensions run in the extension host, a Node process **with the user's full rights**: no
  sandbox, no permission model. Security vendors keep finding malicious marketplace
  extensions (cryptominers in April 2025; typosquats copying names and publisher details;
  over a thousand extensions with known-malicious dependencies in one 2024 scan).
  Sources: [ReversingLabs](https://www.reversinglabs.com/blog/move-over-npm-trust-vs-code-extensions-at-your-own-risk-dev-teams),
  [Koi Security](https://www.koi.security/blog/2-6-exposing-malicious-extensions-shocking-statistics-from-the-vs-code-marketplace).
- **Lesson:** review alone does not scale against malice; without a sandbox every slip is a
  full compromise. Publisher verification and name-squatting rules matter.

### 2.6 Obsidian community plugins

- Obsidian says plainly that it **cannot sandbox** community plugins: they get the app's
  access (files, internet, running programs). It starts in Restricted Mode (no third-party
  code) by default, scans every plugin version automatically, reviews popular, featured and
  flagged ones by hand, and shows a safety scorecard.
  Source: [Obsidian help: plugin security](https://obsidian.md/help/plugin-security).
- **Take:** off by default until the owner opts in; automatic scans of every version; a
  scorecard. **Leave:** no sandbox.

### 2.7 Deno, WASI and capability sandboxes

- **Deno** runs code with no I/O unless granted: `--allow-net` (scoped to hosts and ports),
  `--allow-read`, `--allow-run`…, with `--deny-*` taking precedence. Its docs warn that some
  grants (`--allow-run`, FFI) amount to full access.
  Sources: [Deno permissions](https://docs.deno.com/runtime/reference/permissions/),
  [Deno security](https://docs.deno.com/runtime/fundamentals/security).
- **WASI / Wasmtime** go further: a component can only call what the host links into it.
  Network access is a capability (`wasi:sockets` `network` resource) that the host grants or
  not; Wasmtime gives none by default.
  Sources: [wasmtime-wasi sockets docs](https://docs.rs/wasmtime-wasi/18.0.1/wasmtime_wasi/preview2/command/wasi/sockets/network/index.html),
  [Bytecode Alliance Zulip](https://bytecodealliance.github.io/zulip-archive/stream/206238-general/topic/Run.20a.20C.20program.20which.20needs.20HTTP.2FSocket.20request.20on.20Wasm.20now.html).
- **Extism** wraps Wasm plugins with a manifest that lists `allowed_hosts` and paths, and an
  HTTP host function checked against that list.
  Source: [Extism configuration](https://extism.org/docs/concepts/configuration).
- **Take:** deny by default, grant by name, and better still, *do not link* what a plugin may
  never have. That is the whole "no internet" guarantee: the Wasm import for sockets and HTTP
  does not exist in our host.

### 2.8 Summary

| | Code runs | Sandbox | Signing | Review | Kill switch |
|---|---|---|---|---|---|
| Jellyfin | in server (.NET) | none | checksum only | none for 3rd-party repos | none |
| Plex (until 2018) | in server (Python 2) | none | Plex-hosted directory | Plex | directory removed |
| Home Assistant integrations / HACS | in process (Python) | none | none | HACS CI structure checks | rename folder |
| Home Assistant add-ons | container | AppArmor, rating 1–6 | repo | none for 3rd-party repos | — |
| Nextcloud | in server (PHP) | none | developer cert from Nextcloud | light | store removal |
| VS Code | extension host (Node) | none | publisher | automated scans | marketplace block list |
| Obsidian | in app (JS) | none | — | automated scan + manual for popular | — |
| **Maktaba (this epic)** | **separate host process, Wasm component** | **Wasm (no sockets linked) + OS sandbox on the host** | **developer key + Maktaba store key** | **automated checks + human review** | **signed revocation list, homes honour it** |

## 3. Running plugin code safely

### 3.1 WebAssembly with Wasmtime and the component model

- **Wasmtime** is the Bytecode Alliance's runtime and the reference implementation of WASI 0.2
  (preview 2) and the component model. A component declares its imports and exports in WIT;
  the host links exactly the interfaces it chooses. Unlinked imports fail at instantiation.
  Source: [wasmtime-wasi preview2](https://docs.rs/wasmtime-wasi/15.0.0/src/wasmtime_wasi/preview2/mod.rs.html).
- **Limits per store:** fuel (each instruction costs fuel; deterministic), epoch interruption
  (a cheap wall-clock deadline, checked at loop heads and calls), and a `ResourceLimiter` /
  `StoreLimits` capping linear memory, tables and instances.
  Source: [wasmtime Store docs](https://docs.wasmtime.dev/api/wasmtime/struct.Store.html).
- **Fast start:** `InstancePre` does type checking and import resolution once; the pooling
  allocator and copy-on-write heap images take instantiation "from milliseconds to
  microseconds"; components are compiled ahead of time and cached.
  Sources: [Tuning Wasmtime for fast instantiation](https://docs.wasmtime.dev/examples-fast-instantiation.html),
  [Wasmtime 10 performance](https://bytecodealliance.org/articles/wasmtime-10-performance).
- **Speed:** compiled Wasm usually runs within about 1.1–2× native for compute; host calls
  cost a copy across the boundary. Media decoding stays in the host (ffmpeg), so the heavy
  work a plugin asks for is native.

### 3.2 Languages that compile to components

| Language | Tool | State (2026) | Limits |
|---|---|---|---|
| Rust | `cargo component` / `wit-bindgen` | mature | none worth noting |
| JavaScript / TypeScript | `jco componentize` (StarlingMonkey, SpiderMonkey in Wasm) | usable | components are several MB (an engine is inside); no Node APIs |
| Python | `componentize-py` (CPython in Wasm) | usable for pure Python | **no native extensions** (NumPy, pandas) except community WASI wheels; the interpreter is inside each component |
| Go | TinyGo `-target=wasip2` | usable | a subset of Go; the standard Go compiler has no component or WASI 0.2 support |
| C / C++ | wasi-sdk + `wit-bindgen` | mature | — |

Sources: [componentize-py introduction](https://www.fermyon.com/blog/introducing-componentize-py),
[componentize-py notes](https://hackmd.io/3ZktfALiSZWn41ad7y3gnA),
[TinyGo and WASI P2](https://wasmcloud.website.cncfstack.com/blog/compile-go-directly-to-webassembly-components-with-tinygo-and-wasi-p2),
[TinyGo language guide](https://wasmcloud.website.cncfstack.com/docs/wash/developer-guide/language-support/go/).

**Python-in-Wasm limit, and what we do about it.** A plugin cannot bring torch or NumPy. That
is fine because heavy work is a host service: decoded frames and audio, the transcript, local
embeddings and the local language model are capabilities (stories 54.05, 54.21). A plugin
does the logic; Maktaba does the compute it already has.

### 3.3 Separate processes under OS sandboxes

| OS | Mechanism | No-network control | Notes |
|---|---|---|---|
| macOS | Seatbelt profiles (`sandbox_init` with an SBPL profile, as Chromium does) or the App Sandbox entitlements on a helper | `(deny network*)`; App Sandbox without `com.apple.security.network.client` | `sandbox-exec` and the SBPL language are not public API but are what Chromium and Apple's own daemons use |
| Linux | user + network + mount namespaces (what bubblewrap does), seccomp-bpf, Landlock | `--unshare-net` gives a namespace with only loopback; seccomp can refuse `socket(AF_INET*)`; Landlock ABI 4 (kernel 6.7) restricts TCP bind/connect | unprivileged user namespaces are restricted on some distributions (Ubuntu 24.04 AppArmor); seccomp + Landlock still work there |
| Windows | AppContainer + Job Objects | an AppContainer with no `internetClient` / `privateNetworkClientServer` capability is blocked by the Windows Filtering Platform's default rule | Project Zero found a WFP default-rule bypass in 2021; defence in depth matters |

Sources: [bubblewrap README](https://sources.debian.org/src/bubblewrap/0.11.0-2/README.md),
[Arch wiki: Bubblewrap](https://wiki.archlinux.org/title/Bubblewrap),
[Microsoft: app capability declarations](https://learn.microsoft.com/en-us/WINDOWS/APPS/package-and-deploy/app-capability-declarations),
[Project Zero WFP AppContainer bypass](https://securityaffairs.com/121370/hacking/microsoft-wfp-appcontainer-bypass.html),
[rappct AppContainer network demo](https://docs.rs/crate/rappct/latest/source/examples/network_demo.rs).

**Why both layers.** A sandboxed native subprocess alone would need a different, partly
undocumented sandbox on each OS, native builds per platform from every developer, and a
different proof on each OS. Wasm alone depends on one runtime having no escape bug. With
Wasm as the main boundary (same on every OS, no sockets linked) and the OS sandbox on the one
host process as the second, an escape needs two independent bugs.

### 3.4 Other options we looked at

| Option | Why not |
|---|---|
| Python entry points in the worker (today) | full access; one bad plugin crashes or owns the worker |
| Containers (HA add-ons) | Mac and Windows homes have no container runtime; heavy |
| V8 isolates / Deno embedded | JavaScript only; a large runtime to ship and patch |
| wazero (pure Go Wasm runtime) | no component model, so no typed WIT interfaces and no componentize-py or jco; we would design our own ABI |
| wasmtime-go | needs cgo (broken on the owner's Mac for the API build) and has no component API |
| wasmtime-py inside the pipeline | no crash isolation from the worker; one runtime per worker process |
| Extism | its own ABI over core modules and an HTTP host function we would have to remove; the component model is the standard path |

## 4. Store economics and moderation

### 4.1 Payments

- **Owner decision (2026-11-03): no Stripe, ever** (no Stripe, Stripe Connect, Stripe Tax or
  Stripe-owned services such as Lemon Squeezy). The payment provider and the seller of record
  are decided at Phase 3 (README §6.5, §14).
- **Who is the seller of record decides who owes VAT and sales tax.** When Maktaba sells, the
  tax on digital goods is Maktaba's; a merchant-of-record service takes it on for a fee.
- **Resale of third-party products is the known constraint.** A plugin store resells other
  people's software, which many merchant-of-record services restrict in their seller terms
  (Paddle among them), so each candidate has to confirm in writing that it accepts plugin
  resale before it is chosen.
- **Candidates to evaluate at Phase 3:** Paddle as seller with Maktaba paying developers
  monthly; Maktaba as seller with a marketplace payment provider such as Adyen for Platforms
  (split payments to developer sub-accounts, Maktaba files tax); developers selling directly
  with Maktaba verifying their licence keys offline.
- Splits in the market: Apple and Google 70/30 (85/15 for small developers and
  subscriptions after a year), Epic Games Store 88/12, VS Code and Obsidian have no paid
  marketplace. The owner decided **85/15** (2026-11-03).

### 4.2 Review, signing, revocation

- **Signing by two parties.** Nextcloud binds a developer certificate to an app id; app
  stores countersign after review. A developer key proves *who* built it; the store key
  proves *Maktaba reviewed this exact file*.
- **Revocation must reach installed copies.** Chrome deactivates malicious extensions in
  users' browsers and marks them, but store removal alone does not uninstall; since Chrome 117
  it also flags installed extensions that left the store.
  Sources: [ITPro: 500 extensions removed and deactivated](https://www.itpro.com/security/cyber-security/354776/google-purges-500-harmful-chrome-extensions-from-its-web-store),
  [Malwarebytes: Chrome 117 safety check](https://www.malwarebytes.com/blog/news/2023/08/chrome-will-soon-start-clearing-out-old-potentially-rogue-extensions).
  We use a signed, expiring revocation list that every home fetches and honours.
- **Expiry and anti-rollback** follow Epic 51 H6 (the TUF timestamp and snapshot ideas
  without TUF's four roles): the catalog and the revocation list expire, and a home refuses
  one older than the newest it has seen.
- **Scale of review.** The VS Code and Obsidian experience says: automate every version,
  review humans on first submission and on capability increases, and keep a fast report path.

## 5. Maktaba's own stages, read for the first-party move (2026-11-03)

What the code says, for README §15 (first-party plugins):

- **Stages and where they are defined.** `engine/graph.py` `GRAPH`: 49 stages, of which 5 are
  legacy (`scan`, `probe`, `extract`, `thumbnail`, `categorize`) with handlers in
  `pipeline/src/maktaba_pipeline/handlers.py` and the thumbnail and classify modules, chaining
  by hand and recorded by the engine only through output fingerprints in
  `engine/stages/legacy.py`. The built-in extensions add 7 (`extensions/plex/`,
  `extensions/dizquetv/`). Handlers bind lazily through `engine/stages/registry.py`
  `CORE_MODULES`, each module exposing `IMPL: StageImpl` (run, input and output fingerprints,
  `unavailable`, `still_current`, `refresh`).
- **Pools.** 12 in `POOLS`; `gpu`'s size is the batch of the worker's shared Whisper
  (`stt/batched.py`), not a count of GPU users.
- **SQL.** About 870 SQL string literals in the pipeline package, about 90 of them in the engine
  and `db/`; the rest (about 780, in about 125 files) belong to stage code and the domain
  modules it calls. `detect`, `tag`, `transcribe` and `write_channels` have the most call sites.
  This is why the move uses named statements rather than Go endpoints (README H17).
- **Fingerprints are code, not queries.** For example `chapter_infer.input_fingerprint` hashes
  several reads together with the embedding model id and the stage version. So they move as
  plugin exports served by a light per-machine runner, not as a declarative list (README
  §15.4).
- **Processes.** Transcription already runs in supervised `pipeline/stt-N` processes (slot
  names and metrics in `cmd/maktaba-server/internal/supervisor/instances.go`); the semantic
  index worker `pipeline/index` also serves gRPC queries for the API (`grpc_server.py`).
- **Network from stage code.** YouTube and Google APIs (`integrations/youtube*.py`, `yt-dlp`),
  `ar.wikipedia.org` (`classify/books.py`), the metadata providers in `enrich/web.py`, the
  language model at `rules.<stage>.url`, an optional OpenAI-compatible speech-to-text URL
  (`stt/openai_api.py`), and Plex and dizqueTV (through the service door on nodes,
  story 31.R).
- **Queue tables** key on the stage name: `processing_jobs` (with `input_hash`, `output_hash`),
  `job_stages`, `job_events`, `worker_pools`, `workers`, `cluster_node_stages`. Keeping names
  means nothing in them is rewritten (README H19).
- **The Go API's queue handlers** (`api/internal/handlers/queues/`) only read and write those
  tables, and a few routes enqueue a named stage; none runs stage code.
