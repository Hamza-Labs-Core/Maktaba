# ruff: noqa: E501  (the page CSS and long HTML templates read better unwrapped)
"""The channel plan as one HTML page (the control centre's Reports → Channel plan).

Ported from mediaintel's ``plan_channels.render_html`` (same look as the review pages),
reading Maktaba's plan JSON (:mod:`._plan_json`, format 3): a slot's repeat pattern is its
lane's, the stats carry airtime rather than dead air, and a pool lists clip counts, not
clips. The page has no script, so the API can serve it under a ``script-src 'none'`` policy.
"""

from __future__ import annotations

import html
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from ..schedule.channel_planner import WEEKDAYS

__all__ = ["PLAN_PAGE", "PREVIEW_PAGE", "render_plan", "write_plan_page"]

#: file names in the reports folder (``review_pages.reviews_dir``)
PLAN_PAGE = "channel-plan.html"
PREVIEW_PAGE = "channel-plan-preview.html"

_CSS = """
:root{--bg:#f6f5f2;--card:#fff;--ink:#1c1b19;--mute:#6b675f;--line:#e2ded6;--acc:#2f6f5e;--warn:#a5532a}
@media (prefers-color-scheme:dark){:root{--bg:#161614;--card:#21201d;--ink:#ecebe6;--mute:#a19d93;--line:#33312c;--acc:#6fbfa6;--warn:#e0956b}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.45 system-ui,"Segoe UI",Tahoma,sans-serif}
header{padding:20px 16px 8px;max-width:1400px;margin:auto}h1{margin:0 0 4px;font-size:22px}
.sum{color:var(--mute)}main{max-width:1400px;margin:auto;padding:0 16px 40px}
nav{display:flex;flex-wrap:wrap;gap:6px;margin:12px 0 20px}nav a{padding:3px 9px;border:1px solid var(--line);border-radius:12px;color:var(--ink);text-decoration:none;font-size:13px;background:var(--card)}
h2{margin:0;font-size:18px}.kind{font-size:12px;color:var(--mute);border:1px solid var(--line);border-radius:10px;padding:1px 8px;margin-inline-start:8px}
h3{font-size:15px;margin:14px 0 6px;color:var(--acc)}.meta{color:var(--mute);font-size:12px;font-weight:normal}
details{margin:8px 0}summary{cursor:pointer;color:var(--mute)}
.ch{margin:28px 0;border-top:1px solid var(--line);padding-top:14px}
.num{display:inline-block;min-width:2.6em;text-align:center;border-radius:6px;background:var(--acc);color:var(--bg);font-weight:600;margin-inline-end:8px}
ul.why{margin:6px 0 10px;padding-inline-start:20px;color:var(--mute)}ul.warn{color:var(--warn)}
.bar{display:flex;height:34px;border:1px solid var(--line);border-radius:6px;overflow:hidden;margin:4px 0 2px}
.bar div{overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-size:11px;padding:2px 4px;border-inline-end:1px solid var(--bg);color:#111}
.dayrow{display:grid;grid-template-columns:92px 1fr;gap:8px;align-items:center}.dayrow .d{font-size:12px;color:var(--mute)}
.ticks{display:flex;font-size:10px;color:var(--mute);margin-inline-start:100px}.ticks span{flex:1}
.tg-weekly{background:#9cc3e6}.tg-strip{background:#e6c27a}.tg-strip7{background:#e59a9a}.tg-quran{background:#c8d6b0}.tg-khutbah{background:#cfc6e6}
.tg-flex{background:repeating-linear-gradient(45deg,var(--line),var(--line) 4px,var(--card) 4px,var(--card) 8px);color:var(--mute)!important}
table{border-collapse:collapse;width:100%;font-size:12.5px;margin:6px 0}th,td{border-bottom:1px solid var(--line);padding:3px 6px;text-align:start;vertical-align:top}
th{color:var(--mute);font-weight:600}.k-filler td{color:var(--mute)}.k-flex td{color:var(--warn)}
.pill{font-size:11px;border:1px solid var(--line);border-radius:10px;padding:0 6px;margin-inline-end:4px;white-space:nowrap}
.legend span{display:inline-block;padding:1px 8px;border-radius:8px;margin-inline-end:6px;font-size:12px;color:#111}
.banner{border:1px solid var(--warn);color:var(--warn);border-radius:8px;padding:6px 10px;margin:8px 0}
"""


def _e(s: object) -> str:
    return html.escape(str(s))


def _name(s: object) -> str:
    return f"<bdi>{_e(s)}</bdi>"


def _pattern(slot: Mapping[str, Any], lanes: Mapping[str, Any]) -> str:
    lane = lanes.get(slot.get("lane") or "")
    return str((lane or {}).get("pattern") or "")


def _day_rows(slots: Sequence[Mapping[str, Any]], lanes: Mapping[str, Any]) -> str:
    """One bar per distinct weekday pattern (all days alike -> one 'daily' row)."""
    patterns: dict[str, list[str]] = {}
    per_day: dict[str, list[Mapping[str, Any]]] = {}
    for day in WEEKDAYS:
        segs = [s for s in slots if s["days"] == "daily" or day in s["days"]]
        segs.sort(key=lambda s: str(s["start"]))
        per_day[day] = segs
        sig = json.dumps([(s["start"], s["end"], s["lane"]) for s in segs])
        patterns.setdefault(sig, []).append(day)
    rows: list[str] = []
    for days in patterns.values():
        label = "daily" if len(days) == 7 else ", ".join(days)
        cells = []
        for s in per_day[days[0]]:
            tg = _pattern(s, lanes) or "flex"
            title = f"{s['start']}-{s['end']} {s['lane_name'] or 'filler only'} ({s['daypart']})"
            cells.append(
                f'<div class="tg-{_e(tg)}" style="flex:{int(s["minutes"])}" title="{_e(title)}">'
                f"{_e(s['start'])} {_name(s['lane_name'] or 'flex')}</div>"
            )
        rows.append(
            f'<div class="dayrow"><span class="d">{_e(label)}</span>'
            f'<div class="bar">{"".join(cells)}</div></div>'
        )
    ticks = "".join(f"<span>{h:02d}</span>" for h in range(0, 24, 3))
    return "".join(rows) + f'<div class="ticks">{ticks}</div>'


def _slot_table(slots: Sequence[Mapping[str, Any]], lanes: Mapping[str, Any]) -> str:
    rows = "".join(
        f"<tr><td>{_e(s['start'])}-{_e(s['end'])}</td>"
        f"<td>{_e(s['days'] if s['days'] == 'daily' else ', '.join(s['days']))}</td>"
        f"<td>{_e(s['daypart'])}</td><td>{_name(s['lane_name'] or '- filler only')}</td>"
        f"<td>{_e(_pattern(s, lanes) or '-')}</td><td>{_e(s['filler_pool'] or '-')}</td>"
        f"<td>{'<br>'.join(_e(r) for r in s['reasons'])}</td></tr>"
        for s in slots
    )
    return (
        "<table><tr><th>time</th><th>days</th><th>daypart</th><th>slot</th><th>repeats</th>"
        f"<th>filler pool</th><th>why</th></tr>{rows}</table>"
    )


def _lane_row(key: str, lane: Mapping[str, Any], repeat: object) -> str:
    units = "".join(
        f'<div><span class="pill">{_e(u["genre"])}</span>{_name(u["name"])} S{_e(u["season"])}'
        f" · {len(u['episodes'])} ep · {u['hours']:.1f} h · {_e(u['first_air'] or '?')}"
        f"{' · ramadan' if u['ramadan'] else ''}</div>"
        for u in lane["units"]
    )
    return (
        f"<tr><td>{_name(lane['name'])}</td>"
        f"<td>{_e(lane['pattern'] or lane['special'] or '-')} · {_e(lane['cadence'] or '')}"
        f" · {_e(lane['slot_minutes'] or '-')} min</td>"
        f"<td>{lane['hours']:.1f} h</td><td>{lane['episode_count']}</td>"
        f"<td>{_e(repeat)}</td>"
        f"<td>{len(lane['shows'])} show(s){' · ramadan' if lane['ramadan'] else ''}</td>"
        f"<td><details><summary>{len(lane['units'])} season(s)</summary>{units}</details></td></tr>"
    )


def _channel(ch: Mapping[str, Any], plan: Mapping[str, Any]) -> str:
    lanes: Mapping[str, Any] = plan["lanes"]
    pools: Mapping[str, Any] = plan["filler_pools"]
    st = ch["stats"]
    per_day = " · ".join(f"{k} {v:.0f} min/day" for k, v in st["per_day_minutes"].items())
    lane_rows = "".join(
        _lane_row(k, lanes[k], st["lane_repeat_days"].get(k, "-"))
        for k in ch["lanes"]
        if k in lanes
    )
    pool_rows = "".join(
        f"<tr><td>{_e(k)}</td><td>{pools[k]['count']}</td><td>{pools[k]['minutes']:.0f} min</td>"
        f"<td>{_e(', '.join(f'{s} {n}' for s, n in pools[k]['sources'].items()))}</td>"
        f"<td>{_e(pools[k]['fallback'] or '-')}</td></tr>"
        for k in ch["filler_pools"]
        if k in pools
    )
    sample = "".join(
        f'<tr class="k-{_e(a["kind"])}"><td>{_e(a["start"])}</td><td>{a["minutes"]}</td>'
        f"<td>{_e(a['kind'])}</td><td>{_name(a['title'])}</td></tr>"
        for a in ch["sample_day"]["airings"]
    )
    ramadan = (
        f"<h3>Ramadan grid</h3>{_day_rows(ch['ramadan_slots'], lanes)}"
        f"<details><summary>Ramadan slots</summary>{_slot_table(ch['ramadan_slots'], lanes)}"
        "</details>"
        if ch["ramadan_slots"]
        else ""
    )
    warnings = (
        f'<ul class="why warn">{"".join(f"<li>{_e(w)}</li>" for w in ch["warnings"])}</ul>'
        if ch["warnings"]
        else ""
    )
    return (
        f'<section class="ch" id="ch{_e(ch["number"])}"><h2><span class="num">{_e(ch["number"])}'
        f'</span>{_name(ch["name_ar"] or ch["name"])} <span class="meta">{_e(ch["name"])}</span>'
        f'<span class="kind">{_e(ch["kind"])} · {_e(ch["category"])} · {ch["hours"]:.1f} h</span>'
        f'</h2><ul class="why">{"".join(f"<li>{_e(r)}</li>" for r in ch["reasons"])}</ul>'
        f"{warnings}<h3>Weekly grid</h3>{_day_rows(ch['slots'], lanes)}"
        f'<div class="sum">{_e(per_day)} · programme {st["program_share"]:.0%} (simulated week)</div>'
        f"<details><summary>Slots</summary>{_slot_table(ch['slots'], lanes)}</details>{ramadan}"
        "<h3>Slots and their queues</h3><table><tr><th>slot</th><th>repeats</th><th>content</th>"
        "<th>episodes</th><th>days to repeat</th><th></th><th>seasons</th></tr>"
        f"{lane_rows}</table>"
        "<h3>Filler pools</h3><table><tr><th>pool</th><th>items</th><th>total</th>"
        f"<th>sources</th><th>fallback</th></tr>{pool_rows}</table>"
        f"<details><summary>Sample day ({_e(ch['sample_day']['weekday'])}, "
        f"{len(ch['sample_day']['airings'])} blocks)</summary>"
        f"<table><tr><th>start</th><th>min</th><th>kind</th><th>title</th></tr>{sample}</table>"
        "</details></section>"
    )


def render_plan(plan: Mapping[str, Any], *, title: str = "Channel plan", banner: str = "") -> str:
    """The plan page. ``banner`` is a line shown above the lineup (the preview says it
    is a preview and nothing was written)."""
    nav = "".join(
        f'<a href="#ch{_e(c["number"])}">{_e(c["number"])} · {_name(c["name_ar"] or c["name"])}</a>'
        for c in plan["channels"]
    )
    body = "".join(_channel(c, plan) for c in plan["channels"])
    unplaced = "".join(
        f"<tr><td>{_name(u['what'])}</td><td>{_e(u['why'])}</td></tr>" for u in plan["unplaced"]
    )
    notes = "".join(f"<li>{_e(n)}</li>" for n in plan["notes"])
    legend = "".join(
        f'<span class="tg-{t}">{label}</span>'
        for t, label in (
            ("weekly", "weekly show"),
            ("strip", "weekday strip"),
            ("strip7", "every day"),
            ("quran", "Quran"),
            ("khutbah", "khutbah"),
        )
    )
    note = f'<div class="banner">{_e(banner)}</div>' if banner else ""
    return (
        '<!doctype html><html lang="en" dir="ltr"><head><meta charset="utf-8">'
        '<meta name="viewport" content="width=device-width,initial-scale=1">'
        f"<title>{_e(title)}</title><style>{_CSS}</style></head><body>"
        f'<header><h1>{_e(title)}</h1>{note}<div class="sum">{len(plan["channels"])} channels · '
        f"{len(plan['lanes'])} lanes · {len(plan['filler_pools'])} filler pools · planner "
        f"v{_e(plan['planner_version'])} · {_e(plan['generated_at'])}</div>"
        f'<ul class="why">{notes}</ul>'
        f'<div class="legend">{legend}<span class="tg-flex">filler only</span></div></header>'
        f'<main><nav>{nav}<a href="#unplaced">unplaced</a></nav>{body}'
        '<section class="ch" id="unplaced"><h2>Not on a channel / used as filler</h2>'
        f"<table>{unplaced}</table></section></main></body></html>"
    )


def write_plan_page(
    plan: Mapping[str, Any], name: str = PLAN_PAGE, *, title: str = "Channel plan", banner: str = ""
) -> Path:
    """Render the plan into the reports folder (atomically: a reader never sees half a page)."""
    from .stages.review_pages import reviews_dir  # not at import: the stages import this module

    out = reviews_dir() / name
    out.parent.mkdir(parents=True, exist_ok=True)
    tmp = out.with_suffix(".part.html")
    tmp.write_text(render_plan(plan, title=title, banner=banner), encoding="utf-8")
    tmp.replace(out)
    return out
