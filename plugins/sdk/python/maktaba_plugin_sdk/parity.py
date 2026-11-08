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
- the job time per stage (N16): the CPU each job cost, p50 ≤ 1.10× and p95 ≤ 1.15× of the
  in-process path, and its wall time, paired round by round, within 10% plus the plugin
  path's fixed cost of a job and the hop each statement pays (``perf_check``).

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
    "STATEMENT_HOP_S",
    "JobTiming",
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


@dataclass(frozen=True)
class JobTiming:
    """One benched job of one side, for N16.

    ``wall``: the whole job, seconds. ``cpu``: the CPU the job cost the process that runs
    the worker, the runner and the stage's code (the plugin API's process and Postgres are
    left out: the API's work per statement is N18's and N23's, and both sides run the same
    queries). ``calls`` and ``hop``, the plugin side only: how many plugin API calls the
    job made and the wall time of its cheapest one, which is the hop to the API one
    statement pays."""

    wall: float
    cpu: float
    calls: int = 0
    hop: float = 0.0


#: N18: a named statement's round trip through the plugin API (p95). The hop N16 allows a
#: statement is the measured one, never more than this.
STATEMENT_HOP_S = 0.002


def perf_check(
    stage: str,
    inproc: Sequence[JobTiming],
    plugin: Sequence[JobTiming],
    *,
    floor_s: float = 0.0,
) -> list[str]:
    """N16: a stage's plugin job is not meaningfully slower than its in-process job.

    ``inproc[i]`` and ``plugin[i]`` are one round: the same job run on each side in turn.

    - **The work** (CPU): p50 ≤ 1.10x and p95 ≤ 1.15x of the in-process job's CPU, and more
      than ``floor_s`` over it. CPU time does not count the time a job waits, so a shared
      machine's stalls (seen on CI on both sides, in any span, with the CPU flat) cannot
      fail it, and a plugin that does more work fails it on any machine.
    - **The wait** (wall time, paired): the median over rounds of plugin minus in-process
      wall time is within 10% of the in-process median, plus ``floor_s`` (the plugin path's
      fixed cost of one job: its token and the hop to the runner), plus one hop per plugin
      API call (each statement's hop to the API: the job's cheapest call, capped at N18's
      ``STATEMENT_HOP_S``). The two sides of a round run back to back, so a machine slow
      for a while slows both and the difference keeps; a stall on one side's turn moves
      one difference, which the median does not follow. A plugin that waits (a sleep, a
      lock, more round trips to Postgres than the in-process job) fails it.

    ``floor_s`` is the smallest difference that counts."""
    n = min(len(inproc), len(plugin))
    if n == 0:
        return []
    a, b = inproc[:n], plugin[:n]
    out = []
    for name, q, limit in (("p50", 0.5, P50_LIMIT), ("p95", 0.95, P95_LIMIT)):
        ca, cb = _quantile([x.cpu for x in a], q), _quantile([x.cpu for x in b], q)
        if cb > ca * limit and cb - ca > floor_s:
            out.append(
                f"{stage} {name} (CPU): plugin {cb * 1000:.1f} ms > {limit:.2f} x in-process {ca * 1000:.1f} ms"  # noqa: E501
            )
    slower = _median([y.wall - x.wall for x, y in zip(a, b, strict=True)])
    base = _median([x.wall for x in a])
    calls = round(_median([float(y.calls) for y in b]))
    hop = min(_median([y.hop for y in b]), STATEMENT_HOP_S)
    allowed = base * (P50_LIMIT - 1) + floor_s + calls * hop
    if slower > allowed:
        out.append(
            f"{stage} p50 (wall, paired): plugin slower by {slower * 1000:.1f} ms > allowed"
            f" {allowed * 1000:.1f} ms ({P50_LIMIT - 1:.0%} of in-process {base * 1000:.1f} ms"
            f" + {floor_s * 1000:.1f} ms a job + {calls} calls x {hop * 1000:.2f} ms)"
        )
    return out


def _median(xs: Sequence[float]) -> float:
    s = sorted(xs)
    if not s:
        return 0.0
    m = len(s) // 2
    return s[m] if len(s) % 2 else (s[m - 1] + s[m]) / 2
