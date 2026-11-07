# Story 54.39 — Flows: the declared graph, the leaf rule and the cycle check

> Epic 54 · Phase 1 · Depends on: 54.01, 54.06, 54.07 · Needed by: 54.40, 54.41, 54.42, 54.16

## Why

A plugin that wires itself into the queue from code is a plugin no one can check before it
runs. Plugins that trigger each other can close a loop no single plugin shows, and a plugin
that sits before a core stage can stall every library. The graph a set of plugins builds is
therefore declared, signed and checked before anything runs: the flow is the configuration,
and nothing outside it is wired (README §4.12, H24, H25).

## Description

### 1. `flow.toml`

One document per set of plugins that works together, at the root of every member's package,
byte-identical in each, listed in `files.json` (both signatures cover it). A single plugin is
the trivial flow of one member. Fields, with their limits:

| Field | Meaning | Limits |
|---|---|---|
| `schema` | `1` | — |
| `id` | the flow's id, reverse-DNS under the developer's verified prefix (the same rules as a plugin id, 54.01) | ≤ 100 chars |
| `version` | semver; any change to the flow is a new version | — |
| `[strings] name` | a key into each member's `i18n/en.json` and `i18n/ar.json` | both languages |
| `[[plugin]] id`, `version` | a member, and the version range of it this flow describes | 1–20 members |
| `[[plugin.stage]] name` | a stage of that member | ≤ 20 per member |
| `triggers` | core events (`audio.ready`), own stages (`stage:<name>`), published events (`plugin:<id>/<event>`) | ≤ 10 |
| `emits` | the member's own published events this stage may emit | ≤ 20 |
| `may_request` | the member's own stages it may request | ≤ 20 |
| `[[plugin.event]] name`, `subject`, `payload` | a published event: dotted lower-case name, its subject kind, the path of its payload schema in that member's package | ≤ 20 per member; schema ≤ 16 KB, the settings' JSON Schema subset (54.11) |

A schedule (`[[schedule]]`) is part of the manifest only: it is an origin, not an edge.

`shared/plugins/flow.schema.json` is the one schema, read by the same keyword subset as the
manifest schema, in Go, Rust, Python and TypeScript (the manifest validators already read
that subset).

### 2. The manifest's graph fields

The manifest (54.01) gains, all optional:

- `[[stage]] emits = [...]`: own `[[event]]` names only;
- `[[stage.trigger]]` takes exactly one of `event` (a core event, or `plugin:<id>/<event>`) or
  `stage` (an own stage, an edge after it when its output changed);
- `[[event]]` with `name`, `subject`, `payload`;
- `[[depends]]` with `plugin` and `version` (a range): every plugin whose events a trigger
  names must be listed, and every listed plugin must be named by a trigger;
- `placement`: `home` (the default), `any`, or `gpu` (54.42).

New manifest codes, in the shared corpus (`shared/plugins/testdata/manifests/invalid/`) and
in all four validators: `manifest_bad_emit` (an emit not in `[[event]]`),
`manifest_bad_trigger` (both or neither of `event` and `stage`; a `stage` that is not its
own), `manifest_bad_event` (name, subject or payload path), `manifest_bad_depends` (a
subscription with no `[[depends]]`, a `[[depends]]` no trigger uses, its own id),
`manifest_bad_placement`.

### 3. The combined graph

The checker builds one directed graph:

| Node | Id in the normalised graph |
|---|---|
| a core event of `events.toml` | `core:<event>` |
| a plugin stage | `stage:<plugin id>/<stage>` |
| a published event | `event:<plugin id>/<event>` |

| Edge | From → to |
|---|---|
| trigger by a core event | `core:<e>` → `stage:<p>/<s>` |
| trigger after an own stage, and `may_request` | `stage:<p>/<a>` → `stage:<p>/<b>` |
| emit | `stage:<p>/<s>` → `event:<p>/<e>` |
| subscription | `event:<q>/<e>` → `stage:<p>/<s>` |

There is no edge into a `core:` node: the leaf rule is that no such edge can be written.
The normalised graph is JSON with sorted nodes and edges, identical in Go and TypeScript
(the corpus compares bytes), and is what the chart draws (54.41).

### 4. The checks, in order

1. **Schema** of `flow.toml` and of each payload schema (`flow_invalid`,
   `flow_unknown_key`, `flow_payload_schema`).
2. **Membership**: the package's own plugin is a member, and its version is in the member's
   range (`flow_not_member`).
3. **Match**: the manifest's projection (stages; their triggers, emits and may_request as
   sets; events with subject and the sha256 of the payload schema; dependencies) equals the
   flow member's entry (`flow_manifest_mismatch`, naming the plugin and the first field that
   differs).
4. **Leaves**: a trigger, a `stage:` reference or a `may_request` that names a core stage or
   another plugin's stage (`flow_core_edge`, `flow_foreign_stage`). An emit of another
   plugin's event (`flow_foreign_event`).
5. **Existence and subjects**: every event named exists (a core event, or published by a
   member or by a plugin the flow depends on at a compatible version) and its subject equals
   the subscriber stage's (`flow_unknown_event`, `flow_subject_mismatch`). A dependency that
   is absent, disabled or out of range is a **refusal at the store** and a **warning on the
   home** (`flow_missing_plugin`).
6. **Cycles**: Tarjan's strongly connected components; any component of two or more nodes,
   or a node with an edge to itself, is a cycle (`flow_cycle`). The path is deterministic:
   take the component whose smallest node id sorts first, start at that node, and walk the
   shortest cycle back to it by breadth-first search inside the component, visiting
   neighbours in sorted order. Both languages print it the same way:
   `dev.example.a/tag → plugin:dev.example.a/tagged → dev.example.b/check → dev.example.a/tag`.
7. **Depth**: the longest path from a `core:` node (or a schedule's stage) through stages is
   at most 16 stages, the lineage hop cap (`flow_too_deep`, with the path).
8. **Limits**: members, events, stages and the home's whole graph (README §4.7,
   `flow_limit`).

Every code has its words in `shared/plugins/flow-messages.json`, in English and Arabic, with
`{path}`, `{plugin}`, `{event}`, `{stage}` and `{field}` placeholders; the Go API, the store
and the SDK all print from that one file. The Arabic keeps the ids and the path in an
isolated left-to-right run.

### 5. One checker, two languages, one corpus

- **Go**: `shared/plugins/pluginflow` (`Build`, `Check`, `Normalised`), used by the API.
- **TypeScript**: `tools/plugin-sdk/js/src/flow.ts` (`buildGraph`, `checkFlow`,
  `normalised`), used by the store's checks (54.16 C12), the SDK and the web's chart.
- **The corpus**: `shared/plugins/testdata/flows/`: `valid/<case>/` (a `flow.toml`, the
  members' manifests and an `expect.json` with the normalised graph) and `invalid/<case>/`
  (the same plus `expect.json` naming the code and, for cycles and bad edges, the exact path
  or edge). It holds at least: a cycle across two plugins, a cycle across three, a self-loop
  through `may_request`, an edge to a core stage, a request to another plugin's stage, a
  missing upstream, a subject mismatch, a chain of 17 stages, a manifest that differs from
  its flow, and two flows valid alone that close a cycle together.

### 6. On the home

The API runs the check over core events plus every enabled plugin plus the change:

- **install** (from a file or the store): the package's flow, its manifest, and the home's
  enabled plugins; a cycle or a bad edge refuses the install (`409 plugin_flow_cycle` or
  `409 plugin_flow_invalid`, with `code`, `path`, `message` and `message_ar`), and nothing is
  unpacked;
- **enable**, **update** and **rollback** of the plugin, and **re-enable or update of a
  plugin it depends on** (the subscribers are re-checked with the new upstream);
- **disable** of a plugin that others depend on: `GET /api/plugins/{id}/downstream` lists
  the plugins and stages that go idle, and the web asks before disabling.

`GET /api/plugins/graph` answers the home's whole normalised graph, with each plugin's state
(enabled, installed, missing) and every warning, for the chart (54.41). A plugin with a
missing upstream installs and enables; its subscribing stages are not wired, and its page
says which plugin is missing.

### 7. In the pipeline

The loader wires only what the manifest declares and checks the leaf rule again as an
assertion: an edge from a plugin stage to any stage that is not the same plugin's, or an
edge into a plugin stage from anything but its declared core events and own stages, raises
`GraphViolation` and the plugin is left out with a warning, never half-wired. A `stage`
trigger becomes an `Edge` between the two hashed stage names; a subscription is not an edge
(54.40 delivers events as requests).

## Acceptance criteria

- **Given** plugin A publishing `a.tagged` and subscribing to `plugin:b/b.checked`, and B the
  reverse,
  **when** the admin enables the second of them,
  **then** the enable is refused with `plugin_flow_cycle` and the path, in English and Arabic,
  and nothing changes.
- **Given** a three-plugin cycle split across two flows that are each valid alone,
  **when** the third plugin is installed,
  **then** the install is refused with the three-plugin path.
- **Given** a manifest whose `may_request` names `transcribe`,
  **when** validated,
  **then** `manifest_bad_request` in all four validators, and `flow_core_edge` from both
  flow checkers.
- **Given** a subscriber whose upstream is not installed,
  **when** installed,
  **then** it installs, its page and chart name the missing plugin, and its subscribing stage
  has no jobs.
- **Given** an enabled upstream with an enabled subscriber,
  **when** the admin disables the upstream,
  **then** the dialog lists the subscriber's stages that go idle before anything changes.
- **Given** every case of the flow corpus,
  **when** the Go and the TypeScript checkers run,
  **then** both give the same code, path and normalised graph.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go, ts) | the flow corpus | check each case | valid pass with the golden graph; invalid give the code and path in `expect.json` |
| T02 | unit ×4 (go, rust, py, ts) | the manifest corpus's new cases | validate | each new code |
| T03 | integration (PG, go) | two plugins forming a cycle | install both, enable the second | `409 plugin_flow_cycle`, path; first still enabled, second not |
| T04 | integration (PG, go) | three plugins, two flows | install the third | refused with the three-plugin path |
| T05 | integration (PG, go) | subscriber without its upstream | install, enable | enabled; `graph` marks the upstream missing |
| T06 | integration (PG, go) | upstream and subscriber enabled | `GET …/downstream` | the subscriber's stages |
| T07 | unit (py) | a manifest edited after install to request a core stage | load | `GraphViolation`, plugin left out, warning logged |
| T08 | unit (go, ts) | every code | message lookup | en and ar words, placeholders filled |
| T09 | bench (go, ts) | 100 plugins × 20 stages | check | ≤ 50 ms (N22) |

## Edge cases

- **A package with no `flow.toml`** (built before flows): read as the flow of its own
  manifest; the store refuses such a submission.
- **Two members of one flow from different developers**: the flow's id is under one
  developer's prefix; the store accepts members of other prefixes only when their own
  packages carry the same flow (54.16 C12).
- **A plugin in two flows** (a shared upstream): allowed; the home checks the union.
- **Disabling a plugin in the middle of a chain**: its downstream goes idle, its upstream
  keeps running and its events reach no one.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/flow.schema.json`, `flow-messages.json` (new) | the flow schema, the codes' words |
| `shared/plugins/manifest.schema.json`, the four validators | the graph fields and codes |
| `shared/plugins/pluginflow/` (new, go) | build, check, normalise |
| `tools/plugin-sdk/js/src/flow.ts` (new) | the same in TypeScript |
| `shared/plugins/testdata/flows/` (new) | the corpus |
| `api/internal/plugins/lifecycle.go`, `routes.go` | the check at install, enable, update, rollback; `graph` and `downstream` routes |
| `pipeline/src/maktaba_pipeline/plugins/loader.py` | `stage` triggers; the leaf assertion |
| `web/src/i18n/{en,ar}.json` | the codes' words for the web |

### Tests

T01–T09 above; the corpus runs in the Go unit job and the SDK's vitest job.

### Rollout

Dark until a plugin declares a graph field; existing manifests are flows of one and pass.

### Rollback

A release without flows ignores `flow.toml` and refuses manifests with the new fields
(`manifest_unknown_key`): those plugins go *Waiting* `incompatible` (54.37), never *Failed*.

### Dependencies

54.01 (manifest), 54.06 (loader), 54.07 (lifecycle).
