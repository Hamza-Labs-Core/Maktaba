"""The plugin manifest validator (Epic 54, story 54.01), the pipeline's copy of the rules.

The schema is ``shared/plugins/manifest.schema.json`` (copied into ``plugins/data`` by
``make plugins-sync``; ``make plugins-check`` fails on a stale copy). This module reads
the same keyword subset as the Go, Rust and TypeScript validators, then runs the same
rules across fields in the same order, and normalises a valid manifest to the one JSON
text all four answer byte for byte (the corpus in ``shared/plugins/testdata/manifests``).

The pipeline's plugin loader (story 54.06) reads manifests with it. It never imports
plugin code.
"""

from __future__ import annotations

import hashlib
import importlib.resources
import json
import re
import tomllib
from collections.abc import Callable
from dataclasses import dataclass
from functools import cache
from typing import Any

CODE_INVALID = "manifest_invalid"
CODE_UNKNOWN_KEY = "manifest_unknown_key"
CODE_MISSING_KEY = "manifest_missing_key"
CODE_SCHEMA_NEWER = "manifest_schema_newer"
CODE_RESERVED_ID = "manifest_reserved_id"
CODE_BAD_PLATFORM = "manifest_bad_platform"
CODE_BAD_RANGE = "manifest_bad_range"
CODE_BAD_CAPABILITY = "manifest_bad_capability"
CODE_DUPLICATE_CAPABILITY = "manifest_duplicate_capability"
CODE_DUPLICATE_STAGE = "manifest_duplicate_stage"
CODE_BAD_REQUEST = "manifest_bad_request"
CODE_UNKNOWN_EVENT = "manifest_unknown_event"
CODE_SUBJECT_MISMATCH = "manifest_subject_mismatch"
CODE_BAD_SCHEDULE = "manifest_bad_schedule"
CODE_MISSING_STRING = "manifest_missing_string"
CODE_STRING_TOO_LONG = "manifest_string_too_long"
CODE_MISSING_FILE = "manifest_missing_file"
CODE_BAD_SETTINGS = "manifest_bad_settings"
CODE_NOT_TOML = "manifest_not_toml"
#: a key, a stage name or a subject only Maktaba's own release-signed plugins may use
#: (story 54.22 §1)
CODE_TRUSTED_FIELD = "plugin_trusted_field"

SCHEMA_VERSION = 1
MAX_NAME = 40
MAX_SUMMARY = 140

_KEYWORDS = {
    "$schema",
    "$id",
    "title",
    "description",
    "type",
    "properties",
    "additionalProperties",
    "required",
    "enum",
    "pattern",
    "minLength",
    "maxLength",
    "minimum",
    "maximum",
    "items",
    "minItems",
    "maxItems",
    "uniqueItems",
    "default",
    "x-code",
    "x-unknown-code",
    "x-trusted-only",
}

#: what the package rules read besides the manifest: a file's bytes by its package path,
#: or None when absent
Files = Callable[[str], bytes | None]


@dataclass(frozen=True, slots=True)
class ManifestError:
    code: str
    path: str = ""
    detail: str = ""


class ManifestInvalid(ValueError):
    def __init__(self, errors: list[ManifestError]) -> None:
        super().__init__("; ".join(f"{e.code} at {e.path}: {e.detail}" for e in errors))
        self.errors = errors

    def has(self, code: str) -> bool:
        return any(e.code == code for e in self.errors)


def _data(name: str) -> bytes:
    return importlib.resources.files("maktaba_pipeline.plugins").joinpath("data", name).read_bytes()


@cache
def _schema() -> dict[str, Any]:
    root: dict[str, Any] = json.loads(_data("manifest.schema.json"))

    def check(n: dict[str, Any]) -> None:
        unknown = set(n) - _KEYWORDS
        if unknown:
            raise RuntimeError(f"schema keywords not read by the validators: {sorted(unknown)}")
        for c in n.get("properties", {}).values():
            check(c)
        if "items" in n:
            check(n["items"])

    check(root)
    return root


@cache
def capabilities() -> dict[str, dict[str, Any]]:
    """capabilities.toml by name."""
    doc = tomllib.loads(_data("capabilities.toml").decode())
    return {c["name"]: c for c in doc["capability"]}


@cache
def events() -> dict[str, dict[str, Any]]:
    """events.toml by name."""
    doc = tomllib.loads(_data("events.toml").decode())
    return {e["name"]: e for e in doc["event"]}


def lookup_capability(name: str) -> dict[str, Any] | None:
    return capabilities().get(name.split(":", 1)[0])


def valid_id(plugin_id: str) -> bool:
    pattern = _schema()["properties"]["id"]["pattern"]
    return len(plugin_id) <= 100 and _matches(pattern, plugin_id)


def _matches(pattern: str, s: str) -> bool:
    """Go's and Rust's ``$``: the end of the text, never before a final newline."""
    m = re.search(pattern, s)
    return m is not None and (not pattern.endswith("$") or m.end() == len(s))


def queue_name(plugin_id: str, stage: str) -> str:
    """A plugin stage's ``job_stages.name`` (54.01 §4): ``p_<8 hex of sha256(id)>_<stage>``."""
    return "p_" + hashlib.sha256(plugin_id.encode()).hexdigest()[:8] + "_" + stage


def _type_of(v: Any) -> str:
    if isinstance(v, bool):
        return "boolean"
    if isinstance(v, int):
        return "integer"
    if isinstance(v, float):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "array"
    if isinstance(v, dict):
        return "object"
    return "unknown"


def _equal(a: Any, b: Any) -> bool:
    if _type_of(a) in ("integer", "number") and _type_of(b) in ("integer", "number"):
        return float(a) == float(b)
    return json.dumps(a, sort_keys=True) == json.dumps(b, sort_keys=True)


def _join(path: str, el: str) -> str:
    return el if not path else f"{path}/{el}"


def _check(
    n: dict[str, Any],
    v: Any,
    path: str,
    parent: str,
    errs: list[ManifestError],
    trusted: bool = False,
) -> None:
    code = n.get("x-code") or parent or CODE_INVALID
    t = _type_of(v)
    types = n.get("type")
    if types is not None:
        wants = [types] if isinstance(types, str) else types
        if not any(w == t or (w == "number" and t == "integer") for w in wants):
            errs.append(ManifestError(code, path, "type " + t))
            return
    if "enum" in n and not any(_equal(e, v) for e in n["enum"]):
        errs.append(ManifestError(code, path, "not one of the allowed values"))
        return
    if t == "string":
        count = len(v)
        if ("minLength" in n and count < n["minLength"]) or (
            "maxLength" in n and count > n["maxLength"]
        ):
            errs.append(ManifestError(code, path, "length"))
            return
        if "pattern" in n and not _matches(n["pattern"], v):
            errs.append(ManifestError(code, path, "pattern"))
    elif t in ("integer", "number"):
        if ("minimum" in n and v < n["minimum"]) or ("maximum" in n and v > n["maximum"]):
            errs.append(ManifestError(code, path, "range"))
    elif t == "array":
        if ("minItems" in n and len(v) < n["minItems"]) or (
            "maxItems" in n and len(v) > n["maxItems"]
        ):
            errs.append(ManifestError(code, path, "items"))
            return
        if n.get("uniqueItems"):
            for i in range(len(v)):
                for j in range(i):
                    if _equal(v[i], v[j]):
                        errs.append(ManifestError(code, path, "duplicate item"))
                        return
        if "items" in n:
            for i, x in enumerate(v):
                _check(n["items"], x, _join(path, str(i)), code, errs, trusted)
    elif t == "object":
        for r in n.get("required", []):
            if r not in v:
                errs.append(ManifestError(CODE_MISSING_KEY, _join(path, r)))
        props = n.get("properties", {})
        for k in sorted(v):
            if k not in props:
                if n.get("additionalProperties") is False:
                    errs.append(ManifestError(CODE_UNKNOWN_KEY, _join(path, k)))
                continue
            if props[k].get("x-trusted-only") and not trusted:
                errs.append(ManifestError(CODE_TRUSTED_FIELD, _join(path, k)))
                continue
            _check(props[k], v[k], _join(path, k), code, errs, trusted)


def _fill(n: dict[str, Any], v: Any) -> Any:
    if isinstance(v, dict):
        props = n.get("properties", {})
        for k in sorted(props):
            c = props[k]
            if k not in v and "default" in c:
                v[k] = json.loads(json.dumps(c["default"]))
            if k in v:
                v[k] = _fill(c, v[k])
    elif isinstance(v, list) and "items" in n:
        for i, x in enumerate(v):
            v[i] = _fill(n["items"], x)
    return v


def _plain(v: Any) -> Any:
    """TOML values as the manifest's own types; dates and times are refused."""
    if isinstance(v, dict):
        return {k: _plain(x) for k, x in v.items()}
    if isinstance(v, list):
        return [_plain(x) for x in v]
    if isinstance(v, bool | int | float | str):
        return v
    raise ValueError(f"a {type(v).__name__} value is not allowed in a manifest")


def validate(src: bytes, files: Files | None = None, *, trusted: bool = False) -> dict[str, Any]:
    """Parse and check a manifest.toml; answer the normalised manifest or raise
    :class:`ManifestInvalid`. ``files`` serves the package's files (i18n, ui) to the
    package rules; None skips them. ``trusted`` is only for the manifests of Maktaba's own
    plugins built into this release (story 54.22): their x-trusted-only keys are read."""
    try:
        if len(src) > 1 << 20:
            raise ValueError("over 1 MB")
        raw = _plain(tomllib.loads(src.decode("utf-8")))
    except (ValueError, UnicodeDecodeError) as exc:
        raise ManifestInvalid([ManifestError(CODE_NOT_TOML, "", str(exc))]) from None
    schema = raw.get("schema")
    if _type_of(schema) == "integer" and schema > SCHEMA_VERSION:
        raise ManifestInvalid([ManifestError(CODE_SCHEMA_NEWER, "schema")])
    errs: list[ManifestError] = []
    _check(_schema(), raw, "", "", errs, trusted)
    if errs:
        raise ManifestInvalid(errs)
    m: dict[str, Any] = _fill(_schema(), raw)
    m["maktaba"] = ", ".join(p.replace(" ", "") for p in m["maktaba"].split(","))
    errs = _rules(m, files, trusted)
    if errs:
        raise ManifestInvalid(errs)
    return m


def normalised(m: dict[str, Any]) -> bytes:
    """The one JSON text of a valid manifest (keys sorted, no spaces, UTF-8)."""
    return json.dumps(m, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def _reserved(plugin_id: str) -> bool:
    labels = plugin_id.split(".")
    if plugin_id.startswith("tv.maktaba.community."):
        labels = labels[3:]
    elif plugin_id.startswith(("tv.maktaba.", "dev.maktaba.")):
        return True
    return any(label in ("maktaba", "official") for label in labels)


def _known_capability(name: str) -> bool:
    base, sep, param = name.partition(":")
    c = capabilities().get(base)
    if c is None:
        return False
    if not c.get("param"):
        return not sep
    return bool(sep) and valid_id(param)


@cache
def reserved_stages() -> frozenset[str]:
    """The stage names only Maktaba's own plugins may define (story 54.22 §1)."""
    text = _data("reserved_stages.txt").decode()
    return frozenset(
        ln.strip() for ln in text.splitlines() if ln.strip() and not ln.startswith("#")
    )


def _rules(m: dict[str, Any], files: Files | None, trusted: bool = False) -> list[ManifestError]:
    errs: list[ManifestError] = []
    first_party = m["id"].startswith("tv.maktaba.") and not m["id"].startswith(
        "tv.maktaba.community."
    )
    if (trusted and not first_party) or (not trusted and _reserved(m["id"])):
        errs.append(ManifestError(CODE_RESERVED_ID, "id"))
    if len(m["platforms"]) > 1 and "any" in m["platforms"]:
        errs.append(ManifestError(CODE_BAD_PLATFORM, "platforms", "any stands alone"))
    if not any(p.startswith((">", "=")) for p in m["maktaba"].split(", ")):
        errs.append(ManifestError(CODE_BAD_RANGE, "maktaba", "no lower bound"))
    seen: set[str] = set()
    for lst in ("required", "optional"):
        for i, name in enumerate(m["capabilities"][lst]):
            path = f"capabilities/{lst}/{i}"
            if not _known_capability(name):
                errs.append(ManifestError(CODE_BAD_CAPABILITY, path, name))
                continue
            if name in seen:
                errs.append(ManifestError(CODE_DUPLICATE_CAPABILITY, path, name))
            seen.add(name)
    subjects: dict[str, str] = {}
    for i, st in enumerate(m["stage"]):
        if st["name"] in subjects:
            errs.append(ManifestError(CODE_DUPLICATE_STAGE, f"stage/{i}/name", st["name"]))
        subjects[st["name"]] = st["subject"]
        # R5a: a core stage's name and the dup_group subject are Maktaba's own (54.22 §1)
        if not trusted and (st["name"] in reserved_stages() or st["subject"] == "dup_group"):
            errs.append(ManifestError(CODE_TRUSTED_FIELD, f"stage/{i}/name", st["name"]))
    evs = events()
    for i, st in enumerate(m["stage"]):
        for j, r in enumerate(st["may_request"]):
            if r not in subjects and not trusted:
                errs.append(ManifestError(CODE_BAD_REQUEST, f"stage/{i}/may_request/{j}", r))
        for j, t in enumerate(st["trigger"]):
            ev = t["event"]
            path = f"stage/{i}/trigger/{j}/event"
            e = evs.get(ev)
            if e is None or e.get("trigger") is False:
                errs.append(ManifestError(CODE_UNKNOWN_EVENT, path, ev))
            elif e["subject"] != st["subject"]:
                errs.append(ManifestError(CODE_SUBJECT_MISMATCH, path, ev))
    for i, s in enumerate(m["schedule"]):
        if subjects.get(s["stage"]) != "catalog":
            errs.append(ManifestError(CODE_BAD_SCHEDULE, f"schedule/{i}/stage", s["stage"]))
    if files is None or errs:
        return errs
    return errs + _package_rules(m, files)


def _package_rules(m: dict[str, Any], files: Files) -> list[ManifestError]:
    errs: list[ManifestError] = []
    for lang in ("en", "ar"):
        path = f"i18n/{lang}.json"
        raw = files(path)
        if raw is None:
            errs.append(ManifestError(CODE_MISSING_FILE, "strings", path))
            continue
        try:
            words = json.loads(raw)
            if not isinstance(words, dict):
                raise ValueError
        except ValueError:
            errs.append(ManifestError(CODE_MISSING_STRING, "strings", lang))
            continue
        for key, most in (("name", MAX_NAME), ("summary", MAX_SUMMARY)):
            w = words.get(m["strings"][key])
            if not isinstance(w, str) or not w.strip():
                errs.append(ManifestError(CODE_MISSING_STRING, f"strings/{key}", lang))
            elif len(w) > most:
                errs.append(ManifestError(CODE_STRING_TOO_LONG, f"strings/{key}", lang))
    props: dict[str, Any] = {}
    if "settings" in m:
        path = m["settings"]["schema"]
        raw = files(path)
        if raw is None:
            errs.append(ManifestError(CODE_MISSING_FILE, "settings/schema", path))
        else:
            try:
                doc = json.loads(raw)
                if not isinstance(doc, dict):
                    raise ValueError
                p = doc.get("properties")
                props = p if isinstance(p, dict) else {}
            except ValueError:
                errs.append(ManifestError(CODE_BAD_SETTINGS, "settings/schema", path))
    for i, s in enumerate(m["schedule"]):
        field = s["every_minutes_setting"]
        path = f"schedule/{i}/every_minutes_setting"
        if "settings" not in m:
            errs.append(ManifestError(CODE_BAD_SCHEDULE, path, "no settings"))
            continue
        p = props.get(field)
        if not isinstance(p, dict):
            errs.append(ManifestError(CODE_BAD_SCHEDULE, path, field))
            continue
        low = p.get("minimum")
        if p.get("type") != "integer" or not isinstance(low, int | float) or low < 15:
            errs.append(ManifestError(CODE_BAD_SCHEDULE, path, field))
    return errs
