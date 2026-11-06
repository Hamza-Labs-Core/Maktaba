#!/usr/bin/env python3
"""The log line conformance check (Epic 53 story 53.01, T02).

Validates every line of shared/log/schema/examples/*.ndjson against
shared/log/schema/line.schema.json: the good examples (one file per language) must
pass and every line of bad.ndjson must fail. Also checks that every copy of the shared
redaction list is current (sync.py --check). No third-party package: the schema uses
only the keywords `validate` reads, and the check fails on any other.

    python3 tools/log-schema/check.py            # the check
    python3 tools/log-schema/check.py FILE...    # validate these NDJSON files too
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
SCHEMA = ROOT / "shared/log/schema/line.schema.json"
EXAMPLES = ROOT / "shared/log/schema/examples"

KNOWN = {
    "$schema", "$id", "title", "description", "examples", "type", "required", "properties",
    "additionalProperties", "enum", "pattern", "minLength", "maxLength", "maxProperties",
    "oneOf", "items", "maxItems",
}

TYPES = {
    "object": lambda v: isinstance(v, dict),
    "array": lambda v: isinstance(v, list),
    "string": lambda v: isinstance(v, str),
    "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
    "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
    "boolean": lambda v: isinstance(v, bool),
}


def keywords(schema: Any, where: str = "$") -> list[str]:
    """Keywords this validator does not read (it would silently pass them)."""
    if isinstance(schema, bool):
        return []
    bad = [f"{where}: {k}" for k in schema if k not in KNOWN]
    for k, sub in schema.get("properties", {}).items():
        bad += keywords(sub, f"{where}.{k}")
    for i, sub in enumerate(schema.get("oneOf", [])):
        bad += keywords(sub, f"{where}.oneOf[{i}]")
    if "items" in schema:
        bad += keywords(schema["items"], f"{where}[]")
    return bad


def validate(value: Any, schema: Any, where: str = "$") -> list[str]:
    """Why value does not match schema (empty when it does)."""
    if schema is True:
        return []
    if schema is False:
        return [f"{where}: not allowed"]
    errs: list[str] = []
    if "type" in schema:
        types = schema["type"] if isinstance(schema["type"], list) else [schema["type"]]
        if not any(TYPES[t](value) for t in types):
            return [f"{where}: want {'/'.join(types)}"]
    if "enum" in schema and value not in schema["enum"]:
        errs.append(f"{where}: {value!r} not one of {schema['enum']}")
    if isinstance(value, str):
        if "pattern" in schema and not re.search(schema["pattern"], value):
            errs.append(f"{where}: {value!r} does not match {schema['pattern']}")
        if len(value) < schema.get("minLength", 0):
            errs.append(f"{where}: too short")
        if "maxLength" in schema and len(value) > schema["maxLength"]:
            errs.append(f"{where}: too long")
    if isinstance(value, dict):
        for k in schema.get("required", []):
            if k not in value:
                errs.append(f"{where}: missing {k}")
        props = schema.get("properties", {})
        for k, v in value.items():
            if k in props:
                errs += validate(v, props[k], f"{where}.{k}")
            elif schema.get("additionalProperties", True) is False:
                errs.append(f"{where}.{k}: not allowed")
        if "maxProperties" in schema and len(value) > schema["maxProperties"]:
            errs.append(f"{where}: more than {schema['maxProperties']} keys")
    if isinstance(value, list):
        if "maxItems" in schema and len(value) > schema["maxItems"]:
            errs.append(f"{where}: more than {schema['maxItems']} items")
        if "items" in schema:
            for i, v in enumerate(value):
                errs += validate(v, schema["items"], f"{where}[{i}]")
    if "oneOf" in schema:
        matched = sum(1 for s in schema["oneOf"] if not validate(value, s, where))
        if matched != 1:
            errs.append(f"{where}: matches {matched} of oneOf, want 1")
    return errs


def load_schema() -> dict[str, Any]:
    return json.loads(SCHEMA.read_text(encoding="utf-8"))


def validate_line(line: str, schema: dict[str, Any] | None = None) -> list[str]:
    schema = schema or load_schema()
    if len(line.encode("utf-8")) > 64 * 1024:
        return ["line over 64 KB"]
    try:
        value = json.loads(line)
    except json.JSONDecodeError as e:
        return [f"not JSON: {e}"]
    return validate(value, schema)


def lines(path: Path) -> list[tuple[int, str]]:
    return [(i, ln) for i, ln in enumerate(path.read_text(encoding="utf-8").splitlines(), 1) if ln.strip()]


def main(argv: list[str]) -> int:
    schema = load_schema()
    failed = False
    for k in keywords(schema):
        print(f"::error file={SCHEMA.relative_to(ROOT)}::{k}: a keyword check.py does not read")
        failed = True
    for path in sorted(EXAMPLES.glob("*.ndjson")):
        bad = path.name == "bad.ndjson"
        for no, ln in lines(path):
            errs = validate_line(ln, schema)
            if bad and not errs:
                print(f"::error file={path.relative_to(ROOT)},line={no}::a bad example passed the schema")
                failed = True
            elif not bad and errs:
                print(f"::error file={path.relative_to(ROOT)},line={no}::{'; '.join(errs)}")
                failed = True
    for arg in argv:
        for no, ln in lines(Path(arg)):
            if errs := validate_line(ln, schema):
                print(f"::error file={arg},line={no}::{'; '.join(errs)}")
                failed = True
    sys.path.insert(0, str(Path(__file__).parent))
    import sync  # noqa: PLC0415

    if sync.main(["--check"]):
        failed = True
    if not failed:
        print("log schema: examples and redaction copies are current")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
