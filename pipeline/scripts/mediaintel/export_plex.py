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
- A collection this tool made for a season detection no longer has (the show was
  rebuilt) is *retired*: deleted once the new collections hold (nearly) all of it.

Besides seasons: topic collections (``<library> - ملف - <name>``, a curated playlist
cutting across shows) and clip collections (``<library> - مقاطع - <name>``, filler;
``<library> - منفردات - <name>``, stand-alone videos). Being in one of them never
counts as placement, so it never takes an episode out of a season's collection.
Every collection written carries the Plex label ``maktaba-auto``.
"""

from __future__ import annotations

import contextlib
import json
import re
import sqlite3
from collections import Counter, defaultdict
from typing import Any

from maktaba_pipeline.text.arabic import fold

from .scan_plex import PlexError, PlexServer, quote

SCHEMA = """
CREATE TABLE IF NOT EXISTS plex_collections (
    series_id   TEXT PRIMARY KEY,
    section     TEXT NOT NULL,
    rating_key  TEXT NOT NULL,
    title       TEXT NOT NULL
);
"""


def _title(
    library: str, series: str, season: int, season_count: int, season_name: str | None = None
) -> str:
    base = f"{library} - {series}"
    if season_name and season_count > 1:  # a talk show's guest or year: "<lib> - X - <guest>"
        return f"{base} - {season_name}"
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
    except PlexError as e:
        if e.status == 404:
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
) -> tuple[str, dict[str, str]]:
    """The collection, made anew with ``keys`` in order. Returns its rating key and the
    items Plex refused ({key: what Plex said}); a refused item never stops the others."""

    def uri(ks: list[str]) -> str:
        return quote(
            f"server://{machine}/com.plexapp.plugins.library/library/metadata/{','.join(ks)}"
        )

    refused: dict[str, str] = {}
    if old:
        server.send("DELETE", f"/library/metadata/{old}")  # before creating (tag semantics)
    new = None
    for at, first in enumerate(keys):  # created with its first item Plex accepts
        try:
            created = server.send(
                "POST",
                f"/library/collections?type=1&title={quote(title)}&smart=0"
                f"&sectionId={section}&uri={uri([first])}",
            )
        except PlexError as e:
            if e.status != 400:
                raise
            refused[first] = str(e)
            continue
        new = created["MediaContainer"]["Metadata"][0]["ratingKey"]
        rest = keys[at + 1 :]
        break
    if new is None:
        raise PlexError("POST", "/library/collections", 400, "Plex refused every item")
    for i in range(0, len(rest), 100):
        part = rest[i : i + 100]
        try:
            server.send("PUT", f"/library/collections/{new}/items?uri={uri(part)}")
        except PlexError as e:
            if e.status != 400:
                raise
            for k in part:  # one by one: only the item Plex refuses stays out
                try:
                    server.send("PUT", f"/library/collections/{new}/items?uri={uri([k])}")
                except PlexError as e1:
                    if e1.status != 400:
                        raise
                    refused[k] = str(e1)
    placed = [k for k in keys if k not in refused]
    server.send("PUT", f"/library/metadata/{new}/prefs?collectionSort=2")
    for prev, cur in zip(placed, placed[1:], strict=False):
        server.send("PUT", f"/library/collections/{new}/items/{cur}/move?after={prev}")
    got = _children(server, new)
    if got != placed:
        raise RuntimeError(
            f"{title}: Plex holds {len(got)} items (expected {len(placed)}), "
            f"order ok={got == placed}"
        )
    return str(new), refused


#: every collection the job makes carries this Plex label, so people can tell them apart
LABEL = "maktaba-auto"


def _label(server: PlexServer, section: str, key: str) -> None:
    server.send(
        "PUT",
        f"/library/sections/{section}/all?type=18&id={key}"
        f"&label[0].tag.tag={quote(LABEL)}&label.locked=1",
    )


def _topic_plans(conn: sqlite3.Connection, library: str) -> list[tuple[str, str, list[str]]]:
    """(track id, title, ordered Plex keys) of the library's topic collections."""
    out = []
    with contextlib.suppress(sqlite3.OperationalError):
        for tid, name in conn.execute(
            "SELECT id, name FROM topic_collections WHERE library=? ORDER BY name", (library,)
        ).fetchall():
            keys = [
                k
                for (k,) in conn.execute(
                    """SELECT f.plex_rating_key FROM topic_items t JOIN files f ON f.path = t.path
                       WHERE t.topic_id = ? AND f.plex_rating_key IS NOT NULL ORDER BY t.position""",
                    (tid,),
                )
            ]
            if keys:
                out.append((tid, f"{library} - ملف - {name}", keys))
    return out


def _group_plans(conn: sqlite3.Connection, library: str) -> list[tuple[str, str, list[str]]]:
    """(track id, title, ordered Plex keys) of the library's clip / stand-alone groups."""
    from .group_clips import group_title

    # two folders of one library can make groups of one title ("<lib> - مقاطع متنوعة"): in
    # one Plex section they are one collection, tracked by the first group's id; in two
    # sections (a library split over two Plex libraries) each stays its own
    merged: dict[tuple[str, str | None], tuple[str, list[str]]] = {}
    with contextlib.suppress(sqlite3.OperationalError):
        for gid, kind, basis, name in conn.execute(
            "SELECT id, kind, basis, name FROM clip_groups WHERE library=? ORDER BY kind, name, id",
            (library,),
        ).fetchall():
            rows = conn.execute(
                """SELECT f.plex_rating_key, f.plex_section FROM clip_group_items g
                   JOIN files f ON f.path = g.path
                   WHERE g.group_id = ? AND f.plex_rating_key IS NOT NULL ORDER BY g.position""",
                (gid,),
            ).fetchall()
            if not rows:
                continue
            secs = Counter(sec for _, sec in rows)
            home = min(secs, key=lambda s: (-secs[s], str(s)))
            title = group_title(library, kind, basis, name)
            _, keys = merged.setdefault((title, home), (gid, []))
            keys += [k for k, _ in rows if k not in keys]
    return [(tid, title, keys) for (title, _), (tid, keys) in merged.items()]


class _Sync:
    """Writes one library's collections (seasons, topics, clip groups) with the same
    protection rules; remembers what was planned for the retire / superseded passes."""

    def __init__(
        self,
        conn: sqlite3.Connection,
        server: PlexServer,
        machine: str,
        section: str,
        existing: dict[str, Any],
        ours: dict[str, sqlite3.Row],
        titles: dict[str, str],
        shorts: set[str],
        copy_of: dict[str, str],
        placed: set[str],
        *,
        apply: bool,
        diff: bool,
        replace_existing: bool,
        failed: list[str] | None = None,
        key_section: dict[str, str] | None = None,
    ) -> None:
        self.failed: list[str] = failed if failed is not None else []
        #: the Plex section of each item, when the library spans several sections
        self.key_section = key_section or {}
        self.conn, self.server, self.machine, self.section = conn, server, machine, section
        self.existing, self.ours, self.titles = existing, ours, titles
        self.shorts, self.copy_of, self.placed = shorts, copy_of, placed
        self.apply, self.diff, self.replace_existing = apply, diff, replace_existing
        self.existing_keys = {c["ratingKey"] for c in existing.values()}
        self.everything: list[tuple[str, list[str]]] = []  # seasons, topics and groups
        self.claimed: set[str] = set()  # existing collections taken over by a planned one

    def __call__(
        self,
        track_id: str,
        title: str,
        keys: list[str],
        show_name: str | None,
        *,
        alt_title: str | None = None,
        leave_ok: set[str] | None = None,
    ) -> None:
        """Create or update one collection, keeping what an existing one holds
        unless there is positive evidence it does not belong (:func:`removable`)."""
        leave_ok = self.placed if leave_ok is None else leave_ok
        if self.key_section:
            # a collection lives in one Plex section: the one holding most of its items
            # (it is written in that section's pass); items of another section stay out
            secs = Counter(self.key_section[k] for k in keys if k in self.key_section)
            if secs and min(secs, key=lambda x: (-secs[x], x)) != self.section:
                return
            away = [k for k in keys if self.key_section.get(k, self.section) != self.section]
            if away:
                print(f"  ~  {title}: {len(away)} item(s) are in another Plex library of the "
                      "same name and stay out", flush=True)  # fmt: skip
                keys = [k for k in keys if k not in away]
        mine = self.ours.get(track_id)
        same_name = self.existing.get(title)
        if same_name is None and alt_title:
            same_name = self.existing.get(alt_title)
        if same_name is not None:
            self.claimed.add(same_name["title"])
        old_key = mine["rating_key"] if mine else None
        if same_name is not None and same_name["ratingKey"] != old_key:
            if not self.replace_existing:
                print(f"  !  {title}: a collection with this name already exists "
                      f"({same_name.get('childCount', '?')} items) — use --replace-existing")  # fmt: skip
                return
            old_key = same_name["ratingKey"]
        # counted as coverage for retiring older collections only once it will be written
        self.everything.append((title, keys))
        current_t = (
            _children_titled(self.server, old_key)
            if old_key and old_key in self.existing_keys
            else []
        )
        current = [k for k, _ in current_t]
        # existing collections are protected: an episode detection merely failed to
        # place stays, and a second file is swapped for its kept twin, never dropped.
        # Except one that plainly is not this show: (almost) every episode carries
        # the show's name and its title does not (the detector's own rule).
        name_words = [w for w in fold(show_name or "").split() if len(w) > 2]
        branded = bool(name_words) and sum(
            all(w in fold(self.titles.get(k, "")) for w in name_words) for k in keys
        ) >= 0.7 * len(keys)
        kept: list[str] = []
        for k, t in current_t:
            if k in keys or k in kept:
                continue
            if branded and not all(w in fold(t) for w in name_words):
                continue
            if k in self.copy_of:
                twin = self.copy_of[k]
                if twin not in leave_ok and twin not in keys and twin not in kept:
                    kept.append(twin)
            elif not removable(k, t, leave_ok, self.shorts):
                kept.append(k)
        keys = keys + kept
        # same episodes is not enough: a season 1 that gained a season 2 still has
        # to move from "<lib> - X" to "<lib> - X - الموسم 1"
        now_titled = next(
            (t for t, c in self.existing.items() if old_key and c["ratingKey"] == old_key), None
        )
        if current == keys and now_titled == title:
            print(f"  =  {title} ({len(keys)})")
            if (
                self.apply
                and mine is not None
                and not mine["labeled"]
                and old_key in self.existing_keys
            ):
                _label(self.server, self.section, str(old_key))
                self.conn.execute(
                    "UPDATE plex_collections SET labeled=1 WHERE series_id=?", (track_id,)
                )
                self.conn.commit()
            return
        verb = "update" if current else "new"
        if current == keys:
            verb = f"rename from {now_titled}"
        print(
            f"  +  {title}: {verb}, {len(keys)} items"
            + (f" (was {len(current)})" if current else "")
            + (f"; kept {len(kept)} detection did not place" if kept else "")
        )
        if self.diff and current:
            if set(current) == set(keys):
                print("       (same items, new order)")
            else:
                _print_diff(current_t, keys, self.titles, covered=leave_ok)
        if self.apply:
            try:
                new, refused = _recreate(
                    self.server,
                    self.machine,
                    self.section,
                    title,
                    keys,
                    old_key if current or same_name else None,
                )
            except PlexError as e:  # this collection only; the run goes on
                print(f"  !  {title}: not written — {e}", flush=True)
                self.failed.append(title)
                # not written, so it covers nothing: no older collection retires for it
                self.everything[:] = [p for p in self.everything if p[0] != title]
                return
            for k, why in refused.items():
                print(f"       ! Plex refused {self.titles.get(k, k)}: {why}", flush=True)
            if refused:
                self.failed.append(f"{title} ({len(refused)} item(s) refused)")
                self.everything[:] = [
                    (t, [k for k in ks if k not in refused]) if t == title else (t, ks)
                    for t, ks in self.everything
                ]
            _label(self.server, self.section, new)
            self.conn.execute(
                "INSERT OR REPLACE INTO plex_collections "
                "(series_id, section, rating_key, title, labeled) VALUES (?,?,?,?,1)",
                (track_id, self.section, new, title),
            )
            self.conn.commit()


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
    topics: bool = True,
    clip_groups: bool = True,
    retire: bool = True,
) -> None:
    """Write seasons, then topic and clip collections, as Plex collections (module doc).

    ``retire``: a collection this job made for a season detection no longer has is
    deleted once (nearly) all of it sits in the new collections."""
    conn.executescript(SCHEMA)
    with contextlib.suppress(sqlite3.OperationalError):
        conn.execute("ALTER TABLE plex_collections ADD COLUMN labeled INTEGER")
    failed: list[str] = []  # collections Plex refused (in part), named at the end
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
    libraries = set(by_lib)
    with contextlib.suppress(sqlite3.OperationalError):
        for (lib,) in conn.execute("SELECT DISTINCT library FROM clip_groups"):
            if not library_filter or library_filter in lib:
                libraries.add(lib)
    live_ids = {r["id"] for r in rows}

    # a library can be several Plex sections of one name: each gets its own pass, and a
    # collection goes to the section holding its items
    passes: list[tuple[str, str | None, dict[str, str]]] = []
    for library in sorted(libraries):
        secs = conn.execute(
            "SELECT plex_section, COUNT(*) FROM files WHERE plex_library=? AND plex_section "
            "IS NOT NULL GROUP BY plex_section ORDER BY COUNT(*) DESC, plex_section",
            (library,),
        ).fetchall()
        key_section = (
            {
                k: sec
                for k, sec in conn.execute(
                    "SELECT plex_rating_key, plex_section FROM files WHERE plex_library=? "
                    "AND plex_rating_key IS NOT NULL AND plex_section IS NOT NULL",
                    (library,),
                )
            }
            if len(secs) > 1
            else {}
        )
        passes += [(library, sec, key_section) for sec, _ in secs] or [(library, None, {})]
    for library, section_or_none, key_section in passes:
        items = by_lib.get(library, [])
        if section_or_none is None:
            print(f"\n=== {library}: no Plex section known (run scan-plex) — skipped")
            continue
        section = section_or_none
        existing = {
            c["title"]: c
            for c in server.get(f"/library/sections/{section}/collections")["MediaContainer"].get(
                "Metadata", []
            )
        }
        existing_keys = {c["ratingKey"] for c in existing.values()}
        ours = {
            r["series_id"]: r
            for r in conn.execute("SELECT * FROM plex_collections WHERE section=?", (section,))
        }
        topic_plans = _topic_plans(conn, library) if topics else []
        group_plans = _group_plans(conn, library) if clip_groups else []
        print(f"\n=== {library}" + (f" (Plex section {section})" if key_section else "")
              + f"  ({len(items)} seasons, {len(topic_plans)} topics, "
              f"{len(group_plans)} clip groups)")  # fmt: skip
        titles = {
            k: t
            for k, t in conn.execute(
                "SELECT plex_rating_key, plex_title FROM files WHERE plex_library=? "
                "AND plex_rating_key IS NOT NULL",
                (library,),
            )
        }
        # every episode detection placed in this library, and its shorts: the evidence an
        # episode may leave a collection on. Topic and clip collections are not placement:
        # being in "الشأن الفلسطيني" never takes an episode out of its season's collection.
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
        grouped = {k for _, _, ks in group_plans for k in ks}
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
        planned: list[tuple[str, list[str]]] = []
        everything: list[tuple[str, list[str]]] = []  # seasons, topics and groups
        claimed: set[str] = set()  # existing collections taken over by a planned one

        sync = _Sync(
            conn,
            server,
            machine,
            section,
            existing,
            ours,
            titles,
            shorts,
            copy_of,
            placed,
            apply=apply,
            diff=diff,
            replace_existing=replace_existing,
            failed=failed,
            key_section=key_section,
        )
        everything, claimed = sync.everything, sync.claimed

        for r, m in sorted(items, key=lambda x: (x[0]["name"], x[1]["season"])):
            title = _title(library, r["name"], m["season"], m["season_count"], m.get("season_name"))
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
            planned.append((title, keys))
            # the series grew a season: its old single collection ("<lib> - X") becomes
            # season 1 ("<lib> - X - الموسم 1")
            alt = (
                _title(library, r["name"], 1, 1)
                if m["season_count"] > 1 and m["season"] == 1
                else None
            )
            sync(r["id"], title, keys, r["name"], alt_title=alt)
        for tid, title, keys in topic_plans:
            sync(tid, title, keys, None)
        for gid, title, keys in group_plans:
            # an item may leave a clip collection for its show's season or another group
            sync(gid, title, keys, None, leave_ok=placed | grouped)

        # collections this job made for seasons detection no longer has: retired once the
        # new collections hold (nearly) all of them; otherwise kept and reported
        retired: set[str] = set()
        if retire:
            covered_all = {k for _, ks in everything for k in ks}
            planned_titles_all = {t for t, _ in everything}
            for track_id, row in ours.items():
                if track_id in live_ids or ":" in track_id:
                    continue  # still a season, or a topic / group (handled above)
                if row["rating_key"] not in existing_keys or row["title"] in planned_titles_all:
                    continue
                kids_t = _children_titled(server, row["rating_key"])
                kids = {k for k, _ in kids_t}
                inside = len(kids & covered_all)
                if kids and inside >= 0.8 * len(kids):
                    best = max(everything, key=lambda p: len(kids & set(p[1])), default=None)
                    print(f"  -  retired: {row['title']} ({len(kids)} items; {inside} now in the "
                          f"new collections, most in {best[0] if best else '?'})"
                          + (" — removed" if apply else ""))  # fmt: skip
                    if diff:
                        for k, t in kids_t:
                            if k not in covered_all:
                                print(f"       - {t}   (NOT in any new collection)")
                    if apply:
                        retired.add(row["rating_key"])
                        server.send("DELETE", f"/library/metadata/{row['rating_key']}")
                        conn.execute("DELETE FROM plex_collections WHERE series_id=?", (track_id,))
                        conn.commit()
                elif kids:
                    print(f"  ?  kept: {row['title']} ({len(kids)} items, only {inside} in the "
                          "new collections)")  # fmt: skip

        # older collections whose content is (almost) entirely inside one new season: only
        # seasons as written count (a blocked or failed one, or a refused item, covers nothing)
        planned_titles = {t for t, _ in everything} | claimed  # replaced above
        season_titles = {t for t, _ in planned}
        confirmed = [(t, ks) for t, ks in everything if t in season_titles]
        our_keys = {
            r["rating_key"]
            for r in conn.execute(
                "SELECT rating_key FROM plex_collections WHERE section=?", (section,)
            )
        }
        for c in existing.values():
            if c["ratingKey"] in our_keys | retired or c.get("smart"):
                continue
            if c["title"] in planned_titles:
                continue
            kids_t = _children_titled(server, c["ratingKey"])
            kids = {k for k, _ in kids_t}
            if len(kids) < 3:
                continue
            best = max(confirmed, key=lambda p: len(kids & set(p[1])), default=None)
            if best and len(kids & set(best[1])) >= 0.8 * len(kids):
                print(
                    f"  ~  superseded: {c['title']} ({len(kids)} items) by {best[0]}"
                    + (" — removed" if apply and remove_superseded else "")
                )
                if diff:
                    covered = set().union(*(set(ks) for _, ks in confirmed))
                    for k, t in kids_t:
                        if k not in covered:
                            print(f"       - {t}   (NOT in any new season)")
                if apply and remove_superseded:
                    server.send("DELETE", f"/library/metadata/{c['ratingKey']}")
    if failed:
        print(f"\n! {len(failed)} collection(s) not fully written (Plex refused): "
              + "; ".join(failed), flush=True)  # fmt: skip
    if not apply:
        print(
            "\nDry run. Re-run with --apply to write (see --help for --replace-existing / --remove-superseded)."
        )
