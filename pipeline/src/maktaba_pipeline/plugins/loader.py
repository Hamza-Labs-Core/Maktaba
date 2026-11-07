"""The plugin loader (Epic 54, story 54.06 §1): installed plugins as queue extensions.

Each row of ``plugin_installs`` becomes a :class:`PluginExtension` that satisfies the
built-in extension contract (``extensions/base.py``), so the graph build path is the one
the Plex and dizqueTV extensions use. The loader reads manifests only: it never imports,
loads or runs plugin code (that runs in maktaba-plugind).

For each ``[[stage]]``: a ``StageDef`` named ``p_<hash>_<name>`` with
``extension = "plugin:<id>"``, pool ``plugins``, placement ``home``, two attempts; for each
trigger an edge from the event's core stage (``shared/plugins/events.toml``) or, for a
``stage`` trigger, from the plugin's own stage (54.39); for each schedule an
``every_minutes_rule`` on the plugin's own settings key. A trigger on another plugin's
published event is not an edge: events are delivered as requests (54.40).

The leaf rule (README §4.12, H24): a plugin stage is wired only after core events and its
own stages, and reaches only its own stages. :func:`check_leaves` refuses a manifest that
would break it (a plugin is then left out, never half-wired) and
:func:`assert_plugin_leaves` checks the whole graph again after the build.
"""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

from ..engine.graph import GraphViolation
from ..extensions.base import Manifest
from ..log import get_logger
from . import manifest as pm

if TYPE_CHECKING:
    from ..engine.graph import GraphBuilder, StageDef
    from ..engine.stages.registry import StageImpl

__all__ = [
    "MIGRATE",
    "PREFIX",
    "PluginExtension",
    "assert_plugin_leaves",
    "load_installed",
    "plugin_of",
]

#: a plugin's migrate stage name: its queue is ``p_<hash>__migrate`` (story 54.07 §5 step
#: 4), and its guest's ``run`` gets ``__migrate``; a manifest's stage names start with a
#: letter, so neither can collide
MIGRATE = "_migrate"
#: what the guest's ``run`` is called with for its migrate job
MIGRATE_STAGE_ARG = "__migrate"

#: a plugin stage's ``StageDef.extension`` is ``plugin:<id>``
PREFIX = "plugin:"

_log = get_logger()


def plugin_of(extension: str | None) -> str | None:
    """The plugin id of a stage's extension name, or None for core and built-ins."""
    if extension and extension.startswith(PREFIX):
        return extension[len(PREFIX) :]
    return None


@dataclass
class PluginExtension:
    """One installed plugin, as an extension of the queue graph."""

    plugin_id: str
    version: str
    path: str
    doc: dict[str, Any]
    review_level: str = "none"
    #: the data_version the plugin's data is at while it is behind the manifest's (slot
    #: 0203; story 54.07 §5 step 4): its stages wait for its migrate job. None: up to date.
    data_version: int | None = None
    manifest: Manifest = field(init=False)

    def __post_init__(self) -> None:
        self.manifest = Manifest(
            name=PREFIX + self.plugin_id,
            version=self.version,
            description=f"plugin {self.plugin_id}",
            stages=tuple(pm.queue_name(self.plugin_id, s["name"]) for s in self.doc["stage"])
            + (self.migrate_stage,),
        )

    @property
    def migrate_stage(self) -> str:
        """The queue of its migrate job, ``p_<hash>__migrate`` (story 54.07 §5 step 4)."""
        return pm.queue_name(self.plugin_id, MIGRATE)

    @property
    def target_data_version(self) -> int:
        return max(int(self.doc.get("data_version", 1)), 1)

    @property
    def migrating(self) -> bool:
        """Whether its data is behind the manifest's data_version (its migrate job has not
        brought it up yet)."""
        return self.data_version is not None and self.data_version < self.target_data_version

    @property
    def concurrency(self) -> int:
        return int(self.doc.get("resources", {}).get("concurrency", 2))

    def stage_doc(self, queue_name: str) -> dict[str, Any] | None:
        for s in self.doc["stage"]:
            if pm.queue_name(self.plugin_id, s["name"]) == queue_name:
                return dict(s)
        return None

    def register(self, graph: GraphBuilder) -> None:
        from ..engine.graph import Edge, StageDef

        schedules = {s["stage"]: s["every_minutes_setting"] for s in self.doc.get("schedule", [])}
        events = pm.events()
        # where it runs (story 54.42 §1): the home (the default), any machine, or a machine
        # that serves the gpu pool (its stages take that pool, as core GPU stages do)
        runs_on = str(self.doc.get("placement") or "home")
        placement = "home" if runs_on == "home" else "any"
        pool = "gpu" if runs_on == "gpu" else "plugins"
        for s in self.doc["stage"]:
            name = pm.queue_name(self.plugin_id, s["name"])
            subject = "season" if s["subject"] == "series" else s["subject"]
            setting = schedules.get(s["name"])
            graph.add_stage(
                StageDef(
                    name=name,
                    label=f"{self.plugin_id}: {s['name']}",
                    subject_kinds=(subject,),  # type: ignore[arg-type]
                    pool=pool,
                    lane="sources" if subject == "catalog" else "media",
                    lane_order=900,
                    version=int(s["version"]),
                    may_request=tuple(
                        pm.queue_name(self.plugin_id, r) for r in s.get("may_request", [])
                    ),
                    entry_for=("catalog",) if subject == "catalog" else (),
                    extension=PREFIX + self.plugin_id,
                    max_attempts=2,
                    placement=placement,  # type: ignore[arg-type]
                    every_minutes_rule=(f"plugins.{self.plugin_id}", setting) if setting else None,
                )
            )
        # its migrate job (story 54.07 §5 step 4): one catalog job before the other stages
        # of a version whose data_version grew, on the home only (it records the data's
        # version in plugin_installs, which joined machines do not write)
        graph.add_stage(
            StageDef(
                name=self.migrate_stage,
                label=f"{self.plugin_id}: migrate its data",
                subject_kinds=("catalog",),
                pool="plugins",
                lane="sources",
                lane_order=900,
                version=1,
                extension=PREFIX + self.plugin_id,
                max_attempts=3,
                placement="home",
            )
        )
        for s in self.doc["stage"]:
            name = pm.queue_name(self.plugin_id, s["name"])
            for t in s.get("trigger", []):
                if "stage" in t:
                    # after another of its own stages, when its output changed (54.39)
                    graph.add_edge(pm.queue_name(self.plugin_id, t["stage"]), Edge(name, "same"))
                    continue
                if str(t["event"]).startswith(PREFIX):
                    continue  # another plugin's event: a request when it is emitted (54.40)
                ev = events[t["event"]]
                graph.add_edge(ev["stage"], Edge(name, ev["map"]))

    def check_leaves(self) -> None:
        """Raise GraphViolation when the manifest would wire a plugin stage anywhere but
        after core events and its own stages, or let it reach a stage not its own."""
        from ..engine.graph import GraphViolation

        own = {s["name"] for s in self.doc["stage"]}
        events = pm.events()
        for s in self.doc["stage"]:
            for r in s.get("may_request", []):
                if r not in own:
                    raise GraphViolation(f"plugin {self.plugin_id}: {s['name']} requests {r}")
            for t in s.get("trigger", []):
                if "stage" in t:
                    if t["stage"] not in own:
                        raise GraphViolation(
                            f"plugin {self.plugin_id}: {s['name']} after {t['stage']}"
                        )
                    continue
                ev = str(t.get("event", ""))
                if ev.startswith(PREFIX):
                    if ev[len(PREFIX) :].partition("/")[0] == self.plugin_id:
                        raise GraphViolation(f"plugin {self.plugin_id}: {s['name']} follows itself")
                    continue
                if ev not in events or events[ev].get("trigger") is False:
                    raise GraphViolation(f"plugin {self.plugin_id}: {s['name']} follows {ev}")

    def impls(self) -> Mapping[str, StageImpl]:
        from ..engine.stages.registry import StageImpl
        from .dispatch import run_plugin_stage

        out = {
            name: StageImpl(run=run_plugin_stage, input_fingerprint=self._fingerprint(name))
            for name in self.manifest.stages
            if name != self.migrate_stage
        }
        # no fingerprint: a migrate job always runs (it checks the data's version itself)
        out[self.migrate_stage] = StageImpl(run=run_plugin_stage)
        return out

    def _fingerprint(self, queue_name: str) -> Any:
        """sha256 of the plugin's digest, the stage version, the grants' generation, the
        settings and the triggering core stages' output hashes (54.06 §4): a new version,
        a grant or a setting change reruns only what they change."""
        import hashlib

        doc = self.stage_doc(queue_name) or {}
        events = pm.events()
        triggers = [
            pm.queue_name(self.plugin_id, t["stage"])
            if "stage" in t
            else events[t["event"]]["stage"]
            for t in doc.get("trigger", [])
            if "stage" in t or t["event"] in events
        ]
        plugin_id = self.plugin_id

        async def fp(db: Any, subject: Any) -> str | None:
            row = await db.fetchrow(
                "SELECT i.digest, COALESCE((SELECT generation FROM plugin_grants g"
                " WHERE g.plugin_id = i.plugin_id), 0) AS gen, COALESCE((SELECT CAST(value AS text)"
                " FROM app_settings WHERE key = $2), '') AS settings"
                " FROM plugin_installs i WHERE i.plugin_id = $1",
                plugin_id,
                f"plugins.{plugin_id}",
            )
            if row is None:
                return None
            parts = [
                str(row["digest"]),
                str(doc.get("version", 1)),
                str(row["gen"]),
                str(row["settings"]),
            ]
            for stage in triggers:
                out = await db.fetchrow(
                    "SELECT output_hash FROM processing_jobs WHERE stage = $1 AND subject_id = $2"
                    " AND state = 'done' ORDER BY id DESC LIMIT 1",
                    stage,
                    subject.id,
                )
                parts.append("" if out is None else str(out["output_hash"] or ""))
            return hashlib.sha256("\x1f".join(parts).encode()).hexdigest()

        return fp

    def is_configured(self, settings: Mapping[str, str]) -> bool:  # noqa: ARG002 - 54.11
        return True


async def load_installed(db: Any) -> list[PluginExtension]:
    """Every installed plugin whose manifest still validates here (a manifest this release
    cannot read is left out with a warning; its stages stay ``not_configured``)."""
    try:
        rows = await db.fetch(
            "SELECT plugin_id, version, path, manifest, review_level, data_version"
            " FROM plugin_installs ORDER BY plugin_id"
        )
    except Exception:  # noqa: BLE001 - an older schema (no table yet): no plugins
        return []
    out: list[PluginExtension] = []
    for r in rows:
        raw = r["manifest"]
        doc = raw if isinstance(raw, dict) else json.loads(raw)
        try:
            pm.validate(_toml_of(doc))
        except (pm.ManifestInvalid, ValueError) as exc:
            _log.warning("plugin_manifest_unreadable", plugin=r["plugin_id"], error=str(exc))
            continue
        try:
            dv = r["data_version"]
        except KeyError:  # a fake row with no such column: up to date
            dv = None
        ext = PluginExtension(
            str(r["plugin_id"]),
            str(r["version"]),
            str(r["path"]),
            doc,
            str(r["review_level"]),
            None if dv is None else int(dv),
        )
        try:
            ext.check_leaves()
        except GraphViolation as exc:
            _log.warning("plugin_graph_violation", plugin=r["plugin_id"], error=str(exc))
            continue
        out.append(ext)
    return out


def assert_plugin_leaves(graph: Sequence[StageDef]) -> None:
    """The leaf rule over a built graph (README §4.12, H24): an edge from a plugin stage
    goes only to the same plugin's stages, a plugin stage requests only its own, and an
    edge into a plugin stage comes only from a core event's stage or the same plugin."""
    from ..engine.graph import GraphViolation

    by = {s.name: s for s in graph}
    event_stages = {e["stage"] for e in pm.events().values() if e.get("stage")}

    def leaf(stage: StageDef | None) -> str | None:
        # a third-party plugin's id; Maktaba's own plugins (tv.maktaba.*, §15) are core
        pid = plugin_of(getattr(stage, "extension", None))
        first_party = pid is not None and pid.startswith("tv.maktaba.")
        if pid is None or (first_party and not pid.startswith("tv.maktaba.community.")):
            return None
        return pid

    for s in graph:
        mine = leaf(s)
        for e in s.edges:
            theirs = leaf(by.get(e.to))
            if mine is not None and theirs != mine:
                raise GraphViolation(f"plugin stage {s.name} feeds {e.to}")
            if theirs is not None and mine is None and s.name not in event_stages:
                raise GraphViolation(f"core stage {s.name} feeds plugin stage {e.to} off events")
        if mine is not None:
            for r in s.may_request:
                if leaf(by.get(r)) != mine:
                    raise GraphViolation(f"plugin stage {s.name} requests {r}")


def _toml_of(doc: Mapping[str, Any]) -> bytes:
    """A normalised manifest as TOML again, to check it with this release's rules."""
    lines: list[str] = []

    def val(v: Any) -> str:
        if isinstance(v, bool):
            return "true" if v else "false"
        if isinstance(v, int | float):
            return json.dumps(v)
        if isinstance(v, str):
            return json.dumps(v, ensure_ascii=False)
        if isinstance(v, list) and all(not isinstance(x, dict) for x in v):
            return "[" + ", ".join(val(x) for x in v) + "]"
        raise ValueError("not a TOML value")

    def table(prefix: str, d: Mapping[str, Any]) -> None:
        subs: list[tuple[str, Any]] = []
        for k, v in d.items():
            tables = isinstance(v, list) and v and all(isinstance(x, dict) for x in v)
            if isinstance(v, dict) or tables:
                subs.append((k, v))
            elif isinstance(v, list) and not v and k in ("stage", "schedule", "panel", "trigger"):
                continue
            else:
                lines.append(f"{k} = {val(v)}")
        for k, v in subs:
            name = f"{prefix}{k}"
            if isinstance(v, dict):
                lines.append(f"[{name}]")
                table(name + ".", v)
            else:
                for item in v:
                    lines.append(f"[[{name}]]")
                    table(name + ".", item)

    table("", doc)
    return "\n".join(lines).encode()
