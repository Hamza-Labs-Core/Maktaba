"""Settings: the rules of detection, grouping and channel planning as data.

Every rule lives in a frozen config dataclass (``DetectorConfig``, ``GroupConfig``,
``PlannerConfig``) whose defaults are the built-in values. A settings file holds
only what the user changed (``{"planner": {"dawn_quran": false}, ...}``), so a
newer default still reaches every field the user left alone.

:func:`describe` lists a config's fields for a settings UI (kind, default, current
value, group, help), :func:`apply` builds a config from overrides and refuses a
value of the wrong shape, and :func:`overrides` turns a config back into the
changed fields only.
"""

from __future__ import annotations

import dataclasses
import json
import types
import typing
from collections.abc import Mapping
from pathlib import Path
from typing import Any

__all__ = [
    "Help",
    "SettingsError",
    "apply",
    "describe",
    "load",
    "overrides",
    "save",
    "to_json",
]


class SettingsError(ValueError):
    """A setting with a value of the wrong shape; ``field`` names it."""

    def __init__(self, field: str, message: str) -> None:
        super().__init__(f"{field}: {message}")
        self.field = field


@dataclasses.dataclass(frozen=True, slots=True)
class Help:
    """How a settings UI shows one field: its group, label and explanation.
    ``kind`` overrides the kind read from the type (``time``: minutes after
    midnight shown as HH:MM). ``columns`` names the columns of a list of tuples."""

    group: str
    label: str
    text: str = ""
    kind: str | None = None
    columns: tuple[str, ...] = ()
    choices: tuple[str, ...] = ()
    #: the smallest value a number may take (a planner rule that would divide by it, or
    #: take the median of none, refuses 0 here rather than failing later)
    minimum: float | None = None
    #: for a list of tuples: which columns hold a time (HH:MM), checked on save
    time_columns: tuple[int, ...] = ()


def _hm(m: int) -> str:
    return f"{m // 60:02d}:{m % 60:02d}"


def _minutes(v: Any, field: str) -> int:
    if isinstance(v, int) and not isinstance(v, bool):
        return v
    if isinstance(v, str) and ":" in v:
        h, _, m = v.partition(":")
        if h.isdigit() and m.isdigit() and int(m) < 60:
            return int(h) * 60 + int(m)
    raise SettingsError(field, f"expected a time HH:MM, got {v!r}")


def _kind(tp: Any) -> str:
    origin = typing.get_origin(tp)
    args = typing.get_args(tp)
    if tp is bool:
        return "bool"
    if tp is int:
        return "int"
    if tp is float:
        return "float"
    if tp is str:
        return "str"
    if origin in (types.UnionType, typing.Union) and type(None) in args:
        inner = next(a for a in args if a is not type(None))
        return f"{_kind(inner)}?"
    if origin in (frozenset, set):
        return "words"
    if origin is tuple and len(args) == 2 and args[1] is Ellipsis:
        item = args[0]
        if item is str:
            return "list"
        if dataclasses.is_dataclass(item) or typing.get_origin(item) is tuple:
            return "rows"
    if origin in (Mapping, dict, typing.get_origin(Mapping[str, str])) and len(args) == 2:
        return "map"
    raise TypeError(f"no settings kind for {tp!r}")


def _jsonable(v: Any, kind: str) -> Any:
    if v is None:
        return None
    if kind.startswith("time"):
        return _hm(int(v))
    if kind == "words":
        return sorted(v)
    if kind == "list":
        return list(v)
    if kind == "map":
        return {k: list(x) for k, x in v.items()}
    if kind == "rows":
        out: list[Any] = []
        for row in v:
            if dataclasses.is_dataclass(row):
                out.append(
                    {
                        f.name: (
                            _hm(getattr(row, f.name))
                            if f.name in ("start", "end") and isinstance(getattr(row, f.name), int)
                            else list(getattr(row, f.name))
                            if isinstance(getattr(row, f.name), tuple)
                            else getattr(row, f.name)
                        )
                        for f in dataclasses.fields(row)
                    }
                )
            else:
                out.append(list(row))
        return out
    return v


def _coerce(v: Any, tp: Any, kind: str, field: str) -> Any:
    if kind.endswith("?"):
        if v is None or v == "":
            return None
        inner = next(a for a in typing.get_args(tp) if a is not type(None))
        return _coerce(v, inner, kind[:-1], field)
    if kind == "time":
        return _minutes(v, field)
    if kind == "bool":
        if isinstance(v, bool):
            return v
        raise SettingsError(field, f"expected true or false, got {v!r}")
    if kind == "int":
        if isinstance(v, bool) or not isinstance(v, int | float) or int(v) != v:
            raise SettingsError(field, f"expected a whole number, got {v!r}")
        return int(v)
    if kind == "float":
        if isinstance(v, bool) or not isinstance(v, int | float):
            raise SettingsError(field, f"expected a number, got {v!r}")
        return float(v)
    if kind == "str":
        if not isinstance(v, str):
            raise SettingsError(field, f"expected text, got {v!r}")
        return v
    if kind in ("list", "words"):
        if isinstance(v, str):
            v = [x.strip() for x in v.split(",")]
        if not isinstance(v, list) or not all(isinstance(x, str) for x in v):
            raise SettingsError(field, f"expected a list of words, got {v!r}")
        words = [x for x in v if x]
        return frozenset(words) if kind == "words" else tuple(words)
    if kind == "map":
        if not isinstance(v, dict):
            raise SettingsError(field, f"expected a mapping, got {v!r}")
        return {str(k): _coerce(x, tuple[str, ...], "list", f"{field}.{k}") for k, x in v.items()}
    if kind == "rows":
        if not isinstance(v, list):
            raise SettingsError(field, f"expected a list of rows, got {v!r}")
        item = typing.get_args(tp)[0]
        return tuple(_row(r, item, f"{field}[{i}]") for i, r in enumerate(v))
    raise SettingsError(field, f"unsupported kind {kind}")


def _row(r: Any, item: Any, field: str) -> Any:
    if dataclasses.is_dataclass(item):
        if not isinstance(r, dict):
            raise SettingsError(field, f"expected a row of named values, got {r!r}")
        hints = typing.get_type_hints(item)
        names = {f.name for f in dataclasses.fields(item)}
        if unknown := set(r) - names:
            raise SettingsError(field, f"unknown column(s) {', '.join(sorted(unknown))}")
        kw = {}
        for f in dataclasses.fields(item):
            if f.name not in r:
                continue
            k = (
                "time"
                if f.name in ("start", "end") and hints[f.name] is int
                else _kind(hints[f.name])
            )
            kw[f.name] = _coerce(r[f.name], hints[f.name], k, f"{field}.{f.name}")
        try:
            return typing.cast(Any, item)(**kw)
        except TypeError as e:
            raise SettingsError(field, str(e)) from None
    cols = typing.get_args(item)
    if not isinstance(r, list | tuple) or len(r) != len(cols):
        raise SettingsError(field, f"expected {len(cols)} values, got {r!r}")
    return tuple(
        _coerce(x, t, _kind(t), f"{field}[{j}]")
        for j, (x, t) in enumerate(zip(r, cols, strict=True))
    )


def _fields(cls: type) -> list[tuple[dataclasses.Field[Any], Any, str]]:
    hints = typing.get_type_hints(cls)
    helps: Mapping[str, Help] = getattr(cls, "SETTINGS_HELP", None) or _HELP.get(cls, {})
    out = []
    for f in dataclasses.fields(cls):
        h = helps.get(f.name)
        out.append((f, hints[f.name], (h.kind if h and h.kind else None) or _kind(hints[f.name])))
    return out


#: help registered per config class (the class's module calls :func:`register`)
_HELP: dict[type, Mapping[str, Help]] = {}


def register(cls: type, helps: Mapping[str, Help]) -> None:
    """Attach the settings help of a config class (kept beside the class)."""
    names = {f.name for f in dataclasses.fields(cls)}
    if unknown := set(helps) - names:
        raise ValueError(f"help for unknown field(s) of {cls.__name__}: {sorted(unknown)}")
    _HELP[cls] = helps


def apply(cls: type, changes: Mapping[str, Any], base: Any = None) -> Any:
    """``cls`` with ``changes`` over its defaults (or over ``base``); every value is
    checked against the field's type, and an unknown field is refused."""
    known = {f.name: (f, tp, kind) for f, tp, kind in _fields(cls)}
    if unknown := set(changes) - set(known):
        raise SettingsError(sorted(unknown)[0], "no such setting")
    kw = {name: _coerce(v, known[name][1], known[name][2], name) for name, v in changes.items()}
    helps = _HELP.get(cls, {})
    for name, v in kw.items():
        if (h := helps.get(name)) is not None:
            _check(h, v, name)
    return dataclasses.replace(base if base is not None else cls(), **kw)


def _check(h: Help, v: Any, field: str) -> None:
    """The limits a field's help sets: a minimum, the allowed choices, time columns."""
    if h.minimum is not None and isinstance(v, int | float) and v < h.minimum:
        raise SettingsError(field, f"must be at least {h.minimum:g}, got {v!r}")
    if h.choices and v is not None and v not in h.choices:
        raise SettingsError(field, f"must be one of {', '.join(h.choices)}, got {v!r}")
    for i, row in enumerate(v if h.time_columns else ()):
        for j in h.time_columns:
            t = row[j]
            m = _minutes(t, f"{field}[{i}][{j}]") if t else None
            if m is None or m > 24 * 60:
                raise SettingsError(f"{field}[{i}]", f"expected a time HH:MM, got {t!r}")


def to_json(cfg: Any) -> dict[str, Any]:
    """Every field of ``cfg`` as JSON values."""
    return {f.name: _jsonable(getattr(cfg, f.name), kind) for f, _, kind in _fields(type(cfg))}


def overrides(cfg: Any) -> dict[str, Any]:
    """Only the fields of ``cfg`` that differ from the class defaults, as JSON values."""
    now, default = to_json(cfg), to_json(type(cfg)())
    return {k: v for k, v in now.items() if v != default[k]}


def describe(cfg: Any) -> list[dict[str, Any]]:
    """The fields of ``cfg`` for a settings UI: name, kind, label, group, help,
    current value, default and whether it is changed."""
    cls = type(cfg)
    helps: Mapping[str, Help] = _HELP.get(cls, {})
    default = to_json(cls())
    now = to_json(cfg)
    out = []
    for f, tp, kind in _fields(cls):
        h = helps.get(f.name)
        entry: dict[str, Any] = {
            "name": f.name,
            "kind": kind,
            "group": h.group if h else "Other",
            "label": h.label if h else f.name.replace("_", " "),
            "help": h.text if h else "",
            "value": now[f.name],
            "default": default[f.name],
            "changed": now[f.name] != default[f.name],
        }
        if h and h.choices:
            entry["choices"] = list(h.choices)
        if h and h.minimum is not None:
            entry["minimum"] = h.minimum
        if kind == "rows":
            item = typing.get_args(tp)[0]
            entry["columns"] = (
                [g.name for g in dataclasses.fields(item)]
                if dataclasses.is_dataclass(item)
                else list(
                    h.columns
                    if h
                    else [f"value {i + 1}" for i in range(len(typing.get_args(item)))]
                )
            )
        out.append(entry)
    return out


def load(path: Path) -> dict[str, dict[str, Any]]:
    """The settings file's sections (``{}`` when there is none)."""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return {}
    if not isinstance(data, dict) or not all(isinstance(v, dict) for v in data.values()):
        raise SettingsError(str(path), "expected {section: {setting: value}}")
    return data


def save(path: Path, section: str, cfg: Any) -> dict[str, Any]:
    """Write ``cfg``'s changed fields as ``section`` of the settings file (other
    sections are kept); returns what was written."""
    data = load(path)
    data[section] = overrides(cfg)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    tmp.replace(path)
    return data[section]
