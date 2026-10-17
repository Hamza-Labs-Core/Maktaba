from collections.abc import Iterator

import pytest

from maktaba_pipeline.engine.stages.plan_channels import reset_plan_exporters
from tests.engine.fixtures_db import db, pg_template, sqlite_template  # noqa: F401


@pytest.fixture(autouse=True)
def _no_leaked_plan_exporters() -> Iterator[None]:
    """Extension tests register plan exporters globally; a core stage test must start clean."""
    reset_plan_exporters()
    yield
    reset_plan_exporters()
