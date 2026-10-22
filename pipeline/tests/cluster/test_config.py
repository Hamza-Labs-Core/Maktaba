"""Node config: per-OS paths, the override, atomic private writes."""

from __future__ import annotations

import codecs
import json
import os
import platform
import stat
import time
from collections.abc import Callable
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import config
from maktaba_pipeline.cluster.config import HomeRef, NodeConfig

pytestmark = pytest.mark.unit

HOME = Path("/home/u")


def test_paths_per_os() -> None:
    assert config.config_path(system="Darwin", env={}, home=HOME) == (
        HOME / "Library" / "Application Support" / "Maktaba" / "node.json"
    )
    assert config.config_path(system="Linux", env={}, home=HOME) == (
        HOME / ".config" / "maktaba" / "node.json"
    )
    assert config.config_path(system="Linux", env={"XDG_CONFIG_HOME": "/x/cfg"}, home=HOME) == (
        Path("/x/cfg") / "maktaba" / "node.json"
    )
    assert (
        config.config_path(
            system="Windows", env={"LOCALAPPDATA": r"C:\Users\u\AppData\Local"}, home=HOME
        )
        == Path(r"C:\Users\u\AppData\Local") / "Maktaba" / "node.json"
    )
    # no LOCALAPPDATA: the usual place under the profile
    assert config.config_path(system="Windows", env={}, home=HOME) == (
        HOME / "AppData" / "Local" / "Maktaba" / "node.json"
    )


def test_windows_keeps_a_node_config_from_the_roaming_profile(tmp_path: Path) -> None:
    """Epic 38: a new join writes %LOCALAPPDATA%; a machine that joined before keeps its
    %APPDATA% file, one node config per machine, even with a stray file in the new
    place (maktaba-server never copies it, story 38.1)."""
    env = {"LOCALAPPDATA": str(tmp_path / "Local"), "APPDATA": str(tmp_path / "Roaming")}
    cur = tmp_path / "Local" / "Maktaba" / "node.json"
    old = tmp_path / "Roaming" / "Maktaba" / "node.json"
    assert config.config_path(system="Windows", env=env, home=HOME) == cur
    old.parent.mkdir(parents=True)
    old.write_text("{}", encoding="utf-8")
    assert config.config_path(system="Windows", env=env, home=HOME) == old
    cur.parent.mkdir(parents=True)
    cur.write_text("{}", encoding="utf-8")
    assert config.config_path(system="Windows", env=env, home=HOME) == old


def test_windows_rejoin_and_hand_edit_land_in_the_one_node_config(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A re-join (save) and a hand edit of a machine that joined before Epic 38 both
    land in its %APPDATA% node.json, the file maktaba-server and streaming read too."""
    monkeypatch.delenv(config.ENV_VAR, raising=False)
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "Local"))
    monkeypatch.setenv("APPDATA", str(tmp_path / "Roaming"))
    monkeypatch.setattr(platform, "system", lambda: "Windows")
    old = tmp_path / "Roaming" / "Maktaba" / "node.json"
    first = NodeConfig(node_id="first", name="pc", private_key="k1", database_url="postgres://a")
    config.save(first, old)
    rejoined = NodeConfig(
        node_id="rejoined", name="pc", private_key="k2", database_url="postgres://b"
    )
    assert config.save(rejoined) == old  # where it resolved: the one it read
    loaded = config.load()
    assert loaded is not None and loaded.node_id == "rejoined"
    data = json.loads(old.read_text(encoding="utf-8"))
    data["node_id"] = "edited"
    old.write_text(json.dumps(data), encoding="utf-8")
    loaded = config.load()
    assert loaded is not None and loaded.node_id == "edited"
    assert not (tmp_path / "Local" / "Maktaba" / "node.json").exists()


def _windows_profile(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, this_pc: str) -> Path:
    """A Windows profile under tmp_path, this PC's id faked; the roaming node config."""
    monkeypatch.delenv(config.ENV_VAR, raising=False)
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "Local"))
    monkeypatch.setenv("APPDATA", str(tmp_path / "Roaming"))
    monkeypatch.setenv(config.MACHINE_ID_ENV, this_pc)
    monkeypatch.setattr(platform, "system", lambda: "Windows")
    roaming = tmp_path / "Roaming" / "Maktaba" / "node.json"
    roaming.parent.mkdir(parents=True)
    return roaming


_JOINED = {"node_id": "n1", "name": "pc", "private_key": "k1", "database_url": "postgres://a"}


def test_windows_an_unclaimed_roaming_config_is_claimed(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The first new Maktaba that loads a node config from before Epic 38 claims it for
    its PC: machine_id written in, every other key kept, and no second file."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps({**_JOINED, "extra": 1}), encoding="utf-8")
    cfg = config.load()
    assert cfg is not None and cfg.node_id == "n1" and cfg.machine_id == "pc-a"
    data = json.loads(roaming.read_text(encoding="utf-8"))
    assert data == {**_JOINED, "extra": 1, "machine_id": "pc-a"}
    assert config.config_path() == roaming
    assert not (tmp_path / "Local" / "Maktaba" / "node.json").exists()


def test_windows_this_pcs_roaming_config_wins(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps({**_JOINED, "machine_id": "PC-A"}), encoding="utf-8")
    assert config.config_path() == roaming
    cfg = config.load()
    assert cfg is not None and cfg.joined


def test_windows_another_pcs_roaming_config_is_skipped(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A roaming node config another PC claimed is not this PC's: it is not joined, its
    own join writes %LOCALAPPDATA%, and the other PC's identity stays as it was."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    theirs = json.dumps({**_JOINED, "machine_id": "pc-b"})
    roaming.write_text(theirs, encoding="utf-8")
    local = tmp_path / "Local" / "Maktaba" / "node.json"
    assert config.config_path() == local
    assert config.load() is None
    mine = NodeConfig(node_id="n2", name="pc", private_key="k2", machine_id="pc-a")
    assert config.save(mine) == local  # a join on this PC
    loaded = config.load()
    assert loaded is not None and loaded.node_id == "n2"
    assert roaming.read_text(encoding="utf-8") == theirs


def _icacls(path: Path) -> list[str]:
    """The ACEs icacls lists for path, one per line."""
    import subprocess

    out = subprocess.run(["icacls", str(path)], check=True, capture_output=True, text=True).stdout
    aces: list[str] = []
    for line in out.splitlines():
        if line.startswith("Successfully processed"):
            break
        line = line.replace(str(path), "", 1).strip()
        if line:
            aces.append(line)
    return aces


class _Warnings:
    """A logger that keeps what it was told to warn (the warnings that a node.json was
    made its user's alone only when ``private``: the tests' files have the folder's ACL)."""

    def __init__(self, *, private: bool = False) -> None:
        self.events: list[str] = []
        self.fields: list[dict[str, object]] = []
        self.private = private

    def warning(self, event: str, **kw: object) -> None:
        if not self.private and event in ("node_config_made_private", "node_config_not_private"):
            return
        self.events.append(event)
        self.fields.append(kw)


def _claim_seam(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, replace_file: Any
) -> tuple[Path, _Warnings]:
    """A roaming node config to claim, ReplaceFileW replaced, warnings caught."""
    from maktaba_pipeline import log

    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    monkeypatch.setattr(config, "_replace_file", replace_file)
    monkeypatch.setattr(config, "_claim_failed", set())
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    return roaming, warnings


def _windows_error(code: int) -> OSError:
    return OSError(0, f"ReplaceFileW failed ({code})", None, code)


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_claim_survives_replacement_not_moved(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176) with the old file gone: the new copy is the
    only one, so it takes the name, limited to the user first; nothing is left over."""

    def gone(path: Path, tmp: Path, backup: Path) -> None:
        path.unlink()
        raise _windows_error(1176)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, gone)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert json.loads(roaming.read_text(encoding="utf-8")) == {**_JOINED, "machine_id": "pc-a"}
    aces = _icacls(roaming)
    user = os.environ.get("USERNAME", "")
    assert len(aces) == 1 and user and user.lower() in aces[0].lower(), (
        aces,
        user,
        warnings.events,
    )
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == []


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_claim_failing_with_both_files_kept(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """1176 with a backup name (both files keep their names): the old file stays exactly
    as it was, the node runs on unclaimed, and it is said once."""

    def kept(path: Path, tmp: Path, backup: Path) -> None:
        raise _windows_error(1176)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, kept)
    before = roaming.read_bytes()
    for _ in range(2):
        cfg = config.load()
        assert cfg is not None and cfg.joined and cfg.machine_id == ""
    assert roaming.read_bytes() == before
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_claim_failing_with_the_old_file_moved_aside(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177): the old file is under the backup name;
    it is put back, the same file (the user's alone, as every node.json after a load),
    and nothing is left over."""

    def aside(path: Path, tmp: Path, backup: Path) -> None:
        os.replace(path, backup)
        raise _windows_error(1177)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, aside)
    before = roaming.read_bytes()
    cfg = config.load()
    assert cfg is not None and cfg.joined
    aces = _icacls(roaming)
    user = os.environ.get("USERNAME", "")
    assert roaming.read_bytes() == before and len(aces) == 1 and user.lower() in aces[0].lower()
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]


def test_windows_claim_failing_before_the_replace(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A claim whose new copy cannot even be written leaves the file byte-identical and
    no temp file, and the node runs on."""

    def unflushable(fd: int) -> None:
        raise OSError("the disk is full")

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, None)
    before = roaming.read_bytes()
    monkeypatch.setattr(os, "fsync", unflushable)
    cfg = config.load()
    assert cfg is not None and cfg.joined
    assert roaming.read_bytes() == before
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_a_replace_another_process_holds_up_is_tried_again(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A ReplaceFileW that fails with a sharing violation (a reader has node.json open:
    nothing moved) is tried again for a moment: the claim is written, nothing is left
    over, and nothing is said."""
    monkeypatch.setattr(config, "_BUSY_SLEEP", 0.0005)
    real = config._replace_file
    calls: list[int] = []

    def busy_twice(path: Path, tmp: Path, backup: Path) -> None:
        calls.append(1)
        if len(calls) <= 2:
            raise OSError(0, "the file is in use", None, 32)
        real(path, tmp, backup)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, busy_twice)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a" and len(calls) == 3
    assert json.loads(roaming.read_text(encoding="utf-8")) == _NEW
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == []


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_a_claim_nothing_could_be_put_back_from_is_recovered_at_the_next_start(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """1177, and neither the old file nor the new one can be put back: node.json is under
    its backup name and the claimed copy a temp file. The next start, through the default
    path, still finds the roaming config and runs joined; one after the minute puts
    node.json back and claims it, and no %LOCALAPPDATA% config ever appears."""
    replace, rename, replace_file = os.replace, os.rename, config._replace_file

    def aside(path: Path, tmp: Path, backup: Path) -> None:
        replace(path, backup)
        raise _windows_error(1177)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, aside)

    def stuck(real: Any) -> Any:
        def move(src: Any, dst: Any) -> None:
            if Path(dst) == roaming:
                raise PermissionError(13, "in use", str(dst))
            real(src, dst)

        return move

    monkeypatch.setattr(os, "replace", stuck(replace))  # the new file taking the name
    monkeypatch.setattr(os, "rename", stuck(rename))  # the old one put back
    cfg = config.load()
    assert cfg is not None and cfg.joined and not roaming.exists()
    assert sorted(p.suffix for p in roaming.parent.iterdir()) == [".bak", ".tmp"]
    assert warnings.events == ["node_config_claim_failed"]

    # the next start: nothing in the way any more
    monkeypatch.setattr(os, "replace", replace)
    monkeypatch.setattr(os, "rename", rename)
    monkeypatch.setattr(config, "_replace_file", replace_file)
    monkeypatch.setattr(config, "_claim_failed", set())
    monkeypatch.setattr(config, "_leftovers_said", set())
    assert config.config_path() == roaming
    cfg = config.load()
    assert cfg is not None and cfg.joined and cfg.node_id == "n1"
    later = time.time() + 2 * config._LEFTOVER_GRACE_SEC
    monkeypatch.setattr(config, "_now", lambda: later)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert json.loads(roaming.read_text(encoding="utf-8")) == _NEW
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert not (tmp_path / "Local" / "Maktaba").exists()


@pytest.mark.skipif(os.name != "nt", reason="the DACL a temp file is created with")
def test_windows_a_temp_file_that_cannot_be_the_users_is_never_written(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """When the temp file cannot be created as the user's alone, the secret is never
    written: a claim fails as any failed claim (the file as it was, said once, the node
    runs on) and a save raises, leaving nothing."""

    def no_token() -> str:
        raise OSError("OpenProcessToken failed")

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, None)
    monkeypatch.setattr(config, "_user_only_sddl", no_token)
    before = roaming.read_bytes()
    for _ in range(2):
        cfg = config.load()
        assert cfg is not None and cfg.joined and cfg.machine_id == ""
    assert roaming.read_bytes() == before
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]
    elsewhere = tmp_path / "new" / "node.json"
    with pytest.raises(OSError, match="OpenProcessToken"):
        config.save(NodeConfig(node_id="n2", name="pc", private_key="k2"), elsewhere)
    assert list(elsewhere.parent.iterdir()) == []


def _leftover(
    folder: Path, name: str, body: str, *, age: float = 120.0, restrict: bool = True
) -> Path:
    """A file a write that could not finish left behind, its mtime ``age`` seconds ago."""
    p = folder / name
    p.write_text(body, encoding="utf-8")
    if restrict:
        config._restrict_to_user(p)
    then = time.time() - age
    os.utime(p, (then, then))
    return p


def _named(ext: str, *, age: float) -> str:
    """node.json's backup or temp file name of a write that started ``age`` seconds ago."""
    return f".node.json-{int(time.time() - age)}-4242-0badf00d{ext}"


def _short_waits(monkeypatch: pytest.MonkeyPatch) -> None:
    """A test that lets the readers' wait for a write under way run out: 2 ms, looking
    again every 0.5 ms (2 s and 5 ms in production)."""
    monkeypatch.setattr(config, "_READ_WAIT_SEC", 0.002)
    monkeypatch.setattr(config, "_BUSY_SLEEP", 0.0005)


@pytest.fixture(scope="module", autouse=True)
def _first_load_paid_before_the_tests(tmp_path_factory: pytest.TempPathFactory) -> None:
    """The first load and write in a process pay one-time costs (the logger's first
    warning; on Windows the first security and ReplaceFileW calls: 40 ms here, more on
    CI): paid here, at the module's setup, never inside whichever test runs first (the
    unit cap times each test's call). A temp folder; the fake machine id."""
    folder = tmp_path_factory.mktemp("warm") / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    if os.name != "nt":
        os.chmod(path, 0o644)  # made the user's alone at the load, and said
    _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED))  # recovered, and said
    config.load(path)
    config.save(NodeConfig(node_id="n1", name="pc", private_key="k1"), path)


_NEW = {**_JOINED, "machine_id": "pc-a"}


@pytest.mark.parametrize(
    ("there", "bak", "tmp", "want"),
    [
        # node.json gone: the old file (the backup) first, as _settle decides
        (None, _JOINED, _NEW, _JOINED),
        (None, None, _NEW, _NEW),  # only the new one: it takes the name
        (None, _JOINED, None, _JOINED),
        ("{not json", _JOINED, None, _JOINED),  # node.json broken: the backup
        (None, _JOINED, "{cut", _JOINED),  # a cut temp file is never used, and goes
        (_JOINED, _JOINED, _NEW, _JOINED),  # node.json whole: stale leftovers go
    ],
)
def test_leftovers_are_recovered_at_the_next_load(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    there: dict[str, str] | str | None,
    bak: dict[str, str] | None,
    tmp: dict[str, str] | str | None,
    want: dict[str, str],
) -> None:
    """A write whose ReplaceFileW and settling both failed left node.json's old file
    under its backup name and/or the new one as a temp file: the next load puts the
    right one in place, removes the rest once node.json is whole and names them in its
    warning (the ACL: test_config_acl)."""
    from maktaba_pipeline import log

    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    if there is not None:
        _leftover(
            folder, "node.json", there if isinstance(there, str) else json.dumps(there), age=0
        )
    names: list[str] = []
    if bak is not None:
        names.append(_leftover(folder, _named(".bak", age=120), json.dumps(bak), age=3600).name)
    if tmp is not None:  # named as before they carried the file's name: aged by its mtime
        names.append(
            _leftover(
                folder, ".node-x1y2z3w4.tmp", tmp if isinstance(tmp, str) else json.dumps(tmp)
            ).name
        )
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    monkeypatch.setattr(config, "_leftovers_said", set())

    cfg = config.load(path)
    assert cfg is not None and json.loads(path.read_text(encoding="utf-8")) == want
    kept = sorted(folder.glob("node.json.broken-*"))
    assert sorted(p.name for p in folder.iterdir() if p not in kept) == ["node.json"]
    said = warnings.fields[warnings.events.index("node_config_leftovers")]
    removed = said["removed"]
    assert isinstance(removed, list)
    assert {str(said["restored"]), *map(str, removed)} - {""} == set(names), said
    if isinstance(there, str):  # a broken node.json is kept aside, never removed
        assert [p.read_text(encoding="utf-8") for p in kept] == [there]
        assert said["broken_kept"] == kept[0].name
    else:
        assert kept == [] and said["broken_kept"] == ""
    assert config.load(path) == cfg  # a second load finds nothing left to do


def test_leftovers_that_cannot_be_used_stay_and_are_named(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """node.json gone and only a cut temp file left: nothing is put in place or removed
    (it may be all there is), the load finds no node config, and the warning names it."""
    from maktaba_pipeline import log

    folder = tmp_path / "Maktaba"
    folder.mkdir()
    cut = _leftover(folder, ".node-x1y2z3w4.tmp", "{cut")
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    monkeypatch.setattr(config, "_leftovers_said", set())
    assert config.load(folder / "node.json") is None
    assert config.load(folder / "node.json") is None
    assert cut.exists()
    assert warnings.events == ["node_config_leftovers"]  # said once
    assert warnings.fields[0]["left"] == [cut.name]


def test_another_node_configs_leftovers_are_not_taken(tmp_path: Path) -> None:
    """Two node configs in one folder (MAKTABA_NODE_CONFIG): one's leftovers, named after
    it, never become the other."""
    folder = tmp_path / "nodes"
    folder.mkdir()
    theirs = _leftover(folder, ".node-b.json-123-abcd1234.bak", json.dumps(_JOINED))
    for mine in ("node-a.json", "node.json"):  # node.json also reads the older names
        assert config.load(folder / mine) is None
        assert theirs.exists() and not (folder / mine).exists()


def test_a_write_in_progress_is_left_alone(tmp_path: Path) -> None:
    """A temp file of a few seconds is another process's write in progress: a load that
    finds node.json whole leaves it."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    config.save(NodeConfig(node_id="n1", name="pc", private_key="k1"), folder / "node.json")
    fresh = _leftover(folder, ".node.json-fresh.tmp", json.dumps(_NEW), age=1.0)
    assert config.load(folder / "node.json") is not None
    assert fresh.exists()


def test_a_backup_a_write_is_making_is_read_but_never_moved(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Inside a ReplaceFileW node.json is under the backup's name for a moment, and the
    backup keeps the old file's mtime: a load then reads it (the node is joined) but
    neither moves nor removes it, and says nothing. It is that write's for a minute from
    the time in its name."""
    from maktaba_pipeline import log

    folder = tmp_path / "Maktaba"
    folder.mkdir()
    bak = _leftover(folder, _named(".bak", age=1), json.dumps(_JOINED), age=3600)
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    cfg = config.load(folder / "node.json")
    assert cfg is not None and cfg.joined and cfg.node_id == "n1"
    assert sorted(p.name for p in folder.iterdir()) == [bak.name]
    assert warnings.events == []


def test_a_node_json_that_cannot_be_read_is_never_moved_or_read_past(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """node.json that another program holds (or this user may not read) is neither gone
    nor broken: a load leaves it and every leftover where they are and says why it cannot
    read it, never running on an older backup instead."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    older = _leftover(folder, _named(".bak", age=120), json.dumps({**_JOINED, "node_id": "n0"}))
    read_bytes = Path.read_bytes

    def held(self: Path) -> bytes:
        if self == path:
            raise PermissionError(13, "in use by another process", str(self))
        return read_bytes(self)

    monkeypatch.setattr(Path, "read_bytes", held)
    # on Windows EACCES may be a sharing violation, waited out for a moment
    _short_waits(monkeypatch)
    with pytest.raises(PermissionError):
        config.load(path)
    monkeypatch.setattr(Path, "read_bytes", read_bytes)
    assert json.loads(path.read_text(encoding="utf-8")) == _JOINED
    assert sorted(p.name for p in folder.iterdir()) == sorted(["node.json", older.name])


@pytest.mark.parametrize("broken", [False, True])
def test_a_load_never_puts_a_backup_over_a_file_a_write_put_there(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, broken: bool
) -> None:
    """A write that lands its node.json while a load is putting an old backup back keeps
    its file: the backup only takes the name without replacing, and a broken node.json
    moved aside first is moved back when it turns out to be the write's whole file."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    if broken:
        path.write_text("{cut", encoding="utf-8")
    _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED), age=3600)
    landed = {**_JOINED, "node_id": "n2"}

    def land() -> None:
        path.write_text(json.dumps(landed), encoding="utf-8")

    if broken:
        name = config._broken_name

        def land_then_name(p: Path) -> Path:
            land()  # after the last look at it, just before the broken file is moved aside
            return name(p)

        monkeypatch.setattr(config, "_broken_name", land_then_name)
    else:
        rename = config._rename_no_replace

        def land_then_rename(src: Path, dst: Path) -> None:
            if dst == path and not path.exists():
                land()  # just before the backup takes the name
            rename(src, dst)

        monkeypatch.setattr(config, "_rename_no_replace", land_then_rename)
    cfg = config.load(path)
    assert cfg is not None and cfg.node_id == "n2"
    assert json.loads(path.read_text(encoding="utf-8")) == landed
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]  # the old backup goes


def test_a_whole_node_json_another_load_put_back_is_never_moved_aside(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Several loads start at once after a failed write: each found node.json gone, and
    one puts the backup back first. The others see the whole file there when they come to
    it and leave it, so readers never find node.json gone again and fall to a leftover
    (nor a third load put an older one back into that gap)."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED), age=3600)
    candidate = config._candidate

    def another_load_first(left: list[Path], **kw: Any) -> Path | None:
        chosen = candidate(left, **kw)
        path.write_text(json.dumps(_JOINED), encoding="utf-8")  # the other load's restore
        return chosen

    moved: list[Path] = []
    rename, link = os.rename, os.link

    def watch_rename(src: Any, dst: Any) -> None:
        moved.append(Path(src))
        rename(src, dst)

    def watch_link(src: Any, dst: Any) -> None:
        moved.append(Path(src))
        link(src, dst)

    monkeypatch.setattr(config, "_candidate", another_load_first)
    monkeypatch.setattr(os, "rename", watch_rename)
    monkeypatch.setattr(os, "link", watch_link)
    cfg = config.load(path)
    assert cfg is not None and cfg.node_id == "n1"
    assert path not in moved  # node.json was never moved aside
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]


def test_a_write_that_failed_never_puts_its_backup_over_another_writes_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """B's ReplaceFileW failed half way (node.json under B's backup name); A's found
    node.json gone and put its new file there and succeeded. B putting its old file back
    must not undo A's write: it never replaces, and B's write fails instead."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    backup = _leftover(folder, _named(".bak", age=0), json.dumps(_JOINED))  # node.json, aside
    tmp = _leftover(folder, _named(".tmp", age=0), json.dumps({**_JOINED, "name": "B"}))
    a_wrote = {**_JOINED, "name": "A"}
    exists = Path.exists
    landed: list[bool] = []

    def a_lands_after_b_looks(self: Path, *args: Any, **kw: Any) -> bool:
        there = exists(self, *args, **kw)
        if self == path and not landed:
            landed.append(True)
            path.write_text(json.dumps(a_wrote), encoding="utf-8")  # A's settle, just after
        return there

    monkeypatch.setattr(Path, "exists", a_lands_after_b_looks)
    assert config._settle(path, tmp, backup) is False  # B's write did not take the name
    monkeypatch.setattr(Path, "exists", exists)
    assert json.loads(path.read_text(encoding="utf-8")) == a_wrote
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]


@pytest.mark.skipif(os.name != "nt", reason="the lock beside a node config is Windows'")
def test_windows_writes_of_one_node_config_run_one_at_a_time(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Two ReplaceFileW calls on one file at once can undo each other, so writes (and
    recoveries) of a node config hold its lock: while another process holds it, a claim
    fails as any failed claim, a save fails after waiting, and a recovery leaves the
    leftovers to a later load. The lock file goes once nobody holds it."""
    roaming, warnings = _claim_seam(tmp_path, monkeypatch, config._replace_file)
    before = roaming.read_bytes()
    monkeypatch.setattr(config, "_LOCK_WAIT_SEC", 0.002)
    monkeypatch.setattr(config, "_RECOVER_WAIT_SEC", 0.0)
    stale = _leftover(roaming.parent, _named(".bak", age=120), json.dumps(_JOINED), age=3600)
    lock = roaming.with_name(".node.json.lock")
    with config._one_at_a_time(roaming, wait=0.0) as held:  # another process's write
        assert held and lock.exists()
        cfg = config.load()
        assert cfg is not None and cfg.joined and cfg.machine_id == ""
        with pytest.raises(TimeoutError):
            config.save(NodeConfig(node_id="n2", name="pc", private_key="k2"), roaming)
        assert roaming.read_bytes() == before and stale.exists()
    assert warnings.events == ["node_config_claim_failed"]
    assert not lock.exists()
    monkeypatch.setattr(config, "_claim_failed", set())
    cfg = config.load()  # the lock is free: the stale backup goes, and the claim is written
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]


class _NoLock:
    """``_one_at_a_time`` where the lock file cannot be made: a folder this user may not
    write, another user's lock file, a full disk."""

    def __init__(self, path: Path, *, wait: float) -> None:
        self.path = path

    def __enter__(self) -> bool:
        raise PermissionError(13, "Permission denied", str(self.path.with_name(".node.json.lock")))

    def __exit__(self, *exc: object) -> None:
        return None


@pytest.mark.parametrize("how", ["no lock file can be made", "a read-only lock file"])
def test_a_load_that_cannot_take_the_lock_reads_past_the_leftovers(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, how: str
) -> None:
    """A load whose recovery cannot take the lock moves nothing, still reads the node
    config from what a failed write left (the node stays joined) and says why once."""
    monkeypatch.setattr(config, "_BUSY_SLEEP", 0.0005)
    if how == "a read-only lock file" and os.name != "nt":
        pytest.skip("the lock file is Windows'")
    from maktaba_pipeline import log

    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    bak = _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED), age=3600)
    lock = folder / ".node.json.lock"
    if how == "a read-only lock file":
        lock.write_text("", encoding="utf-8")
        os.chmod(lock, stat.S_IREAD)
        monkeypatch.setattr(config, "_BUSY_TRIES", 2)  # its open is denied for good
    else:
        monkeypatch.setattr(config, "_one_at_a_time", _NoLock)
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    monkeypatch.setattr(config, "_leftovers_said", set())
    try:
        for _ in range(2):
            cfg = config.load(path)
            assert cfg is not None and cfg.joined and cfg.node_id == "n1"
        assert bak.exists() and not path.exists()
        assert warnings.events == ["node_config_leftovers"]  # said once
        assert warnings.fields[0]["left"] == [bak.name] and warnings.fields[0]["error"]
    finally:
        if lock.exists():
            os.chmod(lock, stat.S_IREAD | stat.S_IWRITE)


#: what an editor saves a node.json as (Notepad and PowerShell 5.1's Set-Content -Encoding
#: UTF8: UTF-8 with a BOM; its Out-File and >: UTF-16 little-endian with one)
_SAVED_AS: dict[str, Callable[[str], bytes]] = {
    "UTF-8 with a BOM": lambda text: codecs.BOM_UTF8 + text.encode("utf-8"),
    "UTF-16 LE": lambda text: codecs.BOM_UTF16_LE + text.encode("utf-16-le"),
    "UTF-16 BE": lambda text: codecs.BOM_UTF16_BE + text.encode("utf-16-be"),
    "UTF-32": lambda text: text.encode("utf-32"),
}


@pytest.mark.parametrize("saved_as", list(_SAVED_AS))
def test_a_node_json_an_editor_saved_is_whole(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, saved_as: str
) -> None:
    """A node.json an editor saved with a byte order mark is read in that encoding, as
    maktaba-server, streaming and the installer read it: it is the node config (never
    taken for broken and replaced from a backup), and its owner is read too."""
    encode = _SAVED_AS[saved_as]
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    mine = {**_JOINED, "node_id": "n2", "name": "Ḥamza"}
    path.write_bytes(encode(json.dumps(mine, ensure_ascii=False)))
    before = path.read_bytes()
    _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED), age=3600)
    cfg = config.load(path)
    assert cfg is not None and cfg.node_id == "n2" and cfg.name == "Ḥamza" and cfg.joined
    assert path.read_bytes() == before
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]  # the stale backup goes

    roaming = _windows_profile(tmp_path / "pc", monkeypatch, "pc-a")
    roaming.write_bytes(encode(json.dumps({**_JOINED, "machine_id": "pc-b"})))
    assert config.config_path() == tmp_path / "pc" / "Local" / "Maktaba" / "node.json"


def test_a_node_json_that_does_not_decode_is_broken(tmp_path: Path) -> None:
    """A cut UTF-16 file (an odd byte) is broken for every reader: the load says what is
    wrong, as for any broken file."""
    path = tmp_path / "node.json"
    path.write_bytes(codecs.BOM_UTF16_LE + json.dumps(_JOINED).encode("utf-16-le")[:-1])
    with pytest.raises(ValueError, match="not a node config"):
        config.load(path)


def test_broken_node_configs_kept_aside_go_after_a_while(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A broken node.json kept aside may hold part of a credential: a load removes those
    older than the kept days (housekeeping, by the time in their names), and leaves the
    newer ones and anything else."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    config.save(NodeConfig(node_id="n1", name="pc", private_key="k1"), folder / "node.json")
    days = config._BROKEN_KEEP_DAYS
    old = folder / f"node.json.broken-{int(time.time() - (days + 1) * 86400)}-0bad"
    new = folder / f"node.json.broken-{int(time.time() - (days - 1) * 86400)}-0bad"
    other = folder / "node.json.broken-by-hand"
    for p in (old, new, other):
        p.write_text("{cut", encoding="utf-8")
    assert config.load(folder / "node.json") is not None
    assert sorted(p.name for p in folder.iterdir()) == sorted(["node.json", new.name, other.name])


def test_windows_a_roaming_config_a_failed_claim_left_is_still_this_pcs(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A claim of the roaming node.json whose ReplaceFileW failed with 1177 and whose
    putting back failed too leaves node.json under the backup's name and the claimed copy
    as a temp file. Through the default path (no path given): within the minute the node
    runs joined on the backup, nothing is moved and no %LOCALAPPDATA% config appears;
    after it, the next load puts node.json back and claims it, and nothing else is left."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    monkeypatch.setattr(config, "_claim_failed", set())
    monkeypatch.setattr(config, "_leftovers_said", set())
    bak = _leftover(roaming.parent, _named(".bak", age=5), json.dumps(_JOINED), age=3600)
    tmp = _leftover(roaming.parent, _named(".tmp", age=5), json.dumps(_NEW), age=5)
    assert config.config_path() == roaming
    cfg = config.load()
    assert cfg is not None and cfg.joined and cfg.node_id == "n1"
    assert sorted(p.name for p in roaming.parent.iterdir()) == sorted([bak.name, tmp.name])
    later = time.time() + 2 * config._LEFTOVER_GRACE_SEC
    monkeypatch.setattr(config, "_now", lambda: later)
    cfg = config.load()
    assert cfg is not None and cfg.joined and cfg.machine_id == "pc-a"
    assert json.loads(roaming.read_text(encoding="utf-8")) == _NEW
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert not (tmp_path / "Local" / "Maktaba").exists()


def test_windows_another_pcs_roaming_leftovers_are_skipped(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """What another PC's failed write left of its roaming config is that PC's: this PC
    resolves its own place and never touches them."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    theirs = _leftover(
        roaming.parent, _named(".bak", age=120), json.dumps({**_JOINED, "machine_id": "pc-b"})
    )
    assert config.config_path() == tmp_path / "Local" / "Maktaba" / "node.json"
    assert config.load() is None
    assert sorted(p.name for p in roaming.parent.iterdir()) == [theirs.name]


def test_the_override_wins_on_every_os() -> None:
    env = {config.ENV_VAR: "/elsewhere/n.json", "APPDATA": "C:/a", "XDG_CONFIG_HOME": "/x"}
    for system in ("Darwin", "Linux", "Windows"):
        assert config.config_path(system=system, env=env, home=HOME) == Path("/elsewhere/n.json")


def test_default_path_reads_the_environment(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv(config.ENV_VAR, str(tmp_path / "n.json"))
    assert config.config_path() == tmp_path / "n.json"


def test_save_and_load_round_trip(tmp_path: Path) -> None:
    path = tmp_path / "sub" / "node.json"
    assert config.load(path) is None
    cfg = NodeConfig(
        node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
        name="linux-box",
        private_key="AQID",
        home=HomeRef(url="http://192.168.1.20:8080", instance_id="0b9a", name="home-mac"),
        database_url="postgres://u:p@h/db",
        joined_at="2026-10-18T00:00:00Z",
    )
    config.save(cfg, path)
    assert config.load(path) == cfg
    assert cfg.node8 == "6f1c2a3b" and cfg.joined
    if os.name != "nt":
        assert stat.S_IMODE(path.stat().st_mode) == 0o600
    # a second save replaces the file and leaves no temp files behind
    config.save(NodeConfig(node_id=cfg.node_id, name="n2", private_key="AQID"), path)
    loaded = config.load(path)
    assert loaded is not None and loaded.name == "n2" and not loaded.joined
    assert sorted(p.name for p in path.parent.iterdir()) == ["node.json"]


def test_a_garbage_file_is_an_error(tmp_path: Path) -> None:
    path = tmp_path / "node.json"
    path.write_text("[1]", encoding="utf-8")
    with pytest.raises(ValueError):
        config.load(path)


def test_a_file_that_does_not_parse_names_itself(tmp_path: Path) -> None:
    """A typo or a truncated file is never "not joined"; the error says which file."""
    path = tmp_path / "node.json"
    path.write_text('{"node_id": "6f1c2a3b", "database_url": "postgres://n:pw@h', encoding="utf-8")
    with pytest.raises(ValueError, match=r"node\.json: not a node config \(") as err:
        config.load(path)
    assert "pw@h" not in str(err.value)


def test_a_config_without_its_identity_is_an_error(tmp_path: Path) -> None:
    """A KeyError from a half-written file read as a crash; it names the file instead."""
    path = tmp_path / "node.json"
    path.write_text('{"node_id": "6f1c2a3b", "private_key": " ", "name": "n"}', encoding="utf-8")
    with pytest.raises(ValueError, match=r"node\.json: incomplete node config: no private_key"):
        config.load(path)
    path.write_text('{"database_url": "postgres://u:p@h/db"}', encoding="utf-8")
    with pytest.raises(ValueError, match="no node_id or private_key"):
        config.load(path)


@pytest.mark.skipif(os.name == "nt", reason="file modes")
def test_a_node_json_others_may_read_is_made_the_users_at_load(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Off Windows, a node.json with a wider mode than 0600 (one copied in by hand) is
    0600 after a load, and the warning says so once; one already 0600 is left alone."""
    from maktaba_pipeline import log

    path = tmp_path / "node.json"
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    os.chmod(path, 0o644)
    warnings = _Warnings(private=True)
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    assert config.load(path) is not None
    assert stat.S_IMODE(path.stat().st_mode) == 0o600
    assert config.load(path) is not None
    assert warnings.events == ["node_config_made_private"]


def test_a_rejoin_leaves_nothing_read_as_the_old_node_config(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A re-join sets the node config aside in an order that leaves nothing read as it
    after: a whole leftover that cannot be removed stops it before anything moves; with
    node.json broken and the config in a leftover, the leftover is what is kept and the
    broken file goes too."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    stale = _leftover(folder, _named(".bak", age=120), json.dumps({**_JOINED, "node_id": "n0"}))
    unlink = config._unlink_when_free

    def held(p: Path) -> None:
        if p == stale:
            raise PermissionError(13, "a reader has it open", str(p))
        unlink(p)

    monkeypatch.setattr(config, "_unlink_when_free", held)
    with pytest.raises(OSError, match="cannot be removed"):
        config.set_aside(path)
    assert json.loads(path.read_text(encoding="utf-8")) == _JOINED and stale.exists()
    monkeypatch.setattr(config, "_unlink_when_free", unlink)
    config.set_aside(path)
    assert sorted(p.name for p in folder.iterdir()) == ["node.json.bak"]

    path.write_text('{"node_id": "n1", "priv', encoding="utf-8")
    _leftover(folder, _named(".bak", age=120), json.dumps({**_JOINED, "node_id": "n2"}))
    _leftover(folder, _named(".tmp", age=120), json.dumps({**_JOINED, "node_id": "n3"}))
    kept = config.set_aside(path)
    assert json.loads(kept.read_text(encoding="utf-8"))["node_id"] == "n2"
    assert sorted(p.name for p in folder.iterdir()) == ["node.json.bak"]
    assert config.load(path) is None


def test_the_roaming_config_is_not_lost_to_a_write_under_way(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The resolver never takes the local place for the roaming config while a write of
    it is under way (its lock held): a leftover it picked that is gone a moment later is
    looked for again."""
    monkeypatch.setattr(config, "_BUSY_SLEEP", 0.0005)
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps({**_JOINED, "machine_id": "pc-a"}), encoding="utf-8")
    (roaming.parent / ".node.json.lock").write_text("", encoding="utf-8")
    source = config._source
    calls: list[Path] = []

    def a_moment_gone(path: Path) -> Path:
        calls.append(path)
        if len(calls) == 1:
            return path.with_name(_named(".bak", age=1))  # picked, and gone at once
        return source(path)

    monkeypatch.setattr(config, "_source", a_moment_gone)
    assert config.config_path() == roaming and len(calls) == 2


def test_a_load_waits_out_a_write_under_way(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """node.json missing for a moment while a write holds its lock (ReplaceFileW between
    its renames) is not "not joined": the load waits for it."""
    import threading

    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    lock = folder / ".node.json.lock"
    lock.write_text("", encoding="utf-8")

    def lands() -> None:
        path.write_text(json.dumps(_JOINED), encoding="utf-8")
        lock.unlink()

    monkeypatch.setattr(config, "_BUSY_SLEEP", 0.0005)
    timer = threading.Timer(0.005, lands)
    timer.start()
    try:
        cfg = config.load(path)
    finally:
        timer.join()
    assert cfg is not None and cfg.node_id == "n1"


def test_the_claim_keeps_what_another_write_put_in_meanwhile(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The claim writes machine_id into node.json as it is with the lock held: a field
    another writer (maktaba-server: the home's database CA) put in after this load read
    the file is kept, and a config another PC claimed meanwhile is not claimed again."""
    from maktaba_pipeline import log

    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    read = config._read_steady

    def then_another_write(path: Path) -> tuple[Path, str]:
        got = read(path)
        roaming.write_text(json.dumps({**_JOINED, "database_ca": "PEM"}), encoding="utf-8")
        return got

    monkeypatch.setattr(config, "_read_steady", then_another_write)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert json.loads(roaming.read_text(encoding="utf-8")) == {
        **_JOINED,
        "database_ca": "PEM",
        "machine_id": "pc-a",
    }

    def then_another_pc(path: Path) -> tuple[Path, str]:
        got = read(path)
        roaming.write_text(json.dumps({**_JOINED, "machine_id": "pc-b"}), encoding="utf-8")
        return got

    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    monkeypatch.setattr(config, "_read_steady", then_another_pc)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-b"
    assert json.loads(roaming.read_text(encoding="utf-8"))["machine_id"] == "pc-b"
    assert warnings.events == []


def test_a_write_that_does_not_end_is_an_error_never_not_joined(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A write of the roaming node.json that does not end within the readers' wait (its
    lock taken a moment ago, node.json missing) is never "not joined": the path stays the
    roaming one and the load raises (EBUSY), never None. A lock a process that stopped
    left long ago is no write under way: not joined."""
    import errno

    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    _short_waits(monkeypatch)
    lock = config._lock_path(roaming)
    lock.write_bytes(b"")
    assert config.config_path() == roaming
    with pytest.raises(OSError) as raised:
        config.load(roaming)
    assert not isinstance(raised.value, FileNotFoundError)
    assert raised.value.errno == errno.EBUSY
    then = time.time() - 120
    os.utime(lock, (then, then))
    assert config.load(roaming) is None


@pytest.mark.skipif(os.name != "nt", reason="files held without sharing")
def test_windows_leftovers_held_open_are_never_not_joined(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The review's case: a write's backup and temp file held open without sharing, its
    lock taken a moment ago, node.json missing. The path stays the roaming one and the
    load raises, never None."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    _short_waits(monkeypatch)
    config._lock_path(roaming).write_bytes(b"")
    fds = []
    try:
        for ext in (".bak", ".tmp"):
            fd = config._create_user_only(roaming.with_name(_named(ext, age=0)))
            os.write(fd, json.dumps(_JOINED).encode())
            fds.append(fd)
        assert config.config_path() == roaming
        with pytest.raises(OSError) as raised:
            config.load(roaming)
        assert not isinstance(raised.value, FileNotFoundError)
    finally:
        for fd in fds:
            os.close(fd)


@pytest.mark.skipif(os.name != "nt", reason="a sharing violation")
def test_windows_a_read_held_up_by_another_program_is_waited_out(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """open() reports a sharing violation (an antivirus or a backup tool holding
    node.json) as EACCES without a Windows error code: the load waits it out for a
    moment, with no lock or leftover beside it, rather than raise at once."""
    monkeypatch.setattr(config, "_BUSY_SLEEP", 0.0005)
    import errno

    path = tmp_path / "Maktaba" / "node.json"
    path.parent.mkdir()
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    text = config._text
    held = {"left": 3}

    def busy(p: Path) -> str:
        if held["left"]:
            held["left"] -= 1
            raise PermissionError(errno.EACCES, "Permission denied", str(p))
        return text(p)

    monkeypatch.setattr(config, "_text", busy)
    cfg = config.load(path)
    assert cfg is not None and cfg.node_id == "n1" and held["left"] == 0


@pytest.mark.skipif(os.name != "nt", reason="a reader holding the file open")
def test_windows_a_rejoin_waits_for_a_reader_holding_node_json(tmp_path: Path) -> None:
    """``join --rejoin`` while a reader has node.json open for a moment (a sharing
    violation on the rename, as maktaba-server's renameWhenFree waits out): it waits."""
    import threading

    path = tmp_path / "Maktaba" / "node.json"
    path.parent.mkdir()
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    reader = path.open("rb")
    timer = threading.Timer(0.005, reader.close)
    timer.start()
    try:
        kept = config.set_aside(path)
    finally:
        timer.join()
        reader.close()
    assert kept.name == "node.json.bak" and not path.exists()
    assert json.loads(kept.read_text(encoding="utf-8")) == _JOINED


def test_a_broken_node_json_kept_aside_is_the_users(tmp_path: Path) -> None:
    """A broken node.json a load keeps aside (``.broken-*``, kept for a while: it may hold
    part of a credential) is the user's alone, whatever its folder lets others do."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    path.write_text('{"node_id": "n1", "priv', encoding="utf-8")
    if os.name != "nt":
        os.chmod(path, 0o644)
    assert not config._user_only_now(path)
    _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED))
    cfg = config.load(path)
    assert cfg is not None and cfg.node_id == "n1"
    kept = list(folder.glob("node.json.broken-*"))
    assert len(kept) == 1 and config._user_only_now(kept[0])


def test_securing_at_load_says_nothing_of_a_file_replaced_meanwhile(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The load's check that node.json is its user's alone races a write replacing it:
    a failure for a file that is gone or the user's alone when looked at again says
    nothing; one that stays wider is said once."""
    from maktaba_pipeline import log

    path = tmp_path / "Maktaba" / "node.json"
    path.parent.mkdir()
    path.write_text(json.dumps(_JOINED), encoding="utf-8")
    warnings = _Warnings(private=True)
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    monkeypatch.setattr(config, "_secure_failed", set())

    def replaced(p: Path) -> bool:
        raise FileNotFoundError(2, "replaced meanwhile", str(p))

    monkeypatch.setattr(config, "_secure", replaced)
    monkeypatch.setattr(config, "_user_only_now", lambda p: True)
    config._secure_once(path)
    assert warnings.events == []

    def denied(p: Path) -> bool:
        raise PermissionError(13, "access denied", str(p))

    monkeypatch.setattr(config, "_secure", denied)
    monkeypatch.setattr(config, "_user_only_now", lambda p: False)
    config._secure_once(path)
    config._secure_once(path)
    assert warnings.events == ["node_config_not_private"]


@pytest.mark.parametrize("source", ["node.json", "a leftover"])
def test_a_rejoins_kept_identity_is_the_users(tmp_path: Path, source: str) -> None:
    """The node config ``join --rejoin`` keeps (``node.json.bak``: the old private key)
    is the user's alone, whatever ACL the file it was had: a node.json, or a leftover of
    a write, saved with its folder's ACL (mode 0644 elsewhere)."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    src = path if source == "node.json" else folder / _named(".bak", age=120)
    src.write_text(json.dumps(_JOINED), encoding="utf-8")
    if os.name != "nt":
        os.chmod(src, 0o644)
    assert not config._user_only_now(src)
    kept = config.set_aside(path)
    assert json.loads(kept.read_text(encoding="utf-8")) == _JOINED
    assert config._user_only_now(kept)
