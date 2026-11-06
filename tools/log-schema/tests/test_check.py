"""tools/log-schema/check.py: the conformance check itself (Epic 53 story 53.01)."""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import check  # noqa: E402


def test_the_examples_and_copies_pass():
    assert check.main([]) == 0


def test_a_raw_user_id_fails():
    errs = check.validate_line('{"ts":"2026-11-06T10:00:00Z","level":"info","msg":"x","service":"api","user_id":"42"}')
    assert any("user_id" in e for e in errs)


def test_a_good_line_passes():
    assert check.validate_line('{"ts":"2026-11-06T10:00:00.5Z","level":"warn","msg":"x","service":"api","err":"boom"}') == []


def test_an_unknown_keyword_is_reported():
    assert check.keywords({"type": "object", "properties": {"a": {"format": "uuid"}}}) == ["$.a: format"]


def test_err_object_or_string():
    s = check.load_schema()
    assert check.validate({"ts": "2026-11-06T10:00:00Z", "level": "error", "msg": "x", "service": "api", "err": {"kind": "db"}}, s) == []
    assert check.validate({"ts": "2026-11-06T10:00:00Z", "level": "error", "msg": "x", "service": "api", "err": 3}, s) != []
