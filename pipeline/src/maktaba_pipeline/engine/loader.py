"""Build the full stage graph (core plus extensions) and bind handlers."""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import replace

from ..extensions.base import Extension
from .graph import GRAPH, GraphBuilder, StageDef, validate
from .stages.registry import register_impl

__all__ = ["build_full_graph", "register_extension_impls"]


def build_full_graph(extensions: Sequence[Extension] = ()) -> tuple[StageDef, ...]:
    """The core graph with every extension's stages and trigger edges added."""
    builder = GraphBuilder(GRAPH)
    for ext in extensions:
        before = {s.name for s in builder.build()}
        ext.register(builder)
        added = {s.name for s in builder.build()} - before
        declared = set(ext.manifest.stages)
        if declared != added:
            raise ValueError(
                f"extension {ext.manifest.name}: manifest stages {sorted(declared)} "
                f"differ from registered {sorted(added)}"
            )
    graph = tuple(_tag(s, extensions) for s in builder.build())
    validate(graph)
    return graph


def _tag(stage: StageDef, extensions: Sequence[Extension]) -> StageDef:
    for ext in extensions:
        if stage.name in ext.manifest.stages:
            return replace(stage, extension=ext.manifest.name)
    return stage


def register_extension_impls(extensions: Sequence[Extension]) -> None:
    for ext in extensions:
        for name, impl in ext.impls().items():
            register_impl(name, impl)
