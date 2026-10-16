"""group step (clip / stand-alone groups, promo links) and export-plex's extra collections."""

from __future__ import annotations

import json
import sys
import urllib.parse
from pathlib import Path
from typing import Any

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
            return {"MediaContainer": {"Metadata": [{"ratingKey": key}]}}
        if method == "PUT" and parts[-1] == "items":
            self.cols[parts[2]]["kids"] += q["uri"][0].rsplit("/", 1)[1].split(",")
            return {}
        if method == "PUT" and parts[-1] == "move":
            kids = self.cols[parts[2]]["kids"]
            cur, after = parts[4], q["after"][0]
            kids.remove(cur)
            kids.insert(kids.index(after) + 1, cur)
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
    export_plex.export(conn, plex, apply=False, library_filter=None, include_clips=False,
                       replace_existing=True, remove_superseded=False)  # fmt: skip
    conn.execute("INSERT INTO plex_collections (series_id, section, rating_key, title) "
                 "VALUES ('GONE', '5', ?, 'قناة - مجزرة صبرا وشاتيلا')", (old,))  # fmt: skip
    conn.commit()
    export_plex.export(conn, plex, apply=True, library_filter=None, include_clips=False,
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
