"""The golden stage graph (Epic 54, story 54.25 §2).

Exports the graph the worker assembles (the stages still defined in ``engine/graph.py``,
the stages of Maktaba's first-party plugin manifests, and the built-in extensions'
stages and edges) as canonical JSON: stages sorted by name with every ``StageDef``
field, their edges, and the pools. ``shared/plugins/graph.golden.json`` holds the export
of the graph as it was before the first stage moved; the CI check ``graph-golden``
fails when the assembled graph differs from it and names what differs. A pull request
that changes the graph on purpose rewrites the file (``--write``) in the same PR.

Run from the pipeline's environment::

    cd pipeline && uv run python ../tools/plugins/graph_golden.py --check
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
GOLDEN = ROOT / "shared" / "plugins" / "graph.golden.json"
#: the stage names only Maktaba's own plugins may define (story 54.22 §1): every validator
#: refuses one in a third-party manifest (plugin_trusted_field)
RESERVED = ROOT / "shared" / "plugins" / "reserved_stages.txt"

#: StageDef fields that are code, not data: the export says only whether they are bound
_CALLABLES = ("run", "input_fingerprint", "output_fingerprint")


def export() -> dict[str, Any]:
    from dataclasses import fields

    from maktaba_pipeline.engine.graph import POOLS, StageDef
    from maktaba_pipeline.engine.loader import build_full_graph
    from maktaba_pipeline.extensions import discover

    graph = build_full_graph(discover(include_entry_points=False))
    stages: dict[str, Any] = {}
    for s in sorted(graph, key=lambda x: x.name):
        row: dict[str, Any] = {}
        for f in fields(StageDef):
            if f.name in _CALLABLES:
                continue
            v = getattr(s, f.name)
            if f.name == "edges":
                v = sorted((e.to_json() for e in v), key=lambda e: (e["to"], e["map"]))
            elif isinstance(v, tuple):
                v = list(v)
            row[f.name] = v
        stages[s.name] = row
    pools = {
        p.pool: {"label": p.label, "scope": p.scope, "size": p.size, "default_size": p.default_size}
        for p in POOLS
        if p.pool != "plugins"  # its size follows the machine's CPU count
    }
    return {"schema": 1, "stages": stages, "pools": dict(sorted(pools.items()))}


def dumps(doc: dict[str, Any]) -> str:
    return json.dumps(doc, indent=1, sort_keys=True, ensure_ascii=False) + "\n"


def diff(golden: dict[str, Any], now: dict[str, Any]) -> list[str]:
    """What differs, one line each: a stage added or gone, a field, a pool."""
    out: list[str] = []
    gs, ns = golden.get("stages", {}), now.get("stages", {})
    for name in sorted(set(gs) | set(ns)):
        if name not in ns:
            out.append(f"stage {name}: in the golden graph, not assembled")
            continue
        if name not in gs:
            out.append(f"stage {name}: assembled, not in the golden graph")
            continue
        for k in sorted(set(gs[name]) | set(ns[name])):
            if gs[name].get(k) != ns[name].get(k):
                out.append(
                    f"stage {name}.{k}: golden {json.dumps(gs[name].get(k), ensure_ascii=False)}"
                    f" != {json.dumps(ns[name].get(k), ensure_ascii=False)}"
                )
    if golden.get("pools") != now.get("pools"):
        out.append("pools differ")
    return out


def reserved_text(doc: dict[str, Any]) -> str:
    head = (
        "# Stage names reserved for Maktaba's own plugins (Epic 54, story 54.22 §1), one per\n"
        "# line: every stage of graph.golden.json. Written by tools/plugins/graph_golden.py.\n"
    )
    return head + "".join(f"{n}\n" for n in sorted(doc["stages"]))


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--write", action="store_true", help="rewrite the golden file")
    ap.add_argument("--check", action="store_true", help="fail when it differs (default)")
    args = ap.parse_args(argv)
    now = export()
    if args.write:
        GOLDEN.write_text(dumps(now), encoding="utf-8")
        RESERVED.write_text(reserved_text(now), encoding="utf-8")
        return 0
    golden = json.loads(GOLDEN.read_text(encoding="utf-8"))
    lines = diff(golden, now)
    if RESERVED.read_text(encoding="utf-8") != reserved_text(golden):
        lines.append("reserved_stages.txt is not the golden graph's stages (run --write)")
    for line in lines[:50]:
        print(f"graph-golden: {line}", file=sys.stderr)  # noqa: T201
    return 1 if lines else 0


if __name__ == "__main__":
    sys.exit(main())
