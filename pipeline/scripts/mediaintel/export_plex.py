"""export-plex: write detected seasons to Plex as manual collections.

One collection per season, named ``<library> - <series>`` for one-season
series and ``<library> - <series> - الموسم N`` otherwise, holding the
episodes in detected order (custom collection sort).

Safety:

- Dry run unless ``apply``; the plan lists new / changed / unchanged.
- Collections this tool created are tracked (``plex_collections``) and
  updated in place on later runs. A *foreign* collection with the same name
  is only replaced with ``replace_existing``.
- Plex stores manual-collection membership as a tag named after the
  collection; deleting a same-named collection strips it. So an existing
  collection is deleted **before** its replacement is created, never after,
  and every written collection is read back and its count verified.
- Older collections that a new season fully covers ("superseded", e.g. a
  title-matched collection holding part of a season) are listed; they are
  removed only with ``remove_superseded``.
"""

from __future__ import annotations

import contextlib
import json
import re
import sqlite3
import urllib.error
from collections import defaultdict
from typing import Any

from maktaba_pipeline.text.arabic import fold

from .scan_plex import PlexServer, quote

SCHEMA = """
CREATE TABLE IF NOT EXISTS plex_collections (
    series_id   TEXT PRIMARY KEY,
    section     TEXT NOT NULL,
    rating_key  TEXT NOT NULL,
    title       TEXT NOT NULL
);
"""


def _title(library: str, series: str, season: int, season_count: int) -> str:
    base = f"{library} - {series}"
    if season >= 1900:  # an ongoing show's season is its year: "<lib> - X - 2016"
        return f"{base} - {season}"
    return base if season_count == 1 else f"{base} - الموسم {season}"


#: titles that say a video is not an episode: teaser, trailer, announcement, full-length
#: compilation, song, or a leftover partial download (".f251")
_NOT_EPISODE_RE = re.compile(
    r"(تشويق|تتر|اعلان|إعلان|برومو|promo|trailer|كامل[ةه](?!\w)|قريبا|قريبًا|نشيد|\.f\d{3}$)",
    re.IGNORECASE,
)


def removable(key: str, title: str, placed: set[str], shorts: set[str]) -> bool:
    """May an episode leave an existing collection? Only on positive evidence: it is an
    episode of another season now, a short, or its title says it is not an episode. One
    that detection merely failed to place stays (detection is not perfect; Plex keeps it)."""
    return key in placed or key in shorts or bool(_NOT_EPISODE_RE.search(title.strip()))


def _children_titled(server: PlexServer, key: str) -> list[tuple[str, str]]:
    """Items of a collection; [] when Plex no longer has it (deleted meanwhile → 404)."""
    try:
        got = server.get(f"/library/collections/{key}/children")
    except urllib.error.HTTPError as e:
        if e.code == 404:
            print(f"     (collection {key} is gone from Plex; skipped)", flush=True)
            return []
        raise
    md = got["MediaContainer"].get("Metadata", [])
    return [(m["ratingKey"], m.get("title", "")) for m in md]


def _children(server: PlexServer, key: str) -> list[str]:
    return [k for k, _ in _children_titled(server, key)]


def _print_diff(
    old: list[tuple[str, str]], new: list[str], titles: dict[str, str], *, covered: set[str]
) -> None:
    """What a viewer would notice: episodes leaving the collection, joining it, and
    (for a collection about to be deleted) episodes no new season picks up."""
    old_keys = {k for k, _ in old}
    leaving = [(k, t) for k, t in old if k not in set(new)]
    for k, t in leaving:
        where = "now in another season" if k in covered else "NOT in any new season"
        print(f"       - {t}   ({where})")
    for k in new:
        if k not in old_keys:
            print(f"       + {titles.get(k, k)}")


def _recreate(
    server: PlexServer, machine: str, section: str, title: str, keys: list[str], old: str | None
) -> str:
    def uri(ks: list[str]) -> str:
        return quote(
            f"server://{machine}/com.plexapp.plugins.library/library/metadata/{','.join(ks)}"
        )

    if old:
        server.send("DELETE", f"/library/metadata/{old}")  # before creating (tag semantics)
    created = server.send(
        "POST",
        f"/library/collections?type=1&title={quote(title)}&smart=0&sectionId={section}&uri={uri(keys[:1])}",
    )
    new = created["MediaContainer"]["Metadata"][0]["ratingKey"]
    for i in range(1, len(keys), 100):
        server.send("PUT", f"/library/collections/{new}/items?uri={uri(keys[i : i + 100])}")
    server.send("PUT", f"/library/metadata/{new}/prefs?collectionSort=2")
    for prev, cur in zip(keys, keys[1:], strict=False):
        server.send("PUT", f"/library/collections/{new}/items/{cur}/move?after={prev}")
    got = _children(server, new)
    if got != keys:
        raise RuntimeError(
            f"{title}: Plex holds {len(got)} items (expected {len(keys)}), order ok={got == keys}"
        )
    return str(new)


def export(
    conn: sqlite3.Connection,
    server: PlexServer,
    *,
    apply: bool,
    library_filter: str | None,
    include_clips: bool,
    replace_existing: bool,
    remove_superseded: bool,
    diff: bool = False,
) -> None:
    conn.executescript(SCHEMA)
    machine = server.get("/identity")["MediaContainer"]["machineIdentifier"]
    rows = conn.execute(
        "SELECT id, library, name, metadata FROM series ORDER BY library, name"
    ).fetchall()
    by_lib: dict[str, list[tuple[sqlite3.Row, dict[str, Any]]]] = defaultdict(list)
    for r in rows:
        if library_filter and library_filter not in r["library"]:
            continue
        m = json.loads(r["metadata"])
        if m["kind"] == "clips" and not include_clips:
            continue
        by_lib[r["library"]].append((r, m))

    for library, items in by_lib.items():
        sec = conn.execute(
            "SELECT plex_section FROM files WHERE plex_library=? AND plex_section IS NOT NULL LIMIT 1",
            (library,),
        ).fetchone()
        if not sec:
            print(f"\n=== {library}: no Plex section known (run scan-plex) — skipped")
            continue
        section = sec[0]
        existing = {
            c["title"]: c
            for c in server.get(f"/library/sections/{section}/collections")["MediaContainer"].get(
                "Metadata", []
            )
        }
        ours = {
            r["series_id"]: r
            for r in conn.execute("SELECT * FROM plex_collections WHERE section=?", (section,))
        }
        print(f"\n=== {library}  ({len(items)} seasons)")
        titles = {
            k: t
            for k, t in conn.execute(
                "SELECT plex_rating_key, plex_title FROM files WHERE plex_library=? "
                "AND plex_rating_key IS NOT NULL",
                (library,),
            )
        }
        # every episode detection placed in this library, and its shorts: the evidence an
        # episode may leave a collection on
        placed = {
            k
            for (k,) in conn.execute(
                """SELECT f.plex_rating_key FROM series_episodes e JOIN files f ON f.path = e.path
                   WHERE f.plex_library = ? AND f.plex_rating_key IS NOT NULL""",
                (library,),
            )
        }
        shorts = {
            k
            for (k,) in conn.execute(
                """SELECT f.plex_rating_key FROM shorts s JOIN files f ON f.path = s.path
                   WHERE f.plex_library = ? AND f.plex_rating_key IS NOT NULL""",
                (library,),
            )
        }
        # a second file of a video (X.mkv beside the user's X.mp4) -> the kept file's key
        copy_of: dict[str, str] = {}
        with contextlib.suppress(sqlite3.OperationalError):
            copy_of = {
                a: b
                for a, b in conn.execute(
                    """SELECT fc.plex_rating_key, fk.plex_rating_key FROM file_copies c
                       JOIN files fc ON fc.path = c.path JOIN files fk ON fk.path = c.kept
                       WHERE fc.plex_library = ? AND fc.plex_rating_key IS NOT NULL
                         AND fk.plex_rating_key IS NOT NULL""",
                    (library,),
                )
            }
        planned_keys: list[set[str]] = []
        planned: list[tuple[str, list[str]]] = []
        claimed: set[str] = set()  # existing collections taken over by a planned season
        for r, m in sorted(items, key=lambda x: (x[0]["name"], x[1]["season"])):
            title = _title(library, r["name"], m["season"], m["season_count"])
            keys = [
                k
                for (k,) in conn.execute(
                    """SELECT f.plex_rating_key FROM series_episodes e JOIN files f ON f.path=e.path
                       WHERE e.series_id=? AND f.plex_rating_key IS NOT NULL ORDER BY e.episode""",
                    (r["id"],),
                )
            ]
            if not keys:
                continue
            planned_keys.append(set(keys))
            planned.append((title, keys))
            mine = ours.get(r["id"])
            same_name = existing.get(title)
            if same_name is None and m["season_count"] > 1 and m["season"] == 1:
                # the series grew a season: its old single collection ("<lib> - X")
                # becomes season 1 ("<lib> - X - الموسم 1")
                same_name = existing.get(_title(library, r["name"], 1, 1))
            if same_name is not None:
                claimed.add(same_name["title"])
            old_key = mine["rating_key"] if mine else None
            if same_name is not None and same_name["ratingKey"] != old_key:
                if not replace_existing:
                    print(f"  !  {title}: a collection with this name already exists "
                          f"({same_name.get('childCount', '?')} items) — use --replace-existing")  # fmt: skip
                    continue
                old_key = same_name["ratingKey"]
            current_t = (
                _children_titled(server, old_key)
                if old_key and old_key in {c["ratingKey"] for c in existing.values()}
                else []
            )
            current = [k for k, _ in current_t]
            # existing collections are protected: an episode detection merely failed to
            # place stays, and a second file is swapped for its kept twin, never dropped.
            # Except one that plainly is not this show: (almost) every episode carries
            # the show's name and its title does not (the detector's own rule).
            name_words = [w for w in fold(r["name"]).split() if len(w) > 2]
            branded = bool(name_words) and sum(
                all(w in fold(titles.get(k, "")) for w in name_words) for k in keys
            ) >= 0.7 * len(keys)

            kept: list[str] = []
            for k, t in current_t:
                if k in keys or k in kept:
                    continue
                if branded and not all(w in fold(t) for w in name_words):
                    continue
                if k in copy_of:
                    twin = copy_of[k]
                    if twin not in placed and twin not in keys and twin not in kept:
                        kept.append(twin)
                elif not removable(k, t, placed, shorts):
                    kept.append(k)
            keys = keys + kept
            # same episodes is not enough: a season 1 that gained a season 2 still has
            # to move from "<lib> - X" to "<lib> - X - الموسم 1"
            now_titled = next(
                (t for t, c in existing.items() if old_key and c["ratingKey"] == old_key), None
            )
            if current == keys and now_titled == title:
                print(f"  =  {title} ({len(keys)})")
                continue
            verb = "update" if current else "new"
            if current == keys:
                verb = f"rename from {now_titled}"
            print(
                f"  +  {title}: {verb}, {len(keys)} episodes"
                + (f" (was {len(current)})" if current else "")
                + (f"; kept {len(kept)} detection did not place" if kept else "")
            )
            if diff and current:
                if set(current) == set(keys):
                    print("       (same episodes, new order)")
                else:
                    _print_diff(current_t, keys, titles, covered=placed)
            if apply:
                new = _recreate(
                    server, machine, section, title, keys, old_key if current or same_name else None
                )
                conn.execute(
                    "INSERT OR REPLACE INTO plex_collections (series_id, section, rating_key, title) VALUES (?,?,?,?)",
                    (r["id"], section, new, title),
                )
                conn.commit()

        # older collections whose content is (almost) entirely inside one new season
        planned_titles = {t for t, _ in planned} | claimed  # replaced above
        our_keys = {
            r["rating_key"]
            for r in conn.execute(
                "SELECT rating_key FROM plex_collections WHERE section=?", (section,)
            )
        }
        for c in existing.values():
            if c["ratingKey"] in our_keys or c.get("smart") or c["title"] in planned_titles:
                continue
            kids_t = _children_titled(server, c["ratingKey"])
            kids = {k for k, _ in kids_t}
            if len(kids) < 3:
                continue
            best = max(planned, key=lambda p: len(kids & set(p[1])), default=None)
            if best and len(kids & set(best[1])) >= 0.8 * len(kids):
                print(
                    f"  ~  superseded: {c['title']} ({len(kids)} items) by {best[0]}"
                    + (" — removed" if apply and remove_superseded else "")
                )
                if diff:
                    covered = set().union(*planned_keys)
                    for k, t in kids_t:
                        if k not in covered:
                            print(f"       - {t}   (NOT in any new season)")
                if apply and remove_superseded:
                    server.send("DELETE", f"/library/metadata/{c['ratingKey']}")
    if not apply:
        print(
            "\nDry run. Re-run with --apply to write (see --help for --replace-existing / --remove-superseded)."
        )
