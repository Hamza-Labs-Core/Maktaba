"""The components jobs' failure reports (#646): a failed test writes its report, with what
it printed and logged, to ``<logs>/<test>.failure.txt`` beside the workers' logs, locally as
well as in CI, so a one-off failure can be read after the run."""

from __future__ import annotations

import re
from collections.abc import Generator
from typing import Any

import pytest

from components import rig as R


def pytest_sessionstart(session: pytest.Session) -> None:  # noqa: ARG001 - pytest's hook
    if R.LOGS.parent == R.ROOT / "components-logs":
        R.prune_runs(R.LOGS.parent, R.KEEP_RUNS - 1)


@pytest.hookimpl(wrapper=True)
def pytest_runtest_makereport(item: pytest.Item, call: Any) -> Generator[None, Any, Any]:  # noqa: ARG001
    report = yield
    if report.failed:
        R.LOGS.mkdir(parents=True, exist_ok=True)
        name = re.sub(r"[^A-Za-z0-9_.-]+", "_", item.name)
        parts = [f"{report.nodeid} ({report.when}) failed\n", report.longreprtext]
        for title, text in report.sections:
            parts.append(f"\n----- {title} -----\n{text}")
        with (R.LOGS / f"{name}.failure.txt").open("a", encoding="utf-8") as f:
            f.write("\n".join(parts) + "\n")
    return report
