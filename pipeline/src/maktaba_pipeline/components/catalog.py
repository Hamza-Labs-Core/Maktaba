"""The component catalog (Epic 51, story 51.01), as the pipeline reads it.

``catalog.toml`` beside this module is a byte-for-byte copy of
``shared/components/catalog.toml`` (``make catalog-sync`` writes it; the ``catalog`` CI job
fails when the two differ). This loader applies the same rules as the Go one
(``shared/components/catalog.go``) and answers the same dump
(``shared/components/testdata/catalog.dump.json``)::

    cat = default()
    cat.by_stage("transcribe").id         # "transcription"
    cat.by_process("pipeline", "stt-2")   # transcription
    cat.by_loop("pipeline", "channel-topup")
    cat.order()                           # requirements first

Nothing here changes at runtime: desired and actual state are database rows
(:mod:`maktaba_pipeline.components.store`).
"""

from __future__ import annotations

import functools
import json
import re
import tomllib
from dataclasses import dataclass, field
from importlib import resources
from typing import Any

__all__ = [
    "BUNDLES",
    "DEFAULT_DRAIN_GRACE_S",
    "DEFAULT_STOP_GRACE_S",
    "GROUPS",
    "KNOWN_PHASES",
    "LOOP_HOSTS",
    "PLATFORMS",
    "Catalog",
    "CatalogError",
    "Component",
    "Drain",
    "Resources",
    "default",
    "load",
    "raw",
]

KNOWN_PHASES: tuple[str, ...] = (
    "not_installed",
    "installing",
    "removing",
    "off",
    "stopped",
    "waiting",
    "starting",
    "running",
    "degraded",
    "draining",
    "failed",
)
PLATFORMS = ("darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64")
GROUPS = (
    "core",
    "media",
    "library",
    "processing",
    "livetv",
    "integrations",
    "cluster",
    "system",
    "runtimes",
    "models",
    "tools",
    "languages",
    "plugins",
)
BUNDLES = ("full", "core", "worker")
#: Epic 54 (story 54.37): an installed plugin's run-time entry, never in catalog.toml
KIND_PLUGIN = "plugin"
PLUGIN_PREFIX = "plugin."
PLUGINS_HOST = "plugins.host"
LOOP_HOSTS = ("supervisor", "api", "streaming", "pipeline")
DEFAULT_DRAIN_GRACE_S = 30
DEFAULT_STOP_GRACE_S = 5

_ID = re.compile(r"[a-z][a-z0-9_]*(\.[a-z0-9_*-]+)*")
_STAGE = re.compile(r"[a-z][a-z0-9_]*")
_PROCESS = re.compile(r"[a-z][a-z0-9-]*/(\*|[a-z0-9-]*\*?)")
_LOOP = re.compile(r"(supervisor|api|streaming|pipeline):[a-z][a-z0-9_-]*")
_SETTING = re.compile(r"(rules\.[a-z][a-z0-9_]*|toml:[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*)")
_SINCE = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")
_LICENCE = re.compile(r"[a-z][a-z0-9_]*")
_SHA256 = re.compile(r"[0-9a-f]{64}")
#: a model's source is pinned (story 51.15): a Hugging Face repo at a commit, never a
#: branch; an ollama model at its manifest digest
_HF_SOURCE = re.compile(r"hf:[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*@[0-9a-f]{40}")
_OLLAMA_SOURCE = re.compile(r"ollama:[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}")

_TOP_KEYS = ("schema", "phases", "reasons", "features", "component")
_COMPONENT_KEYS = (
    "id",
    "kind",
    "switchable",
    "group",
    "title_key",
    "desc_key",
    "stages",
    "processes",
    "loops",
    "requires",
    "wants",
    "conflicts",
    "settings",
    "platforms",
    "placement",
    "resources",
    "drain",
    "stop_grace_s",
    "licence",
    "features",
    "bundles",
    "since",
    "source",
    "sources",
    "files",
    "in_payload",
    "gated",
    "on_demand",
)
_PARTS = ("stages", "process", "loop")
_PLURAL = {"stages": "stages", "process": "processes", "loop": "loops"}


class CatalogError(ValueError):
    """The catalog file breaks a rule; every problem found is a line of the message."""


@dataclass(frozen=True, slots=True)
class Drain:
    grace_s: int = DEFAULT_DRAIN_GRACE_S
    checkpoint: bool = False


@dataclass(frozen=True, slots=True)
class Resources:
    ram_mb: int = 0
    disk_mb: int = 0
    gpu: str = ""


@dataclass(frozen=True, slots=True)
class Component:
    id: str
    kind: str
    switchable: bool
    group: str
    title_key: str
    desc_key: str
    stages: tuple[str, ...]
    processes: tuple[str, ...]
    loops: tuple[str, ...]
    requires: tuple[str, ...]
    wants: tuple[str, ...]
    conflicts: tuple[str, ...]
    settings: tuple[str, ...]
    platforms: tuple[str, ...]
    placement: str
    resources: Resources
    drain: Drain
    stop_grace_s: int
    licence: str
    features: tuple[str, ...]
    bundles: tuple[str, ...]
    since: str
    source: str
    sources: dict[str, str] = field(default_factory=dict)
    files: tuple[dict[str, Any], ...] = ()
    in_payload: bool = False
    #: a model whose download needs the owner's Hugging Face login
    gated: bool = False
    #: a process that runs only while a component it serves is on (Epic 54: plugins.host)
    on_demand: bool = False

    def has_part(self, part: str) -> bool:
        return part in self.kind.split("+")

    def source_for(self, platform: str) -> str:
        return self.sources.get(platform, self.source)

    def files_of(self, source: str) -> list[dict[str, Any]]:
        """The asset's files that come from ``source`` (a file without its own
        ``source`` is the default source's)."""
        return [
            f
            for f in self.files
            if f.get("source", "") == source or (not f.get("source") and source == self.source)
        ]


def _split_requirement(r: str) -> list[str]:
    if r.startswith("any:"):
        return [x.strip() for x in r[4:].split("|") if x.strip()]
    return [r]


def _valid_kind(kind: str) -> bool:
    if kind in ("asset", "extension"):
        return True
    at = -1
    for p in kind.split("+"):
        if p not in _PARTS or _PARTS.index(p) <= at:
            return False
        at = _PARTS.index(p)
    return True


def _match_process(pattern: str, role: str, instance: str) -> int:
    """-1 no match, 0 ``*``, 1 a prefix, 2 exactly (as the Go loader)."""
    prole, _, pinst = pattern.partition("/")
    if prole != role:
        return -1
    if pinst == instance:
        return 2
    if pinst == "*":
        return 0
    if pinst.endswith("*") and instance and instance.startswith(pinst[:-1]):
        return 1
    return -1


def _strs(v: Any, where: str, name: str, bad: list[str]) -> tuple[str, ...]:
    if v is None:
        return ()
    if not isinstance(v, list) or not all(isinstance(x, str) for x in v):
        bad.append(f"{where}: {name} must be a list of strings")
        return ()
    return tuple(v)


class Catalog:
    """A loaded, validated catalog. Read-only."""

    def __init__(
        self,
        schema: int,
        phases: tuple[str, ...],
        reasons: tuple[str, ...],
        features: tuple[str, ...],
        components: list[Component],
    ) -> None:
        self.schema = schema
        self.phases = phases
        self.reasons = reasons
        self.features = features
        self._components = components
        self._by_id = {c.id: c for c in components}
        self._by_stage = {s: c for c in components for s in c.stages}
        self._by_loop = {lp: c for c in components for lp in c.loops}
        self._order = self._topo_order()

    def _topo_order(self) -> list[str]:
        state: dict[str, int] = {}
        out: list[str] = []

        def visit(cid: str, path: list[str]) -> None:
            if state.get(cid) == 2:
                return
            if state.get(cid) == 1:
                cyc = [*path[path.index(cid) :], cid]
                raise CatalogError("requires cycle: " + " -> ".join(cyc))
            state[cid] = 1
            for r in self._by_id[cid].requires:
                for dep in _split_requirement(r):
                    visit(dep, [*path, cid])
            state[cid] = 2
            out.append(cid)

        for c in self._components:
            visit(c.id, [])
        return out

    def components(self) -> list[Component]:
        return list(self._components)

    def ids(self) -> list[str]:
        return [c.id for c in self._components]

    def get(self, cid: str) -> Component | None:
        return self._by_id.get(cid)

    def by_stage(self, stage: str) -> Component | None:
        return self._by_stage.get(stage)

    def by_loop(self, host: str, name: str) -> Component | None:
        return self._by_loop.get(f"{host}:{name}")

    def loops_of(self, host: str) -> dict[str, Component]:
        out: dict[str, Component] = {}
        for key, c in self._by_loop.items():
            h, _, name = key.partition(":")
            if h == host:
                out[name] = c
        return out

    def by_process(self, role: str, instance: str = "") -> Component | None:
        best: Component | None = None
        rank = -1
        for c in self._components:
            for p in c.processes:
                r = _match_process(p, role, instance)
                if r > rank:
                    best, rank = c, r
        return best

    def match_processes(self, role: str, instance: str = "") -> list[Component]:
        return [
            c
            for c in self._components
            if any(_match_process(p, role, instance) >= 0 for p in c.processes)
        ]

    def requires(self, cid: str) -> list[tuple[str, ...]]:
        """Each requirement as a group of alternatives (``any:`` keeps its ids together)."""
        c = self._by_id.get(cid)
        return [] if c is None else [tuple(_split_requirement(r)) for r in c.requires]

    def dependents(self, cid: str) -> list[str]:
        return [
            c.id for c in self._components if any(cid in _split_requirement(r) for r in c.requires)
        ]

    def order(self) -> list[str]:
        """Every id, requirements first: start in this order, stop in reverse."""
        return list(self._order)

    def is_reason(self, code: str) -> bool:
        return code in self.reasons

    def is_phase(self, phase: str) -> bool:
        return phase in self.phases

    def in_bundle(self, bundle: str) -> list[str]:
        return [cid for cid in self._order if bundle in self._by_id[cid].bundles]

    def dump(self) -> str:
        """Canonical JSON, the same text the Go loader's ``Dump`` writes."""
        comps = [
            {
                "id": c.id,
                "kind": c.kind,
                "switchable": c.switchable,
                "group": c.group,
                "title_key": c.title_key,
                "desc_key": c.desc_key,
                "stages": list(c.stages),
                "processes": list(c.processes),
                "loops": list(c.loops),
                "requires": [list(r) for r in self.requires(c.id)],
                "wants": list(c.wants),
                "conflicts": list(c.conflicts),
                "settings": list(c.settings),
                "platforms": list(c.platforms),
                "placement": c.placement,
                "bundles": list(c.bundles),
                "features": list(c.features),
                "licence": c.licence,
                "since": c.since,
                "stop_grace_s": c.stop_grace_s,
                "drain": {"grace_s": c.drain.grace_s, "checkpoint": c.drain.checkpoint},
                "resources": {
                    "ram_mb": c.resources.ram_mb,
                    "disk_mb": c.resources.disk_mb,
                    "gpu": c.resources.gpu,
                },
                "source": c.source,
                "sources": dict(c.sources),
                "in_payload": c.in_payload,
                "gated": c.gated,
                "on_demand": c.on_demand,
            }
            for c in self._components
        ]
        doc = {
            "schema": self.schema,
            "phases": list(self.phases),
            "reasons": list(self.reasons),
            "features": list(self.features),
            "components": comps,
            "order": self._order,
        }
        return json.dumps(doc, indent=2, sort_keys=True, ensure_ascii=False) + "\n"


def _component(m: dict[str, Any], where: str, bad: list[str]) -> Component:
    def s(name: str) -> str:
        v = m.get(name, "")
        if not isinstance(v, str):
            bad.append(f"{where}: {name} must be a string")
            return ""
        return v

    def b(name: str, default: bool) -> bool:
        v = m.get(name, default)
        if not isinstance(v, bool):
            bad.append(f"{where}: {name} must be true or false")
            return default
        return v

    kind = s("kind")
    drain_raw = m.get("drain") or {}
    res_raw = m.get("resources") or {}
    for tbl, name, keys in (
        (drain_raw, "drain", ("grace_s", "checkpoint")),
        (res_raw, "resources", ("ram_mb", "disk_mb", "gpu")),
    ):
        for k in tbl:
            if k not in keys:
                bad.append(f"{where}: unknown field {name}.{k}")
    stop = m.get("stop_grace_s")
    if stop is None:
        stop = DEFAULT_STOP_GRACE_S if "loop" in kind.split("+") else 0
    sources = m.get("sources") or {}
    files = m.get("files") or []
    return Component(
        id=s("id"),
        kind=kind,
        switchable=b("switchable", True),
        group=s("group"),
        title_key=s("title_key"),
        desc_key=s("desc_key"),
        stages=_strs(m.get("stages"), where, "stages", bad),
        processes=_strs(m.get("processes"), where, "processes", bad),
        loops=_strs(m.get("loops"), where, "loops", bad),
        requires=_strs(m.get("requires"), where, "requires", bad),
        wants=_strs(m.get("wants"), where, "wants", bad),
        conflicts=_strs(m.get("conflicts"), where, "conflicts", bad),
        settings=_strs(m.get("settings"), where, "settings", bad),
        platforms=_strs(m.get("platforms"), where, "platforms", bad),
        placement=s("placement") or "any",
        resources=Resources(
            ram_mb=int(res_raw.get("ram_mb", 0)),
            disk_mb=int(res_raw.get("disk_mb", 0)),
            gpu=str(res_raw.get("gpu", "")),
        ),
        drain=Drain(
            grace_s=int(drain_raw.get("grace_s", DEFAULT_DRAIN_GRACE_S)),
            checkpoint=bool(drain_raw.get("checkpoint", False)),
        ),
        stop_grace_s=int(stop),
        licence=s("licence"),
        features=_strs(m.get("features"), where, "features", bad),
        bundles=_strs(m.get("bundles"), where, "bundles", bad),
        since=s("since"),
        source=s("source"),
        sources={str(k): str(v) for k, v in sources.items()},
        files=tuple(dict(f) for f in files),
        in_payload=b("in_payload", False),
        gated=b("gated", False),
        on_demand=b("on_demand", False),
    )


def _is_model_source(s: str) -> bool:
    return s.startswith(("hf:", "ollama:"))


def _validate_sources(c: Component, where: str, bad: list[str]) -> None:
    """Every hf: and ollama: source is pinned and has its files; every file is from one
    of the asset's model sources, once per path (the Go loader's validateSources)."""
    used = {s for s in (c.source, *c.sources.values()) if s}
    for s in sorted(used):
        if s.startswith("hf:") and not _HF_SOURCE.fullmatch(s):
            bad.append(
                f"{where}: source {s!r} must be pinned as hf:<owner>/<repo>@<40-hex commit>,"
                " never a branch"
            )
        elif s.startswith("ollama:") and not _OLLAMA_SOURCE.fullmatch(s):
            bad.append(
                f"{where}: source {s!r} must be pinned as"
                " ollama:<name>:<tag>@sha256:<manifest digest>"
            )
        elif _is_model_source(s) and not c.files_of(s):
            bad.append(
                f"{where}: source {s!r} has no files (tools/release/cmd/pin-models fills them)"
            )
    seen: set[tuple[str, str]] = set()
    for f in c.files:
        src = str(f.get("source") or c.source)
        path = str(f.get("path", ""))
        if src not in used or not _is_model_source(src):
            bad.append(f"{where}: file {path!r} is from {src!r}, not one of its model sources")
        if (src, path) in seen:
            bad.append(f"{where}: file {path!r} listed twice for {src!r}")
        seen.add((src, path))


def _validate(c: Component, where: str, features: tuple[str, ...], bad: list[str]) -> None:
    # a plugin's entry exists only at run time (Epic 54, story 54.37 §1)
    if c.kind == KIND_PLUGIN or c.id.startswith(PLUGIN_PREFIX):
        bad.append(
            f"{where}: kind plugin and ids starting {PLUGIN_PREFIX!r} are run-time plugin entries"
        )
    if c.on_demand and not c.has_part("process"):
        bad.append(f"{where}: on_demand is for a component with a process")
    core = c.id.startswith("core.")
    if core and c.switchable:
        bad.append(f"{where}: core.* components must have switchable = false")
    if not core and not c.switchable:
        bad.append(f"{where}: only core.* components may have switchable = false")
    if not _valid_kind(c.kind):
        bad.append(f"{where}: bad kind {c.kind!r}")
    parts = {"stages": c.stages, "process": c.processes, "loop": c.loops}
    if c.kind == "asset":
        for part, lst in parts.items():
            if lst:
                bad.append(f"{where}: an asset has no {_PLURAL[part]}")
    elif c.kind == "extension":
        if c.processes:
            bad.append(f"{where}: an extension has no processes")
    elif _valid_kind(c.kind):
        for part in _PARTS:
            has = c.has_part(part)
            if has and not parts[part]:
                bad.append(f"{where}: kind {c.kind!r} but no {_PLURAL[part]}")
            if not has and parts[part]:
                bad.append(f"{where}: lists {_PLURAL[part]} but kind {c.kind!r} has no {part}")
    if c.kind != "asset" and (c.source or c.sources or c.files or c.in_payload or c.gated):
        bad.append(f"{where}: source, sources, files, in_payload and gated are for assets only")
    if c.group not in GROUPS:
        bad.append(f"{where}: bad group {c.group!r}")
    if c.title_key != f"components.{c.id}.title":
        bad.append(f"{where}: title_key must be 'components.{c.id}.title'")
    if c.desc_key != f"components.{c.id}.desc":
        bad.append(f"{where}: desc_key must be 'components.{c.id}.desc'")

    def each(name: str, lst: tuple[str, ...], ok: Any) -> None:
        seen: set[str] = set()
        for v in lst:
            if v in seen:
                bad.append(f"{where}: {name} lists {v!r} twice")
            seen.add(v)
            if not ok(v):
                bad.append(f"{where}: bad {name} entry {v!r}")

    def req_ok(v: str) -> bool:
        ids = _split_requirement(v)
        return bool(ids) and all(_ID.fullmatch(i) for i in ids)

    each("stages", c.stages, _STAGE.fullmatch)
    each("processes", c.processes, _PROCESS.fullmatch)
    each("loops", c.loops, _LOOP.fullmatch)
    each("requires", c.requires, req_ok)
    each("wants", c.wants, _ID.fullmatch)
    each("conflicts", c.conflicts, _ID.fullmatch)
    each("settings", c.settings, _SETTING.fullmatch)
    each("platforms", c.platforms, lambda v: v in PLATFORMS)
    each("bundles", c.bundles, lambda v: v in BUNDLES)
    each("features", c.features, lambda v: v in features)
    if not c.platforms:
        bad.append(f"{where}: platforms is empty")
    for p in c.sources:
        if p not in PLATFORMS:
            bad.append(f"{where}: sources has unknown platform {p!r}")
    for f in c.files:
        if (
            not f.get("path")
            or not _SHA256.fullmatch(str(f.get("sha256", "")))
            or int(f.get("size", 0)) <= 0
        ):
            bad.append(f"{where}: a file needs path, sha256 (64 hex) and size")
    _validate_sources(c, where, bad)
    if c.placement not in ("any", "home"):
        bad.append(f"{where}: bad placement {c.placement!r}")
    if c.resources.gpu not in ("", "none", "preferred", "required"):
        bad.append(f"{where}: bad resources.gpu {c.resources.gpu!r}")
    if c.resources.ram_mb < 0 or c.resources.disk_mb < 0:
        bad.append(f"{where}: resources must not be negative")
    if c.drain.grace_s <= 0:
        bad.append(f"{where}: drain.grace_s must be positive")
    if c.stop_grace_s < 0 or (c.stop_grace_s > 0 and not c.has_part("loop")):
        bad.append(f"{where}: stop_grace_s is for loops only")
    if c.licence and not _LICENCE.fullmatch(c.licence):
        bad.append(f"{where}: bad licence {c.licence!r}")
    if not _SINCE.fullmatch(c.since):
        bad.append(f"{where}: since must be a release number like 0.3.66, got {c.since!r}")


def load(data: bytes | str) -> Catalog:
    """Parse and validate a catalog; :class:`CatalogError` lists every problem."""
    text = data.decode("utf-8") if isinstance(data, bytes) else data
    try:
        doc = tomllib.loads(text)
    except tomllib.TOMLDecodeError as exc:
        raise CatalogError(f"catalog: {exc}") from exc
    bad: list[str] = []
    for k in sorted(doc):
        if k not in _TOP_KEYS:
            bad.append(f"unknown top-level field {k!r}")
    raw_comps = doc.get("component", [])
    for i, m in enumerate(raw_comps):
        for k in sorted(m):
            if k not in _COMPONENT_KEYS:
                bad.append(f"component {i} ({m.get('id', '')}): unknown field {k!r}")
    if bad:
        raise CatalogError("catalog: " + "\ncatalog: ".join(bad))

    schema = doc.get("schema", 0)
    phases = _strs(doc.get("phases"), "catalog", "phases", bad)
    reasons = _strs(doc.get("reasons"), "catalog", "reasons", bad)
    features = _strs(doc.get("features"), "catalog", "features", bad)
    if schema != 1:
        bad.append(f"schema = {schema}: this code reads schema 1")
    if phases != KNOWN_PHASES:
        bad.append(f"phases must be exactly {list(KNOWN_PHASES)} (the code ranks them)")
    for name, lst in (("reasons", reasons), ("features", features)):
        if not lst:
            bad.append(f"{name} is empty")
        if len(set(lst)) != len(lst):
            bad.append(f"{name} lists a value twice")
        for v in lst:
            if not _STAGE.fullmatch(v):
                bad.append(f"{name}: bad value {v!r}")

    comps: list[Component] = []
    by_id: dict[str, Component] = {}
    stages: dict[str, str] = {}
    procs: dict[str, str] = {}
    loops: dict[str, str] = {}
    for i, m in enumerate(raw_comps):
        c = _component(m, f"component {m.get('id', i)}", bad)
        where = f"component {c.id}"
        if not _ID.fullmatch(c.id):
            bad.append(f"component {i}: bad id {c.id!r}")
            where = f"component {i}"
        if c.id in by_id:
            bad.append(f"component id {c.id!r} is listed twice")
            continue
        by_id[c.id] = c
        comps.append(c)
        _validate(c, where, features, bad)
        for table, values, what in ((stages, c.stages, "stage"), (procs, c.processes, "process"),
                                    (loops, c.loops, "loop")):  # fmt: skip
            for v in values:
                if v in table and table[v] != c.id:
                    bad.append(f"{what} {v!r} is in both {table[v]} and {c.id}")
                    continue
                table[v] = c.id
    for c in comps:
        for r in c.requires:
            for dep in _split_requirement(r):
                if dep not in by_id:
                    bad.append(f"component {c.id}: requires unknown component {dep!r}")
                elif dep == c.id:
                    bad.append(f"component {c.id}: requires itself")
        for name, ids in (("wants", c.wants), ("conflicts", c.conflicts)):
            for oid in ids:
                other = by_id.get(oid)
                if other is None:
                    bad.append(f"component {c.id}: {name} unknown component {oid!r}")
                elif oid == c.id:
                    bad.append(f"component {c.id}: {name} itself")
                elif name == "conflicts" and c.id not in other.conflicts:
                    bad.append(
                        f"component {c.id} conflicts with {oid} but {oid} does not list "
                        f"{c.id}: conflicts must be listed on both sides"
                    )
    if bad:
        raise CatalogError("catalog: " + "\ncatalog: ".join(bad))
    return Catalog(int(schema), phases, reasons, features, comps)


def raw() -> bytes:
    """The packaged catalog file, as it is."""
    return resources.files(__package__).joinpath("catalog.toml").read_bytes()


@functools.cache
def default() -> Catalog:
    """The packaged catalog, parsed once."""
    return load(raw())


def plugin_queue_name(plugin_id: str, stage: str) -> str:
    """A plugin stage's queue name (54.01 §4), as ``components.PluginQueueName``."""
    import hashlib

    return "p_" + hashlib.sha256(plugin_id.encode()).hexdigest()[:8] + "_" + stage


_COMPUTE_NEEDS = {"compute.llm": "llm", "compute.embed": "search.semantic"}


def plugin_entry(
    plugin_id: str,
    stages: tuple[str, ...],
    platforms: tuple[str, ...] = ("any",),
    timeout_s: int = 300,
    since: str = "",
    required: tuple[str, ...] = (),
    optional: tuple[str, ...] = (),
) -> Component:
    """An installed plugin's catalog entry, derived from its manifest (story 54.37 §1),
    the same fields as the Go ``components.PluginEntry``."""
    plats = PLATFORMS if not platforms or "any" in platforms else tuple(platforms)
    grace = timeout_s if 0 < timeout_s <= 120 else 120
    requires = (PLUGINS_HOST, *(_COMPUTE_NEEDS[c] for c in required if c in _COMPUTE_NEEDS))
    wants = tuple(_COMPUTE_NEEDS[c] for c in optional if c in _COMPUTE_NEEDS)
    return Component(
        id=PLUGIN_PREFIX + plugin_id, kind=KIND_PLUGIN, switchable=True, group="plugins",
        title_key=f"plugin.{plugin_id}.name", desc_key=f"plugin.{plugin_id}.summary",
        stages=tuple(stages), processes=(), loops=(), requires=requires, wants=wants,
        conflicts=(), settings=(f"plugins.{plugin_id}",), platforms=plats, placement="home",
        resources=Resources(0, 0, ""), drain=Drain(grace, True), stop_grace_s=0, licence="",
        features=(), bundles=(), since=since, source="",
    )  # fmt: skip


def plugin_entry_of(manifest: dict[str, Any], since: str = "") -> Component:
    """The entry of a normalised manifest (``plugin_installs.manifest``), as the Go
    ``components.PluginSpecOf`` + ``PluginEntry``."""
    pid = str(manifest["id"])
    caps = manifest.get("capabilities") or {}
    return plugin_entry(
        pid,
        tuple(plugin_queue_name(pid, str(st["name"])) for st in manifest.get("stage") or []),
        tuple(manifest.get("platforms") or ("any",)),
        int((manifest.get("resources") or {}).get("timeout_s") or 0),
        since,
        tuple(caps.get("required") or ()),
        tuple(caps.get("optional") or ()),
    )


def with_entries(static: Catalog, entries: list[Component]) -> Catalog:
    """A catalog over ``static`` and the plugin entries (Go ``components.With``)."""
    by_stage = {s: c.id for c in static.components() for s in c.stages}
    ids = {c.id for c in static.components()}
    bad: list[str] = []
    for e in entries:
        if e.kind != KIND_PLUGIN or not e.id.startswith(PLUGIN_PREFIX):
            bad.append(f"{e.id}: not a plugin entry")
        elif e.id in ids:
            bad.append(f"{e.id}: listed twice")
        for r in e.requires:
            if r not in ids:
                bad.append(f"{e.id}: requires unknown component {r!r}")
        for s in e.stages:
            if s in by_stage:
                bad.append(f"{e.id}: stage {s} is {by_stage[s]}'s")
            by_stage[s] = e.id
        ids.add(e.id)
    if bad:
        raise CatalogError("catalog: " + "\ncatalog: ".join(bad))
    return Catalog(
        static.schema,
        static.phases,
        static.reasons,
        static.features,
        [*static.components(), *entries],
    )


def with_plugins(static: Catalog, entries: list[Component]) -> tuple[Catalog, list[str]]:
    """``with_entries`` leaving out an entry it would refuse (Go ``WithPlugins``): the
    merged catalog and the entries left out, with why."""
    kept: list[Component] = []
    skipped: list[str] = []
    for e in entries:
        try:
            with_entries(static, [*kept, e])
        except CatalogError as exc:
            skipped.append(f"{e.id}: {exc}")
            continue
        kept.append(e)
    return with_entries(static, kept), skipped
