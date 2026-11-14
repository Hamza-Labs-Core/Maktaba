# Story 54.44 — One Plugins page for every plugin

> Epic 54 · Phase 5 · Depends on: 54.13, 54.22, 54.27, 54.37, 54.41, Epic 51 (51.08, 51.10, 51.14), Epic 53 · Needed by: 54.45 to 54.52

## Why

Settings → Plugins showed two kinds of plugin. A plugin installed from a file or the store had a card, a page and switches. A plugin Maktaba makes was a line in a second list, "Built into Maktaba", with no switch, no state, no errors and no page, and a sentence sending the admin to another page to turn it on or off. An admin could not see, in one place, what each plugin is doing, what it costs and what is wrong with it. This story makes one page that shows and switches every plugin the same way, says what is part of Maktaba itself and so not a plugin, and is honest about what a plugin that still ships with the release cannot do yet.

## Description

### 1. What is a plugin and what is core

- **A plugin Maktaba publishes** is a first-party plugin of this release's embedded bundle (`shared/plugins/firstparty.json`) whose Epic 51 component is switchable. It is listed, shown and switched as any other plugin, with a publisher: `{"id": "maktaba", "verified": true}`. Verified means the id prefix `tv.maktaba.` is one only the release's signed bundle can carry (54.22 §2).
- **Core is not a plugin.** A first-party plugin whose component is not switchable is an implementation detail of Maktaba itself. Today that is `tv.maktaba.core-media`, which implements `core.pipeline`. It is not in the list, has no page (`GET /api/plugins/tv.maktaba.core-media` is `404 plugin_not_installed`), no switch and no place in the store. The list answers it as `core: [{component, title_key}]`, and the page says in one line what is part of Maktaba itself, with links to Components and System health.
- There is no "built in" class: the response has no `built_in` field.

As built, the page lists twelve plugins as published by Maktaba (component in brackets): Classify (`classify`), Convert to MP4 (`library.convert`), Duplicates (`library.dedup`), Live TV channels (`livetv`), Live TV changes (`livetv.slots`), Plex (`ext.plex`), Repairing files (`library.repair`), Semantic search (`search.semantic`), Subtitle files (`subtitles`), Thumbnails (`media.thumbnails`), Transcription (`transcription`) and YouTube (`youtube`). Core media (`tv.maktaba.core-media`, `core.pipeline`) is core.

### 2. The API: the same shape for every plugin

No route is added. `GET /api/plugins` answers `{plugins, policy, core}`; `plugins` is one list by id. `GET /api/plugins/{id}` answers one plugin of either origin. Beside the fields of 54.13, every plugin has:

| Field | Meaning |
|---|---|
| `publisher` | `{id, verified}`: `maktaba`, or `""` for anyone else |
| `source` | `release` (came with this Maktaba release), `store` or `file` |
| `component` | the Epic 51 component its switch writes: its own component for a Maktaba plugin, `plugin.<id>` otherwise |
| `stage_names`, `queues` | its stages, and their names in the queue (a Maktaba plugin's are the stage names) |
| `switch_locked` | why the switch cannot be used (`licence_missing`, `held`, `revoked`, `platform_unsupported`, `incompatible`, …), absent when it can |
| `activity` | `pending`, `running`, `done_24h`, `failed_24h`, `problems`, `avg_ms`, `p95_ms` over its stages |
| `resources` | the processes that run it (§5) and, for a third-party plugin, its data and memory limits |
| `settings` | the settings its component owns (`rules.<section>`, `toml:<key>`) |

The detail adds `stages` (each with its queue, counts, usual time and `runs_in`), `capabilities` (a Maktaba plugin's, as `fp.trusted.<capability>` words) and `machines` (its status on each machine).

`problems` counts failed jobs that are still the newest job of their subject and stage, which is what the failures view lists; `failed_24h` counts every job that failed in the last day.

### 3. The switch: one source of truth

`POST /api/plugins/{id}/enable`, `/disable`, `/start` and `/stop` work for every plugin. For a Maktaba plugin they apply the change to the desired row of the component whose stages it implements, through the components service (51.08), so the checks, the licence gate, the audit rows and the per-machine overrides are Epic 51's and the Components page shows the same switch. Nothing else stores on or off.

`GET /api/plugins/{id}/downstream` answers what turning the plugin off does beside stopping it: other plugins' stages that go idle (54.39 §6), the components that wait or run with less (from the components preview, plus every dependant that is on), each with the plugin it belongs to when it has one, the component's own processes and loops, the user-facing features it serves, the queued jobs that will wait and the drain time. The page shows a dialog naming each of these before anything changes, and applies at once when there is nothing to name.

A switch that cannot be used is disabled with the reason in words; a stopped plugin has Start beside it.

### 4. What a plugin of the release cannot do yet

A Maktaba plugin with `source: release` is updated, rolled back and removed with Maktaba only. Its own update, rollback, remove, grants, schema settings, writes and store routes answer `409 plugin_with_release`. The page does not offer those controls: Versions says it comes with Maktaba and links to Updates, and the switches section says it cannot be removed yet and links to its component. Stories 54.45 to 54.51 change this.

### 5. What it uses

- **Jobs**: running, waiting, and the mean and 95th percentile time of the jobs done in the last day.
- **Memory**: of each process that runs its stages, per machine, from the supervisor's status rows. The supervisor now also reports each process's own memory (`detail.rss_parts`, a map from `<role>/<instance>` to MB, beside the component's `rss_mb`) when a component has more than one process, so a runner group has its own number.
- **Shared processes are said plainly.** A stage runs in its runner group's process (`plugin-runner/<group>`) when `job_stages.impl` is `plugin`, else in the pipeline worker; a third-party plugin runs in the plugin host. Each of these runs several plugins. The page shows the whole process's memory and names the other plugins it runs (and "Maktaba itself" when core stages run there). It never splits the number.
- **Quota**: a third-party plugin's data against its limit, with a bar, and the memory its recent jobs used against its limit per job (54.09).
- **Not measured**: CPU per process (54.52), and disk used by a Maktaba plugin's files (they are in the payload).

### 6. Errors

The card's problems count and the page's Problems section link to `/queue/failures?stage=<its queues, comma separated>`: the failures list's `stage` filter now takes several stages. The logs links open Epic 53's Logs page filtered to the plugin: `components=<component>` for a Maktaba plugin, `services=plugin.<id>` otherwise, with `level=error` for its errors.

### 7. The page

- **List** at `/settings/plugins`: a filter kept in the address (`?show=maktaba|others|file`), cards with name, version, publisher, state in words, the switch, jobs of the last day, problems, and memory with jobs now; how to install (store, file, the steps, what unreviewed means); the line about core; an empty state that says how to install on a home with no plugins.
- **A plugin's page** at `/settings/plugins/<id>`: overview, stages with links to their queues, permissions, status per machine, what it uses, problems, the flow chart (a Maktaba plugin's is drawn from the manifests' edges, with core stages drawn as core), settings (the schema form, or links to its Rules sections, the Models tab and its settings-file keys), versions, what it wrote, and the switches. A section that does not apply is absent.
- **Components** rows link to the plugin's page (`plugin` on each component view), and the plugin's page links back.

Every string is in English and Arabic, and Help has a Plugins topic in both.

## Tests

- T01 the list has Maktaba's plugins in the same shape, no `built_in`, and no core; core has no page and no switch.
- T02 a Maktaba plugin's switch on the plugin routes writes its component's row; a viewer is 403, no one 401, and nothing changes for them.
- T03 the effect names dependants with their plugin, parts of Maktaba that run with less, and the component's processes, loops and features.
- T04 activity and resources: counts, times, a shared runner group's own memory with who shares it, and the worker for stages not flipped.
- T05 every with-release refusal is `409 plugin_with_release` and changes nothing.
- Web: the list, the filter, the switch for both origins, each disabled reason, the dialog naming what else stops, the errors links, the shared group, deep links and Back, in English and Arabic.
