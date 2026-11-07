# Story 54.41 — The flow chart

> Epic 54 · Phase 1 · Depends on: 54.13, 54.39 · Needed by: 54.18

## Why

An admin approving a plugin should see what it attaches to, what it starts and which other
plugins it works with before it runs, and a developer should see the same picture the store's
reviewer sees. The declared graph is already checked (54.39); this story draws it, from the
same normalised graph, wherever someone decides about it (README §4.12).

## Description

### 1. One layout

`tools/plugin-sdk/js/src/flowchart.ts` lays out a normalised graph (54.39 §3) and returns an
SVG string, so the web, the store's listing and review pages, the SDK and maktaba.dev draw the
same chart:

- **Columns** by longest path from a core event: core events first, then plugin stages and
  their published events in the order they run. In Arabic the chart runs right to left.
- **Rows** ordered to reduce crossings (barycentre passes, deterministic).
- **Nodes**: core events as rounded tags in Maktaba's words (English and Arabic, from the
  web's i18n), plugin stages as boxes grouped by plugin with the plugin's name, published
  events as small diamonds with their name.
- **Highlight**: the plugin being installed or opened is drawn in the accent colour; the
  rest of its flow in the normal colour; plugins of the flow that are not installed dashed
  and labelled "not installed" / «غير مثبّتة»; idle subscriptions dashed.
- **Plain text only**: every label is escaped text from the manifest's strings or the
  graph's ids; no URL, image or markup from a plugin is ever drawn.
- **Accessible**: the SVG has a title and a description, and beside it a list of the same
  steps in reading order for screen readers.

### 2. Where it is drawn

- **Install and approve** (web, 54.13): after the upload, beside the permissions, the
  package's flow with this plugin highlighted and the flow's missing members listed by name
  ("Works with: Chapter writer (not installed)"); a refusal from the flow check shows the
  cycle path or the bad edge in place of the chart.
- **The Plugins page** (web): a *Graph* section on each plugin's page draws the whole home's
  plugin graph (`GET /api/plugins/graph`) with this plugin highlighted; the Installed list's
  header links to the same graph with nothing highlighted.
- **The store** (Phase 2, 54.16 and 54.18): the review page and the listing draw the
  submitted flow.
- **maktaba.dev**: the Plugins guides draw an example flow with the same code at build time.
- **The Remote apps** do not show plugin administration (README §4.10), so they draw no
  chart.

### 3. The SDK and the harness

- `mkp flow` prints a package's flow as a text chart in the terminal and writes `flow.mmd`
  (Mermaid) beside it; `mkp check` runs the flow check (the Rust manifest validator plus
  the TypeScript checker through the SDK's `flow` command, the same corpus).
- `mkp test --with <other.mkplugin>` runs a two-plugin chain in the harness: the first
  plugin's emitted events are delivered to the second's subscriber stages with lineage, and
  the report draws the chain.

## Acceptance criteria

- **Given** a plugin of a three-plugin flow with one member not installed,
  **when** the admin uploads it,
  **then** the install screen draws the flow with this plugin highlighted and the missing
  member dashed and named, before Enable.
- **Given** an Arabic session,
  **when** the chart is drawn,
  **then** it runs right to left with Arabic words for core events, and ids stay readable.
- **Given** a plugin name with markup or a URL,
  **when** drawn,
  **then** the text is shown as text and nothing is fetched.
- **Given** the Plugins page of a plugin,
  **when** opened,
  **then** the home's whole plugin graph is drawn with this plugin highlighted.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (ts) | the flow corpus's valid graphs | lay out | deterministic SVG (golden), no crossings on the chain cases |
| T02 | unit (ts) | label with `<script>`, `https://` | render | escaped text, no `href`, no `<image>` |
| T03 | unit (web) | install screen with a three-plugin flow | render | highlight, missing member named |
| T04 | unit (web) | ar locale | render | RTL columns, Arabic core event words |
| T05 | unit (web) | plugin page | render | home graph, highlight |
| T06 | unit (rust) | `mkp flow` on the corpus | run | text chart and Mermaid |
| T07 | integration (rust) | `mkp test --with` two hello guests | run | the second ran, lineage 2 |

## Edge cases

- **A home with 100 plugins**: the Plugins page draws the highlighted plugin's connected
  component only, with a count of the rest.
- **A flow refused by the check**: no chart; the refusal names the path.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `tools/plugin-sdk/js/src/flowchart.ts` (new) | layout and SVG |
| `web/src/pages/settings/plugins/` | the chart on the install screen and the plugin page |
| `web/src/i18n/{en,ar}.json` | words |
| `plugind/crates/mkp` | `flow`, `test --with` |
| `web/packages/devportal` | the example chart, en and ar |

### Tests

T01–T07.

### Rollout

With 54.39.

### Rollback

Remove the section; nothing is stored.

### Dependencies

54.13 (Plugins page), 54.39 (normalised graph).
