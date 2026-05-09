"""Unit-tier socket guard (AC1 of Story 20.1).

Wired in by ``pipeline/tests/conftest.py``::

    from maktaba_testtier.netguard import unit_netguard  # noqa: F401

The fixture is autouse + session-scoped: it patches
``socket.socket`` so any unit-marked test that tries to open a
network socket gets a clear AssertionError. Integration / e2e tests
are detected by their pytest markers and bypass the guard.
"""

from __future__ import annotations

import socket
from collections.abc import Iterator
from typing import Any

import pytest


class _ForbiddenSocket:
    """Replacement for ``socket.socket`` that always raises."""

    def __init__(self, *_: Any, **__: Any) -> None:
        raise AssertionError("unit tests must not open sockets (Story 20.1 AC1)")


def _is_unit(item: pytest.Item) -> bool:
    if item.get_closest_marker("unit") is None:
        return False
    return item.get_closest_marker("integration") is None and item.get_closest_marker("e2e") is None


@pytest.fixture(autouse=True)
def unit_netguard(request: pytest.FixtureRequest) -> Iterator[None]:
    """Block ``socket.socket`` for unit-marked tests.

    Per-test (function-scoped) so that integration tests sharing the
    same session can still open sockets. The cost of swapping the
    attribute on every test is negligible compared to the cost of a
    real socket setup.
    """
    if not _is_unit(request.node):
        yield
        return
    real = socket.socket
    socket.socket = _ForbiddenSocket  # type: ignore[misc,assignment]
    try:
        yield
    finally:
        socket.socket = real  # type: ignore[misc]
