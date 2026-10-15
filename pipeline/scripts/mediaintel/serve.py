"""serve: a live status page for a running ``mediaintel run`` (or any of its steps).

Reads the cache read-only (``index_jobs``, ``files``, ``series``, ``plex_collections``)
and, when given, the run's log, and serves one auto-refreshing page plus
``/api/status`` (JSON). Inside Maktaba the same information comes from
``processing_jobs`` over the ``jobs.progress`` WebSocket channel (Processing Queue
page); this page is the stand-in until these stages run there.
"""

from __future__ import annotations

import json
import re
import sqlite3
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

_STEP_RE = re.compile(r"^##### (.+)$")

PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>mediaintel status</title>
<style>
:root{--bg:#f7f7f5;--fg:#1b1b1b;--muted:#6b6b6b;--card:#fff;--line:#e3e3df;--ok:#2f7d4f;--bad:#b3261e;--bar:#3b6fd8}
@media (prefers-color-scheme:dark){:root{--bg:#141414;--fg:#ececec;--muted:#9a9a9a;--card:#1e1e1e;--line:#2c2c2c;--ok:#5fbf86;--bad:#ef8079;--bar:#7aa2ff}}
body{margin:0;font:14px/1.45 system-ui,sans-serif;background:var(--bg);color:var(--fg)}
main{max-width:1100px;margin:0 auto;padding:16px}
h1{font-size:20px;margin:0 0 4px} .muted{color:var(--muted)}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 14px;margin:12px 0}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px}
.k{font-size:12px;color:var(--muted)} .v{font-size:22px;font-weight:600}
table{width:100%;border-collapse:collapse;font-size:13px} td,th{padding:4px 6px;border-bottom:1px solid var(--line);text-align:start}
th{color:var(--muted);font-weight:500} td.n{text-align:end;font-variant-numeric:tabular-nums}
.bar{height:6px;background:var(--line);border-radius:3px;overflow:hidden;min-width:80px}.bar>i{display:block;height:100%;background:var(--bar)}
.bad{color:var(--bad)} .ok{color:var(--ok)} pre{white-space:pre-wrap;margin:0;font-size:12px;max-height:280px;overflow:auto}
.lib{max-width:340px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;direction:auto}
</style></head><body><main>
<h1>mediaintel</h1><div class="muted" id="when">loading…</div>
<div class="card"><div class="k">current step</div><div class="v" id="step">–</div><div class="muted" id="steps"></div></div>
<div class="card"><div class="grid" id="totals"></div></div>
<div class="card"><div class="k">indexing jobs per stage</div><table id="stages"></table></div>
<div class="card"><div class="k">libraries</div><table id="libs"></table></div>
<div class="card"><div class="k">recent failures</div><table id="errs"></table></div>
<div class="card"><div class="k">log (last lines)</div><pre id="log"></pre></div>
</main><script>
const el=id=>document.getElementById(id), esc=s=>String(s??"").replace(/[&<>]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;"}[c]));
const bar=(d,t)=>`<div class="bar"><i style="width:${t?Math.min(100,100*d/t):0}%"></i></div>`;
async function tick(){try{const s=await (await fetch("api/status")).json();
el("when").textContent="updated "+new Date().toLocaleTimeString();
el("step").textContent=s.step||"–"; el("steps").textContent=(s.steps||[]).join("  →  ");
el("totals").innerHTML=Object.entries(s.totals).map(([k,v])=>`<div><div class="k">${esc(k)}</div><div class="v">${Number(v).toLocaleString()}</div></div>`).join("");
el("stages").innerHTML="<tr><th>stage</th><th></th><th>done</th><th>failed</th><th>of</th></tr>"+s.stages.map(r=>`<tr><td>${esc(r.stage)}</td><td>${bar(r.done,r.of)}</td><td class="n">${r.done}</td><td class="n ${r.failed?"bad":""}">${r.failed}</td><td class="n">${r.of}</td></tr>`).join("");
el("libs").innerHTML="<tr><th>library</th><th>files</th><th>probed</th><th>signatures</th><th>seasons</th><th>episodes</th><th>in Plex</th></tr>"+s.libraries.map(r=>`<tr><td class="lib" dir="auto">${esc(r.name)}</td><td class="n">${r.files}</td><td class="n">${r.probed}</td><td class="n">${r.signatures}/${r.long}</td><td class="n">${r.seasons}</td><td class="n">${r.episodes}</td><td class="n ${r.collections?"ok":""}">${r.collections}</td></tr>`).join("");
el("errs").innerHTML=s.errors.length?s.errors.map(r=>`<tr><td>${esc(r.stage)}</td><td class="lib" dir="auto">${esc(r.file)}</td><td class="bad">${esc(r.error)}</td><td>${esc(r.attempts)}</td></tr>`).join(""):"<tr><td class='muted'>none</td></tr>";
el("log").textContent=s.log.join("\\n");}catch(e){el("when").textContent="offline: "+e}}
tick(); setInterval(tick,4000);
</script></body></html>"""


def status(db_path: str, log_path: Path | None) -> dict[str, Any]:
    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=30)
    conn.row_factory = sqlite3.Row

    def has(table: str) -> bool:
        return bool(
            conn.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (table,)
            ).fetchone()
        )

    out: dict[str, Any] = {"stages": [], "libraries": [], "errors": [], "log": [], "steps": []}
    files, long_ = conn.execute("SELECT COUNT(*), SUM(duration_s >= 180) FROM files").fetchone()
    out["totals"] = {
        "files": files,
        "episode-length": long_ or 0,
        "seasons": conn.execute("SELECT COUNT(*) FROM series").fetchone()[0]
        if has("series")
        else 0,
        "Plex collections written": (
            conn.execute("SELECT COUNT(*) FROM plex_collections").fetchone()[0]
            if has("plex_collections")
            else 0
        ),
    }
    if has("index_jobs"):
        of = {"probe": files, "thumbnail": long_ or 0, "signature": long_ or 0}
        for r in conn.execute(
            "SELECT stage, SUM(state='done') d, SUM(state='failed') f FROM index_jobs GROUP BY stage"
        ):
            out["stages"].append(
                {
                    "stage": r["stage"],
                    "done": r["d"],
                    "failed": r["f"],
                    "of": of.get(r["stage"], r["d"] + r["f"]),
                }  # fmt: skip
            )
        out["errors"] = [
            {
                "stage": r["stage"],
                "file": Path(r["path"]).name,
                "error": r["error"],
                "attempts": r["attempts"],
            }  # fmt: skip
            for r in conn.execute(
                "SELECT stage, path, error, attempts FROM index_jobs WHERE state='failed' "
                "ORDER BY updated_at DESC LIMIT 15"
            )
        ]
    sig = "(SELECT COUNT(*) FROM index_jobs j WHERE j.path IN (SELECT path FROM files f2 WHERE f2.folder=f.folder) AND j.stage='signature' AND j.state='done')"
    if not has("index_jobs"):
        sig = "0"
    seasons = "(SELECT COUNT(*) FROM series s WHERE json_extract(s.metadata,'$.folder')=f.folder)"
    eps = (
        "(SELECT COUNT(*) FROM series_episodes e JOIN series s ON s.id=e.series_id "
        "WHERE json_extract(s.metadata,'$.folder')=f.folder)"
    )
    cols = (
        "(SELECT COUNT(*) FROM plex_collections c WHERE c.section IN "
        "(SELECT plex_section FROM files f3 WHERE f3.folder=f.folder))"
        if has("plex_collections")
        else "0"
    )
    if not has("series"):
        seasons = eps = "0"
    for r in conn.execute(
        f"""SELECT f.folder, COUNT(*) n, SUM(f.duration_s IS NOT NULL) probed,
                   SUM(f.duration_s >= 180) long, {sig} sig, {seasons} seasons, {eps} eps, {cols} cols
            FROM files f GROUP BY f.folder ORDER BY f.folder"""  # noqa: S608 — fixed fragments
    ):
        out["libraries"].append(
            {
                "name": r["folder"].replace("\\", "/").rstrip("/").rsplit("/", 1)[-1],
                "files": r["n"],
                "probed": r["probed"] or 0,
                "long": r["long"] or 0,
                "signatures": r["sig"] or 0,
                "seasons": r["seasons"],
                "episodes": r["eps"],
                "collections": r["cols"],
            }  # fmt: skip
        )
    conn.close()
    if log_path and log_path.exists():
        lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
        steps = [m.group(1) for ln in lines if (m := _STEP_RE.match(ln))]
        out["steps"] = steps
        out["step"] = steps[-1] if steps else None
        out["log"] = lines[-40:]
    return out


def serve(db_path: str, log_path: Path | None, port: int = 8770) -> None:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802 — http.server API
            if self.path.startswith("/api/status"):
                body = json.dumps(status(db_path, log_path), ensure_ascii=False).encode()
                ctype = "application/json; charset=utf-8"
            elif self.path in ("/", "/index.html"):
                body, ctype = PAGE.encode(), "text/html; charset=utf-8"
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", ctype)
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_: object) -> None:
            return

    print(f"mediaintel status on http://localhost:{port}/  (Ctrl+C to stop)", flush=True)
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
