"""group step (clip / stand-alone groups, promo links) and export-plex's extra collections."""

from __future__ import annotations

import json
import sys
import urllib.parse
from pathlib import Path
from typing import Any, cast

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from scripts.mediaintel import db, export_plex, group_clips  # noqa: E402
from scripts.mediaintel.group_clips import (  # noqa: E402
    Item,
    Show,
    link_promo,
    name_words,
    plan_groups,
)
from scripts.mediaintel.scan_plex import PlexError, PlexServer  # noqa: E402

pytestmark = pytest.mark.unit


# --------------------------------------------------------------------------- group


def _item(path: str, dur: float = 60, pls: dict[str, int] | None = None, genre: str | None = None,
          clip: bool = True) -> Item:  # fmt: skip
    return Item(path, path, dur, "2020-01-01", dict(pls or {}), genre, clip)


def test_groups_prefer_the_smallest_playlist_then_topic_then_rest() -> None:
    items = [_item(f"a{i}", pls={"BIG": i, "SMALL": i}) for i in range(5)]
    items += [_item(f"b{i}", pls={"BIG": 10 + i}) for i in range(5)]
    items += [_item(f"c{i}", genre="fiqh") for i in range(10)]
    items += [_item(f"d{i}", genre="fiqh") for i in range(2)]  # joins the topic group
    items += [_item("e0", genre="seerah")]  # too few for a topic: rest
    items += [_item(f"s{i}", dur=1800, clip=False, genre="tafsir") for i in range(3)]
    groups = plan_groups(items, {"BIG": "مقاطع مختارة", "SMALL": "مقاطع الصيام"})
    by = {(g.kind, g.basis, g.name): g.items for g in groups}
    assert by[("clips", "playlist", "مقاطع الصيام")] == [f"a{i}" for i in range(5)]
    assert by[("clips", "playlist", "مقاطع مختارة")] == [f"b{i}" for i in range(5)]
    assert len(by[("clips", "topic", "الفقه")]) == 12
    assert by[("clips", "rest", "متنوعة")] == ["e0"]
    assert sorted(by[("standalone", "rest", "متنوعة")]) == ["s0", "s1", "s2"]
    placed = [p for g in groups for p in g.items]
    assert len(placed) == len(set(placed)) == len(items)  # one group per video


def test_promo_links_to_the_show_its_title_names() -> None:
    shows = [
        Show("ميديا", "Y:/a", "قصة وفكرة", "قصه وفكره", name_words("قصة وفكرة")),
        Show("الندى", "Y:/b", "أيام معلومات", "ايام معلومات", name_words("أيام معلومات")),
    ]
    hit = link_promo("برومو برنامج قصة وفكرة", "Y:/c", shows)
    assert hit is not None and hit[0].name == "قصة وفكرة" and "in ميديا" in hit[2]
    assert link_promo("برومو برنامج جديد", "Y:/c", shows) is None


def test_group_titles() -> None:
    assert group_clips.group_title("سبائك", "clips", "topic", "الفقه") == "سبائك - مقاطع - الفقه"
    assert group_clips.group_title("سبائك", "clips", "rest", "متنوعة") == "سبائك - مقاطع متنوعة"
    assert group_clips.group_title("X", "standalone", "playlist", "P") == "X - منفردات - P"


# --------------------------------------------------------------------------- export


class FakePlex:
    """Just enough of Plex's collection API for export-plex."""

    name, uri, token = "plex", "http://plex", "t"

    def __init__(self) -> None:
        self.cols: dict[str, dict[str, Any]] = {}
        self.titles: dict[str, str] = {}
        self.next = 1000
        self.deleted: list[str] = []
        self.refuse: set[str] = set()  # items Plex answers 400 for
        self.shuffle_adds = False  # Plex files added items in its own order
        self.moves = 0

    def add(self, title: str, kids: list[str]) -> str:
        self.next += 1
        key = str(self.next)
        self.cols[key] = {"title": title, "kids": list(kids), "labels": set()}
        return key

    def get(self, path: str) -> dict[str, Any]:
        return self.send("GET", path)

    def send(self, method: str, path: str) -> dict[str, Any]:
        u = urllib.parse.urlparse(path)
        q = urllib.parse.parse_qs(u.query)
        parts = u.path.strip("/").split("/")
        if u.path == "/identity":
            return {"MediaContainer": {"machineIdentifier": "m"}}
        if method == "GET" and parts[-1] == "collections":
            md = [{"title": c["title"], "ratingKey": k, "childCount": len(c["kids"])}
                  for k, c in self.cols.items()]  # fmt: skip
            return {"MediaContainer": {"Metadata": md}}
        if method == "GET" and parts[-1] == "children":
            kids = self.cols[parts[2]]["kids"]
            return {"MediaContainer": {"Metadata": [
                {"ratingKey": k, "title": self.titles.get(k, k)} for k in kids]}}  # fmt: skip
        if method == "POST" and u.path == "/library/collections":
            keys = q["uri"][0].rsplit("/", 1)[1].split(",")
            key = self.add(q["title"][0], keys)
            self.cols[key]["section"] = q["sectionId"][0]
            return {"MediaContainer": {"Metadata": [{"ratingKey": key}]}}
        if method == "PUT" and parts[-1] == "items":
            keys = q["uri"][0].rsplit("/", 1)[1].split(",")
            if self.refuse & set(keys):
                raise PlexError(method, path, 400, "Bad Request")
            kids = self.cols[parts[2]]["kids"]
            kids += keys
            if self.shuffle_adds:
                kids.sort(key=lambda k: int(k) % 7)  # an order of Plex's own
            return {}
        if method == "PUT" and parts[-1] == "move":
            self.moves += 1
            kids = self.cols[parts[2]]["kids"]
            cur = parts[4]
            kids.remove(cur)
            if "after" in q:
                kids.insert(kids.index(q["after"][0]) + 1, cur)
            else:
                kids.insert(0, cur)
            return {}
        if method == "PUT" and parts[-1] == "all":
            self.cols[q["id"][0]]["labels"].add(q["label[0].tag.tag"][0])
            return {}
        if method == "PUT" and parts[-1] == "prefs":
            return {}
        if method == "DELETE":
            self.deleted.append(parts[2])
            self.cols.pop(parts[2], None)
            return {}
        raise AssertionError(f"unexpected {method} {path}")


def _srv(fake: FakePlex) -> PlexServer:
    """FakePlex quacks like the PlexServer export-plex is typed for."""
    return cast(PlexServer, fake)


def _db(tmp_path: Path) -> Any:
    conn = db.connect(tmp_path / "mi.db")
    lib, folder = "قناة", "Y:/قناة"
    for i in range(1, 9):
        conn.execute(
            "INSERT INTO files (path, folder, stem, ext, size, mtime, duration_s, plex_rating_key,"
            " plex_section, plex_library, plex_title) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
            (f"{folder}/v{i}.mp4", folder, f"v{i}", ".mp4", 1, 1.0, 1800 if i < 7 else 60,
             str(i), "5", lib, f"حلقة {i}"),
        )  # fmt: skip
    meta = {"series_key": "برنامج", "kind": "episodic", "season": 1, "season_count": 1,
            "folder": folder, "extras": []}  # fmt: skip
    conn.execute(
        "INSERT INTO series (id, library, name, metadata, detector_version) VALUES (?,?,?,?,3)",
        ("S1", lib, "برنامج", json.dumps(meta)),
    )
    for n, i in enumerate((1, 2, 3), 1):
        conn.execute(
            "INSERT INTO series_episodes (series_id, path, season, episode) VALUES (?,?,1,?)",
            ("S1", f"{folder}/v{i}.mp4", n),
        )
    conn.execute(
        "INSERT INTO topic_collections VALUES ('topic:1', ?, ?, 'الشأن الفلسطيني', 'PL')",
        (lib, folder),
    )
    for n, i in enumerate((2, 4), 1):  # v4 is in no season, only in the topic
        conn.execute("INSERT INTO topic_items VALUES ('topic:1', ?, ?)", (f"{folder}/v{i}.mp4", n))
    conn.execute(
        "INSERT INTO clip_groups VALUES ('clips:1', ?, ?, 'clips', 'topic', 'الفقه', 'fiqh')",
        (lib, folder),
    )
    for n, i in enumerate((7, 8), 1):
        conn.execute(
            "INSERT INTO clip_group_items VALUES ('clips:1', ?, ?)", (f"{folder}/v{i}.mp4", n)
        )
    conn.commit()
    return conn


def test_export_writes_topic_and_clip_collections_with_the_label(tmp_path: Path) -> None:
    conn = _db(tmp_path)
    plex = FakePlex()
    # the user's own collection of the season: holds v4, which detection did not place
    plex.add("قناة - برنامج", ["1", "2", "4"])
    # a collection this job made for a season detection no longer has: all inside the new ones
    old = plex.add("قناة - مجزرة صبرا وشاتيلا", ["1", "2", "3"])
    export_plex.export(conn, _srv(plex), apply=False, library_filter=None, include_clips=False,
                       replace_existing=True, remove_superseded=False)  # fmt: skip
    conn.execute("INSERT INTO plex_collections (series_id, section, rating_key, title) "
                 "VALUES ('GONE', '5', ?, 'قناة - مجزرة صبرا وشاتيلا')", (old,))  # fmt: skip
    conn.commit()
    export_plex.export(conn, _srv(plex), apply=True, library_filter=None, include_clips=False,
                       replace_existing=True, remove_superseded=False)  # fmt: skip
    by_title = {c["title"]: c for c in plex.cols.values()}
    # v4 sits in a topic collection, which is not placement: it stays in the season's one
    assert by_title["قناة - برنامج"]["kids"] == ["1", "2", "3", "4"]
    assert by_title["قناة - ملف - الشأن الفلسطيني"]["kids"] == ["2", "4"]
    assert by_title["قناة - مقاطع - الفقه"]["kids"] == ["7", "8"]
    assert all(c["labels"] == {"maktaba-auto"} for c in plex.cols.values())
    assert old in plex.deleted and "قناة - مجزرة صبرا وشاتيلا" not in by_title
    tracked = {r[0] for r in conn.execute("SELECT series_id FROM plex_collections")}
    assert tracked == {"S1", "topic:1", "clips:1"}


def test_promo_links_to_the_season_it_names() -> None:
    show = Show("ميديا", "Y:/a", "قصة وفكرة", "قصه وفكره", name_words("قصة وفكرة"), (1, 2))
    assert group_clips.promo_season("برومو قصة وفكرة2", show) == 2
    assert group_clips.promo_season("برومو برنامج قصة وفكرة", show) == 1


def test_questions_and_fatwas_get_their_own_group() -> None:
    items = [_item(f"q{i}", dur=900, clip=False) for i in range(3)]
    for it in items:
        it.qa = True
    items += [_item("l0", dur=1800, clip=False, genre="fiqh")]
    groups = {(g.kind, g.basis, g.name): g.items for g in plan_groups(items, {})}
    assert groups[("standalone", "topic", "فتاوى ومسائل")] == ["q0", "q1", "q2"]
    assert groups[("standalone", "rest", "متنوعة")] == ["l0"]


def test_a_topic_named_like_a_playlist_group_gets_its_own_title() -> None:
    items = [_item(f"p{i}", pls={"PL": i}) for i in range(5)]
    items += [_item(f"f{i}", genre="fiqh") for i in range(10)]
    groups = plan_groups(items, {"PL": "الفقه"})
    names = sorted(g.name for g in groups if g.kind == "clips")
    assert names == ["الفقه", "الفقه (2)"]


def test_numbered_names_never_repeat_a_final_name() -> None:
    items = [_item(f"{p}{i}", pls={p: i}) for p in ("A", "B", "C") for i in range(5)]
    groups = plan_groups(items, {"A": "الفقه", "B": "الفقه", "C": "الفقه (2)"})
    names = [g.name for g in groups if g.kind == "clips"]
    assert sorted(names) == ["الفقه", "الفقه (2)", "الفقه (2) (2)"]
    assert len(set(names)) == 3


def test_a_phrase_with_a_trailing_space_matches_only_the_whole_word() -> None:
    from maktaba_pipeline.text.arabic import fold

    qa = group_clips.GroupConfig().qa_re()
    assert qa.search(fold("حكم صلاة الجماعة"))
    assert not qa.search(fold("حكمة اليوم"))
    assert qa.search(fold("ما رأيك في"))  # a phrase without the space matches word starts


def test_a_blocked_replacement_never_retires_the_old_collection(tmp_path: Path) -> None:
    conn = _db(tmp_path)
    plex = FakePlex()
    plex.add("قناة - برنامج", ["1", "2"])  # the user's own: without --replace-existing, kept
    old = plex.add("قناة - مجزرة صبرا وشاتيلا", ["1", "2", "3"])
    export_plex.export(conn, _srv(plex), apply=False, library_filter=None, include_clips=False,
                       replace_existing=False, remove_superseded=False)  # fmt: skip
    conn.execute("INSERT INTO plex_collections (series_id, section, rating_key, title) "
                 "VALUES ('GONE', '5', ?, 'قناة - مجزرة صبرا وشاتيلا')", (old,))  # fmt: skip
    conn.commit()
    export_plex.export(conn, _srv(plex), apply=True, library_filter=None, include_clips=False,
                       replace_existing=False, remove_superseded=False)  # fmt: skip
    # the season's collection was not written, so the old one is still the only home of v3
    assert old not in plex.deleted


def test_grouping_works_before_the_tag_step_and_keeps_unlinked_promos_apart(
    tmp_path: Path,
) -> None:
    conn = _db(tmp_path)
    conn.execute("DROP TABLE IF EXISTS video_tags")
    folder = "Y:/قناة"
    conn.execute(
        "INSERT INTO files (path, folder, stem, ext, size, mtime, duration_s, plex_rating_key,"
        " plex_section, plex_library, plex_title) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
        (f"{folder}/p.mp4", folder, "برومو برنامج لا يعرفه أحد", ".mp4", 1, 1.0, 40, "9", "5",
         "قناة", "برومو برنامج لا يعرفه أحد"),
    )  # fmt: skip
    conn.commit()
    group_clips.run(conn, folders=[folder])
    kinds = {
        (k, b)
        for k, b in conn.execute(
            "SELECT g.kind, g.basis FROM clip_group_items i JOIN clip_groups g ON g.id = i.group_id"
            " WHERE i.path = ?",
            (f"{folder}/p.mp4",),
        )
    }
    assert kinds == {("clips", "promo")}  # in the promos' collection, not an ordinary clip


def test_an_item_plex_refuses_stays_out_and_the_run_goes_on(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    conn = _db(tmp_path)
    plex = FakePlex()
    plex.refuse = {"3"}  # v3 is in the season only
    old = plex.add("قناة - مجزرة صبرا وشاتيلا", ["1", "2", "3"])
    export_plex.export(conn, _srv(plex), apply=False, library_filter=None, include_clips=False,
                       replace_existing=True, remove_superseded=False)  # fmt: skip
    conn.execute("INSERT INTO plex_collections (series_id, section, rating_key, title) "
                 "VALUES ('GONE', '5', ?, 'قناة - مجزرة صبرا وشاتيلا')", (old,))  # fmt: skip
    conn.commit()
    export_plex.export(conn, _srv(plex), apply=True, library_filter=None, include_clips=False,
                       replace_existing=True, remove_superseded=False)  # fmt: skip
    by_title = {c["title"]: c for c in plex.cols.values()}
    assert by_title["قناة - برنامج"]["kids"] == ["1", "2"]  # written without the refused one
    assert by_title["قناة - مقاطع - الفقه"]["kids"] == ["7", "8"]  # the next ones too
    out = capsys.readouterr().out
    assert "Plex refused" in out and "not fully written" in out
    # v3 sits in no written collection now, so the old one that holds it is kept
    assert old not in plex.deleted


def test_a_library_split_over_two_plex_sections_writes_each_collection_in_its_own(
    tmp_path: Path,
) -> None:
    conn = _db(tmp_path)
    folder = "Y:/قناة 2"
    for i in range(10, 16):  # a second Plex library of the same name, with its own show
        conn.execute(
            "INSERT INTO files (path, folder, stem, ext, size, mtime, duration_s, plex_rating_key,"
            " plex_section, plex_library, plex_title) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
            (f"{folder}/v{i}.mp4", folder, f"v{i}", ".mp4", 1, 1.0, 1800, str(i), "6", "قناة",
             f"حلقة {i}"),
        )  # fmt: skip
    meta = {"series_key": "مسابقة", "kind": "episodic", "season": 1, "season_count": 1,
            "folder": folder, "extras": []}  # fmt: skip
    conn.execute(
        "INSERT INTO series (id, library, name, metadata, detector_version) VALUES (?,?,?,?,3)",
        ("S2", "قناة", "مسابقة", json.dumps(meta)),
    )
    for n, i in enumerate(range(10, 16), 1):
        conn.execute(
            "INSERT INTO series_episodes (series_id, path, season, episode) VALUES (?,?,1,?)",
            ("S2", f"{folder}/v{i}.mp4", n),
        )
    conn.commit()
    plex = FakePlex()
    export_plex.export(conn, _srv(plex), apply=True, library_filter=None, include_clips=False,
                       replace_existing=True, remove_superseded=False)  # fmt: skip
    by_title = {c["title"]: c for c in plex.cols.values()}
    assert by_title["قناة - مسابقة"]["section"] == "6"
    assert by_title["قناة - مسابقة"]["kids"] == [str(i) for i in range(10, 16)]
    assert by_title["قناة - برنامج"]["section"] == "5"


def test_a_promo_in_a_clip_playlist_is_never_ordinary_filler(tmp_path: Path) -> None:
    from scripts.mediaintel import plan_channels

    conn = _db(tmp_path)
    folder = "Y:/قناة"
    for i, title in ((20, "برومو برنامج"), (21, "إعلان لا يعرف برنامجه"), (22, "مقطع")):
        conn.execute(
            "INSERT INTO files (path, folder, stem, ext, size, mtime, duration_s, plex_rating_key,"
            " plex_section, plex_library, plex_title) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
            (f"{folder}/v{i}.mp4", folder, title, ".mp4", 1, 1.0, 60, str(i), "5", "قناة", title),
        )  # fmt: skip
    meta = {"series_key": "مقاطع", "kind": "clips", "season": 1, "season_count": 1,
            "folder": folder, "extras": []}  # fmt: skip
    conn.execute(
        "INSERT INTO series (id, library, name, metadata, detector_version) VALUES (?,?,?,?,3)",
        ("C1", "قناة", "مقاطع", json.dumps(meta)),
    )
    for n, i in enumerate((20, 21, 22), 1):
        conn.execute(
            "INSERT INTO series_episodes (series_id, path, season, episode) VALUES (?,?,1,?)",
            ("C1", f"{folder}/v{i}.mp4", n),
        )
    conn.execute(
        "INSERT INTO promo_links VALUES (?, ?, 'قناة', 'برنامج', 'قناة', ?, 1, 1.0, 'title')",
        (f"{folder}/v20.mp4", folder, folder),
    )
    conn.commit()
    seasons, loose, _ = plan_channels.load_inputs(conn)
    clip_paths = {e.path for s in seasons if s.kind == "clips" for e in s.episodes}
    assert clip_paths == {f"{folder}/v22.mp4"}  # the promos left the clip playlist
    by_path = {v.path: v for v in loose}
    assert by_path[f"{folder}/v20.mp4"].promo_of == f"{folder}|برنامج"  # airs by its show
    assert f"{folder}/v21.mp4" not in by_path  # advertises nothing found: never airs


@pytest.mark.parametrize(("section_b", "plans"), [("5", 1), ("6", 2)])
def test_groups_of_one_title_from_two_folders(tmp_path: Path, section_b: str, plans: int) -> None:
    conn = _db(tmp_path)
    for g, folder, sec, i in (("ga", "Y:/قناة", "5", 30), ("gb", "Y:/قناة 2", section_b, 31)):
        conn.execute(
            "INSERT INTO files (path, folder, stem, ext, size, mtime, duration_s, plex_rating_key,"
            " plex_section, plex_library, plex_title) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
            (f"{folder}/v{i}.mp4", folder, f"v{i}", ".mp4", 1, 1.0, 60, str(i), sec, "قناة", "x"),
        )  # fmt: skip
        conn.execute(
            "INSERT INTO clip_groups VALUES (?, 'قناة', ?, 'clips', 'rest', 'متنوعة', NULL)",
            (g, folder),
        )
        conn.execute("INSERT INTO clip_group_items VALUES (?, ?, 1)", (g, f"{folder}/v{i}.mp4"))
    conn.commit()
    rest = [p for p in export_plex._group_plans(conn, "قناة") if p[1] == "قناة - مقاطع متنوعة"]
    # one Plex section: one collection holding both; two sections: one in each
    assert len(rest) == plans
    if plans == 1:
        assert rest[0][0] == "ga" and rest[0][2] == ["30", "31"]


@pytest.mark.parametrize("shuffled", [False, True])
def test_only_items_out_of_place_are_moved(shuffled: bool) -> None:
    plex = FakePlex()
    plex.shuffle_adds = shuffled
    keys = [str(i) for i in range(1, 41)]
    new, refused = export_plex._recreate(_srv(plex), "m", "5", "x - y", keys, None)
    assert plex.cols[new]["kids"] == keys and not refused
    if not shuffled:
        assert plex.moves == 0  # added in order: nothing to move
    else:
        assert 0 < plex.moves < len(keys) - 1
