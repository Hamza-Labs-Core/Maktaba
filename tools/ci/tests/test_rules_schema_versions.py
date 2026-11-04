"""tools/ci/rules_schema_versions.py (story 51.13 T04): a field removed, or its kind changed,
without a major bump of its section fails, naming the section and the field."""

from __future__ import annotations

import copy
import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import rules_schema_versions as rsv  # noqa: E402


def _doc(kind: str = "int", version: list[int] | None = None, extra: bool = False) -> dict[str, Any]:
    fields: list[dict[str, Any]] = [
        {"name": "relisten_tries", "kind": kind},
        {"name": "dayparts", "kind": "rows", "column_kinds": ["time", "str"]},
    ]
    if extra:
        fields.append({"name": "new_one", "kind": "bool"})
    sec: dict[str, Any] = {"key": "transcription", "fields": fields}
    if version is not None:
        sec["version"] = version
    return {"sections": [sec, {"key": "planner", "fields": [{"name": "x", "kind": "bool"}]}]}


def test_a_kind_changed_at_the_same_major_fails_naming_the_field() -> None:
    problems = rsv.check(_doc("int"), _doc("str", [1, 0]))
    assert len(problems) == 1
    assert "rules.transcription.relisten_tries" in problems[0]
    assert "from int to str" in problems[0]


def test_a_removed_field_or_a_column_kind_change_fails() -> None:
    new = _doc(version=[1, 1])
    new["sections"][0]["fields"] = [new["sections"][0]["fields"][1]]
    problems = rsv.check(_doc(), new)
    assert problems and "rules.transcription.relisten_tries: removed" in problems[0]

    new = _doc()
    new["sections"][0]["fields"][1]["column_kinds"] = ["int", "str"]
    problems = rsv.check(_doc(), new)
    assert problems and "rules.transcription.dayparts" in problems[0]


def test_a_major_bump_new_fields_and_new_sections_pass() -> None:
    assert rsv.check(_doc("int"), _doc("str", [2, 0])) == []
    assert rsv.check(_doc(), _doc(version=[1, 1], extra=True)) == []
    new = _doc()
    new["sections"].append({"key": "brand_new", "fields": []})
    assert rsv.check(_doc(), new) == []


def test_a_major_that_goes_down_fails() -> None:
    problems = rsv.check(_doc(version=[2, 0]), _doc(version=[1, 3]))
    assert problems == ["rules.transcription: version went down from 2.0 to 1.3"]


def test_the_checked_in_schema_passes_against_itself() -> None:
    import json

    doc = json.loads((rsv.ROOT / rsv.SCHEMA).read_text(encoding="utf-8"))
    assert all("version" in s for s in doc["sections"])
    assert rsv.check(copy.deepcopy(doc), doc) == []
