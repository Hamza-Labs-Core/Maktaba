"""dizquetv-shows: keep dizqueTV custom shows in step with the detected seasons.

Two kinds of show, both in episode order:

- **series show** ``<library> - <series>``: every season back to back (S1 then S2 …),
  for marathons. An existing show with that name is updated; one is only created for
  a series without a show when ``create_series`` is set (the user names their own).
- **season show** ``<library> - <series> - الموسم N``: one per season of a series that
  has several, so a single season can be scheduled. Created when missing.

Shows are matched by folded name, so spelling variants (hamza, ة/ه) still match.
Shows whose names match nothing are left alone.

Safety: dry run unless ``apply``; each show is saved to ``backup_dir`` before it is
changed and read back afterwards (fails loudly if the episodes or their order differ).
dizqueTV channels hold their own copy of programmes, so channels that play an updated
show's episodes are listed: their schedule is regenerated in dizqueTV, not here.
"""

from __future__ import annotations

import dataclasses
import json
import sqlite3
import time
import urllib.request
from collections import defaultdict
from pathlib import Path
from typing import Any

from maktaba_pipeline.text.arabic import fold

from .export_plex import _title
from .scan_plex import PlexServer, cached_items


class _Dz:
    def __init__(self, base: str) -> None:
        self.base = base.rstrip("/")

    def call(self, method: str, path: str, body: Any = None) -> Any:
        data = json.dumps(body, ensure_ascii=False).encode() if body is not None else None
        req = urllib.request.Request(
            self.base + urllib.parse.quote(path, safe="/:?=&"),
            data=data,
            method=method,
            headers={"Accept": "application/json", "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=120) as r:  # noqa: S310 — local API
            raw = r.read()
        return json.loads(raw) if raw else None


def _hms(ms: int) -> str:
    s = ms // 1000
    return f"{s // 3600:02d}:{s // 60 % 60:02d}:{s % 60:02d}.{ms % 1000 // 100}"


def as_registered(server: PlexServer, registered: list[dict[str, Any]]) -> PlexServer:
    """``server`` under the name dizqueTV registered it as: programmes name their server
    by ``serverKey``, and ``--plex-url``/``--plex-token`` only know a placeholder."""
    uri = server.uri.rstrip("/")
    match = [s for s in registered if str(s.get("uri", "")).rstrip("/") == uri]
    if not match and len(registered) == 1:
        match = registered
    if not match:
        names = ", ".join(str(s.get("name")) for s in registered) or "none"
        raise SystemExit(f"dizqueTV has no Plex server at {uri} (registered: {names})")
    return dataclasses.replace(server, name=str(match[0]["name"]))


def _program(m: dict[str, Any], server: PlexServer) -> dict[str, Any] | None:
    """Same shape the dizqueTV web UI saves (as ``tools/fill-custom-shows.js``)."""
    if not m.get("duration") or m.get("type") not in ("movie", "episode"):
        return None
    part = m["Media"][0]["Part"][0]

    def icon(t: str | None) -> str | None:
        return f"{server.uri}{t}?X-Plex-Token={server.token}" if t else None

    date = m.get("originallyAvailableAt") or (f"{m['year']}-01-01" if m.get("year") else None)
    p: dict[str, Any] = {
        "title": m.get("title", ""), "key": m["key"], "ratingKey": str(m["ratingKey"]),
        "icon": icon(m.get("thumb")), "type": m["type"], "duration": int(m["duration"]),
        "durationStr": _hms(int(m["duration"])), "summary": m.get("summary", ""),
        "rating": m.get("contentRating"), "date": date, "year": m.get("year"),
        "plexFile": part["key"], "file": part["file"],
    }  # fmt: skip
    if m["type"] == "episode":
        p.update(showTitle=m.get("grandparentTitle"), season=m.get("parentIndex"),
                 episode=m.get("index"), icon=icon(m.get("grandparentThumb")))  # fmt: skip
    else:
        p.update(showTitle=m.get("title", ""), season=1, episode=1)
    p["serverKey"] = server.name
    p["commercials"] = []
    return p


def _metadata(
    server: PlexServer, keys: list[str], batch: int = 25, conn: sqlite3.Connection | None = None
) -> dict[str, dict[str, Any]]:
    """Plex details of every episode: from the plex-index step's saved records first,
    then only the missing ones from Plex, ``batch`` at a time, with a progress line every
    20 batches (thousands of episodes take a while, and silence looks like a hang)."""
    out: dict[str, dict[str, Any]] = cached_items(conn, keys) if conn is not None else {}
    if out:
        print(f"  {len(out):,} of {len(keys):,} episode details from the Plex index", flush=True)
    keys = [k for k in keys if k not in out]
    batches = range(0, len(keys), batch)
    for n, i in enumerate(batches, 1):
        md = server.get(f"/library/metadata/{','.join(keys[i : i + batch])}")["MediaContainer"]
        for m in md.get("Metadata", []):
            out[str(m["ratingKey"])] = m
        if n % 20 == 0 or n == len(batches):
            print(
                f"  [{min(i + batch, len(keys))}/{len(keys)}] episode details from Plex", flush=True
            )
    return out


def _targets(
    conn: sqlite3.Connection, library_filter: str | None
) -> list[tuple[str, str, list[str], bool]]:
    """(show name, kind, ordered Plex keys, is_series_show) for every series/season."""
    rows = conn.execute(
        "SELECT id, library, name, metadata FROM series ORDER BY library, name"
    ).fetchall()
    by_series: dict[tuple[str, str], list[tuple[int, int, str]]] = defaultdict(list)
    for r in rows:
        if library_filter and library_filter not in r["library"]:
            continue
        m = json.loads(r["metadata"])
        if m["kind"] == "clips":
            continue
        by_series[(r["library"], r["name"])].append((m["season"], m["season_count"], r["id"]))
    out: list[tuple[str, str, list[str], bool]] = []
    for (library, name), seasons in by_series.items():
        seasons.sort()
        all_keys: list[str] = []
        for number, count, sid in seasons:
            keys = [
                k
                for (k,) in conn.execute(
                    """SELECT f.plex_rating_key FROM series_episodes e JOIN files f ON f.path=e.path
                       WHERE e.series_id=? AND f.plex_rating_key IS NOT NULL ORDER BY e.episode""",
                    (sid,),
                )
            ]
            all_keys += keys
            if count > 1 and keys:
                out.append((_title(library, name, number, count), f"S{number}", keys, False))
        if all_keys:
            out.append((_title(library, name, 1, 1), "series", all_keys, True))
    return out


def run(
    conn: sqlite3.Connection,
    server: PlexServer,
    *,
    url: str = "http://localhost:8002",
    apply: bool = False,
    library_filter: str | None = None,
    create_series: bool = False,
    backup_dir: Path = Path("backups/dizquetv-shows"),
) -> None:
    dz = _Dz(url)
    server = as_registered(server, dz.call("GET", "/api/plex-servers"))
    shows = {fold(s["name"]): s for s in dz.call("GET", "/api/shows")}
    targets = _targets(conn, library_filter)
    plan: list[tuple[str, str, list[str], dict[str, Any] | None, list[str]]] = []
    for name, kind, keys, is_series in targets:
        show = shows.get(fold(name))
        if show is None and is_series and not create_series:
            continue
        current: list[str] = []
        titles: dict[str, str] = {}
        if show is not None:
            full = dz.call("GET", f"/api/show/{show['id']}")
            current = [str(p.get("ratingKey")) for p in full.get("content") or []]
            titles = {
                str(p.get("ratingKey")): p.get("title", "") for p in full.get("content") or []
            }
        if current == keys:
            print(f"  =  {name} ({len(keys)})")
            continue
        verb = "update" if show else "new"
        gone = len(set(current) - set(keys))
        added = len(set(keys) - set(current))
        print(
            f"  +  {name}: {verb} {kind}, {len(keys)} episodes"
            + (f" (was {len(current)}: +{added} −{gone})" if show else "")
        )
        for k in current:
            if k not in set(keys):
                print(f"       - {titles.get(k, k)}")
        plan.append((name, kind, keys, show, current))

    changed_keys = {k for _, _, _, s, cur in plan if s for k in cur}
    if changed_keys:
        for ch in dz.call("GET", "/api/channels"):
            num = ch["number"] if isinstance(ch, dict) else ch  # 1.7 lists bare numbers
            full = dz.call("GET", f"/api/channel/{num}")
            hits = sum(
                1 for p in full.get("programs") or [] if str(p.get("ratingKey")) in changed_keys
            )
            if hits:
                print(f"  ·  channel {num} {full.get('name')}: plays {hits} programmes from "
                      "updated shows — regenerate its schedule in dizqueTV after applying")  # fmt: skip
    if not apply:
        print("\nDry run. Re-run with --apply to write.")
        return

    meta = _metadata(server, sorted({k for _, _, keys, _, _ in plan for k in keys}), conn=conn)
    backup_dir.mkdir(parents=True, exist_ok=True)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    for name, _kind, keys, show, _cur in plan:
        content = [p for k in keys if k in meta and (p := _program(meta[k], server))]
        missing = len(keys) - len(content)
        body = {"name": name, "content": content}
        if show is not None:
            old = dz.call("GET", f"/api/show/{show['id']}")
            (backup_dir / f"{show['id']}-{stamp}.json").write_text(
                json.dumps(old, ensure_ascii=False), encoding="utf-8"
            )
            dz.call("POST", f"/api/show/{show['id']}", body)
            sid = show["id"]
        else:
            sid = dz.call("PUT", "/api/show", body)["id"]
        got = [str(p.get("ratingKey")) for p in dz.call("GET", f"/api/show/{sid}")["content"]]
        if got != [p["ratingKey"] for p in content]:
            raise RuntimeError(f"{name}: dizqueTV holds {len(got)} items, expected {len(content)}")
        print(
            f"  ✓  {name}: {len(content)} episodes"
            + (f" ({missing} not in Plex)" if missing else "")
        )
    print(f"\nBackups of changed shows: {backup_dir}")
