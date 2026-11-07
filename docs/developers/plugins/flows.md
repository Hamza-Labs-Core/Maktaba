# Flows: how plugins wire into the queue

> **Status:** the flow format, the manifest's graph fields and the flow check are **on
> `main`**: the home checks every install, enable, update and rollback, and the store's
> verification uses the same check. Emitting events and run-time lineage, the drawn chart
> and plugins on joined machines follow in the next releases of Epic 54. The exact fields
> and codes are in the [plugin reference](reference.md).

A plugin wires into the queue only through the graph it declares. There is no other way in:
no edge, request or event outside the declared graph is ever made, and the plugin API, the
home's loader and the pipeline each refuse one.

## The flow

A **flow** describes a set of plugins that work together: every plugin's stages, the core
events each attaches to, the events they publish, and who subscribes to what. It is the file
`flow.toml` at the root of every package of the set, the same bytes in each, signed with the
package. A single plugin is a flow of one.

```toml
schema  = 1
id      = "dev.example.chapters"
version = "1.2.0"

[strings]
name    = "flow.name"

[[plugin]]
id      = "dev.example.silences"
version = ">=1.4.0, <2.0.0"

  [[plugin.stage]]
  name     = "find_silences"
  subject  = "video"
  triggers = ["audio.ready"]
  emits    = ["silences.found"]

  [[plugin.event]]
  name    = "silences.found"
  subject = "video"
  payload = "events/silences.found.schema.json"

[[plugin]]
id      = "dev.example.chapter-writer"
version = ">=1.0.0, <2.0.0"

  [[plugin.stage]]
  name        = "write_chapters"
  subject     = "video"
  triggers    = ["plugin:dev.example.silences/silences.found"]
  may_request = ["tidy"]

  [[plugin.stage]]
  name     = "tidy"
  subject  = "video"
  triggers = ["stage:write_chapters"]
```

Drawn, this flow is:

```
audio.ready ──► silences/find_silences ──► ◆ silences.found ──► chapter-writer/write_chapters ──► chapter-writer/tidy
```

A trigger has one of three forms:

| Form | Means |
|---|---|
| `audio.ready` | after a stable core event ([the list](queue-and-api.md)) |
| `stage:<name>` | after another stage of the same plugin, when its output changed |
| `plugin:<id>/<event>` | when plugin `<id>` publishes `<event>` |

## The manifest's part

Each manifest carries its own part of the flow, and must match it exactly: the same stages
and subjects, triggers, emits and requests, the same published events, and a `[[depends]]`
entry for every plugin it subscribes to.

```toml
[[stage]]
name    = "find_silences"
subject = "video"
export  = "run-find-silences"
version = 3
emits   = ["silences.found"]
  [[stage.trigger]]
  event = "audio.ready"

[[event]]
name    = "silences.found"
subject = "video"
payload = "events/silences.found.schema.json"   # a JSON Schema in your package
```

And on the subscriber's side:

```toml
[[stage]]
name    = "write_chapters"
subject = "video"
export  = "run-write-chapters"
version = 1
  [[stage.trigger]]
  event = "plugin:dev.example.silences/silences.found"
  [[stage.trigger]]
  stage = "find_more"                           # or after one of its own stages

[[depends]]
plugin  = "dev.example.silences"
version = ">=1.4.0, <2.0.0"
```

A subscriber also asks for the capability `events.subscribe:<publisher id>`, which the
admin approves like any other.

## The rules

1. **Leaves after core.** Your stages run only after core events. Nothing your stages do
   reaches a core stage: no request, trigger or event names one, and no core stage waits on
   your output.
2. **Other plugins only through published events.** You never name another plugin's stage;
   you subscribe to an event it publishes. The payload is checked against the publisher's
   schema when it is emitted.
3. **No cycles.** The home's combined graph (core events, every enabled plugin's stages and
   events) has no cycle. A refusal names the cycle, for example:
   `dev.example.alpha/tag → plugin:dev.example.alpha/a.tagged → dev.example.beta/check →
   plugin:dev.example.beta/b.checked → dev.example.alpha/tag`.
4. **At most 16 steps.** No declared chain of stages from a core event is longer than 16,
   the run-time hop cap.
5. **Everything exists.** Every event named is a core event or is published by a plugin of
   the flow or one it depends on, at a version it accepts, about the same kind of subject.

**Where it is checked.** The store's verification runs the check over a submitted flow and
the plugins it depends on, and refuses a flow that breaks a rule, naming the cycle or the
bad edge. The home runs the same check again at install, enable, update and rollback, over
every enabled plugin: a home can combine flows the store never saw together, and a file
install skips the store. A plugin whose upstream is missing still installs; its subscribing
stages are idle and its page says which plugin is missing.

## Codes

| Code | When |
|---|---|
| `flow_cycle` | the change would close a loop; the path is named |
| `flow_core_edge` | a stage reaches a core stage |
| `flow_foreign_stage` | a stage names another plugin's stage |
| `flow_foreign_event` | a stage emits an event its plugin does not publish |
| `flow_unknown_event` | a trigger names an event no one publishes |
| `flow_subject_mismatch` | an event is about another kind of subject than the stage |
| `flow_missing_plugin` | an upstream is missing or out of range (a warning on a home) |
| `flow_too_deep` | a chain is longer than 16 stages |
| `flow_manifest_mismatch` | the manifest differs from its flow entry |
| `flow_not_member` | the plugin is not in its own flow at this version |

On a home a refusal answers `409 plugin_flow_cycle` (a cycle) or `409 plugin_flow_invalid`,
with every finding in English and Arabic.
