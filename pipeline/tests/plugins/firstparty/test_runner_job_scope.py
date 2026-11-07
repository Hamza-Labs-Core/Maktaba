"""A runner job of a file stage changes only its own subject's files (Epic 54 wave 5, story
54.32 §2, T02): the scope's names (the files, their staged copies and sidecars, never a
neighbour with a longer name), and the audit hook that refuses the rest with
``plugin_scope``. The hook runs in a child process: an audit hook is for good."""

from __future__ import annotations

import json
import subprocess
import sys
import textwrap
from pathlib import Path

import pytest

from maktaba_pipeline.library_mgmt.scope import SCOPED, JobScope


def test_the_scope_allows_the_files_their_staged_copies_and_their_sidecars(
    tmp_path: Path,
) -> None:
    d = tmp_path / "دروس"
    d.mkdir()
    s = JobScope.of("convert_mp4", [str(d / "محاضرة.mkv"), str(d / "محاضرة.mp4")])
    for ok in (
        "محاضرة.mkv",
        "محاضرة.mp4",
        ".محاضرة.maktaba-new.mp4",  # the swap's staged copy
        "محاضرة.ar.srt",  # a sidecar beside the MP4
        "محاضرة.en.2.srt",
        ".محاضرة.ar.srt.maktaba-new",  # a sidecar's staged copy
    ):
        assert s.allows(str(d / ok)), ok
    for no in (
        "محاضرة 2.mp4",  # another video of the folder
        "محاضرة.1080p.mkv",  # a longer name that starts with the stem
        ".محاضرة 2.maktaba-new.mp4",
        "محاضرة.ar.txt",
    ):
        assert not s.allows(str(d / no)), no
    assert not s.allows(str(tmp_path / "محاضرة.mkv")), "the same name in another folder"


def test_the_scope_compares_through_a_symlinked_folder(tmp_path: Path) -> None:
    real = tmp_path / "real"
    real.mkdir()
    (tmp_path / "link").symlink_to(real)
    s = JobScope.of("dedup_apply", [str(tmp_path / "link" / "a.mp4")], [str(tmp_path / "link")])
    assert s.allows(str(real / "a.mp4"))
    assert s.governs(str(real / "b.mp4")) and not s.governs(str(real / ".maktaba" / "t"))


def test_the_scoped_stages_are_the_file_moving_ones() -> None:
    """convert_mp4 for a video, dedup_apply for a group, file_restore for a library: a
    library-wide convert and every other stage keep no scope."""
    assert {k: v[0] for k, v in SCOPED.items()} == {
        "convert_mp4": "video",
        "dedup_apply": "dup_group",
        "file_restore": "library",
    }


HOOK = textwrap.dedent(
    """
    import asyncio, json, os, shutil, sys
    from maktaba_plugin_runner import sandbox
    from maktaba_plugin_sdk.native.stmtdb import PluginRefused
    from maktaba_pipeline.library_mgmt.scope import JobScope
    root = sys.argv[1]
    lib = os.path.join(root, "lib")
    mine, theirs = os.path.join(lib, "g1", "mine.mp4"), os.path.join(lib, "g2", "theirs.mp4")
    trash = os.path.join(lib, ".maktaba", "trash", "dedup", "2026-11-07")
    os.makedirs(trash, exist_ok=True)
    sandbox.guard_scope()
    out = {}
    def probe(name, fn):
        try:
            fn()
            out[name] = "allowed"
        except PluginRefused as exc:
            out[name] = "refused:" + exc.code
        except Exception as exc:
            out[name] = "error:" + type(exc).__name__ + ":" + str(exc)
    scope = JobScope.of("dedup_apply", [mine], [lib])
    # no job: nothing is checked
    probe("no_scope_write", lambda: open(theirs, "ab").close())
    token = sandbox.JOB_SCOPE.set(scope)
    probe("move_mine_to_trash", lambda: os.rename(mine, os.path.join(trash, "mine.mp4")))
    probe("move_mine_back", lambda: os.rename(os.path.join(trash, "mine.mp4"), mine))
    probe("move_theirs_to_trash", lambda: os.rename(theirs, os.path.join(trash, "theirs.mp4")))
    probe("over_theirs", lambda: os.rename(os.path.join(root, "outside.tmp"), theirs))
    probe("remove_theirs", lambda: os.remove(theirs))
    probe("write_theirs", lambda: open(theirs, "wb").close())
    probe("read_theirs", lambda: open(theirs, "rb").close())
    probe("copy_onto_theirs", lambda: shutil.copyfile(os.path.join(root, "outside.tmp"), theirs))
    probe("rmtree_their_folder", lambda: shutil.rmtree(os.path.dirname(theirs)))
    probe("write_in_trash", lambda: open(os.path.join(trash, "x"), "wb").close())
    probe("write_outside", lambda: open(os.path.join(root, "work.tmp"), "wb").close())
    async def threaded():
        await asyncio.to_thread(os.remove, theirs)
    probe("thread_remove_theirs", lambda: asyncio.run(threaded()))
    out["kept"] = list(scope.refused)
    sandbox.JOB_SCOPE.reset(token)
    out["theirs_there"] = os.path.exists(theirs)
    print(json.dumps(out))
    """
)


@pytest.mark.skipif(sys.platform == "win32", reason="runners run on macOS and Linux")
def test_the_hook_refuses_another_subjects_file_and_keeps_the_refusal(tmp_path: Path) -> None:
    lib = tmp_path / "lib"
    for rel in ("g1/mine.mp4", "g2/theirs.mp4"):
        (lib / rel).parent.mkdir(parents=True, exist_ok=True)
        (lib / rel).write_bytes(b"video")
    (tmp_path / "outside.tmp").write_bytes(b"x")
    got = subprocess.run(
        [sys.executable, "-c", HOOK, str(tmp_path)], capture_output=True, text=True, check=False
    )
    assert got.returncode == 0, got.stderr
    out = json.loads(got.stdout)
    assert out.pop("kept") and out.pop("theirs_there") is True
    assert out == {
        "no_scope_write": "allowed",
        "move_mine_to_trash": "allowed",
        "move_mine_back": "allowed",
        "move_theirs_to_trash": "refused:plugin_scope",
        "over_theirs": "refused:plugin_scope",
        "remove_theirs": "refused:plugin_scope",
        "write_theirs": "refused:plugin_scope",
        "read_theirs": "allowed",
        "copy_onto_theirs": "refused:plugin_scope",
        "rmtree_their_folder": "refused:plugin_scope",
        "write_in_trash": "allowed",
        "write_outside": "allowed",
        "thread_remove_theirs": "refused:plugin_scope",
    }
