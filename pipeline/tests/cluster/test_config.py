"""Node config: per-OS paths, the override, atomic private writes."""

from __future__ import annotations

import json
import os
import platform
import stat
import time
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
    """A logger that keeps what it was told to warn."""

    def __init__(self) -> None:
        self.events: list[str] = []
        self.fields: list[dict[str, object]] = []

    def warning(self, event: str, **kw: object) -> None:
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
    it is put back, the same file with the same ACL, and nothing is left over."""

    def aside(path: Path, tmp: Path, backup: Path) -> None:
        os.replace(path, backup)
        raise _windows_error(1177)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, aside)
    before, acl = roaming.read_bytes(), _icacls(roaming)
    cfg = config.load()
    assert cfg is not None and cfg.joined
    assert roaming.read_bytes() == before and _icacls(roaming) == acl
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
    read_text = Path.read_text

    def held(self: Path, *args: Any, **kw: Any) -> str:
        if self == path:
            raise PermissionError(13, "in use by another process", str(self))
        return read_text(self, *args, **kw)

    monkeypatch.setattr(Path, "read_text", held)
    with pytest.raises(PermissionError):
        config.load(path)
    monkeypatch.setattr(Path, "read_text", read_text)
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
    monkeypatch.setattr(config, "_LOCK_WAIT_SEC", 0.01)
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


def test_a_node_json_saved_with_a_bom_is_whole(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A node.json an editor saved with a UTF-8 BOM (Notepad, PowerShell 5.1) is read past
    the BOM, as maktaba-server, streaming and the installer read it: it is the node config
    (never taken for broken and replaced from a backup), and its owner is read too."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    path = folder / "node.json"
    mine = {**_JOINED, "node_id": "n2"}
    path.write_bytes(b"\xef\xbb\xbf" + json.dumps(mine).encode())
    _leftover(folder, _named(".bak", age=120), json.dumps(_JOINED), age=3600)
    cfg = config.load(path)
    assert cfg is not None and cfg.node_id == "n2" and cfg.joined
    assert json.loads(path.read_bytes().decode("utf-8-sig")) == mine
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]  # the stale backup goes

    roaming = _windows_profile(tmp_path / "pc", monkeypatch, "pc-a")
    roaming.write_bytes(b"\xef\xbb\xbf" + json.dumps({**_JOINED, "machine_id": "pc-b"}).encode())
    assert config.config_path() == tmp_path / "pc" / "Local" / "Maktaba" / "node.json"


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
