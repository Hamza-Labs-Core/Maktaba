#!/usr/bin/env python3
"""Writes shared/plugins/testdata/flows/ (story 54.39 §5): each case's manifests, flow.toml
and case.json. The expected findings and graphs (expect.json) are written by the Go test
with UPDATE_GOLDEN=1 and reviewed by hand; the TypeScript checker must give the same.

    python3 shared/plugins/testdata/flows_gen.py
"""

from __future__ import annotations

import json
import shutil
from pathlib import Path

OUT = Path(__file__).resolve().parent / "flows"


def manifest(pid: str, stages: list[dict], events: list[dict] = (), depends: dict | None = None,
             version: str = "1.0.0") -> str:
    lines = [
        "schema = 1",
        f'id = "{pid}"',
        f'version = "{version}"',
        'plugin_api = "1.0"',
        'maktaba = ">=0.6.0"',
        'licence = "MIT"',
        "",
        "[strings]",
        'name = "plugin.name"',
        'summary = "plugin.summary"',
    ]
    for i, s in enumerate(stages):
        lines += ["", "[[stage]]", f'name = "{s["name"]}"', f'subject = "{s.get("subject", "video")}"',
                  f'export = "run-{i}"', "version = 1"]
        if s.get("may_request"):
            lines.append("may_request = " + json.dumps(s["may_request"]))
        if s.get("emits"):
            lines.append("emits = " + json.dumps(s["emits"]))
        for t in s.get("triggers", []):
            lines.append("  [[stage.trigger]]")
            if t.startswith("stage:"):
                lines.append(f'  stage = "{t[6:]}"')
            else:
                lines.append(f'  event = "{t}"')
    for e in events:
        lines += ["", "[[event]]", f'name = "{e["name"]}"', f'subject = "{e.get("subject", "video")}"',
                  f'payload = "events/{e["name"]}.schema.json"']
    for p, r in (depends or {}).items():
        lines += ["", "[[depends]]", f'plugin = "{p}"', f'version = "{r}"']
    return "\n".join(lines) + "\n"


def flow(fid: str, members: list[dict]) -> str:
    lines = ["schema = 1", f'id = "{fid}"', 'version = "1.0.0"', "", "[strings]", 'name = "flow.name"']
    for m in members:
        lines += ["", "[[plugin]]", f'id = "{m["id"]}"', f'version = "{m.get("range", ">=1.0.0, <2.0.0")}"']
        for s in m["stages"]:
            lines += ["", "  [[plugin.stage]]", f'  name = "{s["name"]}"', f'  subject = "{s.get("subject", "video")}"']
            for k in ("triggers", "emits", "may_request"):
                if s.get(k):
                    lines.append(f"  {k} = " + json.dumps(s[k]))
        for e in m.get("events", []):
            lines += ["", "  [[plugin.event]]", f'  name = "{e["name"]}"', f'  subject = "{e.get("subject", "video")}"',
                      f'  payload = "events/{e["name"]}.schema.json"']
    return "\n".join(lines) + "\n"


def depends_of(stages: list[dict]) -> dict:
    out = {}
    for s in stages:
        for t in s.get("triggers", []):
            if t.startswith("plugin:"):
                out[t[7:].split("/")[0]] = ">=1.0.0, <2.0.0"
    return out


CASES: dict[str, dict] = {}


def case(name: str, mode: str, about: str, members: list[dict], flow_members: list[dict] | None = None,
         states: dict | None = None, package: str | None = None, catalog: list[dict] | None = None) -> None:
    CASES[name] = dict(mode=mode, about=about, members=members, flow=flow_members, states=states or {},
                       package=package, catalog=catalog or [])


A, B, C = "dev.example.alpha", "dev.example.beta", "dev.example.gamma"

silences = {"id": "dev.example.silences", "stages": [
    {"name": "find_silences", "triggers": ["audio.ready"], "emits": ["silences.found"]}],
    "events": [{"name": "silences.found"}]}
writer = {"id": "dev.example.chapter-writer", "stages": [
    {"name": "write_chapters", "triggers": ["plugin:dev.example.silences/silences.found"], "may_request": ["tidy"]},
    {"name": "tidy", "triggers": ["stage:write_chapters"]}]}

case("single", "home", "one plugin, a flow of one: core event, own stage after, a request",
     [{"id": A, "stages": [
         {"name": "scan", "triggers": ["audio.ready", "video.changed"], "may_request": ["write"]},
         {"name": "write", "triggers": ["stage:scan"]},
         {"name": "season_pass", "subject": "season", "triggers": ["series.changed"]}]}])
case("chain_two_store", "store", "a two-plugin flow: silences publishes, chapter-writer subscribes",
     [silences, writer], flow_members=[silences, writer])
case("chain_two_home", "home", "the same two plugins on a home, from their manifests", [silences, writer])

a_loop = {"id": A, "stages": [{"name": "tag", "triggers": ["metadata.changed", f"plugin:{B}/checked"],
                                "emits": ["tagged"]}], "events": [{"name": "a.tagged"}]}
# event names need a dot
a_loop = {"id": A, "stages": [{"name": "tag", "triggers": ["metadata.changed", f"plugin:{B}/b.checked"],
                                "emits": ["a.tagged"]}], "events": [{"name": "a.tagged"}]}
b_loop = {"id": B, "stages": [{"name": "check", "triggers": [f"plugin:{A}/a.tagged"], "emits": ["b.checked"]}],
          "events": [{"name": "b.checked"}]}
case("cycle_two_home", "home", "two plugins that start each other: refused with the path", [a_loop, b_loop])

a3 = {"id": A, "stages": [{"name": "one", "triggers": ["audio.ready", f"plugin:{C}/c.done"], "emits": ["a.done"]}],
      "events": [{"name": "a.done"}]}
b3 = {"id": B, "stages": [{"name": "two", "triggers": [f"plugin:{A}/a.done"], "emits": ["b.done"]}],
      "events": [{"name": "b.done"}]}
c3 = {"id": C, "stages": [{"name": "three", "triggers": [f"plugin:{B}/b.done"], "emits": ["c.done"]}],
      "events": [{"name": "c.done"}]}
case("cycle_three_store", "store", "a three-plugin flow with a cycle, submitted together: refused (54.16 T09)",
     [a3, b3, c3], flow_members=[a3, b3, c3])
case("cycle_three_home", "home", "the same three plugins from two flows, each valid alone, together on a home",
     [a3, b3, c3])
case("cycle_three_home_one_off", "home", "the three-plugin loop with one plugin turned off: no cycle, a warning",
     [a3, b3, c3], states={C: "installed"})

case("self_loop_request", "home", "a stage that may request itself",
     [{"id": A, "stages": [{"name": "again", "triggers": ["audio.ready"], "may_request": ["again"]}]}])
case("own_stage_cycle", "home", "two own stages after each other",
     [{"id": A, "stages": [{"name": "one", "triggers": ["audio.ready", "stage:two"]},
                            {"name": "two", "triggers": ["stage:one"]}]}])
case("core_edge_request", "store", "a flow whose stage requests a core stage",
     [], flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["audio.ready"], "may_request": ["transcribe"]}]}])
case("core_edge_trigger", "store", "a flow whose stage runs after a core stage by name",
     [], flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["stage:extract"]}]}])
case("core_stage_as_event", "store", "a flow that names a core stage as if it were an event",
     [], flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["transcribe"]}]}])
case("foreign_stage", "store", "a flow whose stage requests another plugin's stage",
     [], flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["audio.ready"], "may_request": ["two"]}]},
                       {"id": B, "stages": [{"name": "two", "triggers": ["audio.ready"]}]}])
case("foreign_event_emit", "store", "a flow whose stage emits an event its plugin does not publish",
     [], flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["audio.ready"], "emits": ["b.done"]}]},
                       {"id": B, "stages": [{"name": "two", "triggers": ["audio.ready"], "emits": ["b.done"]}],
                        "events": [{"name": "b.done"}]}])
case("missing_upstream_home", "home", "a subscriber whose upstream is not installed: a warning, not a refusal",
     [writer])
case("missing_upstream_store", "store", "a flow that depends on a plugin not in the store: refused",
     [], flow_members=[writer])
case("unknown_event", "home", "a subscription to an event the publisher does not publish",
     [silences, {"id": "dev.example.chapter-writer", "stages": [
         {"name": "write_chapters", "triggers": ["plugin:dev.example.silences/silences.lost"]}]}])
case("subject_mismatch", "home", "a season stage subscribed to a video event",
     [silences, {"id": "dev.example.chapter-writer", "stages": [
         {"name": "write_chapters", "subject": "season", "triggers": ["plugin:dev.example.silences/silences.found"]}]}])
deep = [{"name": "s01", "triggers": ["audio.ready"]}] + [
    {"name": f"s{i:02d}", "triggers": [f"stage:s{i - 1:02d}"]} for i in range(2, 18)]
case("too_deep", "home", "a chain of 17 stages, past the 16-hop lineage cap", [{"id": A, "stages": deep}])
case("sixteen_deep", "home", "a chain of exactly 16 stages: allowed", [{"id": A, "stages": deep[:16]}])
case("manifest_mismatch", "store", "a manifest whose trigger differs from its flow entry",
     [{"id": A, "stages": [{"name": "one", "triggers": ["video.added"]}]}],
     flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["audio.ready"]}]}], package=A)
case("not_member", "store", "a manifest at a version outside its flow's range",
     [{"id": A, "version": "2.0.0", "stages": [{"name": "one", "triggers": ["audio.ready"]}]}],
     flow_members=[{"id": A, "stages": [{"name": "one", "triggers": ["audio.ready"]}]}], package=A)
case("version_out_of_range_home", "home", "an installed upstream at a version the subscriber does not accept",
     [dict(silences, version="2.0.0"), writer])


X, Y, Z = "dev.example.xray", "dev.example.yankee", "dev.example.zulu"
fa = {"id": A, "stages": [{"name": "one", "triggers": ["audio.ready", f"plugin:{X}/x.done"], "emits": ["a.done"]}],
      "events": [{"name": "a.done"}]}
cx = {"id": X, "stages": [{"name": "x", "triggers": [f"plugin:{Y}/y.done"], "emits": ["x.done"]}],
      "events": [{"name": "x.done"}]}
cy = {"id": Y, "stages": [{"name": "y", "triggers": [f"plugin:{A}/a.done"], "emits": ["y.done"]}],
      "events": [{"name": "y.done"}]}
case("cycle_through_store_plugins", "store",
     "a one-plugin flow that loops through two store plugins found only by walking publishers",
     [fa], flow_members=[fa], catalog=[cx, cy])
cz = {"id": X, "stages": [{"name": "x", "triggers": [f"plugin:{Z}/z.done"], "emits": ["x.done"]}],
      "events": [{"name": "x.done"}]}
case("transitive_missing_store", "store",
     "a flow whose store upstream depends on a plugin not in the store: refused",
     [dict(fa, stages=[{"name": "one", "triggers": [f"plugin:{X}/x.done"], "emits": ["a.done"]}])],
     flow_members=[dict(fa, stages=[{"name": "one", "triggers": [f"plugin:{X}/x.done"], "emits": ["a.done"]}])],
     catalog=[cz])
case("transitive_chain_store", "store", "a flow over a two-deep chain of store plugins: allowed",
     [dict(fa, stages=[{"name": "one", "triggers": [f"plugin:{X}/x.done"], "emits": ["a.done"]}])],
     flow_members=[dict(fa, stages=[{"name": "one", "triggers": [f"plugin:{X}/x.done"], "emits": ["a.done"]}])],
     catalog=[dict(cx, stages=[{"name": "x", "triggers": ["audio.ready", f"plugin:{Y}/y.done"], "emits": ["x.done"]}]),
              dict(cy, stages=[{"name": "y", "triggers": ["audio.ready"], "emits": ["y.done"]}])])


def main() -> None:
    if OUT.exists():
        keep = {p.name: (p / "expect.json").read_bytes() for p in OUT.iterdir() if (p / "expect.json").exists()}
        shutil.rmtree(OUT)
    else:
        keep = {}
    for name, c in CASES.items():
        d = OUT / name
        (d / "plugins").mkdir(parents=True)
        for m in c["members"]:
            deps = depends_of(m["stages"])
            (d / "plugins" / f'{m["id"]}.toml').write_text(
                manifest(m["id"], m["stages"], m.get("events", []), deps, m.get("version", "1.0.0")))
        if c["catalog"]:
            # the store's other plugins: found only by walking the flow's publishers
            (d / "catalog").mkdir()
            for m in c["catalog"]:
                deps = depends_of(m["stages"])
                (d / "catalog" / f'{m["id"]}.toml').write_text(
                    manifest(m["id"], m["stages"], m.get("events", []), deps, m.get("version", "1.0.0")))
        if c["flow"] is not None:
            (d / "flow.toml").write_text(flow("dev.example.flow", c["flow"]))
        meta = {"about": c["about"], "mode": c["mode"], "states": c["states"]}
        if c["package"]:
            meta["package"] = c["package"]
        (d / "case.json").write_text(json.dumps(meta, indent=2, sort_keys=True) + "\n")
        if name in keep:
            (d / "expect.json").write_bytes(keep[name])


if __name__ == "__main__":
    main()
