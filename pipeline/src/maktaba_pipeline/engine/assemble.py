"""The graph from manifests (Epic 54, story 54.25 §1).

``GRAPH`` in :mod:`.graph` names every stage in its order; a stage one of Maktaba's own
first-party plugins defines is a ``_fp("<name>")`` there, whose fields are its manifest's
``[[stage]]`` (:func:`maktaba_pipeline.plugins.firstparty.stage_def`). :func:`check` is what
the build refuses: a first-party stage the graph does not name (it would never be synced
into ``job_stages``), and a stage the graph defines in code while a manifest defines it too.
``tools/plugins/graph_golden.py`` then compares the assembled graph, field for field, with
``shared/plugins/graph.golden.json``.
"""

from __future__ import annotations

from collections.abc import Sequence

from .graph import GraphError, StageDef

__all__ = ["check"]


def check(graph: Sequence[StageDef], first_party: Sequence[StageDef]) -> None:
    """Raise :class:`GraphError` when the graph and the manifests disagree on who defines
    which stage."""
    by_name = {s.name: s for s in graph}
    for fp in first_party:
        mine = by_name.get(fp.name)
        if mine is None:
            raise GraphError(f"{fp.name}: a first-party manifest defines it; GRAPH does not name it")
        if mine != fp:
            raise GraphError(f"{fp.name}: defined in GRAPH and in its manifest (use _fp)")
