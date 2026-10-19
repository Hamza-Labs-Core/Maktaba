"""The stage graph: single source of truth for the queue engine (plan 06-11 section 3).

Every stage is described once, here. At worker start :mod:`.sync` upserts the
graph into ``job_stages``; Go and the web only ever read that table.

Handlers, input fingerprints and output fingerprints are bound lazily through
:mod:`maktaba_pipeline.engine.stages.registry` so that importing the graph (for
tests, for the SQL seed check, for tooling) never imports ffmpeg wrappers or
HTTP clients.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field, replace
from typing import TYPE_CHECKING, Any, Literal
from uuid import UUID

from ..audio.chromaprint import AUDIO_FP_VERSION
from ..classify.books import BOOKS_VERSION
from ..classify.production_style import STYLE_VERSION
from ..classify.series_detector import DETECTOR_VERSION
from ..classify.tag_check import CLIP_CHECK_VERSION, TAG_CHECK_VERSION
from ..classify.topic_tags import TAGGER_VERSION
from ..schedule.channel_planner import PLANNER_VERSION
from ..search.semantic import INDEX_VERSION
from ..thumbnail.dhash import DHASH_VERSION, FRAMES_VERSION

if TYPE_CHECKING:
    from ..db.jobs import DBConn, Job
    from .context import StageContext

__all__ = [
    "CONTENT_HASH_VERSION",
    "CATALOG_ID",
    "GRAPH",
    "MONOTONE_STAGES",
    "GraphBuilder",
    "POOLS",
    "SIG_VERSION",
    "Edge",
    "GraphError",
    "GraphViolation",
    "PoolDef",
    "Request",
    "StageDef",
    "StageHandler",
    "StageResult",
    "Subject",
    "SubjectKind",
    "bound_graph",
    "edge_targets",
    "get_stage",
    "stage_names",
    "validate",
]

SubjectKind = Literal["video", "library", "series", "season", "catalog"]
SUBJECT_KINDS: tuple[SubjectKind, ...] = ("video", "library", "series", "season", "catalog")

#: The single global planning subject (decision D2).
CATALOG_ID = UUID("00000000-0000-0000-0000-00000000ca7a")

#: ``SIG_VERSION`` as the mediaintel prototype defines it (signatures.py:45).
SIG_VERSION: int = AUDIO_FP_VERSION * 100 + FRAMES_VERSION

LINK_VERSION = 1
#: thumb_small / review_pages versions (stages/thumb_small.py, stages/review_pages.py)
THUMB_SMALL_VERSION = 1
CONTENT_HASH_VERSION = 1
REVIEW_VERSION = 1
#: topic_collections / promo_links / clip_groups (plan 06-11 section 17)
TOPIC_VERSION = 1
PROMO_VERSION = 1
CLIP_GROUPS_VERSION = 1
YT_SYNC_VERSION = 1
CH_WRITE_VERSION = 2
#: exact_dates / repair_broken (plan 06-11, mediaintel parity)
EXACT_DATES_VERSION = 1
REPAIR_VERSION = 1
#: transcribe / subtitle_gen (plan 03-10): stages/transcribe.py, stages/subtitle_gen.py
TRANSCRIBE_VERSION = 1
SUBTITLE_VERSION = 1
#: transcript analysis (stages/chapter_infer.py, topic_assign.py, topic_recluster.py)
CHAPTER_VERSION = 1
CHAPTER_THUMBS_VERSION = 1
TOPIC_ASSIGN_VERSION = 1
TOPIC_RECLUSTER_VERSION = 1

EdgeMap = Literal[
    "same",
    "library_of",
    # a video's library, only while the video is in no season (the library tag's
    # loose videos): a series episode's transcript re-tags its season, not its library
    "loose_library_of",
    "season_of",
    "seasons_changed",
    "videos_of",
    "catalog",
    "payload_seasons",
]


class GraphError(Exception):
    """The static graph is inconsistent (raised by :func:`validate`)."""


class GraphViolation(Exception):
    """A stage tried to enqueue a stage it did not declare in ``may_request``."""


@dataclass(frozen=True, slots=True)
class Subject:
    """What a job acts on."""

    kind: SubjectKind
    id: UUID
    library_id: UUID | None = None


@dataclass(frozen=True, slots=True)
class Edge:
    """A dependency: when the source stage's output changes, enqueue ``to``."""

    to: str
    map: EdgeMap
    when: Literal["output_changed", "always"] = "output_changed"
    #: display-only: the legacy handler enqueues the target itself
    legacy: bool = False

    def to_json(self) -> dict[str, Any]:
        return {"to": self.to, "map": self.map, "when": self.when, "legacy": self.legacy}


@dataclass(frozen=True, slots=True)
class Request:
    """An explicit enqueue a handler asks for (target must be in ``may_request``)."""

    stage: str
    subject_kind: SubjectKind
    subject_id: UUID
    reason: str
    payload: dict[str, Any] | None = None
    priority: int | None = None


@dataclass(frozen=True, slots=True)
class StageResult:
    """What a handler returns."""

    output_hash: str
    outcome: Literal["changed", "unchanged", "awaiting_review", "waiting_inputs"] | None = None
    changed_subjects: tuple[tuple[SubjectKind, UUID], ...] = ()
    requests: tuple[Request, ...] = ()
    summary: dict[str, Any] = field(default_factory=dict)
    log: tuple[tuple[str, str, dict[str, Any]], ...] = ()


StageHandler = Callable[["DBConn", "Job", "StageContext"], Awaitable[StageResult]]
InputFingerprint = Callable[["DBConn", Subject], Awaitable["str | None"]]
OutputFingerprint = Callable[["DBConn", Subject], Awaitable[str]]


@dataclass(frozen=True, slots=True)
class PoolDef:
    pool: str
    label: str
    scope: Literal["global", "per_worker"]
    size: int | None
    default_size: int | None


POOLS: tuple[PoolDef, ...] = (
    PoolDef("nas_read", "NAS reads", "global", 2, 2),
    PoolDef("cpu", "CPU", "per_worker", None, None),
    # transcription: the jobs of a worker share one batched Whisper (stt/batched.py), so
    # the pool size is the batch, not a count of competing GPU users
    PoolDef("gpu", "GPU", "per_worker", 32, 32),
    # writes next to the videos (subtitle files): small, kept apart from the reads
    PoolDef("nas_write", "NAS writes", "global", 4, 4),
    PoolDef("network_plex", "Plex", "global", 2, 2),
    PoolDef("network_tv", "dizqueTV", "global", 1, 1),
    PoolDef("network_yt", "YouTube", "global", 1, 1),
    # the language model (Ollama) and the encyclopedia answer one question at a time
    PoolDef("lookup", "Model and encyclopedia", "global", 1, 1),
    # review pages only: they never take a cpu or network slot from the stages that write
    PoolDef("reports", "Review pages", "global", 1, 1),
    # the channel audit reads dizqueTV for minutes; a repair downloads for hours: neither
    # takes the slot that writes the channels or lists YouTube
    PoolDef("audits", "Channel audit", "global", 1, 1),
    PoolDef("repairs", "Repairs", "global", 1, 1),
)


@dataclass(frozen=True, slots=True)
class StageDef:
    name: str
    label: str
    subject_kinds: tuple[SubjectKind, ...]
    pool: str
    lane: Literal["sources", "episodes", "speech", "seasons", "channels", "media"]
    lane_order: int
    version: int
    run: StageHandler | None = None
    input_fingerprint: InputFingerprint | None = None
    output_fingerprint: OutputFingerprint | None = None
    edges: tuple[Edge, ...] = ()
    may_request: tuple[str, ...] = ()
    entry_for: tuple[SubjectKind, ...] = ()
    debounce_sec: int = 0
    debounce_max_sec: int = 0
    external_write: bool = False
    #: name of the extension that contributes this stage (None = core)
    extension: str | None = None
    legacy: bool = False
    hidden: bool = False
    max_attempts: int = 3
    #: where it may run (Epic 31): "any" machine, or the "home" only (its output is a
    #: local directory the home's API or search reads: the vector index, thumbnails,
    #: review pages)
    placement: Literal["any", "home"] = "any"
    #: a home-only stage that asks a language model at ``rules.<this>.url``: a node may
    #: run it too when that address is not the machine itself (localhost), since every
    #: machine then asks the same model server
    model_rules: str | None = None
    #: enqueued (catalog subject) whenever the scheduler's top-up extended a channel's
    #: timeline: a stage that tells another system the guide grew (Plex Live TV)
    after_topup: bool = False

    def edges_json(self) -> list[dict[str, Any]]:
        return [e.to_json() for e in self.edges]


def _legacy(
    name: str,
    label: str,
    kinds: tuple[SubjectKind, ...],
    pool: str,
    lane_order: int,
    *,
    edges: tuple[Edge, ...] = (),
    lane: Literal["media", "episodes"] = "media",
) -> StageDef:
    return StageDef(
        name=name,
        label=label,
        subject_kinds=kinds,
        pool=pool,
        lane=lane,
        lane_order=lane_order,
        version=1,
        edges=edges,
        legacy=True,
    )


#: Stages in lane order. Edge and request wiring is exactly plan 06-11 section 3.2.
GRAPH: tuple[StageDef, ...] = (
    # ---- sources -------------------------------------------------------
    StageDef(
        name="youtube_sync",
        label="YouTube listing",
        subject_kinds=("library",),
        pool="network_yt",
        lane="sources",
        lane_order=0,
        version=YT_SYNC_VERSION,
        edges=(
            Edge("link", "videos_of"),
            Edge("detect", "same"),
            Edge("exact_dates", "same"),
        ),
        entry_for=("library",),
        debounce_sec=300,
        debounce_max_sec=3600,
    ),
    # Exact upload dates per video for series members (mediaintel "exact"); off until the
    # "exactdates" rules enable it. Detect runs again on the dates it finds.
    StageDef(
        name="exact_dates",
        label="Exact dates",
        subject_kinds=("library",),
        pool="network_yt",
        lane="sources",
        lane_order=1,
        version=EXACT_DATES_VERSION,
        edges=(Edge("detect", "same"),),
        entry_for=("library",),
        debounce_sec=300,
        debounce_max_sec=3600,
    ),
    # ---- episodes lane -------------------------------------------------
    StageDef(
        name="content_hash",
        label="File identity",
        subject_kinds=("video",),
        pool="nas_read",
        lane="episodes",
        lane_order=0,
        version=CONTENT_HASH_VERSION,
        # a video imported from mediaintel exists before its file was read; its small
        # thumbnail lives under the real hash. Its speech follows its identity, and so do
        # its poster and sprite: a video with no audio (or not transcribed yet) has them too
        edges=(
            Edge("thumb_small", "same"),
            Edge("transcribe", "same"),
            Edge("thumbnail", "same"),
        ),
    ),
    _legacy(
        "probe",
        "Length",
        ("video",),
        "nas_read",
        1,
        lane="episodes",
        edges=(
            Edge("thumb_hash", "same"),
            Edge("link", "same"),
            Edge("detect", "library_of"),
            Edge("extract", "same", legacy=True),
        ),
    ),
    StageDef(
        name="thumb_hash",
        label="Thumbnail",
        subject_kinds=("video",),
        pool="nas_read",
        lane="episodes",
        lane_order=2,
        version=DHASH_VERSION,
        # the sidecar it hashed is the small copy's source too (YouTube archives never run
        # the legacy thumbnail stage)
        edges=(Edge("detect", "library_of"), Edge("thumb_small", "same")),
    ),
    StageDef(
        name="link",
        label="Link",
        subject_kinds=("video",),
        pool="cpu",
        lane="episodes",
        lane_order=3,
        version=LINK_VERSION,
        edges=(Edge("detect", "library_of"),),
    ),
    StageDef(
        name="fingerprint",
        label="Fingerprint",
        subject_kinds=("video",),
        pool="nas_read",
        lane="episodes",
        lane_order=4,
        version=SIG_VERSION,
        edges=(Edge("detect", "library_of"), Edge("fp_gate", "payload_seasons")),
    ),
    StageDef(
        name="style_sample",
        label="Style sample",
        subject_kinds=("video",),
        pool="nas_read",
        lane="episodes",
        lane_order=5,
        version=STYLE_VERSION,
        edges=(Edge("rate_style", "payload_seasons"),),
    ),
    StageDef(
        name="thumb_small",
        label="Small thumbnail",
        subject_kinds=("video",),
        pool="nas_read",
        lane="episodes",
        lane_order=6,
        version=THUMB_SMALL_VERSION,
        edges=(Edge("review_pages", "library_of"),),
        entry_for=("video",),
        placement="home",
    ),
    # ---- seasons and libraries lane ------------------------------------
    StageDef(
        name="detect",
        label="Place episodes",
        subject_kinds=("library",),
        pool="cpu",
        lane="seasons",
        lane_order=1,
        version=DETECTOR_VERSION,
        edges=(
            Edge("tag", "seasons_changed"),
            Edge("rate_style", "seasons_changed"),
            Edge("tag", "same"),
            Edge("plan_channels", "catalog"),
            Edge("review_pages", "same"),
            Edge("topic_collections", "same"),
            Edge("promo_links", "same"),
        ),
        may_request=("fingerprint", "fp_gate"),
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
    ),
    StageDef(
        name="fp_gate",
        label="Samples to candidates",
        subject_kinds=("season",),
        pool="cpu",
        lane="seasons",
        lane_order=1,
        version=SIG_VERSION,
        may_request=("fingerprint",),
        debounce_sec=10,
        debounce_max_sec=120,
        hidden=True,
    ),
    StageDef(
        name="tag",
        label="Tag topics",
        subject_kinds=("season", "library"),
        pool="cpu",
        lane="seasons",
        lane_order=2,
        version=TAGGER_VERSION,
        # a new run rewrites the tags, so the model's and the book's corrections are
        # applied again (from their cached answers) before a clip group or a channel
        # reads them
        edges=(
            Edge("plan_channels", "catalog"),
            Edge("check_tags", "same"),
            Edge("check_clips", "library_of"),
        ),
        entry_for=("season",),
        # transcripts arrive one video at a time: a season's are tagged together
        debounce_sec=300,
        debounce_max_sec=3600,
    ),
    # ---- what a language model and the book behind a series say about the tags ----
    StageDef(
        name="check_tags",
        label="Check series tags",
        subject_kinds=("season",),
        pool="lookup",
        lane="seasons",
        lane_order=3,
        version=TAG_CHECK_VERSION,
        edges=(Edge("books", "same"), Edge("plan_channels", "catalog")),
        entry_for=("season",),
        # the model server defaults to localhost: the home's own (a node would ask
        # another model, or none) unless the settings name a shared one
        placement="home",
        model_rules="tagcheck",
    ),
    StageDef(
        name="check_clips",
        label="Check clip tags",
        subject_kinds=("library",),
        pool="lookup",
        lane="seasons",
        lane_order=3,
        version=CLIP_CHECK_VERSION,
        edges=(Edge("clip_groups", "same"), Edge("plan_channels", "catalog")),
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
        # the model server defaults to localhost: the home's own (a node would ask
        # another model, or none) unless the settings name a shared one
        placement="home",
        model_rules="tagcheck",
    ),
    StageDef(
        name="books",
        label="Books",
        subject_kinds=("season",),
        pool="lookup",
        lane="seasons",
        lane_order=4,
        version=BOOKS_VERSION,
        edges=(Edge("plan_channels", "catalog"),),
        entry_for=("season",),
    ),
    # ---- what detection left over: topics, promos, clip groups (section 17) ----
    StageDef(
        name="topic_collections",
        label="Topic collections",
        subject_kinds=("library",),
        pool="cpu",
        lane="seasons",
        lane_order=2,
        version=TOPIC_VERSION,
        edges=(Edge("clip_groups", "same"), Edge("plan_channels", "catalog")),
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
    ),
    StageDef(
        name="promo_links",
        label="Promo links",
        subject_kinds=("library",),
        pool="cpu",
        lane="seasons",
        lane_order=2,
        version=PROMO_VERSION,
        edges=(Edge("clip_groups", "same"), Edge("plan_channels", "catalog")),
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
    ),
    StageDef(
        name="clip_groups",
        label="Clip groups",
        subject_kinds=("library",),
        pool="cpu",
        lane="seasons",
        lane_order=3,
        version=CLIP_GROUPS_VERSION,
        edges=(Edge("plan_channels", "catalog"),),
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
    ),
    StageDef(
        name="rate_style",
        label="Rate style",
        subject_kinds=("season",),
        pool="cpu",
        lane="seasons",
        lane_order=3,
        version=STYLE_VERSION,
        edges=(Edge("plan_channels", "catalog"),),
        may_request=("style_sample",),
        entry_for=("season",),
        debounce_sec=10,
        debounce_max_sec=120,
    ),
    # Its own pool and no edges out: nothing waits for a review page (plan 06-11 section 18).
    StageDef(
        name="review_pages",
        label="Review pages",
        subject_kinds=("library",),
        pool="reports",
        lane="seasons",
        lane_order=6,
        version=REVIEW_VERSION,
        entry_for=("library",),
        debounce_sec=120,
        debounce_max_sec=900,
        placement="home",
    ),
    # ---- channels lane -------------------------------------------------
    StageDef(
        name="plan_channels",
        label="Plan channels",
        subject_kinds=("catalog",),
        pool="cpu",
        lane="channels",
        lane_order=1,
        version=PLANNER_VERSION,
        edges=(Edge("write_channels", "same"),),
        entry_for=("catalog",),
        debounce_sec=300,
        debounce_max_sec=3600,
        # the logo / watermark / loading pictures are files on the home (dz_logo_dir,
        # dz_watermark_*), and the plan page is written there
        placement="home",
    ),
    # Core write_channels writes Maktaba Live TV only (a cpu-bound DB write);
    # the dizqueTV channel export is the dizquetv extension's own stage.
    StageDef(
        name="write_channels",
        label="Write channels",
        subject_kinds=("catalog",),
        pool="cpu",
        lane="channels",
        lane_order=2,
        version=CH_WRITE_VERSION,
        # the art it stores is looked up as files on the home (dz_logo_dir,
        # dz_watermark_*): on a node the paths would not exist
        placement="home",
    ),
    # ---- media lane (legacy) -------------------------------------------
    _legacy(
        "scan",
        "Scan folders",
        ("library",),
        "nas_read",
        0,
        edges=(Edge("probe", "videos_of", legacy=True),),
    ),
    _legacy(
        "extract",
        "Extract audio",
        ("video",),
        "nas_read",
        1,
        edges=(Edge("transcribe", "same", legacy=True),),
    ),
    # plan 03-10: transcription on the engine. The file is decoded straight off the share
    # and batched across the worker's jobs; the legacy extract edge still reaches it.
    StageDef(
        name="transcribe",
        label="Transcribe",
        subject_kinds=("video",),
        pool="gpu",
        lane="speech",
        lane_order=2,
        version=TRANSCRIBE_VERSION,
        # what is said is tagging evidence too: its season, or its library's loose videos
        edges=(
            Edge("subtitle_gen", "same"),
            Edge("index", "same"),
            Edge("tag", "season_of"),
            Edge("tag", "loose_library_of"),
        ),
        max_attempts=3,
    ),
    StageDef(
        name="subtitle_gen",
        label="Subtitle files",
        subject_kinds=("video",),
        pool="nas_write",
        lane="speech",
        lane_order=3,
        version=SUBTITLE_VERSION,
        external_write=True,
    ),
    # plan 03-10: the transcript's sentences as e5 vectors (search/semantic.py)
    StageDef(
        name="index",
        label="Search index",
        subject_kinds=("video",),
        pool="cpu",
        lane="speech",
        lane_order=4,
        version=INDEX_VERSION,
        edges=(Edge("chapter_infer", "same"),),
        placement="home",
    ),
    # Stories 9.18 / 9.9: chapters at the transcript's topic shifts, and the video's mean
    # vector for its topics. Reads the index's vectors (Chroma: the home holds it)
    StageDef(
        name="chapter_infer",
        label="Chapters",
        subject_kinds=("video",),
        pool="cpu",
        lane="speech",
        lane_order=5,
        version=CHAPTER_VERSION,
        # only the chapter frames depend on chapters; the poster and sprite came from
        # content_hash already
        edges=(Edge("chapter_thumbs", "same"), Edge("topic_assign", "same")),
        placement="home",
    ),
    StageDef(
        name="chapter_thumbs",
        label="Chapter frames",
        subject_kinds=("video",),
        pool="nas_read",
        lane="speech",
        lane_order=6,
        version=CHAPTER_THUMBS_VERSION,
        # the frames go to the thumbnail cache of the machine that serves them
        placement="home",
    ),
    StageDef(
        name="topic_assign",
        label="Topics",
        subject_kinds=("video",),
        pool="cpu",
        lane="speech",
        lane_order=7,
        version=TOPIC_ASSIGN_VERSION,
        # every newly vectored video counts towards the library's next clustering
        edges=(Edge("topic_recluster", "library_of", when="always"),),
    ),
    StageDef(
        name="topic_recluster",
        label="Library topics",
        subject_kinds=("library",),
        pool="cpu",
        lane="speech",
        lane_order=8,
        version=TOPIC_RECLUSTER_VERSION,
        # once per batch of transcriptions, not per video: a 47 000-video library
        # clusters in seconds, but there is no point doing it every few seconds
        debounce_sec=1800,
        debounce_max_sec=6 * 3600,
    ),
    _legacy(
        "thumbnail",
        "Poster and sprites",
        ("video",),
        "nas_read",
        5,
        edges=(Edge("thumb_small", "same"),),
    ),
    # Files whose probe failed, fetched from YouTube again and swapped in on the share
    # (mediaintel repair-broken). Writes to the share: off until the "repair" rules enable it.
    StageDef(
        name="repair_broken",
        label="Repair broken files",
        subject_kinds=("library",),
        pool="repairs",
        lane="media",
        lane_order=10,
        version=REPAIR_VERSION,
        may_request=("probe", "content_hash"),
        # not an entry queue: a library retrigger never starts it, an operator runs it
        external_write=True,
        max_attempts=1,
    ),
    _legacy("categorize", "Categorize", ("video",), "cpu", 8),
)

#: Stages on which a request cycle (detect -> fingerprint -> detect ...) is allowed,
#: because their input fingerprints are monotone (plan 06-11 section 3.4).
MONOTONE_STAGES: frozenset[str] = frozenset(
    {"detect", "fingerprint", "fp_gate", "rate_style", "style_sample"}
)

_BY_NAME: dict[str, StageDef] = {s.name: s for s in GRAPH}


class GraphBuilder:
    """Mutable view of the graph handed to ``Extension.register``.

    An extension adds its own stages and wires them in by adding edges *from*
    core (or its own) stages, so the core graph never names an extension.
    """

    def __init__(self, base: tuple[StageDef, ...]) -> None:
        self._stages: dict[str, StageDef] = {s.name: s for s in base}

    def has_stage(self, name: str) -> bool:
        return name in self._stages

    def add_stage(self, stage: StageDef) -> None:
        if stage.name in self._stages:
            raise GraphError(f"stage {stage.name!r} already defined")
        self._stages[stage.name] = stage

    def add_edge(self, source: str, edge: Edge) -> None:
        src = self._stages.get(source)
        if src is None:
            raise GraphError(f"add_edge: unknown source stage {source!r}")
        if any(e.to == edge.to and e.map == edge.map for e in src.edges):
            return
        self._stages[source] = replace(src, edges=(*src.edges, edge))

    def build(self) -> tuple[StageDef, ...]:
        return tuple(self._stages.values())


def stage_names() -> tuple[str, ...]:
    return tuple(s.name for s in GRAPH)


def get_stage(name: str) -> StageDef | None:
    return _BY_NAME.get(name)


def edge_targets(stage: StageDef, *, include_legacy: bool = False) -> tuple[Edge, ...]:
    return tuple(e for e in stage.edges if include_legacy or not e.legacy)


def validate(graph: tuple[StageDef, ...] = GRAPH, pools: tuple[PoolDef, ...] = POOLS) -> None:
    """Raise :class:`GraphError` if the graph is inconsistent.

    Checks: unique names, edge and request targets exist, pools exist, the
    non-legacy edge relation is acyclic, and request cycles only pass through
    stages with monotone input fingerprints.
    """
    names = [s.name for s in graph]
    if len(set(names)) != len(names):
        raise GraphError("duplicate stage names")
    known = set(names)
    pool_names = {p.pool for p in pools}
    for s in graph:
        if s.pool not in pool_names:
            raise GraphError(f"{s.name}: unknown pool {s.pool!r}")
        for e in s.edges:
            if e.to not in known:
                raise GraphError(f"{s.name}: edge to unknown stage {e.to!r}")
        for r in s.may_request:
            if r not in known:
                raise GraphError(f"{s.name}: may_request unknown stage {r!r}")
        for k in s.entry_for:
            if k not in s.subject_kinds:
                raise GraphError(f"{s.name}: entry_for {k!r} not in subject_kinds")
    _check_acyclic({s.name: [e.to for e in s.edges if not e.legacy] for s in graph})
    # Request cycles: follow edges plus requests; any cycle must be monotone-only.
    combined = {
        s.name: [e.to for e in s.edges if not e.legacy] + list(s.may_request) for s in graph
    }
    for cycle in _cycles(combined):
        bad = [n for n in cycle if n not in MONOTONE_STAGES]
        if bad:
            raise GraphError(f"request cycle through non-monotone stages: {cycle}")


def _check_acyclic(adj: dict[str, list[str]]) -> None:
    state: dict[str, int] = {}

    def visit(n: str, path: list[str]) -> None:
        state[n] = 1
        for m in adj.get(n, ()):
            if state.get(m) == 1:
                raise GraphError(f"edge cycle: {' -> '.join([*path, n, m])}")
            if m not in state:
                visit(m, [*path, n])
        state[n] = 2

    for n in adj:
        if n not in state:
            visit(n, [])


def _cycles(adj: dict[str, list[str]]) -> list[list[str]]:
    out: list[list[str]] = []
    state: dict[str, int] = {}
    stack: list[str] = []

    def visit(n: str) -> None:
        state[n] = 1
        stack.append(n)
        for m in adj.get(n, ()):
            if state.get(m) == 1:
                out.append(stack[stack.index(m) :])
            elif m not in state:
                visit(m)
        stack.pop()
        state[n] = 2

    for n in adj:
        if n not in state:
            visit(n)
    return out


def bound_graph(graph: tuple[StageDef, ...] | None = None) -> tuple[StageDef, ...]:
    """``graph`` (default: the core graph) with handlers and fingerprints bound
    from the stage registry (core stages and registered extensions)."""
    from .stages.registry import get_impl

    out: list[StageDef] = []
    for s in graph if graph is not None else GRAPH:
        impl = get_impl(s.name)
        if impl is None:
            out.append(s)
            continue
        out.append(
            replace(
                s,
                run=impl.run,
                input_fingerprint=impl.input_fingerprint,
                output_fingerprint=impl.output_fingerprint,
            )
        )
    return tuple(out)
