"""report: an HTML page per folder to review detected series visually.

Each season is a grid of thumbnail cards (air date, duration, how the video
got there: playlist position or attached by signals). Extras (promos,
trailers) and unassigned videos are listed below. Thumbnails are copied as
small JPEGs into ``<out>/thumbs`` (cached), so the report folder is
self-contained and opens fast.
"""

from __future__ import annotations

import hashlib
import html
import json
import sqlite3
import subprocess
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any

CSS = """
:root{--bg:#f6f5f2;--card:#fff;--ink:#1c1b19;--mute:#6b675f;--line:#e2ded6;--acc:#2f6f5e;--warn:#a5532a;--att:#7a5aa8}
@media (prefers-color-scheme:dark){:root{--bg:#161614;--card:#21201d;--ink:#ecebe6;--mute:#a19d93;--line:#33312c;--acc:#6fbfa6;--warn:#e0956b;--att:#b59ae0}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.45 system-ui,"Segoe UI",Tahoma,sans-serif}
header{padding:20px 16px 8px;max-width:1400px;margin:auto}h1{margin:0 0 4px;font-size:22px}
.sum{color:var(--mute)}main{max-width:1400px;margin:auto;padding:0 16px 40px}
nav{display:flex;flex-wrap:wrap;gap:6px;margin:12px 0 20px}nav a{padding:3px 9px;border:1px solid var(--line);border-radius:12px;color:var(--ink);text-decoration:none;font-size:13px;background:var(--card)}
section.series{margin:28px 0;border-top:1px solid var(--line);padding-top:14px}
h2{margin:0;font-size:18px}.kind{font-size:12px;color:var(--mute);border:1px solid var(--line);border-radius:10px;padding:1px 8px;margin-inline-start:8px}
h3{font-size:15px;margin:14px 0 6px;color:var(--acc)}.meta{color:var(--mute);font-size:12px;font-weight:normal}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(170px,1fr));gap:10px}
.card{background:var(--card);border:1px solid var(--line);border-radius:8px;overflow:hidden}
.card img{width:100%;aspect-ratio:16/9;object-fit:cover;display:block;background:var(--line)}
.card .t{padding:6px 8px 2px;font-size:12.5px;max-height:3.9em;overflow:hidden}
.card .m{padding:0 8px 7px;color:var(--mute);font-size:11.5px;display:flex;gap:6px;flex-wrap:wrap}
.n{font-weight:600;color:var(--ink)}.att{color:var(--att)}.x .t{color:var(--warn)}
details{margin:8px 0}summary{cursor:pointer;color:var(--mute)}
.approx{opacity:.75}
"""


def _thumb_name(src: str) -> str:
    return hashlib.sha1(src.encode()).hexdigest()[:16] + ".jpg"


def _make_thumbs(srcs: list[str], out_dir: Path, workers: int = 8) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    todo = [s for s in srcs if not (out_dir / _thumb_name(s)).exists()]

    def one(src: str) -> None:
        subprocess.run(
            ["ffmpeg", "-v", "error", "-nostdin", "-y", "-i", src, "-vf", "scale=320:-2",
             "-frames:v", "1", "-q:v", "5", str(out_dir / _thumb_name(src))],
            capture_output=True, timeout=60, check=False,
        )  # fmt: skip

    with ThreadPoolExecutor(workers) as pool:
        list(pool.map(one, todo))


def _card(row: sqlite3.Row, n: int | None, tag: str, cls: str = "") -> str:
    thumb = f"thumbs/{_thumb_name(row['thumb_path'])}" if row["thumb_path"] else ""
    dur = f"{(row['duration_s'] or 0) / 60:.0f}m" if row["duration_s"] else "?"
    d = row["upload_date"] or "—"
    approx = (
        ' class="approx" title="approximate date"' if row["date_quality"] == "approximate" else ""
    )
    num = f'<span class="n">{n}</span>' if n is not None else ""
    return (
        f'<div class="card {cls}"><img loading="lazy" src="{html.escape(thumb)}" alt="">'
        f'<div class="t">{html.escape(row["title"])}</div>'
        f'<div class="m">{num}<span{approx}>{d}</span><span>{dur}</span>{tag}</div></div>'
    )


def _rows(conn: sqlite3.Connection, paths: list[str]) -> dict[str, sqlite3.Row]:
    out: dict[str, sqlite3.Row] = {}
    for i in range(0, len(paths), 500):
        chunk = paths[i : i + 500]
        q = (
            "SELECT f.path, COALESCE(y.title, f.stem) title, f.thumb_path, "
            "COALESCE(f.duration_s, y.duration_s) duration_s, y.upload_date, y.date_quality "
            "FROM files f LEFT JOIN yt_videos y ON y.youtube_id=f.youtube_id "
            f"WHERE f.path IN ({','.join('?' * len(chunk))})"
        )
        out.update({r["path"]: r for r in conn.execute(q, chunk)})
    return out


def build(conn: sqlite3.Connection, folder: str, out_dir: Path) -> Path:
    series = conn.execute(
        "SELECT id, name, metadata FROM series WHERE json_extract(metadata,'$.folder')=?", (folder,)
    ).fetchall()
    grouped: dict[str, list[tuple[sqlite3.Row, dict[str, Any]]]] = {}
    for s in series:
        m = json.loads(s["metadata"])
        grouped.setdefault(f"{m['series_key']}|{m['kind']}|{s['name']}", []).append((s, m))
    order = sorted(
        grouped.values(), key=lambda v: min((m.get("first_air") or "9999") for _, m in v)
    )

    all_paths = [r[0] for r in conn.execute("SELECT path FROM files WHERE folder=?", (folder,))]
    rows = _rows(conn, all_paths)
    _make_thumbs([r["thumb_path"] for r in rows.values() if r["thumb_path"]], out_dir / "thumbs")
    placed: set[str] = set()
    body: list[str] = []
    nav: list[str] = []
    total_eps = 0
    for idx, seasons in enumerate(order):
        seasons.sort(key=lambda x: x[1]["season"])
        s0, m0 = seasons[0]
        anchor = f"s{idx}"
        n_eps = 0
        parts: list[str] = []
        for s, m in seasons:
            eps = [
                r[0]
                for r in conn.execute(
                    "SELECT path FROM series_episodes WHERE series_id=? ORDER BY episode",
                    (s["id"],),
                )
            ]
            n_eps += len(eps)
            attached = set(m.get("attached") or [])
            cards = [
                _card(rows[p], i, '<span class="att">+attached</span>' if p in attached else "")
                for i, p in enumerate(eps, 1)
                if p in rows
            ]
            extras = [_card(rows[p], None, "", "x") for p in m.get("extras") or [] if p in rows]
            placed.update(eps)
            placed.update(m.get("extras") or [])
            dups = [_card(rows[k], None, "", "x") for k in m.get("duplicates") or [] if k in rows]
            placed.update(m.get("duplicates") or [])
            med = (
                f"{(m.get('median_duration_s') or 0) / 60:.0f} min"
                if m.get("median_duration_s")
                else ""
            )
            span = f"{m.get('first_air') or '?'} → {m.get('last_air') or '?'}"
            parts.append(
                f'<h3>Season {m["season"]} <span class="meta">· {len(eps)} episodes · {span} · {med}'
                f" · playlists: {len(m.get('playlists') or [])}</span></h3>"
                f'<div class="grid">{"".join(cards)}</div>'
                + (
                    f"<details><summary>{len(extras)} promos / trailers / off-length</summary>"
                    f'<div class="grid">{"".join(extras)}</div></details>'
                    if extras
                    else ""
                )
                + (
                    f"<details><summary>{len(dups)} second uploads (duplicates)</summary>"
                    f'<div class="grid">{"".join(dups)}</div></details>'
                    if dups
                    else ""
                )
            )
        total_eps += n_eps if m0["kind"] != "clips" else 0
        nav.append(f'<a href="#{anchor}">{html.escape(s0["name"])} · {n_eps}</a>')
        body.append(
            f'<section class="series" id="{anchor}"><h2>{html.escape(s0["name"])}'
            f'<span class="kind">{m0["kind"]} · {len(seasons)} season(s)</span></h2>{"".join(parts)}</section>'
        )
    shorts = {r[0] for r in conn.execute("SELECT path FROM shorts WHERE folder=?", (folder,))}
    unassigned = sorted(
        (p for p in all_paths if p not in placed and p not in shorts),
        key=lambda p: rows[p]["upload_date"] or "",
    )
    body.append(
        f'<section class="series" id="unassigned"><h2>Unassigned<span class="kind">{len(unassigned)}</span></h2>'
        f'<details><summary>show</summary><div class="grid">{"".join(_card(rows[p], None, "") for p in unassigned)}'
        "</div></details></section>"
    )
    name = Path(folder).name
    page = (
        f'<!doctype html><html lang="ar" dir="rtl"><head><meta charset="utf-8">'
        f'<meta name="viewport" content="width=device-width,initial-scale=1"><title>{html.escape(name)} · series</title>'
        f"<style>{CSS}</style></head><body><header><h1>{html.escape(name)}</h1>"
        f'<div class="sum">{len(all_paths)} videos · {len(order)} series · {total_eps} episodes · '
        f"{len(unassigned)} unassigned · {len(shorts)} shorts left out</div></header><main><nav>{''.join(nav)}"
        f'<a href="#unassigned">Unassigned · {len(unassigned)}</a></nav>{"".join(body)}</main></body></html>'
    )
    out_dir.mkdir(parents=True, exist_ok=True)
    out = out_dir / f"{name}.html"
    out.write_text(page, encoding="utf-8")
    return out


def run(conn: sqlite3.Connection, only_folder: str | None, out_dir: Path) -> None:
    for (folder,) in conn.execute("SELECT DISTINCT folder FROM files ORDER BY folder").fetchall():
        if only_folder and only_folder not in folder:
            continue
        print(build(conn, folder, out_dir))
