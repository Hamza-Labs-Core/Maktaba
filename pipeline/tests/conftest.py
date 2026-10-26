"""Pipeline pytest setup — wires the shared testtier plugin.

The shared plugin lives at ``shared/testtier/py/maktaba_testtier`` so
other Python packages in the repo can pull it in too. We don't
currently install it as a uv dependency (no separate distribution,
no version churn) — instead we add the parent directory to
``sys.path`` here so ``import maktaba_testtier`` works.
"""

from __future__ import annotations

import gc
import sys
from pathlib import Path

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
from maktaba_testtier.softcap import (  # noqa: F401,E402
    pytest_runtest_call,
    pytest_runtest_makereport,
)


def pytest_collection_finish(session: pytest.Session) -> None:
    """Freeze what collection left alive (every imported module, every collected test).
    A full GC pass otherwise walks all of it inside whichever test happens to trip it:
    60-85 ms here, more on CI, enough to push a fast test past the unit soft cap (Story
    20.1 AC4). Objects the tests make are still collected as usual."""
    gc.collect()
    gc.freeze()


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
