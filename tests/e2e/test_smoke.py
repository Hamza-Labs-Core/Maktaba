"""End-to-end smoke tests (Story 20.5, Track V).

These run against the live compose stack via the ``api_base`` session
fixture (see ``conftest.py``). They are intentionally minimal — the
point of this track is to make ``make test-e2e`` a real gate that
fails when the stack is broken, not to exhaustively cover the API.
"""

from __future__ import annotations

import urllib.error
import urllib.request

import pytest

pytestmark = pytest.mark.e2e


def _status(url: str) -> int:
    """GET ``url`` and return the HTTP status, treating HTTP errors as
    a status code rather than an exception (4xx/5xx are expected
    outcomes for some of these assertions)."""
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            return resp.status
    except urllib.error.HTTPError as exc:
        return exc.code


def test_api_health_is_green(api_base: str) -> None:
    """The api's liveness endpoint must answer 200 once the stack is up."""
    assert _status(f"{api_base}/healthz") == 200


# TODO(W0-R3): remove xfail — once W0-R3 merges, an unauthenticated
# business route must be rejected with 401/403. Until then the current
# stack does not yet enforce this, so the assertion is expected to fail
# (strict=False so an early enforcement still passes the gate).
@pytest.mark.xfail(reason="enforced once W0-R3 merges", strict=False)
def test_unauthenticated_business_route_is_rejected(api_base: str) -> None:
    """An unauthenticated request to a business route must be rejected.

    Encodes the post-R3 contract: ``GET /api/libraries`` without
    credentials returns 401 or 403, never 200.
    """
    assert _status(f"{api_base}/api/libraries") in (401, 403)
