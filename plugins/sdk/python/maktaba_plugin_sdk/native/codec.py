"""Values between a native plugin and the plugin API (Epic 54, story 54.24 §2).

Statement arguments and the rows the API answers travel as JSON. Plain JSON carries
``None``, ``bool``, ``int``, ``float``, ``str`` and lists; every other value the moved code
passes or reads is a tagged object ``{"$t": <type>, "v": <text>}``, decoded to the same
Python type asyncpg answers, so the code reads exactly what it read in-process:

=========  ===========================  =====================================
``$t``     Python                       Postgres
=========  ===========================  =====================================
uuid       ``uuid.UUID``                uuid
ts         aware ``datetime`` (UTC)     timestamptz
tsn        naive ``datetime``           timestamp
date       ``datetime.date``            date
time       ``datetime.time``            time
dec        ``decimal.Decimal``          numeric
bytes      ``bytes`` (base64)           bytea
iv         ``datetime.timedelta`` (µs)  interval
float      ``float`` (``NaN``, ``±Infinity``)
=========  ===========================  =====================================
"""

from __future__ import annotations

import base64
import math
from datetime import UTC, date, datetime, time, timedelta
from decimal import Decimal
from typing import Any
from uuid import UUID

__all__ = ["decode", "encode"]


def encode(v: Any) -> Any:
    if v is None or isinstance(v, bool | str):
        return v
    if isinstance(v, int):
        return v
    if isinstance(v, float):
        if math.isnan(v) or math.isinf(v):
            return {"$t": "float", "v": "NaN" if math.isnan(v) else ("Infinity" if v > 0 else "-Infinity")}
        return v
    if isinstance(v, UUID):
        return {"$t": "uuid", "v": str(v)}
    if isinstance(v, datetime):
        if v.tzinfo is None:
            return {"$t": "tsn", "v": v.isoformat()}
        return {"$t": "ts", "v": v.astimezone(UTC).isoformat()}
    if isinstance(v, date):
        return {"$t": "date", "v": v.isoformat()}
    if isinstance(v, time):
        return {"$t": "time", "v": v.isoformat()}
    if isinstance(v, Decimal):
        return {"$t": "dec", "v": str(v)}
    if isinstance(v, bytes | bytearray | memoryview):
        return {"$t": "bytes", "v": base64.b64encode(bytes(v)).decode()}
    if isinstance(v, timedelta):
        return {"$t": "iv", "v": str(v // timedelta(microseconds=1))}
    if isinstance(v, list | tuple):
        return [encode(x) for x in v]
    raise TypeError(f"a {type(v).__name__} cannot be a statement argument")


def decode(v: Any) -> Any:
    if isinstance(v, list):
        return [decode(x) for x in v]
    if not isinstance(v, dict):
        return v
    t, s = v.get("$t"), v.get("v")
    if not isinstance(s, str):
        raise ValueError(f"bad tagged value {v!r}")
    if t == "uuid":
        return UUID(s)
    if t == "ts":
        d = datetime.fromisoformat(s)
        return d if d.tzinfo else d.replace(tzinfo=UTC)
    if t == "tsn":
        return datetime.fromisoformat(s).replace(tzinfo=None)
    if t == "date":
        return date.fromisoformat(s)
    if t == "time":
        return time.fromisoformat(s)
    if t == "dec":
        return Decimal(s)
    if t == "bytes":
        return base64.b64decode(s)
    if t == "iv":
        return timedelta(microseconds=int(s))
    if t == "float":
        return float(s.replace("Infinity", "inf"))
    raise ValueError(f"unknown value type {t!r}")
