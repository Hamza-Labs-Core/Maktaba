"""A video's age tier worked out from its transcript (Epic 57, story 57.17).

The home's own language model reads the transcript with the video's title, description
and classification, and answers a tier (0, 9, 13, 16, 18, or none when it cannot tell), how
sure it is, and six categories with up to three transcript times each. The answer is
written to ``video_content_signals``; the API's recompute reads it as the ``detected``
source, which only ever tightens a video.

**The transcript never leaves the home.** The one way out of this module is the cluster
door (:func:`maktaba_pipeline.llm.cluster_llm`): a runtime on this machine, or a machine of
this home over the agent channel. There is no address setting, no HTTP client and no
plugin capability here; ``tests/classify/test_content_screen_home_only.py`` holds that.

What is asked, and of what, is kept beside the answer (``model``, ``input_hash``): the
same pair is never asked twice. A long transcript is asked in windows and the video takes
the strictest window that passes the confidence line. Music and Quran recitation are not
asked about at all (the library's own tier applies), a video with no transcript or one in
a language outside :data:`LANGUAGES` gets a row that says it was not checked, and a home
with no model gets no row.
"""

from __future__ import annotations

import contextlib
import json
import math
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.hashing import canonical_hash
from ..engine.sql import dumps, is_pg, loads, now, to_ts, uid
from ..llm import ClusterUnavailable, NoModel, cluster_llm

__all__ = [
    "CATEGORIES",
    "DEFAULT_LINE",
    "LANGUAGES",
    "LINE_KEY",
    "ROLE",
    "TIERS",
    "Answer",
    "Asker",
    "Outcome",
    "Window",
    "combine",
    "detect_line",
    "is_music",
    "model_name",
    "model_reachable",
    "parse_answer",
    "prompt",
    "screen_video",
    "windows",
]

#: the tiers a video can get (the parental ladder of slot 0220)
TIERS = (0, 9, 13, 16, 18)
#: what the model is asked about, in the order the row keeps them
CATEGORIES = (
    "violence",
    "sexual_content",
    "coarse_language",
    "drugs_alcohol",
    "frightening_scenes",
    "mature_themes",
)
#: transcript languages the model is asked about (the test set covers both)
LANGUAGES = ("ar", "en")
#: the app_settings key of the confidence a detected tier needs to count, and its default
#: (api/internal/parental/detected.go reads the same key the same way)
LINE_KEY = "parental.detect_min_confidence"
DEFAULT_LINE = 0.8
#: the database role the row is written under (slot 0224; tools/plugins/roles.py)
ROLE = "maktaba_fp_content_screen"
#: bump when the question or the way answers are combined changes: every video is asked again
PROMPT_VERSION = 1
#: transcript text per question (characters): a 7B model reads this much with room to answer
WINDOW_CHARS = 6000
#: transcript times kept per category
MAX_EVIDENCE = 3
#: of the description, what the model is shown
DESCRIPTION_CHARS = 600

NOT_CHECKED_NO_TRANSCRIPT = "no_transcript"
NOT_CHECKED_LANGUAGE = "unsupported_language"

#: ``(model, messages) -> the model's text``. The default is the cluster door.
Asker = Callable[[str, Sequence[Mapping[str, Any]]], Awaitable[str]]


@dataclass(frozen=True)
class Window:
    """A stretch of the transcript asked about in one question."""

    start: float
    end: float
    text: str


@dataclass
class Answer:
    """What the model said about one window, or about the whole video once combined."""

    tier: int | None = None
    confidence: float = 0.0
    #: category -> {"level": 0..3, "evidence": [seconds]}
    categories: dict[str, dict[str, Any]] = field(default_factory=dict)


@dataclass
class Outcome:
    """What a run did with a video (the job's summary)."""

    #: written | unchanged | skipped | no_model | not_checked
    state: str
    detail: str = ""
    tier: int | None = None
    confidence: float = 0.0
    windows: int = 0
    asked: int = 0
    input_hash: str = ""

    def summary(self) -> dict[str, Any]:
        out: dict[str, Any] = {"content_screen": self.state}
        if self.detail:
            out["detail"] = self.detail
        if self.state == "written":
            out.update(tier=self.tier, confidence=round(self.confidence, 3),
                       windows=self.windows, asked=self.asked)  # fmt: skip
        return out


# --- the question ------------------------------------------------------------------------

_SYSTEM = (
    "You rate videos for a family's home video library. You are given a part of one"
    " video's transcript (each line starts with its time in seconds, in square brackets),"
    " with the video's title, description and classification. The transcript may be in"
    " Arabic or English. Decide the youngest age the part suits and how sure you are.\n"
    "Tiers: 0 = for everyone; 9 = mild peril or mild rude words; 13 = moderate violence,"
    " some coarse language, references to drugs or alcohol, frightening scenes;"
    " 16 = strong violence, strong language, sexual references, drug use;"
    " 18 = graphic violence, explicit sexual content, extreme content.\n"
    "Religious teaching, history and news told soberly are not mature by themselves:"
    " rate what is said and described, not the subject.\n"
    "Answer with one JSON object and nothing else:\n"
    '{"tier": 0|9|13|16|18|null, "confidence": 0.0-1.0, "categories": {'
    + ", ".join(f'"{c}": {{"level": 0-3, "evidence": [seconds]}}' for c in CATEGORIES)
    + "}}\n"
    "tier is null when the part does not let you tell. level is 0 none, 1 mild,"
    " 2 moderate, 3 strong. evidence is up to three times, in seconds, copied from the"
    " lines that show it; an empty list when the level is 0."
)


def prompt(meta: Mapping[str, Any], window: Window, index: int, total: int) -> list[dict[str, str]]:
    """The messages for one window."""
    head = [f"Title: {meta.get('title') or ''}"]
    description = str(meta.get("description") or "").strip()
    if description:
        head.append(f"Description: {description[:DESCRIPTION_CHARS]}")
    if meta.get("content_type"):
        head.append(f"Kind: {meta['content_type']}")
    if meta.get("tags"):
        head.append("Tags: " + ", ".join(meta["tags"]))
    head.append(f"Transcript language: {meta.get('language') or ''}")
    head.append(f"Part {index} of {total}, from {int(window.start)} s to {int(window.end)} s:")
    return [
        {"role": "system", "content": _SYSTEM},
        {"role": "user", "content": "\n".join(head) + "\n\n" + window.text},
    ]


def windows(segments: Sequence[tuple[float, float, str]], size: int = WINDOW_CHARS) -> list[Window]:
    """The transcript's sentences ``(start, end, text)`` cut into windows of about ``size``
    characters, each line carrying its start time. A sentence is never split."""
    out: list[Window] = []
    lines: list[str] = []
    used = 0
    first = last = 0.0
    for start, end, text in segments:
        text = " ".join(str(text or "").split())
        if not text:
            continue
        line = f"[{int(start)}] {text}"
        if lines and used + len(line) > size:
            out.append(Window(first, last, "\n".join(lines)))
            lines, used = [], 0
        if not lines:
            first = float(start)
        lines.append(line)
        used += len(line) + 1
        last = max(float(end), float(start))
    if lines:
        out.append(Window(first, last, "\n".join(lines)))
    return out


# --- the answer --------------------------------------------------------------------------

_OBJECT = re.compile(r"\{.*\}", re.DOTALL)


def _number(value: Any) -> float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    f = float(value)
    return f if math.isfinite(f) else None


def parse_answer(text: str, window: Window) -> Answer | None:
    """The model's answer for ``window``, or None when it is not the JSON asked for.

    Nothing the model says is trusted beyond its shape: a tier outside the ladder is no
    tier, the confidence is held to 0..1 (a tier with none given has confidence 0), a
    category outside the six is dropped, and a time outside the window is dropped (the
    model made it up)."""
    found = _OBJECT.search(text or "")
    if found is None:
        return None
    try:
        raw = json.loads(found.group(0))
    except ValueError:
        return None
    if not isinstance(raw, dict) or "tier" not in raw:
        return None
    tier_n = _number(raw.get("tier"))
    tier = int(tier_n) if tier_n is not None and tier_n in TIERS else None
    conf = _number(raw.get("confidence"))
    confidence = min(1.0, max(0.0, conf)) if conf is not None else 0.0
    cats: dict[str, dict[str, Any]] = {}
    given = raw.get("categories")
    for name in CATEGORIES:
        entry = given.get(name) if isinstance(given, dict) else None
        if isinstance(entry, dict):
            level_n = _number(entry.get("level"))
            evidence = entry.get("evidence")
        else:
            level_n = _number(entry)
            evidence = None
        level = int(min(3, max(0, level_n))) if level_n is not None else 0
        times: list[float] = []
        if level > 0 and isinstance(evidence, list):
            for item in evidence:
                t = _number(item)
                if t is None or t < window.start - 1 or t > window.end + 1:
                    continue
                t = round(t, 1)
                if t not in times:
                    times.append(t)
        cats[name] = {"level": level, "evidence": sorted(times)[:MAX_EVIDENCE]}
    return Answer(tier=tier, confidence=confidence, categories=cats)


def combine(answers: Sequence[Answer], line: float) -> Answer:
    """One answer for the video from its windows': the strictest tier among the windows
    at or above ``line``; when none passes, the strictest tier any window gave with that
    window's (low) confidence, which the API does not count; no tier when no window gave
    one. A category takes its highest level, with the times of the windows that gave it."""
    rated = [a for a in answers if a.tier is not None]
    passing = [a for a in rated if a.confidence >= line]
    pool = passing or rated
    out = Answer()
    if pool:
        top = max(a.tier for a in pool if a.tier is not None)
        out.tier = top
        out.confidence = max(a.confidence for a in pool if a.tier == top)
    for name in CATEGORIES:
        level = max((int(a.categories.get(name, {}).get("level", 0)) for a in answers), default=0)
        times: list[float] = []
        if level > 0:
            for a in answers:
                entry = a.categories.get(name, {})
                if int(entry.get("level", 0)) == level:
                    times.extend(float(t) for t in entry.get("evidence", []))
        out.categories[name] = {"level": level, "evidence": sorted(set(times))[:MAX_EVIDENCE]}
    return out


# --- the home's model --------------------------------------------------------------------


async def model_name(db: Any) -> str | None:
    """The home's language model (``rules.tagcheck``: the model its other checks ask), or
    None when the home turned its language model checks off. Only the name is read: the
    address beside it is never used here."""
    from .. import rules

    cfg, _changes, _bad = await rules.read(db, "tagcheck")
    if not getattr(cfg, "enabled", False):
        return None
    name = str(getattr(cfg, "model", "") or "").strip()
    return name or None


async def model_reachable(model: str) -> bool:
    """Whether asking could work at all: a runtime here has the model, or there is a
    channel to the home (which knows the machines that do)."""
    door = cluster_llm()
    with contextlib.suppress(Exception):  # failing detection: nothing local
        await door.local.runtimes.ensure_fresh()
    if door.local.has(model):
        return True
    transport = door.transport()
    return transport is not None and transport.llm_available()


async def _ask_door(model: str, messages: Sequence[Mapping[str, Any]]) -> str:
    result = await cluster_llm().chat(model, messages, temperature=0)
    return result.text


# --- the database ------------------------------------------------------------------------


async def detect_line(db: Any) -> float:
    """The confidence line; the default when the key is absent or not a number in 0..1."""
    raw = await db.fetchval("SELECT value FROM app_settings WHERE key = $1", LINE_KEY)
    if raw is None:
        return DEFAULT_LINE
    try:
        value = loads(raw)
    except ValueError:
        return DEFAULT_LINE
    f = _number(value)
    return f if f is not None and 0 <= f <= 1 else DEFAULT_LINE


def is_music(content_type: str | None, tags: Mapping[str, float]) -> bool:
    """Music or Quran recitation: the video's kind is ``music_video``, its strongest topic
    is recitation or nasheed, or its strongest format is recitation. A talk that quotes the
    Quran or mentions a nasheed is neither."""
    if (content_type or "") == "music_video":
        return True

    def strongest(prefix: str) -> str:
        own = {t: s for t, s in tags.items() if t.startswith(prefix)}
        return max(own, key=lambda t: (own[t], t)) if own else ""

    return strongest("topic:") in ("topic:quran_recitation", "topic:nasheed") or (
        strongest("format:") == "format:recitation"
    )


async def _video(db: Any, video_id: Any) -> Any:
    return await db.fetchrow(
        "SELECT id, library_id, title, description, content_type FROM videos"
        " WHERE id = $1 AND deleted_at IS NULL",
        uid(video_id),
    )


async def _tags(db: Any, video_id: Any) -> dict[str, float]:
    rows = await db.fetch(
        "SELECT tag, score FROM video_topic_tags WHERE video_id = $1", uid(video_id)
    )
    return {str(r["tag"]): float(r["score"] or 0.0) for r in rows}


async def _transcript(db: Any, video_id: Any) -> Any:
    return await db.fetchrow(
        "SELECT id, language, COALESCE(revision, 0) AS revision FROM transcripts"
        " WHERE video_id = $1 AND is_active",
        uid(video_id),
    )


async def _segments(db: Any, transcript_id: Any) -> list[tuple[float, float, str]]:
    rows = await db.fetch(
        "SELECT start_sec, end_sec, text FROM transcript_segments"
        " WHERE transcript_id = $1 ORDER BY seq",
        transcript_id,
    )
    return [(float(r["start_sec"]), float(r["end_sec"]), str(r["text"] or "")) for r in rows]


async def _row(db: Any, video_id: Any) -> Any:
    return await db.fetchrow(
        "SELECT tier, confidence, not_checked, model, input_hash FROM video_content_signals"
        " WHERE video_id = $1",
        uid(video_id),
    )


async def _role_usable(db: Any) -> bool:
    if not is_pg(db):
        return False
    try:
        return bool(
            await db.fetchval(
                "SELECT to_regrole($1::text) IS NOT NULL"
                " AND pg_has_role(current_user, $1::text, 'MEMBER')",
                ROLE,
            )
        )
    except Exception:  # noqa: BLE001 - an unreadable answer is "not usable"
        return False


class _AsStage:
    """A transaction whose statements run as the stage's own role where the database has
    it (Postgres, slot 0224): the role may write ``video_content_signals`` and nothing
    else, so no mistake here can set a video's tier."""

    def __init__(self, db: Any) -> None:
        self._db = db
        self._tx: Any = None
        self._switched = False

    async def __aenter__(self) -> None:
        usable = await _role_usable(self._db)
        self._tx = self._db.transaction()
        await self._tx.__aenter__()
        if usable:
            await self._db.execute(f"SET LOCAL ROLE {ROLE}")
            self._switched = True

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> Any:
        if self._switched and exc_type is None:
            await self._db.execute("RESET ROLE")
        return await self._tx.__aexit__(exc_type, exc, tb)


async def _write(
    db: Any,
    video_id: Any,
    *,
    tier: int | None,
    confidence: float,
    categories: Mapping[str, Any],
    not_checked: str | None,
    model: str,
    input_hash: str,
) -> None:
    """The video's row, replaced in place: the earlier answer stands until this one lands.
    A new answer is unreviewed (an admin's review was of the old one)."""
    async with _AsStage(db):
        await db.execute(
            "INSERT INTO video_content_signals (video_id, tier, confidence, categories,"
            " not_checked, model, input_hash, reviewed_by, reviewed_at, updated_at)"
            " VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, NULL, $8)"
            " ON CONFLICT (video_id) DO UPDATE SET tier = EXCLUDED.tier,"
            " confidence = EXCLUDED.confidence, categories = EXCLUDED.categories,"
            " not_checked = EXCLUDED.not_checked, model = EXCLUDED.model,"
            " input_hash = EXCLUDED.input_hash, reviewed_by = NULL, reviewed_at = NULL,"
            " updated_at = EXCLUDED.updated_at",
            uid(video_id),
            tier,
            float(confidence),
            dumps(dict(categories)),
            not_checked,
            model,
            input_hash,
            to_ts(db, now()),
        )


async def _remove(db: Any, video_id: Any) -> bool:
    async with _AsStage(db):
        had = await db.fetchval(
            "SELECT 1 FROM video_content_signals WHERE video_id = $1", uid(video_id)
        )
        if had:
            await db.execute("DELETE FROM video_content_signals WHERE video_id = $1", uid(video_id))
    return bool(had)


# --- one video ---------------------------------------------------------------------------


async def screen_video(db: Any, video_id: Any, *, ask: Asker | None = None) -> Outcome:
    """Work out the video's row. ``ask`` stands in for the model in tests; the default
    asks the home's model through the cluster door.

    Raises :class:`maktaba_pipeline.llm.LLMError` when the model was reachable and failed
    (the job is tried again); a home with no model is not an error."""
    video = await _video(db, video_id)
    if video is None:
        return Outcome("skipped", "no such video")
    tags = await _tags(db, video_id)
    if is_music(video["content_type"], tags):
        removed = await _remove(db, video_id)
        return Outcome("skipped", "music or recitation" + (", row removed" if removed else ""))
    have = await _row(db, video_id)
    transcript = await _transcript(db, video_id)
    segments = await _segments(db, transcript["id"]) if transcript is not None else []
    asked_windows = windows(segments)
    if transcript is None or not asked_windows:
        return await _not_checked(db, video_id, have, NOT_CHECKED_NO_TRANSCRIPT, {})
    language = str(transcript["language"] or "").lower().split("-")[0]
    if language not in LANGUAGES:
        return await _not_checked(
            db, video_id, have, NOT_CHECKED_LANGUAGE,
            {"transcript": str(transcript["id"]), "language": language},
        )  # fmt: skip
    model = await model_name(db)
    if model is None:
        return Outcome("no_model", "the home's language model checks are off")
    meta = {
        "title": str(video["title"] or ""),
        "description": str(video["description"] or ""),
        "content_type": str(video["content_type"] or ""),
        "tags": sorted(tags),
        "language": language,
    }
    input_hash = canonical_hash(
        {
            "transcript": str(transcript["id"]),
            "revision": int(transcript["revision"] or 0),
            "meta": meta,
            "model": model,
            "v": PROMPT_VERSION,
        }
    )
    if have is not None and have["input_hash"] == input_hash and have["not_checked"] is None:
        return Outcome("unchanged", "same input, same model", input_hash=input_hash)
    asker = ask or _ask_door
    answers: list[Answer] = []
    asked = 0
    for i, window in enumerate(asked_windows, 1):
        try:
            text = await asker(model, prompt(meta, window, i, len(asked_windows)))
        except (NoModel, ClusterUnavailable) as exc:
            # no machine of this home has the model: nothing is written, nothing fails
            return Outcome("no_model", str(exc)[:200])
        asked += 1
        got = parse_answer(text, window)
        if got is not None:
            answers.append(got)
    result = combine(answers, await detect_line(db))
    await _write(
        db, video_id, tier=result.tier, confidence=result.confidence,
        categories=result.categories, not_checked=None, model=model, input_hash=input_hash,
    )  # fmt: skip
    return Outcome(
        "written",
        "" if answers else "no usable answer",
        tier=result.tier,
        confidence=result.confidence,
        windows=len(asked_windows),
        asked=asked,
        input_hash=input_hash,
    )


async def _not_checked(
    db: Any, video_id: Any, have: Any, why: str, of: Mapping[str, Any]
) -> Outcome:
    input_hash = canonical_hash({"not_checked": why, **of})
    if have is not None and have["input_hash"] == input_hash and have["not_checked"] == why:
        return Outcome("unchanged", why, input_hash=input_hash)
    await _write(
        db, video_id, tier=None, confidence=0.0, categories={}, not_checked=why,
        model="", input_hash=input_hash,
    )  # fmt: skip
    return Outcome("not_checked", why, input_hash=input_hash)
