"""Session fixtures for the e2e tier (Story 20.5, Track V).

The e2e suite drives the live compose stack defined in
``deploy/compose/docker-compose.yml``. ``api_base`` blocks until the
api's ``/healthz`` liveness endpoint answers 200, so every e2e test
can assume a reachable, live API. If the stack is not up the fixture
raises ``RuntimeError`` and the whole suite errors out — that is the
intended behaviour, an empty/green e2e gate is the gap this track
closes.

The base URL is overridable via ``MAKTABA_E2E_BASE_URL`` because the
api container's public port is not necessarily host-published in
every environment (compose routes most host traffic through Caddy,
which does not proxy ``/healthz``). CI / operators point this at
wherever the api's public listener is reachable.
"""

from __future__ import annotations

import os
import time
import urllib.error
import urllib.request

import pytest

# Default assumes the api's public listener (MAKTABA_HTTP_ADDR :8080)
# is reachable on localhost. Override with MAKTABA_E2E_BASE_URL.
DEFAULT_BASE_URL = "http://localhost:8080"

# Wait budget for the stack to become live. The compose `--wait` in CI
# already gates on healthchecks, but a local `make test-e2e` may race
# the stack coming up, so we re-probe here.
_READY_TIMEOUT_S = 60.0
_POLL_INTERVAL_S = 1.0


def _base_url() -> str:
    return os.environ.get("MAKTABA_E2E_BASE_URL", DEFAULT_BASE_URL).rstrip("/")


@pytest.fixture(scope="session")
def api_base() -> str:
    """Return the api base URL once ``/healthz`` answers 200.

    Polls ``GET {base}/healthz`` for up to 60s. Raises ``RuntimeError``
    if the endpoint never returns 200 — the e2e suite cannot run
    against a stack that is not live.
    """
    base = _base_url()
    health_url = f"{base}/healthz"
    deadline = time.monotonic() + _READY_TIMEOUT_S
    last_err: str = "no attempt made"

    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(health_url, timeout=5) as resp:
                if resp.status == 200:
                    return base
                last_err = f"GET {health_url} -> HTTP {resp.status}"
        except urllib.error.HTTPError as exc:
            last_err = f"GET {health_url} -> HTTP {exc.code}"
        except (urllib.error.URLError, OSError, TimeoutError) as exc:
            last_err = f"GET {health_url} -> {exc!r}"
        time.sleep(_POLL_INTERVAL_S)

    raise RuntimeError(
        f"api stack not ready after {_READY_TIMEOUT_S:.0f}s "
        f"(base={base!r}); last error: {last_err}. "
        "Is the compose stack up? "
        "Set MAKTABA_E2E_BASE_URL if the api is reachable elsewhere."
    )
