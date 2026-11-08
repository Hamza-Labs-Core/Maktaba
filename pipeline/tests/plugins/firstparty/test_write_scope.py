"""The scope checks the build works out for a write (README §15.12, tools/plugins/write_scope.py).

A write's rows must be tied to ids the plugin API checks against the job; the build reads
which from the parse tree and ``scope_tables.toml``, and fails one it cannot bind."""

from __future__ import annotations

import re
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
    got, problems = ws.write_checks(text, T)
    assert problems == [], problems
    return list(got)


def problems(text: str) -> list[str]:
    return list(ws.write_checks(text, T)[1])


def test_an_update_by_id_is_checked_by_the_ids_kind() -> None:
    assert checks("UPDATE series_seasons SET retired_at = $2 WHERE id = $1") == [("season", 1)]
    assert checks("UPDATE videos SET full_hash = NULL WHERE id = $1::uuid") == [("subject", 1)]
    assert checks("DELETE FROM duplicate_groups WHERE id = $1") == [("dup_group", 1)]
    assert checks("UPDATE youtube_videos SET description = $2 WHERE youtube_id = $1") == [
        ("youtube_video", 1)
    ]


def test_every_owner_term_of_the_where_is_checked() -> None:
    got = checks("UPDATE file_actions SET error = $3 WHERE id = $1 AND video_id = $2")
    assert got == [("file_action", 1), ("subject", 2)]


def test_an_update_its_rows_are_not_tied_to_is_refused() -> None:
    (p,) = problems("UPDATE series_seasons SET retired_at = $2 WHERE name = $1")
    assert "without tying its rows" in p
    # a term under OR binds nothing: the other side reaches every row
    (p,) = problems("DELETE FROM series_episodes WHERE video_id = $1 OR true")
    assert "without tying its rows" in p
    (p,) = problems("DELETE FROM relisten_attempts WHERE video_id IN (SELECT v.id FROM videos v)")
    assert "without tying its rows" in p


def test_arrays_and_bound_subqueries_bind_rows() -> None:
    assert checks("DELETE FROM series_episodes WHERE video_id = ANY($1::uuid[])") == [
        ("subject", 1)
    ]
    assert checks(
        "DELETE FROM series_episodes WHERE video_id IN (SELECT value FROM json_each($1))"
    ) == [("subject", 1)]
    assert checks(
        "DELETE FROM episode_tags WHERE video_id IN (SELECT v.id FROM videos v"
        " WHERE v.library_id = $1 AND NOT EXISTS (SELECT 1 FROM series_episodes e))"
    ) == [("library", 1)]
    # the subquery's column must be an id of the same kind
    assert (
        problems(
            "DELETE FROM episode_tags WHERE video_id IN (SELECT v.keeper_video_id FROM"
            " duplicate_groups v WHERE v.library_id = $1)"
        )
        == []
    )
    (p,) = problems(
        "DELETE FROM episode_tags WHERE video_id IN (SELECT s.id FROM series s"
        " WHERE s.library_id = $1)"
    )
    assert "without tying its rows" in p


def test_an_owner_column_an_update_sets_is_checked() -> None:
    got = checks(
        "UPDATE file_actions SET updated_at = $2,"
        " video_id = CASE WHEN $3 THEN $4 ELSE video_id END WHERE id = $1"
    )
    # the CASE's condition ($3) is no id; its value ($4) is
    assert got == [("file_action", 1), ("subject", 4)]
    assert checks("UPDATE duplicate_groups SET library_id = $2 WHERE id = $1") == [
        ("dup_group", 1),
        ("library", 2),
    ]
    (p,) = problems("UPDATE duplicate_groups SET library_id = md5($2)::uuid WHERE id = $1")
    assert "cannot check" in p


def test_an_insert_checks_every_owner_column() -> None:
    got = checks(
        "INSERT INTO promo_links (video_id, library_id, series_id, season_id, season, score,"
        " evidence, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
    )
    assert got == [("subject", 1), ("library", 2), ("series", 3), ("season", 4)]
    (p,) = problems("INSERT INTO season_tags (tag, score) VALUES ($1, $2)")
    assert "with none of" in p


def test_on_conflict_do_update_needs_an_owner_column_in_its_target() -> None:
    assert checks(
        "INSERT INTO series (id, library_id, name) VALUES ($1,$2,$3)"
        " ON CONFLICT (id) DO UPDATE SET library_id = EXCLUDED.library_id"
    ) == [("series", 1), ("library", 2)]
    # the conflict target is no owner column: the update may reach another library's row
    (p,) = problems(
        "INSERT INTO videos (library_id, content_hash, path) VALUES ($1,$2,$3)"
        " ON CONFLICT (content_hash) DO UPDATE SET path = EXCLUDED.path"
    )
    assert "may update another scope's row" in p
    # unless the update keeps to the inserted row's owner
    assert checks(
        "INSERT INTO videos (library_id, content_hash, path) VALUES ($1,$2,$3)"
        " ON CONFLICT (content_hash) DO UPDATE SET path = excluded.path"
        " WHERE videos.library_id = excluded.library_id"
    ) == [("library", 1)]
    # DO NOTHING never touches the row that is there
    assert checks(
        "INSERT INTO youtube_playlist_items (playlist_id, youtube_id) VALUES ($1,$2)"
        " ON CONFLICT DO NOTHING"
    ) == [("youtube_playlist", 1)]


def test_insert_select_is_bound_by_its_source_rows() -> None:
    assert checks(
        "INSERT INTO transcript_segments (transcript_id, seq, text) SELECT transcript_id, seq,"
        " text FROM transcript_segments WHERE transcript_id = $1"
    ) == [("transcript", 1)]
    (p, _) = problems(
        "INSERT INTO segment_embeddings (segment_id, video_id, library_id) SELECT u.s, $1, $2"
        " FROM unnest($3::bigint[]) AS u(s) ON CONFLICT (segment_id) DO UPDATE SET model = 'x'"
    )
    assert "cannot tie to a checked id" in p


def test_home_tables_need_no_check_and_unlisted_ones_fail() -> None:
    assert checks("DELETE FROM channels WHERE id = $1") == []
    (p,) = problems("DELETE FROM fp_not_listed WHERE id = $1")
    assert "does not list" in p


def test_a_header_carries_every_check_and_round_trips() -> None:
    text = (
        "-- name: promo_links.run.3\n-- mode: write\n"
        "-- scope: subject $1, library $2, series $3, season $4\n-- tables: promo_links*\n"
        "INSERT INTO promo_links (video_id, library_id, series_id, season_id)"
        " VALUES ($1,$2,$3,$4)\n"
    )
    st = parse_file(text)
    assert st.checks == (("subject", 1), ("library", 2), ("series", 3), ("season", 4))
    assert render_file(st) == text
    assert st.to_json()["also"] == [
        {"scope": "library", "arg": 2},
        {"scope": "series", "arg": 3},
        {"scope": "season", "arg": 4},
    ]


def test_the_kinds_are_the_ones_the_api_resolves() -> None:
    """Every kind in scope_tables.toml is one the Go executor looks up, and the other way."""
    go = (ROOT / "api" / "internal" / "plugins" / "statements" / "resolve.go").read_text()
    body = go.split("var rowKinds = map[string]rowKind{", 1)[1].split("\n}\n", 1)[0]
    in_go = set(re.findall(r'^\t"([a-z_]+)":', body, re.M))
    used = {k for cols in T.owners.values() for k in cols.values()} - {"subject", "library"}
    assert in_go == set(ROW_SCOPES)
    assert used == set(ROW_SCOPES)


def test_a_home_table_of_the_catalog_is_refused_to_a_library_plugin(tmp_path: Path) -> None:
    (tmp_path / "manifest.toml").write_text(
        'id = "tv.maktaba.x"\n[[stage]]\nname = "s"\nsubject_kinds = ["library"]\n'
        '[trusted.db]\nwrite = ["channels"]\n'
    )
    st = sb.Statement("x.w", "DELETE FROM channels WHERE id = $1", "write", writes=("channels",))
    got = sb.check_tables(tmp_path, [st])
    assert any("the home's" in p for p in got), got
    (tmp_path / "manifest.toml").write_text(
        'id = "tv.maktaba.x"\n[[stage]]\nname = "s"\nsubject_kinds = ["catalog"]\n'
        '[trusted.db]\nwrite = ["channels"]\n'
    )
    assert sb.check_tables(tmp_path, [st]) == []


def test_every_first_party_write_is_bound() -> None:
    """The guard on the real plugins: what ``check --all`` runs in CI."""
    for d in sb.plugin_dirs():
        assert sb.check(d) == [], d.name
