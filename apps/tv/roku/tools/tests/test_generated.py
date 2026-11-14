"""Story 58.15: the generated files match their generators (vectors and the device's tables)."""

from __future__ import annotations

import json

import gen_tables
import gen_vectors


def test_the_vectors_match_the_reference() -> None:
    assert gen_vectors.main(["--check"]) == 0


def test_the_device_tables_are_current() -> None:
    assert gen_tables.main(["--check"]) == 0


def test_the_vectors_cover_the_story() -> None:
    doc = json.loads(gen_vectors.VECTORS.read_text(encoding="utf-8"))
    cats = {v["id"].split(".")[0] for v in doc["vectors"]}
    assert {
        "join",
        "lamalef",
        "tashkeel",
        "digits",
        "time",
        "episode",
        "mixed",
        "punct",
        "trunc",
        "wrap",
        "base",
        "fallback",
        "title",
    } <= cats
    assert len(doc["vectors"]) >= 300
    assert doc["reference"] == {"python-bidi": "0.6.12", "arabic-reshaper": "3.1.0"}


def test_a_changed_expectation_is_caught(tmp_path) -> None:  # type: ignore[no-untyped-def]
    doc = json.loads(gen_vectors.VECTORS.read_text(encoding="utf-8"))
    doc["vectors"][0]["visual"] = "wrong"
    f = tmp_path / "v.json"
    f.write_text(gen_vectors.render(doc), encoding="utf-8")
    assert gen_vectors.main(["--check", "--file", str(f)]) == 1
