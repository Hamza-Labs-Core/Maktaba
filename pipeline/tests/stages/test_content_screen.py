"""``content_screen`` on the engine (Epic 57, story 57.17): a stored transcript in, the
video's detected tier out, in ``video_content_signals``.

No test asks a real model: a scripted one stands in (``stage.ASK``).

On a database: the integration tier (SQLite; Postgres with ``MAKTABA_TEST_PG_URL``)."""

from __future__ import annotations

import json
from collections.abc import Iterator, Mapping, Sequence
from typing import Any
from uuid import uuid4

import pytest
from maktaba_fp_transcription.stages import transcribe as tr

from maktaba_pipeline.classify import content_screen as screen
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import is_pg, loads, now, to_ts
from maktaba_pipeline.engine.stages import content_screen as stage
from maktaba_pipeline.llm import NoModel, RuntimeFailed
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.stages.world import core_runtime, drain_core, set_rules

pytestmark = pytest.mark.integration

MODEL = "home-model-a"


def answer(tier: int | None, confidence: float, **levels: Any) -> str:
    """A model's answer: ``violence=(2, [12, 40])`` gives a category its level and times."""
    cats = {}
    for name in screen.CATEGORIES:
        level, times = levels.get(name, (0, []))
        cats[name] = {"level": level, "evidence": times}
    return json.dumps({"tier": tier, "confidence": confidence, "categories": cats})


class FakeModel:
    """Answers by the first of its phrases found in the question; counts what it is asked."""

    def __init__(self, script: Mapping[str, str | Exception], default: str | None = None) -> None:
        self.script = dict(script)
        self.default = default
        self.calls: list[tuple[str, str]] = []

    async def __call__(self, model: str, messages: Sequence[Mapping[str, Any]]) -> str:
        text = str(messages[-1]["content"])
        self.calls.append((model, text))
        for phrase, reply in self.script.items():
            if phrase in text:
                if isinstance(reply, Exception):
                    raise reply
                return reply
        if self.default is None:
            raise AssertionError(f"the fake model has no answer for: {text[:200]}")
        return self.default


@pytest.fixture(autouse=True)
def clean_stage() -> Iterator[None]:
    stage.ASK = None
    stage._NO_MODEL_AT.clear()
    stage._LAST_PLAN.clear()
    yield
    stage.ASK = None
    stage._NO_MODEL_AT.clear()
    stage._LAST_PLAN.clear()


async def _engine(db: Database, model: str | None = MODEL) -> Any:
    rt = await core_runtime(db)
    if model is not None:
        await set_rules(db, "tagcheck", {"enabled": True, "model": model})
    return rt


async def _video(db: Database, title: str, lib: Any = None) -> tuple[Any, Any]:
    lib = lib or await make_library(db)
    return await make_video(db, lib, title=title), lib


async def _transcript(
    db: Database, vid: Any, sentences: Sequence[str], *, language: str = "en", step: float = 10.0
) -> str:
    await db.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(vid))
    tid = str(uuid4())
    track = await tr._first_audio_track(db, vid)
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
        " word_level, diarized, is_active, metadata)"
        " VALUES ($1, $2, $4, $3, 'mlx-batched', 'whisper-test', false, false, true, '{}')",
        tid, str(vid), language, track,
    )  # fmt: skip
    for seq, text in enumerate(sentences):
        await db.execute(
            "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text,"
            " confidence, metadata) VALUES ($1, $2, $3, $4, $5, 0.9, '{}')",
            tid, seq, seq * step, seq * step + step - 1, text,
        )  # fmt: skip
    return tid


async def _screen(db: Database, rt: Any, vid: Any) -> dict[str, Any]:
    """Run the stage for the video; the job's summary."""
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="content_screen")
    assert await drain_core(db, rt, only={"content_screen"}) == ["content_screen"]
    row = await db.fetchrow(
        "SELECT state, output_summary FROM processing_jobs WHERE stage = 'content_screen'"
        " AND subject_id = $1 ORDER BY id DESC LIMIT 1",
        str(vid),
    )
    assert row["state"] == "done"
    return dict(loads(row["output_summary"]) or {})


async def _row(db: Database, vid: Any) -> dict[str, Any] | None:
    r = await db.fetchrow("SELECT * FROM video_content_signals WHERE video_id = $1", str(vid))
    if r is None:
        return None
    d = dict(r)
    d["categories"] = loads(d["categories"]) or {}
    d["confidence"] = round(float(d["confidence"]), 3)
    return d


def test_the_stage_follows_the_second_listen_on_the_home() -> None:
    by = {s.name: s for s in GRAPH}
    assert "content_screen" in {e.to for e in by["second_listen"].edges}
    st = by["content_screen"]
    assert st.placement == "home" and set(st.subject_kinds) == {"video", "library"}
    # no address of a model server is this stage's to read
    assert st.model_rules is None and st.relay is None and not st.external_write


# --- T01: fixed English and Arabic transcripts, their tiers, and the hash skip -----------

ENGLISH = {
    "The Tortoise and the Hare": (
        [
            "Once upon a time a tortoise and a hare agreed to race.",
            "The hare ran ahead and took a nap under a tree.",
            "Slow and steady, the tortoise won the race.",
        ],
        answer(0, 0.95),
        0,
    ),  # fmt: skip
    "Cold case: the harbour murders": (
        [
            "The detective describes how the victim was stabbed again and again.",
            "Blood covered the floor of the warehouse, he says.",
            "The killer had been drinking heavily that night.",
        ],
        answer(
            16, 0.9, violence=(3, [0, 10]), drugs_alcohol=(1, [20]), frightening_scenes=(2, [10])
        ),
        16,
    ),  # fmt: skip
}
ARABIC = {
    "حكاية الأرنب والسلحفاة": (
        [
            "كان يا ما كان أرنب وسلحفاة اتفقا على سباق.",
            "نام الأرنب تحت الشجرة وهو واثق من الفوز.",
            "ووصلت السلحفاة أولاً بالصبر والمثابرة.",
        ],
        answer(0, 0.93),
        0,
    ),  # fmt: skip
    "شهادات من الحرب": (
        [
            "يروي الشاهد كيف قُتل المدنيون أمام عينيه في القصف.",
            "كانت الجثث في الشوارع والدماء في كل مكان.",
            "لم ينم الأطفال من الخوف أسابيع.",
        ],
        answer(
            16, 0.88, violence=(3, [0, 10]), frightening_scenes=(2, [20]), mature_themes=(2, [0])
        ),
        16,
    ),  # fmt: skip
}


@pytest.mark.asyncio
@pytest.mark.parametrize(("language", "cases"), [("en", ENGLISH), ("ar", ARABIC)])
async def test_t01_fixed_transcripts_get_their_tiers_and_are_asked_once(
    db: Database, language: str, cases: dict[str, tuple[list[str], str, int]]
) -> None:
    rt = await _engine(db)
    # each title's first sentence is what the fake model recognises
    fake = FakeModel({sentences[0]: reply for sentences, reply, _ in cases.values()})
    stage.ASK = fake
    lib = await make_library(db)
    videos = {}
    for title, (sentences, _reply, _tier) in cases.items():
        vid = await make_video(db, lib, title=title)
        videos[title] = (vid, await _transcript(db, vid, sentences, language=language))
    for title, (sentences, reply, tier) in cases.items():
        vid, tid = videos[title]
        summary = await _screen(db, rt, vid)
        assert summary["content_screen"] == "written" and summary["tier"] == tier
        row = await _row(db, vid)
        assert row is not None
        want = json.loads(reply)
        assert row["tier"] == tier and row["confidence"] == want["confidence"]
        assert row["not_checked"] is None and row["model"] == MODEL and row["input_hash"]
        assert row["reviewed_at"] is None and row["reviewed_by"] is None
        assert set(row["categories"]) == set(screen.CATEGORIES)
        for name, entry in want["categories"].items():
            got = row["categories"][name]
            assert got["level"] == entry["level"]
            assert got["evidence"] == [float(t) for t in entry["evidence"]][:3]
            assert len(got["evidence"]) <= 3
        # the model saw the transcript with its times, the title and the language
        asked = [text for _m, text in fake.calls if sentences[0] in text]
        assert len(asked) == 1
        assert f"Title: {title}" in asked[0] and f"Transcript language: {language}" in asked[0]
        assert f"[10] {sentences[1]}" in asked[0]
    calls = len(fake.calls)
    assert calls == len(cases) and {m for m, _ in fake.calls} == {MODEL}

    # the same transcript and the same model again: the model is not asked, the row stands
    for title in cases:
        vid, _tid = videos[title]
        before = await _row(db, vid)
        summary = await _screen(db, rt, vid)
        assert summary["content_screen"] == "unchanged"
        assert await _row(db, vid) == before
    assert len(fake.calls) == calls

    # a corrected transcript (its revision moved) is a new input: asked once more
    title = next(iter(cases))
    vid, tid = videos[title]
    await db.execute(
        "UPDATE transcripts SET revision = COALESCE(revision, 0) + 1 WHERE id = $1", tid
    )
    before = await _row(db, vid)
    assert (await _screen(db, rt, vid))["content_screen"] == "written"
    after = await _row(db, vid)
    assert len(fake.calls) == calls + 1
    assert before is not None and after is not None
    assert after["input_hash"] != before["input_hash"]


# --- T02: nothing to read ------------------------------------------------------------------


@pytest.mark.asyncio
async def test_t02_no_transcript_and_an_unsupported_language_are_not_checked(db: Database) -> None:
    rt = await _engine(db)
    fake = FakeModel({})
    stage.ASK = fake
    bare, lib = await _video(db, "No transcript yet")
    french = await make_video(db, lib, title="Le petit prince")
    await _transcript(db, french, ["Dessine-moi un mouton."], language="fr")
    empty = await make_video(db, lib, title="Silent film")
    await _transcript(db, empty, ["", "   "])

    for vid, why in ((bare, "no_transcript"), (french, "unsupported_language"),
                     (empty, "no_transcript")):  # fmt: skip
        summary = await _screen(db, rt, vid)
        assert summary == {"content_screen": "not_checked", "detail": why}
        row = await _row(db, vid)
        assert row is not None
        assert row["tier"] is None and row["not_checked"] == why
        assert row["confidence"] == 0 and row["categories"] == {} and row["model"] == ""
        # and again: nothing is rewritten
        assert (await _screen(db, rt, vid))["content_screen"] == "unchanged"
    assert fake.calls == []

    # the transcript arrives: the video is asked about and the row says so
    fake.script["A calm talk"] = answer(0, 0.9)
    await _transcript(db, bare, ["A calm talk about gardening."])
    assert (await _screen(db, rt, bare))["content_screen"] == "written"
    row = await _row(db, bare)
    assert row is not None and row["tier"] == 0 and row["not_checked"] is None
    # a regional code of a supported language is that language
    await db.execute("UPDATE transcripts SET language = 'en-GB' WHERE video_id = $1", str(french))
    fake.script["Dessine"] = answer(0, 0.9)
    assert (await _screen(db, rt, french))["content_screen"] == "written"


# --- long transcripts ------------------------------------------------------------------------


def _long(marks: dict[int, str], n: int = 30) -> list[str]:
    """``n`` sentences of filler; sentence ``i`` of ``marks`` carries a phrase the fake
    model recognises."""
    return [marks.get(i, "") + " filler " * 55 + f"sentence {i}." for i in range(n)]


@pytest.mark.asyncio
async def test_a_long_transcript_takes_the_strictest_window_that_passes_the_line(
    db: Database,
) -> None:
    rt = await _engine(db)
    sentences = _long({0: "OPENING", 14: "MIDDLE", 28: "ENDING"})
    spans = screen.windows([(i * 10.0, i * 10.0 + 9, s) for i, s in enumerate(sentences)])
    assert len(spans) >= 3
    # which window holds which mark
    place = {m: next(i for i, w in enumerate(spans) if m in w.text) for m in
             ("OPENING", "MIDDLE", "ENDING")}  # fmt: skip
    assert len(set(place.values())) == 3
    mid, end = spans[place["MIDDLE"]], spans[place["ENDING"]]
    fake = FakeModel(
        {
            # sure of little; unsure of 18; sure of 13
            "OPENING": answer(0, 0.9),
            "MIDDLE": answer(18, 0.5, sexual_content=(3, [mid.start, mid.start + 10])),
            "ENDING": answer(13, 0.85, violence=(2, [end.start, 5.0, 99999, end.start + 10,
                                                     end.start + 20, end.start + 30])),
        },
        default=answer(None, 0.2),
    )  # fmt: skip
    stage.ASK = fake
    vid, _lib = await _video(db, "A long documentary")
    await _transcript(db, vid, sentences)
    summary = await _screen(db, rt, vid)
    assert summary["windows"] == len(spans) == summary["asked"] == len(fake.calls)
    row = await _row(db, vid)
    assert row is not None
    assert (row["tier"], row["confidence"]) == (13, 0.85)
    # each question says which part it is
    assert f"Part 1 of {len(spans)}" in fake.calls[0][1]
    # the unsure window's category is still kept for the reviewer, with its times
    assert row["categories"]["sexual_content"] == {
        "level": 3,
        "evidence": [mid.start, mid.start + 10],
    }
    # times outside the window were made up and are dropped; three are kept
    assert row["categories"]["violence"] == {
        "level": 2, "evidence": [end.start, end.start + 10, end.start + 20]}  # fmt: skip

    # the home lowers its line: asked again (a new transcript), the 18 now passes
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)",
        screen.LINE_KEY, "0.5", to_ts(db, now()),
    )  # fmt: skip
    await db.execute("UPDATE transcripts SET revision = 7 WHERE video_id = $1", str(vid))
    await _screen(db, rt, vid)
    row = await _row(db, vid)
    assert row is not None and (row["tier"], row["confidence"]) == (18, 0.5)


@pytest.mark.asyncio
async def test_no_window_passing_keeps_the_strictest_guess_with_its_low_confidence(
    db: Database,
) -> None:
    rt = await _engine(db)
    stage.ASK = FakeModel({"OPENING": answer(9, 0.4), "ENDING": answer(16, 0.6)},
                          default=answer(None, 0.9))  # fmt: skip
    vid, _lib = await _video(db, "An unclear film")
    await _transcript(db, vid, _long({0: "OPENING", 28: "ENDING"}))
    await _screen(db, rt, vid)
    row = await _row(db, vid)
    # below the line: the API does not count it, the review list shows it
    assert row is not None and (row["tier"], row["confidence"]) == (16, 0.6)


@pytest.mark.asyncio
async def test_an_answer_that_is_not_the_json_asked_for_gives_no_tier(db: Database) -> None:
    rt = await _engine(db)
    stage.ASK = FakeModel({}, default="I cannot rate this video, sorry.")
    vid, _lib = await _video(db, "A talk")
    await _transcript(db, vid, ["A talk about many things."])
    summary = await _screen(db, rt, vid)
    assert summary["content_screen"] == "written" and summary["detail"] == "no usable answer"
    row = await _row(db, vid)
    assert row is not None and row["tier"] is None and row["confidence"] == 0
    assert row["not_checked"] is None and row["model"] == MODEL


def test_an_answer_is_held_to_its_shape() -> None:
    w = screen.Window(100.0, 200.0, "")
    got = screen.parse_answer(
        '```json\n{"tier": 12, "confidence": 7, "categories": {"violence": {"level": 9,'
        ' "evidence": [150, "x", 150, 500, true]}, "gambling": {"level": 3},'
        ' "mature_themes": 2}}\n```',
        w,
    )
    assert got is not None
    assert got.tier is None  # 12 is not a tier of the ladder
    assert got.confidence == 1.0
    assert got.categories["violence"] == {"level": 3, "evidence": [150.0]}
    assert got.categories["mature_themes"] == {"level": 2, "evidence": []}
    assert set(got.categories) == set(screen.CATEGORIES)
    assert screen.parse_answer("no json here", w) is None
    assert screen.parse_answer('{"rating": 18}', w) is None
    assert screen.parse_answer('{"tier": 18}', w) == screen.Answer(
        18, 0.0, {c: {"level": 0, "evidence": []} for c in screen.CATEGORIES}
    )
    # a tier with no confidence never passes a line above 0
    assert screen.combine([screen.Answer(18, 0.0), screen.Answer(9, 0.9)], 0.8).tier == 9


# --- music and recitation ---------------------------------------------------------------------


@pytest.mark.asyncio
async def test_music_and_recitation_are_not_asked_about(db: Database) -> None:
    rt = await _engine(db)
    fake = FakeModel({}, default=answer(13, 0.9))
    stage.ASK = fake
    lib = await make_library(db)
    nasheed = await make_video(db, lib, title="Nasheed")
    await db.execute("UPDATE videos SET content_type = 'music_video' WHERE id = $1", str(nasheed))
    recitation = await make_video(db, lib, title="سورة البقرة")
    lecture = await make_video(db, lib, title="Tafsir of Surat al-Baqarah")
    for vid, tags in (
        (
            recitation,
            {"topic:quran_recitation": 0.9, "topic:tafsir": 0.2, "format:recitation": 0.8},
        ),
        # a lesson that quotes the Quran is a lesson
        (lecture, {"topic:tafsir": 0.9, "topic:quran_recitation": 0.3, "format:lecture": 0.8}),
    ):
        for tag, score in tags.items():
            await db.execute(
                "INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,$2,$3)",
                str(vid), tag, score,
            )  # fmt: skip
    for vid in (nasheed, recitation, lecture):
        await _transcript(db, vid, ["Words that would be read."], language="ar")

    for vid in (nasheed, recitation):
        summary = await _screen(db, rt, vid)
        assert summary == {"content_screen": "skipped", "detail": "music or recitation"}
        assert await _row(db, vid) is None
    assert fake.calls == []
    assert (await _screen(db, rt, lecture))["content_screen"] == "written"
    assert len(fake.calls) == 1 and "Tags: format:lecture" in fake.calls[0][1]

    # a video found to be recitation after it got a row loses the row (the library's tier
    # applies again)
    await db.execute("DELETE FROM video_topic_tags WHERE video_id = $1", str(lecture))
    await db.execute(
        "INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,'format:recitation',0.9)",
        str(lecture),
    )
    summary = await _screen(db, rt, lecture)
    assert summary["detail"] == "music or recitation, row removed"
    assert await _row(db, lecture) is None and len(fake.calls) == 1


# --- a home with no model ---------------------------------------------------------------------


@pytest.mark.asyncio
async def test_a_home_with_no_model_gets_no_detected_tiers(db: Database) -> None:
    # the home's language model checks are off: nothing is asked
    rt = await _engine(db, model=None)
    fake = FakeModel({}, default=answer(18, 0.99))
    stage.ASK = fake
    vid, lib = await _video(db, "A film")
    await _transcript(db, vid, ["Something strong."])
    assert (await _screen(db, rt, vid))["content_screen"] == "no_model"
    assert await _row(db, vid) is None and fake.calls == []
    plan = await stage.plan_library(db, lib)
    assert plan.summary == {"content_screen": "no_model"}

    # they are on, but no machine of the home has the model
    await set_rules(db, "tagcheck", {"enabled": True, "model": MODEL})
    stage.ASK = FakeModel({}, default="")
    stage.ASK.script["Something strong"] = NoModel("no machine has home-model-a")
    summary = await _screen(db, rt, vid)
    assert summary["content_screen"] == "no_model"
    assert await _row(db, vid) is None
    # the job is done, not failed: a home without a model is not a broken home
    states = {r["state"] for r in await db.fetch(
        "SELECT state FROM processing_jobs WHERE stage = 'content_screen'")}  # fmt: skip
    assert states == {"done"}
    # and this worker plans nothing for a while
    assert (await stage.plan_library(db, lib)).summary["detail"] == "held"

    # the model arrives: the same video is asked about (no earlier run stands in its way)
    stage._NO_MODEL_AT.clear()
    stage.ASK = FakeModel({}, default=answer(16, 0.9))
    assert (await _screen(db, rt, vid))["content_screen"] == "written"
    row = await _row(db, vid)
    assert row is not None and row["tier"] == 16


@pytest.mark.asyncio
async def test_a_model_that_fails_fails_the_job_and_keeps_the_row(db: Database) -> None:
    rt = await _engine(db)
    stage.ASK = FakeModel({}, default=answer(9, 0.9))
    vid, _lib = await _video(db, "A film")
    await _transcript(db, vid, ["First words."])
    await _screen(db, rt, vid)
    before = await _row(db, vid)
    await db.execute("UPDATE transcripts SET revision = 3 WHERE video_id = $1", str(vid))
    stage.ASK = FakeModel({}, default="")
    stage.ASK.script["First words"] = RuntimeFailed("the runtime crashed")
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="content_screen")
    await drain_core(db, rt, only={"content_screen"}, max_jobs=1)
    job = await db.fetchrow(
        "SELECT state, attempts, error FROM processing_jobs WHERE stage = 'content_screen'"
        " AND subject_id = $1 ORDER BY id DESC LIMIT 1",
        str(vid),
    )
    assert job["state"] != "done" and "runtime crashed" in str(job["error"])
    assert await _row(db, vid) == before


# --- a change of model ------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_a_new_model_asks_about_every_video_again_at_low_priority(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await _engine(db)

    async def reachable(model: str) -> bool:
        return True

    monkeypatch.setattr(screen, "model_reachable", reachable)
    stage.ASK = FakeModel({}, default=answer(9, 0.9))
    lib = await make_library(db)
    vids = [await make_video(db, lib, title=f"Film {i}") for i in range(3)]
    for vid in vids:
        await _transcript(db, vid, [f"Words of {vid}."])
    nasheed = await make_video(db, lib, title="Nasheed")
    await db.execute("UPDATE videos SET content_type = 'music_video' WHERE id = $1", str(nasheed))
    untranscribed = await make_video(db, lib, title="Not transcribed")

    # the first plan: every video that has no row, behind every other stage
    plan = await stage.plan_library(db, lib)
    assert plan.summary == {"model": MODEL, "new": 4, "model_changed": 0}
    jobs = await db.fetch(
        "SELECT subject_id, priority, state FROM processing_jobs WHERE stage = 'content_screen'"
    )
    assert {str(j["subject_id"]) for j in jobs} == {str(v) for v in (*vids, untranscribed)}
    assert {j["priority"] for j in jobs} == {stage.BACKFILL_PRIORITY}
    assert stage.BACKFILL_PRIORITY > 100
    # planning twice enqueues nothing twice
    assert (await stage.plan_library(db, lib)).summary["new"] == 0
    await drain_core(db, rt, only={"content_screen"})
    assert all([(r := await _row(db, v)) is not None and r["tier"] == 9 for v in vids])
    # everything has its row: nothing to plan
    assert (await stage.plan_library(db, lib)).outcome == "unchanged"

    # the home changes its model: every asked video again; a not-checked one has nothing
    # a model could change
    await set_rules(db, "tagcheck", {"enabled": True, "model": "home-model-b"})
    stage.ASK = FakeModel({}, default=answer(16, 0.95))
    plan = await stage.plan_library(db, lib)
    assert plan.summary == {"model": "home-model-b", "new": 0, "model_changed": 3}
    pending = await db.fetch(
        "SELECT subject_id, priority FROM processing_jobs WHERE stage = 'content_screen'"
        " AND state = 'pending'"
    )
    assert {str(j["subject_id"]) for j in pending} == {str(v) for v in vids}
    assert {j["priority"] for j in pending} == {stage.BACKFILL_PRIORITY}
    # until its job runs, a video keeps the tier the old model gave it
    for vid in vids:
        row = await _row(db, vid)
        assert row is not None and (row["tier"], row["model"]) == (9, MODEL)
    await drain_core(db, rt, only={"content_screen"}, max_jobs=1)
    rows = [await _row(db, v) for v in vids]
    assert sorted((r["tier"], r["model"]) for r in rows if r is not None) == [
        (9, MODEL), (9, MODEL), (16, "home-model-b")]  # fmt: skip
    await drain_core(db, rt, only={"content_screen"})
    for vid in vids:
        row = await _row(db, vid)
        assert row is not None and (row["tier"], row["model"]) == (16, "home-model-b")
    assert {m for m, _ in stage.ASK.calls} == {"home-model-b"}

    # nothing is planned while no model can be reached
    async def unreachable(model: str) -> bool:
        return False

    monkeypatch.setattr(screen, "model_reachable", unreachable)
    await set_rules(db, "tagcheck", {"enabled": True, "model": "home-model-c"})
    assert (await stage.plan_library(db, lib)).summary["detail"] == "not reachable"


@pytest.mark.asyncio
async def test_a_transcript_that_arrives_another_way_is_planned(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A merge of two copies moves the loser's transcript to the keeper with no stage
    run: the keeper's row still says there is none."""
    rt = await _engine(db)

    async def reachable(model: str) -> bool:
        return True

    monkeypatch.setattr(screen, "model_reachable", reachable)
    stage.ASK = FakeModel({}, default=answer(16, 0.9))
    vid, lib = await _video(db, "The keeper")
    blank = await make_video(db, lib, title="Silent")
    await _transcript(db, blank, ["", " "])
    for v in (vid, blank):
        assert (await _screen(db, rt, v))["detail"] == "no_transcript"
    assert (await stage.plan_library(db, lib)).outcome == "unchanged"
    await _transcript(db, vid, ["Words that came with the other copy."])
    plan = await stage.plan_library(db, lib)
    assert plan.summary == {"model": MODEL, "new": 1, "model_changed": 0}
    await drain_core(db, rt, only={"content_screen"})
    row = await _row(db, vid)
    assert row is not None and (row["tier"], row["not_checked"]) == (16, None)
    # a transcript of nothing is still nothing to read: not planned again and again
    assert (await stage.plan_library(db, lib)).outcome == "unchanged"


@pytest.mark.asyncio
async def test_the_worker_plans_each_library_now_and_then(db: Database) -> None:
    await _engine(db)
    lib = await make_library(db)
    await stage.refresh(db, False)
    assert not await db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE stage = 'content_screen'")  # fmt: skip
    await stage.refresh(db, True)
    await stage.refresh(db, True)  # within the interval: once
    jobs = await db.fetch(
        "SELECT subject_kind, subject_id, priority FROM processing_jobs"
        " WHERE stage = 'content_screen'"
    )
    assert [(j["subject_kind"], str(j["subject_id"]), j["priority"]) for j in jobs] == [
        ("library", str(lib), stage.BACKFILL_PRIORITY)]  # fmt: skip


# --- the row's review and its writer ----------------------------------------------------------


@pytest.mark.asyncio
async def test_a_new_answer_is_unreviewed(db: Database) -> None:
    rt = await _engine(db)
    stage.ASK = FakeModel({}, default=answer(13, 0.9))
    vid, _lib = await _video(db, "A film")
    await _transcript(db, vid, ["Words."])
    await _screen(db, rt, vid)
    await db.execute(
        "UPDATE video_content_signals SET reviewed_at = $2 WHERE video_id = $1",
        str(vid), to_ts(db, now()),
    )  # fmt: skip
    # the same input: the review stands
    await _screen(db, rt, vid)
    row = await _row(db, vid)
    assert row is not None and row["reviewed_at"] is not None
    # a new transcript: the admin reviewed another answer
    await db.execute("UPDATE transcripts SET revision = 2 WHERE video_id = $1", str(vid))
    await _screen(db, rt, vid)
    row = await _row(db, vid)
    assert row is not None and row["reviewed_at"] is None


@pytest.mark.asyncio
async def test_the_row_is_written_as_the_stage_role_which_cannot_set_a_tier(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("database roles are Postgres's")
    if not await screen._role_usable(db):
        pytest.skip("this login may not use the stage's role")
    vid, lib = await _video(db, "A film")
    async with screen._AsStage(db):
        assert await db.fetchval("SELECT current_user") == screen.ROLE
        # what it reads
        await db.fetchval("SELECT count(*) FROM videos")
        await db.fetchval("SELECT count(*) FROM transcript_segments")
    for sql in (
        "UPDATE videos SET age_tier = 0, age_tier_source = 'detected'",
        "UPDATE videos SET age_tier_admin = 0",
        "UPDATE libraries SET age_tier = 0",
        "INSERT INTO app_settings (key, value) VALUES ('parental.detect_min_confidence', '0')",
        "DELETE FROM parental_recompute_jobs",
    ):
        with pytest.raises(Exception, match="permission denied"):
            async with screen._AsStage(db):
                await db.execute(sql)
    # and the login is itself again afterwards
    assert await db.fetchval("SELECT current_user") != screen.ROLE
    await screen._write(db, vid, tier=13, confidence=0.9, categories={}, not_checked=None,
                        model=MODEL, input_hash="h")  # fmt: skip
    row = await _row(db, vid)
    assert row is not None and row["tier"] == 13
    # the write queued the API's recompute (slot 0224's trigger), which sets the tier
    assert await db.fetchval(
        "SELECT count(*) FROM parental_recompute_jobs WHERE kind = 'video' AND subject = $1",
        str(vid),
    )
    assert await db.fetchval("SELECT age_tier FROM videos WHERE id = $1", str(vid)) is None
