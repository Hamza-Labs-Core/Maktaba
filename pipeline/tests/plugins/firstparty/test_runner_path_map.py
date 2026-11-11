"""The runner resolves a job's paths with the worker's map (story 59.13): a stage that
calls :func:`~maktaba_pipeline.engine.paths.to_local` (the scanner) sees the map the job's
frame carries, shares and character maps included."""

from __future__ import annotations

import os
from pathlib import Path


def test_a_runner_job_maps_a_converted_root_as_the_worker_does(tmp_path: Path) -> None:
    """Story 59.13: once a NAS home's paths are in their share form, its scan runs in the
    runner; the runner makes the worker's map its own, so the library root
    ``smb://nas/Movies`` is walked at the NAS's folder (it found no file before) and a found
    file is stored in the conversion's form: NFC, from the shares the frame now carries."""
    import json
    import unicodedata

    from maktaba_pipeline.engine import paths
    from maktaba_pipeline.plugins.native import _path_map_frame
    from maktaba_pipeline.sharepath.resolver import Resolver, ShareMount
    from maktaba_plugin_runner.server import job_path_map

    local = f"{tmp_path.as_posix()}/volume1/Movies/"
    (tmp_path / "volume1" / "Movies" / "en").mkdir(parents=True)
    (tmp_path / "volume1" / "Movies" / "en" / "a.mp4").write_bytes(b"x")
    worker = Resolver(
        (("smb://nas/Movies/", local),),
        shares=(ShareMount("smb://nas/Movies/", local, "admin", "nas", "nas_home"),),
    )
    frame = json.loads(json.dumps(_path_map_frame(worker)))
    before = paths.active_path_map()
    try:
        got = job_path_map(frame)
        assert got == worker
        assert paths.to_local("smb://nas/Movies") == local.rstrip("/")
        assert os.listdir(paths.to_local("smb://nas/Movies/en")) == ["a.mp4"]
        nfd = unicodedata.normalize("NFD", "درس آخر.mp4")
        assert paths.to_db(local + "en/" + nfd) == "smb://nas/Movies/en/" + unicodedata.normalize(
            "NFC", nfd
        )
        # a frame from an older worker (entries alone) still maps
        assert job_path_map({"path_map": frame["path_map"]}).entries == worker.entries
        assert job_path_map({}) == Resolver()
    finally:
        paths.set_active_path_map(before)
