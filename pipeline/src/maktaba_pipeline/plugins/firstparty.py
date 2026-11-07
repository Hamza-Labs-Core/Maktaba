"""Maktaba's own stages as first-party plugins: the pipeline's view (Epic 54, README §15).

The plugins are built into the release: ``plugins/data/firstparty.json`` is the bundle
``tools/plugins/bundle.py`` writes (the same bytes the server binary embeds), with each
plugin's manifest, strings and compiled statements. This module reads it:

- :func:`stage_def` is a first-party stage's :class:`~maktaba_pipeline.engine.graph.StageDef`,
  field for field from its manifest's ``[[stage]]`` (story 54.25 §1): the graph names the
  stage, the manifest defines it;
- :func:`impl_entry` is where its code is (``module:IMPL``, a :class:`StageImpl`), which
  the worker imports to run the stage in-process (``job_stages.impl = 'inproc'``) and the
  runner imports to run it as a plugin (``'plugin'``);
- :func:`registry` is a plugin's named statements (story 54.24);
- :func:`runner_group` is the runner group a stage runs in (story 54.23 §4).

Every manifest is checked with the validator's trusted mode, which only these built-in
manifests ever get (story 54.22).
"""

from __future__ import annotations

import hashlib
import importlib.resources
import json
from dataclasses import dataclass, field
from functools import cache
from typing import Any

from maktaba_plugin_sdk.statements import Registry, Statement

from ..engine.graph import Edge, StageDef
from . import manifest as pm

__all__ = [
    "FirstParty",
    "bundle_digest",
    "by_id",
    "by_stage",
    "impl_entry",
    "plugins",
    "registry",
    "runner_group",
    "stage_def",
    "stage_names",
]


@dataclass(frozen=True)
class FirstParty:
    id: str
    folder: str
    version: str
    digest: str
    manifest: dict[str, Any]
    strings: dict[str, dict[str, str]]
    statements: tuple[Statement, ...] = ()
    stages: dict[str, dict[str, Any]] = field(default_factory=dict)

    @property
    def trusted(self) -> dict[str, Any]:
        return dict(self.manifest.get("trusted") or {})

    @property
    def component(self) -> str:
        return str(self.trusted.get("component") or "")

    @property
    def runner(self) -> str:
        return str(self.trusted.get("runner") or "light")


def _raw() -> bytes:
    return (
        importlib.resources.files("maktaba_pipeline.plugins")
        .joinpath("data", "firstparty.json")
        .read_bytes()
    )


@cache
def _load() -> tuple[tuple[FirstParty, ...], str]:
    doc = json.loads(_raw())
    out: list[FirstParty] = []
    for p in doc.get("plugins") or []:
        strings = p.get("strings") or {}

        def files(path: str, strings: dict[str, Any] = strings) -> bytes | None:
            lang = path.removeprefix("i18n/").removesuffix(".json")
            if not path.startswith("i18n/") or lang not in strings:
                return None
            return json.dumps(strings[lang]).encode()

        m = pm.validate(str(p["manifest"]).encode(), files, trusted=True)
        if m["id"] != p["id"]:
            raise ValueError(f"firstparty.json: {p['folder']} is {m['id']}, not {p['id']}")
        sts = tuple(Statement.from_json(s, m["id"]) for s in p.get("statements") or [])
        out.append(
            FirstParty(
                id=m["id"],
                folder=str(p["folder"]),
                version=str(m["version"]),
                digest=str(p.get("digest") or ""),
                manifest=m,
                strings=strings,
                statements=sts,
                stages={s["name"]: s for s in m["stage"]},
            )
        )
    return tuple(out), str(doc.get("digest") or "")


def plugins() -> tuple[FirstParty, ...]:
    return _load()[0]


def bundle_digest() -> str:
    """The bundle's own digest, and the sha256 of its bytes as installed."""
    return _load()[1]


def bundle_sha256() -> str:
    return hashlib.sha256(_raw()).hexdigest()


def by_id(plugin_id: str) -> FirstParty | None:
    return next((p for p in plugins() if p.id == plugin_id), None)


@cache
def _stage_index() -> dict[str, FirstParty]:
    return {name: p for p in plugins() for name in p.stages}


def by_stage(name: str) -> FirstParty | None:
    return _stage_index().get(name)


def stage_names() -> frozenset[str]:
    return frozenset(_stage_index())


@cache
def registry(plugin_id: str) -> Registry:
    p = by_id(plugin_id)
    if p is None:
        raise KeyError(plugin_id)
    return Registry.of(plugin_id, p.statements)


def impl_entry(name: str) -> str | None:
    """``module:attribute`` of a first-party stage's :class:`StageImpl`."""
    p = by_stage(name)
    if p is None:
        return None
    return str(p.stages[name].get("entry") or "") or None


def runner_group(name: str) -> str | None:
    """The runner group a first-party stage runs in: the stage's own ``runner``, else its
    plugin's ``[trusted] runner`` (``own`` is a group named after the plugin)."""
    p = by_stage(name)
    if p is None:
        return None
    g = str(p.stages[name].get("runner") or p.runner)
    return p.folder if g == "own" else g


def stage_def(name: str) -> StageDef:
    """A first-party stage's definition from its manifest (story 54.25 §1)."""
    p = by_stage(name)
    if p is None:
        raise KeyError(f"no first-party plugin defines the stage {name!r}")
    s = p.stages[name]
    kinds = tuple(s.get("subject_kinds") or [s["subject"]])
    rule = s.get("every_minutes_rule")
    return StageDef(
        name=name,
        label=str(s.get("label") or name),
        subject_kinds=kinds,  # type: ignore[arg-type]
        pool=str(s.get("pool") or "plugins"),
        lane=s.get("lane") or "media",
        lane_order=int(s.get("lane_order", 0)),
        version=int(s["version"]),
        edges=tuple(
            Edge(
                to=e["to"],
                map=e["map"],
                when=e.get("when", "output_changed"),
                legacy=bool(e.get("legacy", False)),
            )
            for e in s.get("edges") or []
        ),
        may_request=tuple(s.get("may_request") or ()),
        entry_for=tuple(s.get("entry_for") or ()),  # type: ignore[arg-type]
        debounce_sec=int(s.get("debounce_sec", 0)),
        debounce_max_sec=int(s.get("debounce_max_sec", 0)),
        external_write=bool(s.get("external_write", False)),
        legacy=bool(s.get("legacy", False)),
        hidden=bool(s.get("hidden", False)),
        max_attempts=int(s.get("max_attempts", 3)),
        placement=s.get("placement") or "any",
        storage=s.get("storage"),
        model_rules=s.get("model_rules"),
        relay=s.get("relay"),
        after_topup=bool(s.get("after_topup", False)),
        every_minutes_rule=(str(rule[0]), str(rule[1])) if rule else None,
        unreleased=bool(s.get("unreleased", False)),
    )
