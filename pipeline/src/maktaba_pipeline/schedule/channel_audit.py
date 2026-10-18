# ruff: noqa: E501  (the report strings read better unwrapped, as in the mediaintel tool)
"""Audit: is what each channel airs about the channel?

Looks at what dizqueTV actually holds (its channels, their programmes and filler lists) and
traces every item back to its file, series and tags. Each item gets a verdict against its
channel's theme (the same rules the planner uses):

* ``strong``: the show's genre is the theme, or a topic/occasion tag with real evidence
  (the name, the description, most episodes), or the episode's own tag;
* ``weak``: matched only on thin evidence: a playlist name, having aired during
  Ramadan, a few episode titles, or a person's name («الشيخ رمضان …» is not Ramadan);
* ``audience``/``format``: matched by audience (Kids) or format (Talk Shows), the
  channel's own rule;
* ``none``: nothing ties it to the channel.

Pure: the caller reads dizqueTV and the tag tables and hands them in as an
:class:`AuditLookup`. Shared by the queue engine's ``dizquetv_audit`` stage and the
stand-alone mediaintel tool (``scripts/mediaintel/audit_channels.py``).
"""

from __future__ import annotations

import html
import re
import time
from collections import defaultdict
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from .channel_planner import PlannerConfig, ThemeSpec

__all__ = [
    "NAME_RAMADAN",
    "VERDICTS",
    "WINDOW_DAYS",
    "AuditLookup",
    "ChannelAudit",
    "Item",
    "airtime",
    "audit",
    "evidence_strength",
    "html_report",
    "judge",
    "summarize",
    "text_report",
]

#: a person's name, not the month: «الشيخ رمضان الشنوري», «سعيد رمضان», «رمضان عبد المعز»
NAME_RAMADAN = re.compile(
    r"(?:الشيخ|الشّيخ|الدكتور|د\.|الأستاذ|أ\.|سعيد|مسجد(?:\s+\S+){0,3})\s+رمضان(?!\s*(?:المبارك|الكريم))"
    r"|رمضان\s+(?:عبد|الشنوري|البوطي|صبحي)"
)
EPS = re.compile(r"eps (\d+)/(\d+)")
VERDICTS = ("strong", "design", "audience", "format", "weak", "none")
#: what airs in this window from now is weighed (minutes on air, not the archive)
WINDOW_DAYS = 28


@dataclass
class Item:
    key: str
    title: str
    path: str | None
    series: str | None
    library: str
    show: str  # the dizqueTV show (lane) it airs in, "" for filler
    minutes: float
    verdict: str = "none"
    why: str = ""


@dataclass
class ChannelAudit:
    number: int
    name: str
    theme: ThemeSpec | None
    programmes: list[Item] = field(default_factory=list)
    fillers: list[Item] = field(default_factory=list)


def evidence_strength(evidence: str) -> str:
    """``strong`` when a tag rests on the name, the description or most episodes;
    ``weak`` when only on a playlist, on having aired in Ramadan or on a few titles."""
    parts = [p.strip() for p in (evidence or "").split(";") if p.strip()]
    for p in parts:
        if p.startswith(("name:", "desc", "library")):
            return "strong"
        if m := EPS.match(p):
            k, n = int(m.group(1)), int(m.group(2))
            if n and k / n >= 0.5:
                return "strong"
    return "weak" if parts else "strong"


def judge(
    theme: ThemeSpec,
    cfg: PlannerConfig,
    title: str,
    library: str,
    series_tags: Mapping[str, tuple[float, str]],
    item_tags: Mapping[str, float],
) -> tuple[str, str]:
    """The item's verdict against the channel's theme, and why."""
    lo = cfg.topic_min_score
    lib = set(cfg.library_tags.get(library, ()))
    for tag in lib:
        if tag in {f"audience:{a}" for a in theme.exclude} | set(theme.exclude):
            return "none", f"excluded: library is {tag}"
    genre = next((t.split(":", 1)[1] for t in series_tags if t.startswith("genre:")), "")
    if genre and genre in theme.topics:
        return "strong", f"genre {genre}"
    wanted = [f"topic:{t}" for t in theme.topics] + [f"occasion:{o}" for o in theme.occasions]
    weak_why = ""
    for tag in wanted:
        if item_tags.get(tag, 0.0) >= lo:
            if tag == "occasion:ramadan" and NAME_RAMADAN.search(title):
                weak_why = weak_why or "episode: «رمضان» is a person's or a mosque's name"
                continue
            return "strong", f"episode {tag}"
    for tag in wanted:
        score, ev = series_tags.get(tag, (0.0, ""))
        if score >= lo:
            if evidence_strength(ev) == "strong":
                return "strong", f"show {tag} ({ev[:80]})"
            weak_why = weak_why or f"show {tag} on thin evidence ({ev[:80]})"
    if weak_why:
        return "weak", weak_why
    for a in theme.audiences:
        tag = f"audience:{a}"
        if tag in lib or series_tags.get(tag, (0.0, ""))[0] >= lo or item_tags.get(tag, 0) >= lo:
            return "audience", tag + (" (library rule)" if tag in lib else "")
    for f_ in theme.formats:
        tag = f"format:{f_}"
        if tag in lib or series_tags.get(tag, (0.0, ""))[0] >= lo or item_tags.get(tag, 0) >= lo:
            return "format", tag + (" (library rule)" if tag in lib else "")
    top = sorted(
        ((s, t) for t, (s, _) in series_tags.items() if t.startswith(("topic:", "occasion:"))),
        reverse=True,
    )[:2]
    own = sorted(((s, t) for t, s in item_tags.items() if t.startswith("topic:")), reverse=True)[:2]
    about = ", ".join(t for _, t in (own or top)) or (f"genre {genre}" if genre else "untagged")
    return "none", f"about {about}"


def airtime(ch: Mapping[str, Any], now_ms: int, days: int = WINDOW_DAYS) -> dict[int, float]:
    """Minutes each programme (by index) is on air from now for ``days``."""
    progs = ch.get("programs") or []
    total = sum(int(p.get("duration", 0)) for p in progs)
    if not progs or total <= 0:
        return {}
    start = int(
        datetime.fromisoformat(str(ch["startTime"]).replace("Z", "+00:00")).timestamp() * 1000
    )
    t = start + max(0, (now_ms - start)) // total * total
    end = now_ms + days * 86_400_000
    out: dict[int, float] = defaultdict(float)
    while t < end:
        for i, p in enumerate(progs):
            d = int(p.get("duration", 0))
            a, b = max(t, now_ms), min(t + d, end)
            if b > a:
                out[i] += (b - a) / 60_000
            t += d
            if t >= end:
                break
    return out


@dataclass(slots=True)
class AuditLookup:
    """What the audit traces a programme back to (built by the caller from its own store)."""

    #: dizqueTV ``ratingKey`` -> the item's id (file path / video id)
    item_of: Mapping[str, str]
    #: item id -> series (or season) id
    series_of: Mapping[str, str]
    #: series id -> library name
    library_of: Mapping[str, str]
    #: item id -> its folder's name (the library of an item outside any series)
    folder_of: Mapping[str, str]
    #: series id -> tag -> (score, evidence)
    series_tags: Mapping[str, Mapping[str, tuple[float, str]]]
    #: item id -> tag -> score (an episode's own tags win)
    item_tags: Mapping[str, Mapping[str, float]]


def audit(
    channels: Sequence[Mapping[str, Any]],
    fillers: Mapping[str, Mapping[str, Any]],
    lookup: AuditLookup,
    cfg: PlannerConfig,
    now_ms: int | None = None,
    *,
    window_days: int = WINDOW_DAYS,
    on_channel: Callable[[int, int, str], None] | None = None,
) -> list[ChannelAudit]:
    """Every channel's programmes (what airs in the next ``window_days``) and filler clips,
    each with its verdict against the channel's theme."""
    now_ms = now_ms if now_ms is not None else int(time.time() * 1000)
    themes = {t.key: t for t in cfg.themes}

    def item(prog: Mapping[str, Any], show: str) -> Item:
        key = str(prog.get("ratingKey"))
        path = lookup.item_of.get(key)
        sid = lookup.series_of.get(path) if path else None
        lib = (lookup.library_of.get(sid) if sid else None) or (
            lookup.folder_of.get(path, "") if path else ""
        )
        return Item(
            key, str(prog.get("title", "")), path, sid, lib or "", show,
            int(prog.get("duration", 0)) / 60_000,
        )  # fmt: skip

    out: list[ChannelAudit] = []
    for k_ch, ch in enumerate(channels, 1):
        if on_channel is not None:
            on_channel(k_ch, len(channels), str(ch.get("name", "")))
        plan = ch.get("maktabaPlan") or {}
        theme = themes.get(str(plan.get("key", "")))
        ca = ChannelAudit(int(ch["number"]), str(ch.get("name", "")), theme)
        seen: dict[str, Item] = {}
        progs = ch.get("programs", [])
        for i, mins in airtime(ch, now_ms, window_days).items():
            prog = progs[i]
            if prog.get("isOffline"):
                continue
            k = str(prog.get("ratingKey"))
            if k not in seen:
                seen[k] = item(prog, str(prog.get("customShowName", "")))
                seen[k].minutes = 0.0
                ca.programmes.append(seen[k])
            seen[k].minutes += mins
        # each clip weighs what dizqueTV picks it: its list's weight over its list's size
        fcs = [(fc, fillers.get(str(fc.get("id"))) or {}) for fc in ch.get("fillerCollections", [])]
        wsum = sum(float(fc.get("weight", 1)) for fc, f in fcs if f.get("content")) or 1.0
        for fc, f in fcs:
            content = f.get("content", [])
            promos = str(f.get("name", "")).rstrip("]").endswith(".promos")
            for prog in content:
                it = item(prog, "promo" if promos else "")
                it.minutes = 100 * float(fc.get("weight", 1)) / wsum / len(content)
                ca.fillers.append(it)
        if theme is not None:
            for it in [*ca.programmes, *ca.fillers]:
                if it.show == "promo":
                    it.verdict, it.why = "strong", "a promo of one of the channel's shows"
                    continue
                if theme.kind != "quran" and "library.quran" in it.show:
                    it.verdict, it.why = "design", "the dawn Quran hour every channel has"
                    continue
                it.verdict, it.why = judge(
                    theme, cfg, it.title, it.library,
                    lookup.series_tags.get(it.series or "", {}),
                    lookup.item_tags.get(it.path or "", {}),
                )  # fmt: skip
        else:
            for it in [*ca.programmes, *ca.fillers]:
                it.verdict, it.why = "strong", "not a theme channel (presenter / mix)"
        out.append(ca)
    return out


# --------------------------------------------------------------------------- output


def _hours(items: Iterable[Item]) -> float:
    return sum(i.minutes for i in items) / 60


def _share(items: Sequence[Item]) -> dict[str, float]:
    total = _hours(items) or 1.0
    return {v: 100 * _hours(i for i in items if i.verdict == v) / total for v in VERDICTS}


def _by_show(items: Sequence[Item]) -> list[tuple[str, list[Item]]]:
    groups: dict[str, list[Item]] = defaultdict(list)
    for i in items:
        groups[i.show or i.library].append(i)
    return sorted(groups.items(), key=lambda kv: -_hours(kv[1]))


def summarize(ca: ChannelAudit) -> dict[str, Any]:
    """One channel's result as plain JSON (what the engine stores): hours and verdict
    shares of its programmes and filler, and per show where the hours went."""
    prog, fill = _share(ca.programmes), _share(ca.fillers) if ca.fillers else None
    shows = []
    for show, items in _by_show(ca.programmes):
        why: dict[str, float] = defaultdict(float)
        for i in items:
            why[f"{i.verdict}: {i.why}"] += i.minutes
        shows.append({
            "show": show,
            "library": items[0].library,
            "hours": round(_hours(items), 2),
            "share": {k: round(v, 1) for k, v in _share(items).items()},
            "why": max(why.items(), key=lambda kv: kv[1])[0],
            "examples": [
                {"title": i.title, "verdict": i.verdict, "why": i.why}
                for i in items
                if i.verdict in ("weak", "none")
            ][:8],
        })  # fmt: skip
    return {
        "number": ca.number,
        "name": ca.name,
        "theme": ca.theme.key if ca.theme is not None else None,
        "hours": round(_hours(ca.programmes), 2),
        "share": {k: round(v, 1) for k, v in prog.items()},
        "filler_clips": len(ca.fillers),
        "filler_share": None if fill is None else {k: round(v, 1) for k, v in fill.items()},
        "shows": shows,
        "filler_unrelated": [
            {"title": i.title, "library": i.library, "verdict": i.verdict, "why": i.why}
            for i in ca.fillers
            if i.verdict in ("none", "weak")
        ][:40],
    }


def text_report(audits: Sequence[ChannelAudit], window_days: int = WINDOW_DAYS) -> str:
    lines = ["CHANNEL AUDIT: is what each channel airs about the channel?",
             f"(hours on air in the next {window_days} days; the dawn Quran hour is counted as"
             " strong: every channel has it)", "",
             f"{'channel':<34}{'hours':>8}{'strong':>8}{'aud/fmt':>8}{'weak':>7}{'none':>7}"
             f"{'filler none':>13}"]  # filler: share of what dizqueTV picks  # fmt: skip
    for ca in sorted(audits, key=lambda a: a.number):
        s = _share(ca.programmes)
        fs = _share(ca.fillers) if ca.fillers else {"none": 0.0}
        lines.append(f"#{ca.number} {ca.name[:29]:<30}{_hours(ca.programmes):>8.0f}{s['strong'] + s['design']:>7.0f}%"
                     f"{s['audience'] + s['format']:>7.0f}%{s['weak']:>6.0f}%{s['none']:>6.0f}%"
                     f"{fs['none']:>12.0f}%")  # fmt: skip
    for ca in sorted(audits, key=lambda a: a.number):
        if ca.theme is None:
            continue
        lines += ["", f"=== #{ca.number} {ca.name}: shows that are weak or unrelated ==="]
        for show, items in _by_show(ca.programmes):
            bad = [i for i in items if i.verdict in ("weak", "none")]
            if not bad or _hours(bad) < 0.5:
                continue
            why: dict[str, float] = defaultdict(float)
            for i in bad:
                why[f"{i.verdict}: {i.why}"] += i.minutes
            top = max(why.items(), key=lambda kv: kv[1])[0]
            lines.append(
                f"  {_hours(bad):6.1f} of {_hours(items):6.1f} h  {show[:60]}  [{items[0].library[:30]}]"
            )
            lines.append(f"           {top[:140]}")
            for i in bad[:2]:
                lines.append(f"           e.g. {i.title[:110]}")
        fb = [i for i in ca.fillers if i.verdict == "none"]
        if fb:
            lines.append(f"  filler: {len(fb)} of {len(ca.fillers)} clips unrelated, e.g. "
                         + " | ".join(i.title[:50] for i in fb[:3]))  # fmt: skip
    return "\n".join(lines) + "\n"


def html_report(audits: Sequence[ChannelAudit], window_days: int = WINDOW_DAYS) -> str:
    e = html.escape
    rows = []
    for ca in sorted(audits, key=lambda a: a.number):
        s = _share(ca.programmes)
        fs = _share(ca.fillers) if ca.fillers else {"none": 0.0, "weak": 0.0}
        rows.append(
            f"<tr><td><a href='#c{ca.number}'>#{ca.number} {e(ca.name)}</a></td>"
            f"<td>{_hours(ca.programmes):.0f}</td><td class=g>{s['strong'] + s['design']:.0f}%</td>"
            f"<td>{s['audience'] + s['format']:.0f}%</td><td class=w>{s['weak']:.0f}%</td>"
            f"<td class=b>{s['none']:.0f}%</td><td>{len(ca.fillers)}</td>"
            f"<td class=b>{fs['none']:.0f}%</td></tr>"
        )
    parts = [
        "<!doctype html><meta charset=utf-8><title>Channel audit</title><style>"
        "body{font:14px system-ui;margin:24px;background:#fafaf7;color:#222}"
        "table{border-collapse:collapse;margin:8px 0}td,th{padding:4px 8px;border-bottom:1px solid #ddd;"
        "text-align:left;vertical-align:top}.g{color:#17803d}.w{color:#b26b00}.b{color:#b42318}"
        "details{margin:4px 0}summary{cursor:pointer}.ar{direction:rtl;unicode-bidi:plaintext}"
        "small{color:#666}</style><h1>Channel audit</h1>"
        f"<p>Hours on air in the next {window_days} days. What dizqueTV holds, traced to each file's series and tags, judged against the "
        "channel's theme. <b>weak</b>: matched on a playlist name, on having aired in Ramadan, "
        "on a few titles, or on a person's name. <b>none</b>: nothing ties it to the channel.</p>"
        "<table><tr><th>channel<th>hours<th>strong<th>audience/format<th>weak<th>none"
        "<th>filler clips<th>filler none</tr>" + "".join(rows) + "</table>"
    ]
    for ca in sorted(audits, key=lambda a: a.number):
        parts.append(f"<h2 id=c{ca.number}>#{ca.number} {e(ca.name)}</h2>")
        if ca.theme is None:
            parts.append("<p><small>not a theme channel</small></p>")
            continue
        t = ca.theme
        parts.append(f"<p><small>rules: topics {', '.join(t.topics) or '-'}; occasions "
                     f"{', '.join(t.occasions) or '-'}; audiences {', '.join(t.audiences) or '-'};"
                     f" formats {', '.join(t.formats) or '-'}</small></p><table>"
                     "<tr><th>show<th>library<th>hours<th>strong<th>weak<th>none<th>why (most hours)</tr>")  # fmt: skip
        for show, items in _by_show(ca.programmes):
            s = _share(items)
            why: dict[str, float] = defaultdict(float)
            for i in items:
                why[f"{i.verdict}: {i.why}"] += i.minutes
            top = max(why.items(), key=lambda kv: kv[1])[0]
            bad = [i for i in items if i.verdict in ("weak", "none")]
            eg = "".join(f"<div class=ar>{e(i.title)} <small>({e(i.verdict)}: {e(i.why)})</small></div>"
                         for i in bad[:8])  # fmt: skip
            cell = (
                f"<details><summary class=ar>{e(show)}</summary>{eg}</details>"
                if eg
                else (f"<span class=ar>{e(show)}</span>")
            )
            parts.append(
                f"<tr><td>{cell}</td><td class=ar>{e(items[0].library)}</td>"
                f"<td>{_hours(items):.1f}</td><td class=g>{s['strong'] + s['design'] + s['audience'] + s['format']:.0f}%</td>"
                f"<td class=w>{s['weak']:.0f}%</td><td class=b>{s['none']:.0f}%</td>"
                f"<td class=ar><small>{e(top[:160])}</small></td></tr>"
            )
        parts.append("</table>")
        fb = [i for i in ca.fillers if i.verdict in ("none", "weak")]
        if fb:
            eg = "".join(f"<div class=ar>{e(i.title)} <small>[{e(i.library)}] {e(i.verdict)}: "
                         f"{e(i.why)}</small></div>" for i in fb[:40])  # fmt: skip
            parts.append(f"<details><summary>filler: {len(fb)} of {len(ca.fillers)} clips weak or "
                         f"unrelated</summary>{eg}</details>")  # fmt: skip
    return "".join(parts)
