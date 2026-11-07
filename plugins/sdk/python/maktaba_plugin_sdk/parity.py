"""The parity proof's comparisons (Epic 54, story 54.26).

A stage that moves into a first-party plugin must do **exactly** what it did in-process.
``tools/plugins/parity.py`` runs the same jobs twice, on two copies of one seeded database
(``impl = inproc`` on A, ``impl = plugin`` on B, through the real plugin API with the
plugin's statements and role), then compares what this module reads:

- the rows of every table the plugin writes (``parity.toml`` ``tables``, by default the
  manifest's ``db.write``), canonical JSON sorted by primary key, with only the columns in
  ``ignore`` masked;
- the files under the areas it writes (sha256 per relative path);
- the queue's own record of each job: its stage, subject, state, outcome, input and output
  hashes, summary and the jobs it enqueued;
- the input and output fingerprints of every subject;
- the job time per stage: p50 ≤ 1.10× and p95 ≤ 1.15× of the in-process path (N16).

Any difference fails, naming the table, the key and the column (the first 20).
"""

from __future__ import annotations

import hashlib
import json
import math
import tomllib
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import date, datetime
from decimal import Decimal
from pathlib import Path
from typing import Any
from uuid import UUID

__all__ = [
    "MAX_SHOWN",
    "ParityConfig",
    "ParityError",
    "canonical",
    "diff_files",
    "diff_rows",
    "files_tree",
    "perf_check",
]

MAX_SHOWN = 20
P50_LIMIT = 1.10
P95_LIMIT = 1.15
#: the queue's columns parity compares for every job (its StageResult and its effects)
JOB_COLUMNS = (
    "stage",
    "subject_kind",
    "subject_id",
    "library_id",
    "state",
    "outcome",
    "input_hash",
    "output_hash",
    "stage_version",
    "output_summary",
    "error_cause",
    "attempts",
    "priority",
    "payload",
    "reasons",
    "triggered_by_stage",
)

_KEYS = {"tables", "ignore", "cases", "failure_cases", "calls_note", "files", "perf"}


class ParityError(ValueError):
    """A parity.toml that does not follow the schema (unknown keys are refused, T06)."""


@dataclass(frozen=True)
class ParityConfig:
    tables: tuple[str, ...] = ()
    ignore: Mapping[str, tuple[str, ...]] = field(default_factory=dict)
    cases: tuple[Mapping[str, Any], ...] = ()
    failure_cases: tuple[Mapping[str, Any], ...] = ()
    files: tuple[str, ...] = ()
    #: why the plugin API calls per job may grow more than 10 % in this pull request
    calls_note: str = ""
    #: false: report the timing, do not fail on it (a stage whose jobs take milliseconds)
    perf: bool = True

    @classmethod
    def parse(cls, text: str, *, where: str = "parity.toml") -> ParityConfig:
        raw = tomllib.loads(text)
        unknown = set(raw) - _KEYS
        if unknown:
            raise ParityError(f"{where}: unknown keys {sorted(unknown)}")
        ignore = raw.get("ignore") or {}
        if not isinstance(ignore, dict) or not all(isinstance(v, list) for v in ignore.values()):
            raise ParityError(f"{where}: ignore is a table of column lists")
        for k in ("cases", "failure_cases"):
            for c in raw.get(k) or ():
                if not isinstance(c, dict) or "name" not in c:
                    raise ParityError(f"{where}: each of {k} needs a name")
        return cls(
            tables=tuple(raw.get("tables") or ()),
            ignore={str(t): tuple(map(str, cols)) for t, cols in ignore.items()},
            cases=tuple(raw.get("cases") or ()),
            failure_cases=tuple(raw.get("failure_cases") or ()),
            files=tuple(raw.get("files") or ()),
            calls_note=str(raw.get("calls_note") or ""),
            perf=bool(raw.get("perf", True)),
        )

    @classmethod
    def load(cls, path: Path) -> ParityConfig:
        return cls.parse(path.read_text(encoding="utf-8"), where=str(path))


def _plain(v: Any) -> Any:
    if isinstance(v, UUID | Decimal):
        return str(v)
    if isinstance(v, datetime | date):
        return v.isoformat()
    if isinstance(v, bytes | bytearray | memoryview):
        return "sha256:" + hashlib.sha256(bytes(v)).hexdigest()
    if isinstance(v, float) and (math.isnan(v) or math.isinf(v)):
        return repr(v)
    if isinstance(v, str) and v[:1] in "[{":
        try:
            return json.loads(v)  # JSON text: compared as data
        except ValueError:
            return v
    if isinstance(v, list | tuple):
        return [_plain(x) for x in v]
    if isinstance(v, dict):
        return {str(k): _plain(x) for k, x in v.items()}
    return v


def canonical(row: Mapping[str, Any], ignore: Iterable[str] = ()) -> dict[str, Any]:
    """A row as compared: each ignored column masked, and an ignored ``column.key.key``
    (a dotted path) masked inside that JSON column (a measured time in ``metadata``)."""
    masked = {x for x in ignore if "." not in x}
    paths = [x.split(".") for x in ignore if "." in x]
    out = {k: ("<ignored>" if k in masked else _plain(v)) for k, v in sorted(row.items())}
    for col, *keys in paths:
        if col in out:
            out[col] = _mask_path(out[col], keys)
    return out


def _mask_path(v: Any, keys: list[str]) -> Any:
    if isinstance(v, str) and v[:1] in "[{":
        try:
            v = json.loads(v)
        except ValueError:
            return v
    if not isinstance(v, dict) or not keys:
        return v
    k, rest = keys[0], keys[1:]
    if k not in v:
        return v
    return {**v, k: "<ignored>" if not rest else _mask_path(v[k], rest)}


def diff_rows(
    table: str,
    a: Sequence[Mapping[str, Any]],
    b: Sequence[Mapping[str, Any]],
    key: Sequence[str],
    ignore: Iterable[str] = (),
) -> list[str]:
    """What differs between two tables' rows, by key: a row only in one, or a column."""
    ig = tuple(ignore)

    def index(rows: Sequence[Mapping[str, Any]]) -> dict[str, dict[str, Any]]:
        out: dict[str, dict[str, Any]] = {}
        for r in rows:
            c = canonical(r, ig)
            k = json.dumps([c.get(x) for x in key], ensure_ascii=False, default=str)
            out[k] = c
        return out

    ia, ib = index(a), index(b)
    lines: list[str] = []
    for k in sorted(set(ia) | set(ib)):
        if k not in ib:
            lines.append(f"{table} {k}: only in-process")
        elif k not in ia:
            lines.append(f"{table} {k}: only through the plugin")
        else:
            for col in sorted(set(ia[k]) | set(ib[k])):
                if ia[k].get(col) != ib[k].get(col):
                    lines.append(
                        f"{table} {k} {col}: in-process {json.dumps(ia[k].get(col), ensure_ascii=False, default=str)}"  # noqa: E501
                        f" != plugin {json.dumps(ib[k].get(col), ensure_ascii=False, default=str)}"
                    )
    return lines


def files_tree(root: Path) -> dict[str, str]:
    if not root.is_dir():
        return {}
    return {
        str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in sorted(root.rglob("*"))
        if p.is_file()
    }


def diff_files(area: str, a: Mapping[str, str], b: Mapping[str, str]) -> list[str]:
    out = []
    for k in sorted(set(a) | set(b)):
        if a.get(k) != b.get(k):
            out.append(
                f"file {area}/{k}: in-process {a.get(k, 'missing')} != plugin {b.get(k, 'missing')}"
            )
    return out


def _quantile(xs: Sequence[float], q: float) -> float:
    s = sorted(xs)
    if not s:
        return 0.0
    i = min(len(s) - 1, max(0, math.ceil(q * len(s)) - 1))
    return s[i]


def perf_check(
    stage: str,
    inproc: Sequence[float],
    plugin: Sequence[float],
    *,
    floor_s: float = 0.0,
    inproc_p95: Sequence[float] | None = None,
    plugin_p95: Sequence[float] | None = None,
) -> list[str]:
    """N16: p50 ≤ 1.10× and p95 ≤ 1.15× of the in-process job time. ``floor_s`` is the
    smallest difference that counts (a job of a few milliseconds is noise).

    ``inproc_p95`` / ``plugin_p95``, when given, are the samples p95 is taken over instead:
    the CPU each job cost (the worker's process and the plugin API's), not its wall time. On
    a shared CI machine a job's wall time stalls now and then for 30-250 ms, on either side,
    in any span, with the CPU flat (a round trip waiting on Postgres): with eleven runs a
    side p95 is about the slowest run, so a single stall decided it. The median of the
    wall time is not moved by such a stall and keeps the plugin path's latency (its round
    trips) in the gate; the tail is judged by the work the plugin path does."""
    if not inproc or not plugin:
        return []
    out = []
    for name, q, limit in (("p50", 0.5, P50_LIMIT), ("p95", 0.95, P95_LIMIT)):
        xa, xb = inproc, plugin
        if name == "p95" and inproc_p95 and plugin_p95:
            xa, xb = inproc_p95, plugin_p95
        a, b = _quantile(xa, q), _quantile(xb, q)
        if b > a * limit and b - a > floor_s:
            what = " (CPU)" if xa is not inproc else ""
            out.append(
                f"{stage} {name}{what}: plugin {b * 1000:.1f} ms > {limit:.2f} x in-process {a * 1000:.1f} ms"  # noqa: E501
            )
    return out
