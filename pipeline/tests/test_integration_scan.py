"""tests/integration_scan.py: the integration tier's narrowed collection loses no test.

The equality test carries no tier marker on purpose: it collects the whole tree, the very
cost the tier sheds, so it runs in the integration job's step for untiered tests, outside
the tier's time budget. Each collection's output is printed when it fails.
"""

from __future__ import annotations

import subprocess
import sys
import textwrap
from pathlib import Path

import pytest

from tests.integration_scan import integration_modules

_PIPELINE = Path(__file__).resolve().parents[1]


def _collect(*extra: str) -> tuple[set[str], str]:
    cmd = [sys.executable, "-m", "pytest", "--collect-only", "-q", "-m", "integration"]
    cmd += ["-p", "no:cacheprovider", *extra]
    run = subprocess.run(cmd, cwd=_PIPELINE, capture_output=True, text=True, timeout=600)
    log = f"$ {' '.join(cmd)} (exit {run.returncode})\n{run.stdout}\n{run.stderr}"
    assert run.returncode == 0, log
    return {line for line in run.stdout.splitlines() if "::" in line}, log


def test_integration_files_collects_every_integration_test() -> None:
    whole, whole_log = _collect()
    narrowed, narrowed_log = _collect("--integration-files")
    assert whole, whole_log
    # tests/conftest.py's summary line, which the integration tier's log carries
    assert "session time: collection " in whole_log, whole_log
    lost = sorted(whole - narrowed)
    assert not lost, (
        "--integration-files lost these integration tests: teach tests/integration_scan.py "
        "how they get the marker\n" + "\n".join(lost) + "\n\n" + narrowed_log
    )
    assert narrowed == whole, narrowed_log


def _tree(tmp_path: Path, files: dict[str, str]) -> set[str]:
    for name, body in files.items():
        path = tmp_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(body), encoding="utf-8")
    return {str(p.relative_to(tmp_path.resolve())) for p in integration_modules(tmp_path)}


@pytest.mark.unit
def test_the_scan_keeps_each_way_a_module_gets_the_marker(tmp_path: Path) -> None:
    kept = _tree(
        tmp_path,
        {
            "fixtures.py": """
                import pytest
                needs_pg = pytest.mark.integration
                @pytest.fixture(params=["a", pytest.param("b", marks=pytest.mark.integration)])
                def store(request): return request.param
                @pytest.fixture
                def engine(store): return store
                @pytest.fixture(name="renamed")
                def _looks_up(request): return request.getfixturevalue("engine")
            """,
            "test_marked.py": "import pytest\npytestmark = pytest.mark.integration\n",
            "test_direct.py": "def test_a(store): pass\n",
            "test_transitive.py": "class TestX:\n    def test_a(self, engine): pass\n",
            "test_renamed.py": "def test_a(renamed): pass\n",
            "test_usefixtures.py": (
                "import pytest\n@pytest.mark.usefixtures('engine')\ndef test_a(): pass\n"
            ),
            "test_alias.py": "from fixtures import needs_pg\n@needs_pg\ndef test_a(): pass\n",
            "auto/conftest.py": """
                import pytest
                @pytest.fixture(autouse=True)
                def everywhere(engine): yield
            """,
            "auto/deeper/test_autouse.py": "def test_a(): pass\n",
            "test_plain.py": "import pytest\npytestmark = pytest.mark.unit\n",
            "helper.py": "def test_like(store): pass\n",
        },
    )
    assert kept == {
        "test_marked.py",
        "test_direct.py",
        "test_transitive.py",
        "test_renamed.py",
        "test_usefixtures.py",
        "test_alias.py",
        "auto/deeper/test_autouse.py",
    }
