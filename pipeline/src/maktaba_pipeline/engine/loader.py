"""Build the full stage graph (core plus extensions) and bind handlers."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import replace

from ..extensions.base import Extension
from ..extensions.store import ExtensionState
from .graph import GRAPH, GraphBuilder, StageDef, validate
from .stages.plan_channels import register_plan_exporter
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


def register_extension_impls(
    extensions: Sequence[Extension],
    states: Mapping[str, ExtensionState] | None = None,
) -> None:
    """Register every extension's stage implementations and, for enabled and configured
    extensions, its plan exporters (how an extension adds data to the channel plan)."""
    for ext in extensions:
        for name, impl in ext.impls().items():
            register_impl(name, impl)
        exporters = getattr(ext, "plan_exporters", None)
        if not callable(exporters):
            continue
        state = None if states is None else states.get(ext.manifest.name)
        if state is None or (state.enabled and state.configured):
            for name, fn in exporters().items():
                register_plan_exporter(name, fn)
