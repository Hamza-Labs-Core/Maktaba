"""The scope checks the build works out for a read by id (README §15.12, write_scope.py).

Wherever a statement compares a column to a ``$N``, the ``$N`` picks whose row it reads:
an owner column's ``$N`` is checked by the API, an unlisted table or an untyped id-named
column fails the build, and only a ``[not_ids]`` entry with its reason lets one pass."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "tools" / "plugins"))
sys.path.insert(0, str(ROOT / "plugins" / "sdk" / "python"))

pytest.importorskip("pglast")

import statements_build as sb  # noqa: E402
import write_scope as ws  # noqa: E402
from maktaba_plugin_sdk.statements import ROW_SCOPES, parse_file, render_file  # noqa: E402

T = ws.load(kinds=("subject", "library", *ROW_SCOPES))


def checks(text: str) -> list[tuple[str, int]]:
    got, problems = ws.read_checks(text, T)
    assert problems == [], problems
    return list(got)


def problems(text: str) -> list[str]:
    return list(ws.read_checks(text, T)[1])


def test_a_read_by_id_is_checked_by_the_ids_kind() -> None:
    assert checks("SELECT name FROM series_seasons WHERE id = $1") == [("season", 1)]
    assert checks("SELECT title FROM videos WHERE id = $1::uuid") == [("subject", 1)]
    assert checks("SELECT text FROM transcript_segments WHERE transcript_id = $1") == [
        ("transcript", 1)
    ]
    assert checks("SELECT pause_requested FROM processing_jobs WHERE id = $1") == [("job", 1)]
    assert checks("SELECT name FROM libraries WHERE id = $1") == [("library", 1)]


def test_every_comparison_is_checked_wherever_it_stands() -> None:
    # a JOIN's ON, an OR, a subquery: each $N compared to an owner column is checked
    got = checks(
        "SELECT s.text FROM transcript_segments s JOIN transcripts t ON t.id = s.transcript_id"
        " AND t.video_id = $1 WHERE s.transcript_id = $2 OR s.id = $3"
    )
    assert got == [("subject", 1), ("transcript", 2), ("segment", 3)]
    got = checks(
        "SELECT v.title FROM videos v WHERE v.library_id = $1"
        " AND EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id AND e.series_id = $2)"
    )
    assert got == [("library", 1), ("series", 2)]


def test_arrays_lists_and_functions_of_ids_are_checked() -> None:
    assert checks("SELECT id FROM videos WHERE id = ANY($1::uuid[])") == [("subject", 1)]
    assert checks("SELECT id FROM videos WHERE id IN ($1, $2)") == [("subject", 1), ("subject", 2)]
    assert checks("SELECT id FROM videos WHERE id IN (SELECT value FROM json_each($1))") == [
        ("subject", 1)
    ]
    assert checks("SELECT id FROM videos WHERE id IS NOT DISTINCT FROM $1") == [("subject", 1)]


def test_a_correlated_subquery_reads_the_outer_alias() -> None:
    got = checks(
        "SELECT v.id FROM videos v WHERE EXISTS (SELECT 1 FROM transcripts t WHERE v.id = $1)"
    )
    assert got == [("subject", 1)]


def test_an_unqualified_column_is_the_one_table_that_lists_it() -> None:
    got = checks(
        "SELECT s.text FROM transcript_segments s JOIN chapters c ON true WHERE video_id = $1"
    )
    assert got == [("subject", 1)]


def test_a_jobs_subject_is_any_kind_of_subject() -> None:
    got = checks(
        "SELECT output_hash FROM processing_jobs WHERE subject_kind = $1 AND subject_id = $2"
    )
    assert got == [("job_subject", 2)]


def test_non_id_columns_ctes_and_home_tables_need_no_check() -> None:
    assert checks("SELECT id FROM videos WHERE library_id = $1 AND path = $2") == [("library", 1)]
    assert checks("WITH x AS (SELECT id FROM videos) SELECT * FROM x WHERE x.id = $1") == []
    assert checks("SELECT id FROM channels WHERE id = $1") == []
    # a public YouTube id, typed as no kind with its reason
    assert checks("SELECT library_id FROM youtube_downloads WHERE youtube_id = $1") == []


def test_an_unlisted_table_or_an_untyped_id_column_fails() -> None:
    (p,) = problems("SELECT * FROM some_new_table WHERE x = $1")
    assert "does not list" in p
    (p,) = problems("SELECT * FROM videos WHERE parent_id = $1")
    assert "no kind for videos.parent_id" in p
    # an id-named column no joined table lists: the build cannot tell whose it is
    (p,) = problems("SELECT 1 FROM chapters a, clip_checks b WHERE season_id = $1")
    assert "qualify it" in p


def test_not_ids_need_a_reason_and_a_listed_non_owner_column(tmp_path: Path) -> None:
    base = '[tables.videos]\nid = "subject"\n[home.books]\nreason = "r"\n'
    for extra, msg in (
        ('[not_ids]\n"videos.parent_id" = ""\n', "needs a reason"),
        ('[not_ids]\n"videos.id" = "r"\n', "is an owner column"),
        ('[not_ids]\n"nope.x_id" = "r"\n', r"not in \[tables\]"),
    ):
        f = tmp_path / "t.toml"
        f.write_text(base + extra)
        with pytest.raises(ValueError, match=msg):
            ws.load(f)


def test_a_reads_checks_are_in_its_header_and_round_trip() -> None:
    text = "SELECT s.text FROM transcript_segments s WHERE s.transcript_id = $2 AND s.id = $1"
    st = sb.Statement(
        "x.r", text, "read", "segment", 1, ("transcript_segments",), also=(("transcript", 2),)
    )
    back = parse_file(render_file(st))
    assert back.checks == (("segment", 1), ("transcript", 2))
    assert "-- scope: segment $1, transcript $2" in render_file(st)


def test_a_stale_read_header_fails_the_check(tmp_path: Path) -> None:
    """A read whose header lists fewer checks than its comparisons need fails ``check``."""
    plugin = tmp_path / "p"
    (plugin / "src" / "maktaba_fp_p").mkdir(parents=True)
    (plugin / "manifest.toml").write_text(
        'id = "tv.maktaba.p"\n[[stage]]\nname = "s"\nsubject_kinds = ["library"]\n'
        '[trusted.db]\nread = ["series_seasons"]\n'
    )
    (plugin / "src" / "maktaba_fp_p" / "__init__.py").write_text(
        "async def run(db, x):\n"
        '    return await db.fetch("SELECT name FROM series_seasons WHERE id = $1", x)\n'
    )
    assert sb.sync(plugin) == []
    q = next((plugin / "queries").glob("*.sql"))
    assert "-- scope: season $1" in q.read_text()
    q.write_text(q.read_text().replace("-- scope: season $1", "-- scope: catalog"))
    got = sb.check(plugin)
    assert any("the read needs season $1" in p for p in got), got


def test_every_first_party_read_is_typed() -> None:
    """The guard on the real plugins: every comparison of every statement is typed."""
    for d in sb.plugin_dirs():
        for st in sb.wanted(d)[0]:
            got, probs = sb.read_checks(st.text, d)
            assert probs == [] or st.name in sb._scopes(d), (d.name, st.name, probs)
            assert set(got) <= set(st.checks) or st.name in sb._scopes(d), (d.name, st.name)
