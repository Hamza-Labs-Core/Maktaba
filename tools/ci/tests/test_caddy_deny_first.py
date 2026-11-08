"""No Caddyfile hides a terminal directive behind its handle blocks (tools/ci/caddy_deny_first.py):
the repository's files pass, and the two files that broke the rule (origin-aop.caddy before
#927, Caddyfile.witness before #930) fail, as do the other shapes of the same bug."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import caddy_deny_first  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]
FIXTURES = Path(__file__).resolve().parent / "fixtures" / "caddy"


def test_the_repository_passes() -> None:
    files = caddy_deny_first.tracked(ROOT)
    assert "deploy/cloud-ha/caddy/origin-aop.caddy" in files
    assert "deploy/cloud-ha/monitoring/Caddyfile.witness" in files
    assert "web/docker-Caddyfile" in files
    assert not any(f.startswith("tools/ci/tests/fixtures/") for f in files)
    assert caddy_deny_first.main(["caddy_deny_first.py", str(ROOT)]) == 0


@pytest.mark.parametrize(
    ("fixture", "line", "handle"),
    [("origin-aop.before-927.caddy", 21, 23), ("Caddyfile.witness.before-930", 13, 15)],
)
def test_the_files_that_broke_the_rule_fail(fixture: str, line: int, handle: int) -> None:
    found = caddy_deny_first.problems_in((FIXTURES / fixture).read_text(), fixture)
    assert len(found) == 1
    assert found[0].startswith(f"{fixture}:{line}: a bare respond beside handle (line {handle})")
    assert caddy_deny_first.main(["caddy_deny_first.py", "--files", str(FIXTURES / fixture)]) == 1


def _check(text: str) -> list[str]:
    return caddy_deny_first.problems_in(text, "t")


def test_a_deny_in_a_handle_of_its_own_passes() -> None:
    assert _check(
        "example.com {\n"
        "\t@out not remote_ip 10.0.0.0/8\n"
        "\thandle @out {\n\t\trespond 403\n\t}\n"
        "\thandle { reverse_proxy 127.0.0.1:8090 }\n"
        "}\n"
    ) == []


@pytest.mark.parametrize("directive", sorted(caddy_deny_first.TERMINAL))
def test_each_terminal_directive_beside_handle_fails(directive: str) -> None:
    found = _check(f"example.com {{\n\t{directive} @x 403\n\thandle {{\n\t\trespond 200\n\t}}\n}}\n")
    assert [f.split(":")[1] for f in found] == ["2"]
    assert f"a bare {directive} beside handle (line 3)" in found[0]


def test_handle_path_counts_as_handle() -> None:
    assert len(_check("example.com {\n\trespond 401\n\thandle_path /x/* {\n\t\tfile_server\n\t}\n}\n")) == 1


def test_a_route_block_runs_as_written() -> None:
    assert _check("example.com {\n\troute {\n\t\trespond @x 403\n\t\thandle { respond 200 }\n\t}\n}\n") == []


def test_terminal_directives_without_handle_pass() -> None:
    assert _check("example.com {\n\t@x path /a\n\trespond @x 404\n\treverse_proxy 127.0.0.1:8090\n}\n") == []


def test_a_snippet_is_checked() -> None:
    assert len(_check("(site) {\n\trespond 401\n\thandle { respond 200 }\n}\n")) == 1


def test_nested_blocks_are_checked_alone() -> None:
    # the inner handle's respond is its own block's only directive: fine; the outer abort is not
    found = _check("example.com {\n\thandle /a {\n\t\trespond 200\n\t}\n\tabort\n}\n")
    assert [f.split(":")[1] for f in found] == ["5"]


def test_placeholders_quotes_and_comments_are_not_braces() -> None:
    text = (
        "{$SITE} {\n"
        '\theader X-Saw "{remote_host} }"\n'
        "\t# respond 403 { in a comment\n"
        "\treverse_proxy {$UP} {\n\t\theader_up CF-Connecting-IP {remote_host}\n\t}\n"
        "}\n"
    )
    assert _check(text) == []


def test_matcher_blocks_are_not_directives() -> None:
    assert _check("example.com {\n\t@push {\n\t\tmethod POST\n\t}\n\thandle @push { reverse_proxy :9091 }\n}\n") == []


def test_no_files_is_an_error() -> None:
    assert caddy_deny_first.main(["caddy_deny_first.py", "--files"]) == 1
