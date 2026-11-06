"""The rig keeps each local run's logs (#646): runs have folders of their own and only the
oldest beyond :data:`rig.KEEP_RUNS` are removed. No database needed."""

from __future__ import annotations

from pathlib import Path

from tests.components import rig as R


def test_prune_runs_keeps_the_newest(tmp_path: Path) -> None:
    names = [f"2026110{d}-120000-{d}" for d in range(1, 6)]
    for n in names:
        (tmp_path / n).mkdir()
    (tmp_path / "notes").mkdir()  # not a run folder: left alone
    gone = R.prune_runs(tmp_path, keep=2)
    assert [p.name for p in gone] == names[:3]
    assert sorted(p.name for p in tmp_path.iterdir()) == sorted([*names[3:], "notes"])


def test_a_local_run_has_a_folder_of_its_own(monkeypatch: object) -> None:
    import os

    if os.environ.get("MAKTABA_COMPONENTS_LOGS"):
        assert Path(os.environ["MAKTABA_COMPONENTS_LOGS"]) == R.LOGS
    else:
        assert R.LOGS.parent == R.ROOT / "components-logs"
        assert R.LOGS.name.endswith(f"-{os.getpid()}")
