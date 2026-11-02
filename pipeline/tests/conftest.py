"""Pipeline pytest setup — wires the shared testtier plugin.

The shared plugin lives at ``shared/testtier/py/maktaba_testtier`` so
other Python packages in the repo can pull it in too. We don't
currently install it as a uv dependency (no separate distribution,
no version churn) — instead we add the parent directory to
``sys.path`` here so ``import maktaba_testtier`` works.
"""

from __future__ import annotations

import gc
import os
import sys
import threading
import time
from collections.abc import Generator
from pathlib import Path
from typing import NoReturn

# pipeline/tests/conftest.py → repo root is three parents up.
_REPO_ROOT = Path(__file__).resolve().parents[2]
_SHARED_PY = _REPO_ROOT / "shared" / "testtier" / "py"

if str(_SHARED_PY) not in sys.path:
    sys.path.insert(0, str(_SHARED_PY))

# Register the plugin's hooks + autouse fixture by re-exporting them
# from the conftest scope. pytest discovers `pytest_*` hooks and
# `@pytest.fixture` definitions on conftest.py at collection time.
import pytest  # noqa: E402,I001
from maktaba_testtier.netguard import unit_netguard  # noqa: F401,E402
from maktaba_testtier.softcap import pytest_runtest_protocol  # noqa: F401,E402


def pytest_collection_finish(session: pytest.Session) -> None:
    """Freeze what collection left alive (every imported module, every collected test).
    A full GC pass otherwise walks all of it inside whichever test happens to trip it:
    60-85 ms here, more on CI, enough to push a fast test past the unit soft cap (Story
    20.1 AC4). Objects the tests make are still collected as usual."""
    gc.collect()
    gc.freeze()


#: the test run's own process (a forked child still exits for real)
_PYTEST_PID = os.getpid()
_REAL_OS_EXIT = os._exit
#: how long a thread a test started may take to end once the test is over
_THREAD_END_GRACE_SEC = 2.0
_THREADS_BEFORE = pytest.StashKey[frozenset[threading.Thread]]()


def _non_daemon_threads() -> frozenset[threading.Thread]:
    return frozenset(
        t
        for t in threading.enumerate()
        if t is not threading.main_thread() and not t.daemon and t.is_alive()
    )


@pytest.hookimpl(wrapper=True, tryfirst=True)
def pytest_runtest_setup(item: pytest.Item) -> Generator[None, None, None]:
    item.stash[_THREADS_BEFORE] = _non_daemon_threads()
    return (yield)


@pytest.hookimpl(wrapper=True, trylast=True)
def pytest_runtest_teardown(item: pytest.Item) -> Generator[None, None, None]:
    """A test leaves no non-daemon thread running once its fixtures are torn down. One
    that did made a later test end the whole run: the worker's exit (``__main__``
    ``_exit_if_threads_linger``) counts the threads still running and leaves with
    ``os._exit``, and pytest stopped at that test with no summary (a model thread the
    second listen loaded in tests/stages/test_transcribe.py, then the run test in
    tests/cluster/test_node_cli.py)."""
    result = yield
    before = item.stash.get(_THREADS_BEFORE, frozenset())
    deadline = time.monotonic() + _THREAD_END_GRACE_SEC
    for t in _non_daemon_threads() - before:
        t.join(max(0.0, deadline - time.monotonic()))
    left = sorted(t.name for t in _non_daemon_threads() - before)
    if left:
        pytest.fail(f"the test left non-daemon threads running: {left}", pytrace=False)
    return result


@pytest.fixture(autouse=True)
def _no_process_exit(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """``os._exit`` in the test run's process fails the test instead of ending the run.
    It skips pytest's summary and keeps the exit code it was given: a worker's exit
    with code 0 under a test made a truncated run look green. A test of the exit path
    stands in for it (tests/test_exit_after_stop.py)."""
    calls: list[int] = []

    def refuse(code: int) -> NoReturn:
        if os.getpid() != _PYTEST_PID:
            _REAL_OS_EXIT(code)
        if threading.current_thread() is not threading.main_thread():
            calls.append(code)
        pytest.fail(f"a test reached os._exit({code}): it would end the run with no summary")

    monkeypatch.setattr(os, "_exit", refuse)
    yield
    if calls:  # raised on another thread, where pytest does not see it
        pytest.fail(f"a test reached os._exit{tuple(calls)}")


@pytest.fixture(autouse=True)
def _mlx_engine(monkeypatch: pytest.MonkeyPatch) -> None:
    """The transcription tests run as Apple Silicon runs them (the MLX engine), on any
    machine; the CTranslate2 engine's tests (tests/stt/test_ct2.py) switch to it."""
    from maktaba_pipeline.stt import engine

    monkeypatch.setattr(engine, "current", lambda: engine.MLX)


#: what the machine checks of the second listen say in the tests (stt/cohere_cuda.py)
NO_TEST_GPU = "no CUDA device (the tests never ask this machine's GPU)"


@pytest.fixture(autouse=True)
def _no_gpu_probe(monkeypatch: pytest.MonkeyPatch) -> None:
    """No test imports torch to ask this machine's GPU whether the second listen fits (on
    the CTranslate2 engine): the GPU is absent unless a test stands in for one
    (tests/stt/test_cohere_cuda.py)."""
    from maktaba_pipeline.stt import cohere_cuda

    monkeypatch.setattr(cohere_cuda, "_gpu_problem", lambda _index: NO_TEST_GPU)


@pytest.fixture(autouse=True)
def _no_remembered_load_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    """A failed load of the second listen's model is remembered for the process
    (stt/cohere.py, with a backoff): each test starts with none."""
    from maktaba_pipeline.stt import cohere

    monkeypatch.setattr(cohere, "_FAILED", {})


@pytest.fixture(autouse=True)
def _no_real_second_listen_model(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """No test loads the second listen's real model (stt/cohere.py): on a Mac with
    mlx-audio and the model in its Hugging Face cache, the engine's transcribe stage
    loaded it onto the GPU (11 s) and kept it, and its thread, for the rest of the run.
    A test that needs a recogniser stands in for the load (tests/stt/test_model_share.py).
    Each test starts with none loaded, and the ones it loaded are shut down after it."""
    from maktaba_pipeline.stt import cohere

    def refuse(_self: cohere.CohereRecogniser) -> NoReturn:
        raise RuntimeError("a test must stand in for the second listen's model")

    loaded: dict[str, cohere.Recogniser] = {}
    monkeypatch.setattr(cohere.CohereRecogniser, "_load", refuse)
    monkeypatch.setattr(cohere, "_REC", loaded)
    monkeypatch.setattr(cohere, "_PUBLISHED", set())
    yield
    for rec in loaded.values():
        gpu = getattr(rec, "_gpu", None)
        if gpu is not None:
            gpu.shutdown(wait=True)


@pytest.fixture(autouse=True)
def _no_model_or_encyclopedia_calls(monkeypatch: pytest.MonkeyPatch) -> None:
    """No test reaches the language model server or the encyclopedia: the stages that ask
    them (check_tags, check_clips, books) get a stand-in from the test, or fail loudly."""
    from maktaba_pipeline.classify import books, tag_check

    def refuse(*_a: object, **_k: object) -> None:
        raise RuntimeError("a test must stand in for the model / encyclopedia")

    monkeypatch.setattr(tag_check, "ask", refuse)
    monkeypatch.setattr(books, "http_get", refuse)


@pytest.fixture(autouse=True)
def _no_web_metadata_calls() -> object:
    """No test reaches Google, Ollama's web search, a model server or a web page through
    the metadata provider (the enrich stage): a test that runs it passes fakes."""
    from maktaba_pipeline.enrich import providers
    from maktaba_pipeline.enrich.web import HttpResponse

    async def refuse(*_a: object, **_k: object) -> HttpResponse:
        raise RuntimeError("a test must stand in for the web search / model / page")

    providers.use_network(refuse, lambda _host: False)
    yield
    providers.use_network()


@pytest.fixture(autouse=True)
def _app_cache_in_tmp(tmp_path_factory: pytest.TempPathFactory) -> object:
    """thumb_small and review_pages write the app's media cache: never the real home."""
    from maktaba_pipeline.engine.stages import thumb_small

    old = thumb_small.ROOT
    thumb_small.ROOT = str(tmp_path_factory.mktemp("cache") / "thumbnails")
    yield
    thumb_small.ROOT = old


@pytest.fixture(autouse=True)
def _no_cluster_agent(monkeypatch: pytest.MonkeyPatch) -> None:
    """Workers started by tests run without the cluster agent (no home to dial, no
    files on a share); the agent's own tests build one explicitly."""
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "0")


@pytest.fixture(autouse=True)
def _secrets_key_in_tmp(
    monkeypatch: pytest.MonkeyPatch, tmp_path_factory: pytest.TempPathFactory
) -> None:
    """home_secrets reads the key file named here: never the real home's secrets.key."""
    monkeypatch.setenv("MAKTABA_SECRETS_KEY", str(tmp_path_factory.mktemp("keys") / "secrets.key"))


@pytest.fixture(autouse=True)
def _node_config_in_tmp(
    monkeypatch: pytest.MonkeyPatch, tmp_path_factory: pytest.TempPathFactory
) -> None:
    """cluster.config.config_path() (role detection, the agent, join) reads the file
    named here: never this machine's real node.json (a joined machine's holds a
    database credential). A test that needs a node config sets its own. This PC's id
    is a fake one too: never the real MachineGuid a node config is claimed with."""
    monkeypatch.setenv("MAKTABA_NODE_CONFIG", str(tmp_path_factory.mktemp("node") / "node.json"))
    monkeypatch.setenv("MAKTABA_MACHINE_ID", "test-pc")


@pytest.fixture(autouse=True)
def _forget_refusals() -> object:
    """A test that revokes a machine must not make later workers exit 77."""
    from maktaba_pipeline.cluster import exitcodes

    yield
    exitcodes.reset()


@pytest.fixture(autouse=True)
def _no_leftover_log_setup() -> object:
    """A test that ran the entry point in-process installed the process-wide log hooks
    (stdlib forwarder, excepthooks) and pointed structlog at that test's captured
    stdout, closed once it ends; later tests start without either."""
    import structlog

    from maktaba_pipeline.log import hooks

    yield
    hooks.uninstall()
    structlog.reset_defaults()


@pytest.fixture(autouse=True)
def _unit_tests_skip_disk_syncs(
    request: pytest.FixtureRequest, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Unit tests write throwaway files; ``os.fsync`` waits for the disk, which on a busy
    CI runner pushed tests that write atomically (temp file, fsync, rename, directory
    fsync) past their 0.1 s cap. Durability is not what a unit test checks."""
    if request.node.get_closest_marker("unit") is not None:
        monkeypatch.setattr("os.fsync", lambda fd: None)


@pytest.fixture(autouse=True)
def _search_index_open() -> None:
    """A worker that stopped closed the search index's writes for the process
    (search/gate.py, the runtime's shutdown): each test starts with them open."""
    from maktaba_pipeline.search import gate

    gate.reopen()


@pytest.fixture(scope="session", autouse=True)
def _migrated_sqlite_schema() -> None:
    """The SQLite schema's kept steps, built once a process before any test is timed:
    a migration test applies only the few slots before its own (tests/engine/migrate.py)."""
    from tests.engine.migrate import warm_sqlite

    warm_sqlite()
