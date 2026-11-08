"""The stage graph: single source of truth for the queue engine (plan 06-11 section 3).

Every stage is described once, here. At worker start :mod:`.sync` upserts the
graph into ``job_stages``; Go and the web only ever read that table.

Handlers, input fingerprints and output fingerprints are bound lazily through
:mod:`maktaba_pipeline.engine.stages.registry` so that importing the graph (for
tests, for the SQL seed check, for tooling) never imports ffmpeg wrappers or
HTTP clients.
"""

from __future__ import annotations

import os
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field, replace
from typing import TYPE_CHECKING, Any, Literal
from uuid import UUID

from ..audio.chromaprint import AUDIO_FP_VERSION
from ..classify.display_title import DISPLAY_TITLE_VERSION
from ..thumbnail.dhash import FRAMES_VERSION

if TYPE_CHECKING:
    from ..db.jobs import DBConn, Job
    from .context import StageContext

__all__ = [
    "CONTENT_HASH_VERSION",
    "CATALOG_ID",
    "EPIC47_PRIORITIES",
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

SubjectKind = Literal["video", "library", "series", "season", "catalog", "dup_group"]
SUBJECT_KINDS: tuple[SubjectKind, ...] = (
    "video",
    "library",
    "series",
    "season",
    "catalog",
    # Epic 47: a duplicate_groups row (dedup_apply); its library is the group's
    "dup_group",
)

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
#: youtube_collections (Epic 56 story 56.06)
YOUTUBE_COLLECTIONS_VERSION = 1
PROMO_VERSION = 1
CLIP_GROUPS_VERSION = 1
YT_SYNC_VERSION = 1
#: 3: look.watermark_skip_filler; 4: look.loudnorm filter, expansion; 5: the corner logo on
#: filler by default (rules tv.watermark_skip_filler off): the version sweep runs the stage
#: again, which re-stages every channel whose stored look differs from the effective one
CH_WRITE_VERSION = 5
#: exact_dates / repair_broken (plan 06-11, mediaintel parity)
EXACT_DATES_VERSION = 1
REPAIR_VERSION = 1
#: transcribe / subtitle_gen (plan 03-10): stages/transcribe.py, stages/subtitle_gen.py
TRANSCRIBE_VERSION = 1
#: second_listen (Cohere on Whisper's stored transcript): stages/second_listen.py
SECOND_LISTEN_VERSION = 1
SUBTITLE_VERSION = 1
#: word_check (Epic 52): stages/word_check.py; bumps re-check every transcript
WORD_CHECK_VERSION = 1
#: transcript analysis (stages/chapter_infer.py, topic_assign.py, topic_recluster.py)
CHAPTER_VERSION = 1
CHAPTER_THUMBS_VERSION = 1
TOPIC_ASSIGN_VERSION = 1
TOPIC_RECLUSTER_VERSION = 1
#: enrich (stages/enrich.py): the metadata provider
ENRICH_VERSION = 1
#: Epic 47 (offline, MP4, no duplicates): stages/convert_mp4.py, dedup_scan.py,
#: dedup_apply.py, youtube_fetch.py, youtube_backfill.py, trash_purge.py, file_restore.py
CONVERT_VERSION = 1
DEDUP_SCAN_VERSION = 1
DEDUP_APPLY_VERSION = 1
YT_FETCH_VERSION = 1
YT_BACKFILL_VERSION = 1
TRASH_PURGE_VERSION = 1
FILE_RESTORE_VERSION = 1

#: Epic 47's enqueue priorities (lower runs first; normal pipeline work is 100): a
#: library conversion or dedup never starves new videos, new uploads beat them, and the
#: YouTube back catalogue only runs when nothing else wants the pool
EPIC47_PRIORITIES: dict[str, int] = {
    "convert_mp4": 150,
    #: a per-video "Convert now" from the failures page
    "convert_mp4_now": 50,
    "dedup_scan": 150,
    "dedup_apply": 150,
    "youtube_fetch": 120,
    "youtube_backfill": 300,
    "trash_purge": 200,
    #: a Restore someone asked for on the file-actions page
    "file_restore": 50,
}

EdgeMap = Literal[
    "same",
    # the same subject when it is a video (a stage that also runs on libraries: its
    # library jobs trigger nothing through this edge)
    "same_video",
    "library_of",
    # a video's library, only while the video is in no season (the library tag's
    # loose videos): a series episode's transcript re-tags its season, not its library
    "loose_library_of",
    "season_of",
    "seasons_changed",
    "videos_of",
    # only the videos the job names as changed (a scan's new files); none when it names none
    "videos_changed",
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
    #: the inputs this result answers, when not all the fingerprint names (a transcript
    #: whose second listen was skipped): stored as the job's input hash, so a worker
    #: that can do more does not take it as done
    input_hash: str | None = None


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


#: the plugins pool's default size: a quarter of the home's cores, at least one
PLUGINS_POOL_SIZE = max(1, (os.cpu_count() or 1) // 4)

POOLS: tuple[PoolDef, ...] = (
    PoolDef("nas_read", "NAS reads", "global", 2, 2),
    PoolDef("cpu", "CPU", "per_worker", None, None),
    # transcription: the jobs of a worker share one batched Whisper (stt/batched.py), so
    # the pool size is the batch, not a count of competing GPU users
    PoolDef("gpu", "GPU", "per_worker", 32, 32),
    # the second listen: Cohere hears one file's stretches at a time on the GPU; the files
    # beyond it wait with their audio decoded. On a GPU that holds one model at a time
    # the supervisor sets the worker's own size to its Whisper batch
    # (MAKTABA_POOL_LISTEN), so that a turn of the second listen has a batch to hear
    PoolDef("listen", "Second listen", "per_worker", 4, 4),
    # writes next to the videos (subtitle files): small, kept apart from the reads
    PoolDef("nas_write", "NAS writes", "global", 4, 4),
    PoolDef("network_plex", "Plex", "global", 2, 2),
    PoolDef("network_yt", "YouTube", "global", 1, 1),
    # the language model (Ollama) and the encyclopedia answer one question at a time
    PoolDef("lookup", "Model and encyclopedia", "global", 1, 1),
    # review pages only: they never take a cpu or network slot from the stages that write
    PoolDef("reports", "Review pages", "global", 1, 1),
    # a repair downloads for hours: it never takes the slot that lists YouTube
    PoolDef("repairs", "Repairs", "global", 1, 1),
    # Epic 47: a library's conversion to MP4 (ffmpeg plus a full decode), one at a time
    PoolDef("convert", "Convert to MP4", "global", 1, 1),
    # Epic 54 (story 54.06 §2): third-party plugin stages, run in maktaba-plugind; each
    # plugin's resources.concurrency caps its share (claim.py)
    PoolDef("plugins", "Plugins", "global", PLUGINS_POOL_SIZE, PLUGINS_POOL_SIZE),
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
    #: the storage check a stage needs besides its pool's (``nas_read`` reads, ``nas_write``
    #: writes): one that reads the share from another pool (transcribe, ``gpu``) is not
    #: claimed by a machine whose read check failed, where every job would fail
    storage: Literal["read", "write"] | None = None
    #: a home-only stage that asks a language model at ``rules.<this>.url``: a node may
    #: run it too when that address is not the machine itself (localhost), since every
    #: machine then asks the same model server
    model_rules: str | None = None
    #: a home-only stage whose only reason is an extension secret (the Plex token): a
    #: node may run it too when the home's extension relay is on (rules ``relay``), its
    #: client then calling through the home, which adds the secret (story 31.R)
    relay: str | None = None
    #: enqueued (catalog subject) whenever the scheduler's top-up extended a channel's
    #: timeline: a stage that tells another system the guide grew (Plex Live TV)
    after_topup: bool = False
    #: a catalog stage the reconciler enqueues periodically: ``(section, field)`` names the
    #: rule holding the interval in minutes (``rules.<section>.<field>``; 0: never). For a
    #: stage that checks another system nothing tells Maktaba about (Plex's channel map)
    every_minutes_rule: tuple[str, str] | None = None
    #: in the graph (a tile on /queue) but its story has not shipped: the graph sync keeps
    #: it off (``disabled_reason = 'not_configured'``) until the flag is dropped
    unreleased: bool = False

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
    placement: Literal["any", "home"] = "any",
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
        placement=placement,
    )


def _fp(name: str) -> StageDef:
    """A stage one of Maktaba's first-party plugins defines (Epic 54, story 54.25 §1): the
    graph keeps its place and its name, its manifest's ``[[stage]]`` gives every field."""
    from ..plugins.firstparty import stage_def

    return stage_def(name)


#: Stages in lane order. Edge and request wiring is exactly plan 06-11 section 3.2.
GRAPH: tuple[StageDef, ...] = (
    # ---- sources -------------------------------------------------------
    # Epic 54: defined by tv.maktaba.youtube's manifest
    _fp("youtube_sync"),
    # Epic 47: download what youtube_sync listed and the library lacks: new uploads, and
    # (its own tile, so it pauses alone) the back catalogue. One pool for both: one stream
    # of requests per home.
    # Epic 54: defined by tv.maktaba.youtube's manifest
    _fp("youtube_fetch"),
    # Epic 54: defined by tv.maktaba.youtube's manifest
    _fp("youtube_backfill"),
    # slot 0175: every library's YouTube sources from Tartube's records (off), and what
    # Tartube downloaded seeded done. An admin runs it (Admin > Libraries); on the home,
    # where Tartube's database is.
    StageDef(
        name="youtube_tartube",
        label="Tartube import",
        subject_kinds=("catalog",),
        pool="network_yt",
        lane="sources",
        lane_order=4,
        version=1,
        placement="home",
        max_attempts=1,
    ),
    # Exact upload dates per video for series members (mediaintel "exact"); off until the
    # "exactdates" rules enable it. Detect runs again on the dates it finds.
    # Epic 54: defined by tv.maktaba.youtube's manifest
    _fp("exact_dates"),
    # ---- episodes lane -------------------------------------------------
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("content_hash"),
    # Epic 54 story 54.36 §2: on the engine. It returns the hash the legacy bookkeeping
    # computed (length and frame size), so its edges fire as they did.
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("probe"),
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("thumb_hash"),
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("link"),
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("fingerprint"),
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("style_sample"),
    # Epic 54: defined by tv.maktaba.thumbnails's manifest
    _fp("thumb_small"),
    # ---- seasons and libraries lane ------------------------------------
    # Epic 54 wave 4 (story 54.31): defined by tv.maktaba.classify's manifest
    _fp("detect"),
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("fp_gate"),
    # Epic 54 wave 4 (story 54.31): defined by tv.maktaba.classify's manifest
    _fp("tag"),
    # ---- what a language model and the book behind a series say about the tags ----
    # Epic 54 wave 4 (story 54.31): defined by tv.maktaba.classify's manifest
    _fp("check_tags"),
    # Epic 54 wave 4 (story 54.31): defined by tv.maktaba.classify's manifest
    _fp("check_clips"),
    # ---- the on-air titles the guide and the channel strip show (Story 27.11) ----
    StageDef(
        name="display_title",
        label="Display titles",
        subject_kinds=("library",),
        pool="cpu",
        lane="seasons",
        lane_order=4,
        version=DISPLAY_TITLE_VERSION,
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
        # with rules display_titles.use_llm it asks the model server, which defaults to
        # localhost: the home's own unless the settings name a shared one
        placement="home",
        model_rules="tagcheck",
    ),
    # Epic 54 wave 4 (story 54.31): defined by tv.maktaba.classify's manifest (net group)
    _fp("books"),
    # ---- what detection left over: topics, promos, clip groups (section 17) ----
    # Epic 54 wave 1 (story 54.28): defined by tv.maktaba.classify's manifest
    _fp("topic_collections"),
    # Epic 56 story 56.06: a collection from each YouTube playlist and channel that is
    # neither a season nor a topic (stages/youtube_collections.py)
    StageDef(
        name="youtube_collections",
        label="Collections from YouTube",
        subject_kinds=("library",),
        pool="cpu",
        lane="seasons",
        lane_order=3,
        version=YOUTUBE_COLLECTIONS_VERSION,
        entry_for=("library",),
        debounce_sec=30,
        debounce_max_sec=300,
    ),
    # Epic 54 wave 1 (story 54.28): defined by tv.maktaba.classify's manifest
    _fp("promo_links"),
    # Epic 54 wave 1 (story 54.28): defined by tv.maktaba.classify's manifest
    _fp("clip_groups"),
    # Epic 54 wave 1 (story 54.28): defined by tv.maktaba.classify's manifest
    _fp("rate_style"),
    # Its own pool and no edges out: nothing waits for a review page (plan 06-11 section 18).
    # Epic 54 wave 4 (story 54.31): defined by tv.maktaba.classify's manifest
    _fp("review_pages"),
    # ---- channels lane -------------------------------------------------
    # Epic 54: defined by tv.maktaba.livetv's manifest
    _fp("plan_channels"),
    # Core write_channels writes Maktaba Live TV (a cpu-bound DB write).
    # Epic 54: defined by tv.maktaba.livetv's manifest
    _fp("write_channels"),
    # Epic 33: TV changes. The API's change controller enqueues them (payload
    # {"change_id"}): build a change's release and schedule generation, put an approved
    # change on the air (the swap commit; the front door switches the streams), the daily
    # GC.
    # Epic 54: defined by tv.maktaba.livetv-slots's manifest
    _fp("tv_stage"),
    # Epic 54: defined by tv.maktaba.livetv-slots's manifest
    _fp("tv_swap"),
    # Epic 54: defined by tv.maktaba.livetv-slots's manifest
    _fp("tv_gc"),
    # story 33.9: the reverse commit of a rollback (warm: the front door switches the
    # streams back; the API or the controller enqueues it)
    # Epic 54: defined by tv.maktaba.livetv-slots's manifest
    _fp("tv_rollback"),
    # ---- media lane (legacy) -------------------------------------------
    # Epic 54 story 54.36 §2: on the engine. Its new videos are its changed subjects and
    # the edge queues their probes (before, the scanner's store queued each by hand)
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("scan"),
    # the WAV a backend that reads a file needs (openai-api): only transcribe asks for it
    # (may_request), and extract asks for transcribe again once it is written. Nothing else
    # leads here: a streaming backend never gets an extract job. Epic 54 story 54.36 §2: on
    # the engine; its transcribe is a request (it carries the payload transcribe reads)
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.transcription's manifest
    _fp("extract"),
    # plan 03-10: transcription on the engine. The file is decoded straight off the share
    # and batched across the worker's jobs (content_hash's edge reaches it). When the
    # configured STT backend reads a file it asks for the extract stage's WAV first.
    # Epic 54: defined by tv.maktaba.transcription's manifest
    _fp("transcribe"),
    # Cohere Transcribe Arabic on Whisper's stored transcript and the file's audio: any
    # machine whose GPU holds it (beside Whisper, or taking turns with it); with the second
    # listen off it passes the transcript on as it is
    # Epic 54: defined by tv.maktaba.transcription's manifest
    _fp("second_listen"),
    # Epic 54: defined by tv.maktaba.subtitles's manifest
    _fp("subtitle_gen"),
    # Epic 52 (stories 52.6, 52.15): the transcript's suspect words, saved for a home
    # admin to review (the review.words component). CPU only, re-runnable without the GPU
    # whenever a language pack, the vocabulary or the library's settings change. On a
    # library it plans the backfill of the transcripts made before it (story 52.6 §7)
    StageDef(
        name="word_check",
        label="Word review",
        subject_kinds=("video", "library"),
        pool="cpu",
        lane="speech",
        lane_order=4,
        version=WORD_CHECK_VERSION,
        max_attempts=3,
    ),
    # plan 03-10: the transcript's sentences as e5 vectors (search/semantic.py)
    # Epic 54: defined by tv.maktaba.semantic-search's manifest
    _fp("index"),
    # Stories 9.18 / 9.9: chapters at the transcript's topic shifts, and the video's mean
    # vector for its topics. Reads the index's vectors (Chroma: the home holds it)
    # Epic 54: defined by tv.maktaba.semantic-search's manifest
    _fp("chapter_infer"),
    # Epic 54: defined by tv.maktaba.thumbnails's manifest
    _fp("chapter_thumbs"),
    # Epic 54: defined by tv.maktaba.semantic-search's manifest
    _fp("topic_assign"),
    # Epic 54: defined by tv.maktaba.semantic-search's manifest
    _fp("topic_recluster"),
    # The poster and sprites go to the thumbnail cache of the machine that makes them
    # and their paths into videos.poster_path / sprite_path, which the home serves:
    # the per-machine caches stay on the home (Epic 31 R3.4, R4.2; moving them to the
    # share is out of scope), so a node never claims it (as thumb_small after it).
    # Epic 54 story 54.36 §2: on the engine, its output the poster's and the sprite's paths
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.thumbnails's manifest
    _fp("thumbnail"),
    # Files whose probe failed, fetched from YouTube again and swapped in on the share
    # (mediaintel repair-broken). Writes to the share: off until the "repair" rules enable it.
    # Epic 54: defined by tv.maktaba.repair's manifest
    _fp("repair_broken"),
    # Story 31.3: the home converts the stored paths to canonical share paths by itself
    # (sharepath/auto.py). Its own agent enqueues it (catalog subject) when there may be
    # work, an admin asks for it on the Machines page, or a run was cut short; the home's
    # mounts decide the mapping, so it never runs on a node.
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("storage_canonicalize"),
    # Epic 40: an admin's "check access" on the failures page reads 64 KiB of the file on
    # the worker host; when it reads, the video's failed jobs are queued again
    # Epic 54 wave 9 (story 54.36): defined by tv.maktaba.core-media's manifest
    _fp("access_check"),
    # ---- Epic 47: offline, MP4, no duplicates (specs/epics/47-offline-convert-dedup) --
    # Convert a video's file to MP4 without losing it: the original goes to the library's
    # trash once the MP4 is verified and swapped in (library_mgmt/swap.py). The work
    # folder and the full decode are on the home. A library's job plans its videos and
    # enqueues one job per video that needs work (or, dry run, only writes the plan).
    # One edge: a video's duplicate check runs once its conversion is done, whatever the
    # outcome (the per-item chain download -> convert -> dedup, Epic 47 "Pipeline
    # order"). Nothing downstream of the content hash (transcribe, index, embeddings)
    # runs again: the words and the picture are the same; only probe reads the new
    # container, and the thumbnails are made again only when their folder cannot move.
    # Epic 54: defined by tv.maktaba.convert's manifest
    _fp("convert_mp4"),
    # Epic 54: defined by tv.maktaba.dedup's manifest
    _fp("dedup_scan"),
    # merges one duplicate group: user data to the keeper, the loser's file to the trash
    # Epic 54: defined by tv.maktaba.dedup's manifest
    _fp("dedup_apply"),
    # deletes the trash entries whose grace period has passed (library_mgmt/trash.py)
    # Epic 54: defined by tv.maktaba.dedup's manifest
    _fp("trash_purge"),
    # undoes ledger rows from the file-actions page (library_mgmt/swap.py restore): the
    # trashed file goes back and the DB change is reversed; the API only queues it
    # Epic 54: defined by tv.maktaba.dedup's manifest
    _fp("file_restore"),
    # a placeholder from slot 0087 that never had a handler. Epic 54 wave 9 (story 54.36)
    # leaves it here, in the core graph and in core.pipeline: whether it is removed or moved
    # into tv.maktaba.core-media is an open decision
    _legacy("categorize", "Categorize", ("video",), "cpu", 8),
    # Stub audit #4 (Story 26.5): a video's metadata suggestions from Maktaba's own intel
    # and, when configured, a web search a language model reads. The API (Find metadata,
    # Re-enrich) and the home's sweep of enrich_jobs enqueue it; nothing waits for it. The
    # search keys are in home_secrets, so the home runs it.
    StageDef(
        name="enrich",
        label="Find metadata",
        subject_kinds=("video",),
        pool="lookup",
        lane="media",
        lane_order=12,
        version=ENRICH_VERSION,
        placement="home",
    ),
    # Epic 51 (story 51.06): a component enabled again gets the jobs its stages missed
    # while it was off (the edges logged skipped_component_off); its leader enqueues it with
    # {"components": [{"component", "off_since"}]}. Core: never switched off itself. Hidden
    # like the other bookkeeping stages; its job shows its progress
    StageDef(
        name="component_catchup",
        label="Catch up",
        subject_kinds=("catalog",),
        pool="cpu",
        lane="media",
        lane_order=99,
        version=1,
        placement="home",
        hidden=True,
    ),
)

#: Stages on which a request cycle (detect -> fingerprint -> detect ...) is allowed,
#: because their input fingerprints are monotone (plan 06-11 section 3.4).
MONOTONE_STAGES: frozenset[str] = frozenset(
    {
        "detect",
        "fingerprint",
        "fp_gate",
        "rate_style",
        "style_sample",
        # Epic 47: each job takes at least one wanted item off the list before it asks
        # for the next job, so the self-request ends when the list is empty
        # story 54.36 §2: transcribe asks for extract only while the video has no WAV and its
        # job carries no extract payload; extract's request always carries that payload, so
        # each asks the other at most once
        "transcribe",
        "extract",
        "youtube_fetch",
        "youtube_backfill",
    }  # fmt: skip
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

    Checks: unique names, no core stage named ``p_*`` (plugin stages' prefix), edge
    and request targets exist, pools exist, the
    non-legacy edge relation is acyclic, and request cycles only pass through
    stages with monotone input fingerprints.
    """
    names = [s.name for s in graph]
    if len(set(names)) != len(names):
        raise GraphError("duplicate stage names")
    known = set(names)
    pool_names = {p.pool for p in pools}
    for s in graph:
        # "p_" names belong to third-party plugin stages (Epic 54, story 54.01 §4): a core
        # or built-in extension stage never takes one, so the two can never collide
        if s.name.startswith("p_") and not (s.extension or "").startswith("plugin:"):
            raise GraphError(f"{s.name}: names starting p_ are kept for plugin stages")
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
