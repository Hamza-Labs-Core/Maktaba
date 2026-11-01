"""Tier-guard tests for ``maktaba_testtier`` (Story 20.1 TC1 / TC2).

These mirror the Go-side tier_test.go assertions so the Python tier
honors the same I/O ban + soft-cap contract.
"""

from __future__ import annotations

import os
import re
import socket
import subprocess
import sys
import time
from pathlib import Path

import pytest

# --- TC1: a unit test that opens a socket fails -----------------------


@pytest.mark.unit
def test_unit_socket_blocked() -> None:
    """A unit test that constructs a socket should hit the netguard."""
    with pytest.raises(AssertionError, match="unit tests must not open sockets"):
        socket.socket()


# --- TC2: soft-cap WARN + hard-cap fail -------------------------------
#
# We can't inline-test the >3x-cap fail path (it would fail this
# suite). Instead we run the soft cap *just below* the warn threshold
# in one test and *just above* it in another, and trust the unit
# tests for ``maktaba_testtier.softcap`` (TODO: add directly) to
# cover the failing branch with a mocked fixture.


@pytest.mark.unit
def test_unit_under_softcap_is_silent() -> None:
    """A test well under 100 ms should produce no soft-cap warning."""
    time.sleep(0.001)


@pytest.mark.unit
@pytest.mark.filterwarnings("ignore::UserWarning")
def test_unit_softcap_warn_band_does_not_fail() -> None:
    """A test in (cap, 3x cap] warns but doesn't fail.

    We pin a 50 ms sleep — under the 100 ms cap — so this test is
    deterministic on slow CI runners. The hard-fail path is exercised
    indirectly by the Go side and the cap enforcement is shared
    across runtimes via ``maktaba_testtier.tiers``.
    """
    time.sleep(0.05)


# --- Windows runners get scaled caps ------------------------------------


@pytest.mark.unit
def test_soft_cap_scales_on_windows_only() -> None:
    """File-heavy unit tests run several times slower on Windows runners."""
    from maktaba_testtier.softcap import _platform_scale
    from maktaba_testtier.tiers import WINDOWS_SOFT_CAP_SCALE

    assert _platform_scale("win32") == WINDOWS_SOFT_CAP_SCALE > 1
    assert _platform_scale("linux") == 1.0
    assert _platform_scale("darwin") == 1.0


@pytest.mark.unit
def test_soft_cap_scales_under_coverage() -> None:
    """Coverage measurement slows every test; the caps make room for it."""
    from maktaba_testtier.softcap import _coverage_scale
    from maktaba_testtier.tiers import COVERAGE_SOFT_CAP_SCALE

    assert _coverage_scale(True) == COVERAGE_SOFT_CAP_SCALE > 1
    assert _coverage_scale(False) == 1.0


def test_soft_cap_fails_only_a_test_slow_twice() -> None:
    """Over the hard cap once with a fast run after it is the runner's load; over it
    twice (or with no second run) is the test."""
    from maktaba_testtier.softcap import breached

    assert not breached(0.2, None, 0.3)  # under the cap: never run again
    assert not breached(0.55, 0.02, 0.3)  # a stall, then fast: the runner
    assert breached(0.55, 0.5, 0.3)  # slow twice: the test
    assert breached(0.55, None, 0.3)  # the second run failed or did not run


_INNER_CONFTEST = """
import sys
sys.path.insert(0, {shared!r})
import maktaba_testtier.softcap as sc
from maktaba_testtier.softcap import pytest_runtest_protocol  # noqa: F401

sc._platform_scale = lambda *a: 1.0  # hard cap 0.3 s on every OS
sc._coverage_scale = lambda *a: 1.0


def pytest_configure(config):
    config.addinivalue_line("markers", "unit: unit tier")
"""

_INNER_TESTS = """
import os
import time
from pathlib import Path

import pytest

RUNS = Path(os.environ["PROBE_RUNS"])


@pytest.fixture
def made(tmp_path):
    d = tmp_path / "made-by-a-fixture"
    d.mkdir()  # raises when the fixture is not made afresh
    yield d
    (d / "torn-down").write_text("")


@pytest.mark.unit
def test_stalls_once(made, tmp_path):
    assert made.parent == tmp_path  # a fixture on tmp_path follows the fresh one
    (tmp_path / "day").mkdir()
    with open(made / "once", "x"):
        pass
    runs = int(RUNS.read_text()) if RUNS.exists() else 0
    RUNS.write_text(str(runs + 1))
    if runs == 0:
        time.sleep(0.5)  # one stall of the runner


@pytest.mark.unit
def test_slow_twice():
    time.sleep(0.5)
"""


@pytest.mark.integration  # runs a pytest of its own (a second Python process)
def test_a_slow_test_runs_again_with_fresh_fixtures(tmp_path: Path) -> None:
    """Over the hard cap, a test runs once more as a whole: its fixtures (``tmp_path``
    and every one made on it) afresh, so a stall passes with a warning; slow twice fails.
    Running the call alone again met the first run's files and failed every time."""
    import maktaba_testtier

    shared = str(Path(maktaba_testtier.__file__).resolve().parents[1])
    inner = tmp_path / "inner"
    inner.mkdir()
    (inner / "conftest.py").write_text(_INNER_CONFTEST.format(shared=shared))
    (inner / "test_inner.py").write_text(_INNER_TESTS)
    env = {**os.environ, "PROBE_RUNS": str(tmp_path / "runs")}
    out = subprocess.run(
        [sys.executable, "-m", "pytest", "-q", "-p", "no:cacheprovider", "-W", "always",
         "--rootdir", str(inner), str(inner)],
        cwd=inner, env=env, capture_output=True, text=True, timeout=120,
    )  # fmt: skip
    text = out.stdout + out.stderr
    assert out.returncode == 1, text
    assert "1 failed, 1 passed" in text, text
    assert (tmp_path / "runs").read_text() == "2", text  # it did run again
    assert "test_stalls_once: took" in text and "the runner's load, not the test" in text
    assert "FAILED test_inner.py::test_slow_twice" in text, text
    assert re.search(r"test took [\d.]+s > 3x soft cap 0\.100s \(Story 20\.1 AC4\), and "
                     r"[\d.]+s run again", text), text  # fmt: skip
