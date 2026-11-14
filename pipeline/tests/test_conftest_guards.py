"""The suite's guards against a test that ends the whole run (tests/conftest.py): a test
that leaves a non-daemon thread running, or reaches ``os._exit``, fails by name, and the
run still ends with its summary and a failing exit code.

Before them, a model thread the second listen loaded in one test made the worker's exit
in a later one (``__main__._exit_if_threads_linger``) call ``os._exit(0)``: pytest
stopped at ~87% with no summary and exit code 0."""

from __future__ import annotations

import importlib.util
import os
import re
from pathlib import Path

import pytest

from maktaba_pipeline.stt import cohere

pytest_plugins = ["pytester"]

_PIPELINE = Path(__file__).resolve().parents[1]

_INNER_CONFTEST = """
import threading

import tests.conftest
from tests.conftest import _no_process_exit, pytest_runtest_setup, pytest_runtest_teardown

tests.conftest._THREAD_END_GRACE_SEC = 0.1  # the thread below never ends on its own

RELEASE = threading.Event()


def pytest_sessionfinish(session, exitstatus):
    RELEASE.set()  # the leaked thread ends with the run
"""

_INNER_TESTS = """
import os
import re
import threading

import conftest


def test_leaves_a_thread():
    threading.Thread(target=conftest.RELEASE.wait, name="left-behind").start()


def test_exits_the_process():
    os._exit(0)


def test_after_them():
    pass
"""


def test_a_leaked_thread_and_os_exit_fail_loudly(
    pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("PYTHONPATH", str(_PIPELINE))
    pytester.makeconftest(_INNER_CONFTEST)
    pytester.makepyfile(test_inner=_INNER_TESTS)
    result = pytester.runpytest_subprocess("-p", "no:cacheprovider", "-p", "no:randomly")
    out = result.stdout.str()
    assert "the test left non-daemon threads running: ['left-behind']" in out
    assert "a test reached os._exit(0)" in out
    # the run went on past both and printed its summary
    result.assert_outcomes(passed=2, failed=1, errors=1)
    assert result.ret == pytest.ExitCode.TESTS_FAILED


def test_os_exit_in_a_test_fails_it() -> None:
    with pytest.raises(pytest.fail.Exception, match=r"os\._exit\(3\)"):
        os._exit(3)


def test_no_test_loads_the_real_second_listen_model(monkeypatch: pytest.MonkeyPatch) -> None:
    """mlx-audio "installed", the model on the machine: the load is refused, nothing is
    kept, and the guard above fails this test if its model thread is left."""
    real_find = importlib.util.find_spec
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: object() if name == "mlx_audio" else real_find(name, *a),
    )
    with pytest.raises(RuntimeError, match="stand in for the second listen"):
        cohere.recogniser(cohere.DEFAULT_MODEL)
    assert not cohere.loaded(cohere.DEFAULT_MODEL)


_SLOW_TEST = """
import time


def test_waits_too_long():
    time.sleep(1.5)
"""


def test_the_suite_dumps_a_hung_tests_stack(pytestconfig: pytest.Config) -> None:
    """``faulthandler_timeout`` (pyproject.toml) is on for every tier: a hung test prints
    every thread's stack, its own wait among them, instead of a CI job cancelled at its
    limit with no output."""
    assert float(pytestconfig.getini("faulthandler_timeout")) > 0


def test_a_test_past_the_timeout_prints_where_it_waits(pytester: pytest.Pytester) -> None:
    pytester.makepyfile(test_slow=_SLOW_TEST)
    result = pytester.runpytest_subprocess(
        "-p", "no:cacheprovider", "-p", "no:randomly", "-o", "faulthandler_timeout=0.3"
    )
    result.assert_outcomes(passed=1)  # the dump does not end the run
    err = result.stderr.str()
    assert "Timeout" in err
    assert "test_waits_too_long" in err


_TIMED_CONFTEST = """
from tests.conftest import pytest_collection, pytest_collection_finish, pytest_terminal_summary
"""

_TIMED_TESTS = """
import time

import pytest


@pytest.fixture
def slow_setup():
    time.sleep(0.2)


def test_sets_up_slowly(slow_setup):
    pass


def test_calls_slowly():
    time.sleep(0.3)
"""


def test_the_session_says_where_its_time_went(
    pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The ``session time:`` line (tests/conftest.py): collection, and each test phase summed
    over the tests. It showed the integration tier spending 125 s of a 215 s session on
    collection (PR #1303), which the 25 slowest tests could not."""
    monkeypatch.setenv("PYTHONPATH", str(_PIPELINE))
    pytester.makeconftest(_TIMED_CONFTEST)
    pytester.makepyfile(test_timed=_TIMED_TESTS)
    result = pytester.runpytest_subprocess("-p", "no:cacheprovider", "-p", "no:randomly")
    result.assert_outcomes(passed=2)
    line = next(s for s in result.stdout.lines if s.startswith("session time: "))
    secs = {
        name: float(value)
        for name, value in re.findall(r"(collection|setup|call|teardown) ([0-9.]+)s", line)
    }
    assert set(secs) == {"collection", "setup", "call", "teardown"}, line
    assert secs["setup"] >= 0.2, line
    assert secs["call"] >= 0.3, line
