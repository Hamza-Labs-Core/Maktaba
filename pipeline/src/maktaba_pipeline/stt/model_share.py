"""A gated model's files, handed from the home to the workers through the share.

The second listen's model is a gated Hugging Face repo: only the home accepts its licence
and logs in. A machine that loads it from Hugging Face publishes a copy of the snapshot
(:func:`publish`) to::

    <share>/maktaba/models/<org>__<name>/<revision>/
        manifest.json       {model, revision, published_at, files: [{path, size, sha256}]}
        <the snapshot's files>

written whole or not at all: the files go to a hidden ``.<revision>.incoming-…`` folder,
the manifest last, and the folder is renamed into place. A machine that cannot load the
model from Hugging Face takes the newest complete snapshot on the share
(:func:`share_snapshots`), copies it once to ``~/.maktaba/models/...`` checking every file
against the manifest (:func:`fetch`), and loads from there; a copy already there is used
as it is, with the share out of reach too (:func:`local_snapshots`).

The share is ``<share root>/maktaba/models``, beside the cluster directory
(:func:`models_dir`); ``MAKTABA_MODELS_DIR`` overrides it. Nothing under it is followed
through a link (:mod:`~..cluster.safefs`).
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import time
import uuid
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from ..cluster import safefs
from ..log import get_logger

__all__ = [
    "ENV_MODELS_DIR",
    "MANIFEST",
    "ModelFile",
    "ShareModelError",
    "Snapshot",
    "fetch",
    "from_share",
    "hf_snapshot",
    "local_root",
    "local_snapshots",
    "model_folder",
    "models_dir",
    "publish",
    "publish_from_cache",
    "resolve_models_dir",
    "share_snapshots",
]

_log = get_logger()

ENV_MODELS_DIR = "MAKTABA_MODELS_DIR"
MANIFEST = "manifest.json"
_CHUNK = 8 * 1024 * 1024
#: a partial copy on the share touched this recently is another process's, still going
_BUSY_SEC = 3600.0
_REVISION = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}")
_HEX64 = re.compile(r"[0-9a-f]{64}")


class ShareModelError(RuntimeError):
    """No usable copy of the model on the share or in the local cache."""


@dataclass(frozen=True)
class ModelFile:
    path: str  #: relative, ``/``-separated
    size: int
    sha256: str


@dataclass(frozen=True)
class Snapshot:
    """A complete copy of a model: its folder and its manifest."""

    path: Path
    model: str
    revision: str
    published_at: str
    files: tuple[ModelFile, ...]

    def manifest(self) -> dict[str, Any]:
        return {
            "model": self.model,
            "revision": self.revision,
            "published_at": self.published_at,
            "files": [{"path": f.path, "size": f.size, "sha256": f.sha256} for f in self.files],
        }


def model_folder(model_id: str) -> str:
    """``org/name`` as one folder name, ``org__name``."""
    name = model_id.strip().replace("/", "__")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", name) or ".." in name:
        raise ValueError(f"not a model id: {model_id!r}")
    return name


def local_root() -> Path:
    """This machine's own copies: ``$MAKTABA_MODEL_CACHE_DIR/share-copies``
    (:mod:`~maktaba_pipeline.model_dirs`), else ``~/.maktaba/models``."""
    from ..model_dirs import share_copies_dir

    return share_copies_dir() or Path.home() / ".maktaba" / "models"


def models_dir(cluster_dir: Path | None, env: Mapping[str, str] | None = None) -> Path | None:
    """The share's models folder: beside the cluster directory (``<share
    root>/maktaba/models``); ``MAKTABA_MODELS_DIR`` overrides."""
    env = os.environ if env is None else env
    override = (env.get(ENV_MODELS_DIR) or "").strip()
    if override:
        return Path(override).expanduser()
    return None if cluster_dir is None else cluster_dir.parent / "models"


async def resolve_models_dir(db: Any | None) -> Path | None:
    """The share's models folder on this machine, or None (no share here). The share is
    probed off the event loop (a stale mount can hang a ``stat``)."""
    import asyncio

    from ..cluster import share
    from ..engine.paths import active_path_map

    env = os.environ
    if (env.get(ENV_MODELS_DIR) or "").strip():
        return models_dir(None, env)
    override = (env.get(share.ENV_CLUSTER_DIR) or "").strip()
    if override:
        return models_dir(Path(override).expanduser(), env)
    try:
        root, _how = await share.find_share_root(db, env)
        if root is None:
            return None
        found, _how, _base = await asyncio.wait_for(
            asyncio.to_thread(share.locate_cluster_dir, root, active_path_map()), 30
        )
    except Exception:  # noqa: BLE001 - the share is optional
        _log.warning("stt.models_dir_unresolved", exc_info=True)
        return None
    return models_dir(found, env)


# --- the manifest ------------------------------------------------------------------------


def _safe_rel(path: str) -> list[str]:
    """The parts of a manifest path, refused when absolute, hidden or climbing out."""
    if not isinstance(path, str) or path.startswith("/") or "\\" in path or ":" in path:
        raise ValueError(f"bad path {path!r}")
    parts = path.split("/")
    for p in parts:
        if p in ("", ".", "..") or p.startswith(".") or "\x00" in p:
            raise ValueError(f"bad path {path!r}")
    return parts


def _parse_manifest(folder: Path, data: Any, model_id: str) -> Snapshot:
    if not isinstance(data, dict) or data.get("model") != model_id:
        raise ValueError("not this model's manifest")
    revision, published = data.get("revision"), data.get("published_at")
    if not isinstance(revision, str) or revision != folder.name:
        raise ValueError("revision does not match its folder")
    if not isinstance(published, str):
        raise ValueError("no published_at")
    raw = data.get("files")
    if not isinstance(raw, list) or not raw:
        raise ValueError("no files")
    files: list[ModelFile] = []
    for f in raw:
        if not isinstance(f, dict):
            raise ValueError("bad file entry")
        path, size, sha = f.get("path"), f.get("size"), f.get("sha256")
        _safe_rel(path)  # type: ignore[arg-type]
        if not isinstance(size, int) or isinstance(size, bool) or size < 0:
            raise ValueError(f"bad size for {path!r}")
        if not isinstance(sha, str) or not _HEX64.fullmatch(sha):
            raise ValueError(f"bad sha256 for {path!r}")
        files.append(ModelFile(str(path), size, sha))
    return Snapshot(folder, model_id, revision, published, tuple(files))


def _read_manifest(folder: Path, model_id: str) -> Snapshot:
    with safefs.open_read(folder / MANIFEST) as fh:
        return _parse_manifest(folder, json.load(fh), model_id)


def _file_path(folder: Path, rel: str) -> Path:
    """``folder/rel`` with every directory in between real (not a link)."""
    parts = _safe_rel(rel)
    return safefs.safe_dir(folder, parts[:-1], create=False) / parts[-1]


def _complete(snap: Snapshot) -> bool:
    """Every file of the manifest there, a regular file of its size (no hashing)."""
    try:
        for f in snap.files:
            p = _file_path(snap.path, f.path)
            if not safefs.real_file(p) or p.stat().st_size != f.size:
                return False
    except (OSError, ValueError):
        return False
    return True


def _snapshots(base: Path | None, model_id: str) -> list[Snapshot]:
    """The complete snapshots of ``model_id`` under ``base``, newest first. A folder
    still being written (a hidden name, no manifest yet, files missing or short) is not
    one."""
    if base is None:
        return []
    found: list[Snapshot] = []
    for folder in safefs.list_real_dirs(base / model_folder(model_id)):
        if folder.name.startswith(".") or not _REVISION.fullmatch(folder.name):
            continue
        try:
            snap = _read_manifest(folder, model_id)
        except (OSError, ValueError) as exc:
            _log.debug("stt.model_snapshot_skipped", folder=str(folder), error=str(exc))
            continue
        if _complete(snap):
            found.append(snap)
    return sorted(found, key=lambda s: (s.published_at, s.revision), reverse=True)


def share_snapshots(share: Path | None, model_id: str) -> list[Snapshot]:
    return _snapshots(share, model_id)


def local_snapshots(model_id: str, root: Path | None = None) -> list[Snapshot]:
    return _snapshots(local_root() if root is None else root, model_id)


# --- copying -----------------------------------------------------------------------------


def _copy_hashing(src: Any, dest: Path) -> tuple[int, str]:
    """Copy the open binary ``src`` to the new file ``dest``: ``(bytes, sha256)``."""
    h = hashlib.sha256()
    n = 0
    fd = os.open(dest, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_BINARY", 0), 0o644)
    with os.fdopen(fd, "wb") as out:
        while chunk := src.read(_CHUNK):
            h.update(chunk)
            out.write(chunk)
            n += len(chunk)
    return n, h.hexdigest()


def _partial(parent: Path, revision: str) -> Path:
    tmp = parent / f".{revision}.incoming-{os.getpid()}-{uuid.uuid4().hex[:8]}"
    os.mkdir(tmp, safefs.DIR_MODE)
    return tmp


def _install(tmp: Path, final: Path, model_id: str) -> bool:
    """Rename the finished ``tmp`` into place: False when another process's complete
    copy got there first (``tmp`` removed). A broken folder in the way (files lost since)
    is replaced."""
    try:
        os.rename(tmp, final)
        return True
    except OSError:
        if not safefs.real_dir(final):
            raise
    if _whole(final, model_id):
        shutil.rmtree(tmp, ignore_errors=True)
        return False
    shutil.rmtree(final)
    os.rename(tmp, final)
    return True


def _whole(folder: Path, model_id: str) -> bool:
    try:
        return _complete(_read_manifest(folder, model_id))
    except (OSError, ValueError):
        return False


def _busy(parent: Path, revision: str, now: float) -> bool:
    """Another process is copying ``revision`` here (a incoming folder touched lately)."""
    prefix = f".{revision}.incoming-"
    for d in safefs.list_real_dirs(parent):
        if d.name.startswith(prefix):
            try:
                if now - d.stat().st_mtime < _BUSY_SEC:
                    return True
            except OSError:
                continue
    return False


def _now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


def _snapshot_files(snapshot: Path) -> list[tuple[str, Path]]:
    """``(relative path, file)`` of a Hugging Face snapshot (its files are links into the
    cache's blobs: followed); hidden names left out."""
    out: list[tuple[str, Path]] = []
    for dirpath, dirnames, filenames in os.walk(snapshot):
        dirnames[:] = sorted(d for d in dirnames if not d.startswith("."))
        for name in sorted(filenames):
            if name.startswith("."):
                continue
            p = Path(dirpath) / name
            if p.is_file():
                out.append((p.relative_to(snapshot).as_posix(), p))
    return out


def publish(
    model_id: str,
    snapshot: Path,
    share: Path,
    *,
    revision: str | None = None,
    now: Callable[[], str] = _now,
) -> Path | None:
    """Put the Hugging Face ``snapshot`` of ``model_id`` on the share: ``<share>/<org>__
    <name>/<revision>/`` (the revision is the snapshot folder's name, a commit hash).
    Returns the folder, or None when a complete copy of that revision is already
    there. ``share`` must exist (it is the share's models folder, created under the
    share root by the caller); the model's folders below it are created."""
    rev = revision or snapshot.name
    if not _REVISION.fullmatch(rev):
        raise ValueError(f"not a revision: {rev!r}")
    parent = safefs.safe_dir(share, [model_folder(model_id)])
    final = parent / rev
    if safefs.real_dir(final):
        if _whole(final, model_id):
            return None
        raise ShareModelError(f"{final} is there but not a complete copy; remove it by hand")
    if _busy(parent, rev, time.time()):
        return None  # another of the home's workers is publishing it
    files = _snapshot_files(snapshot)
    if not files:
        raise ShareModelError(f"no files in {snapshot}")
    tmp = _partial(parent, rev)
    try:
        listed: list[ModelFile] = []
        for rel, src in files:
            parts = rel.split("/")
            d = safefs.safe_dir(tmp, parts[:-1])
            with open(src, "rb") as fh:
                size, sha = _copy_hashing(fh, d / parts[-1])
            listed.append(ModelFile(rel, size, sha))
        snap = Snapshot(final, model_id, rev, now(), tuple(listed))
        safefs.write_replace(tmp, MANIFEST, json.dumps(snap.manifest(), indent=1))
        if not _install(tmp, final, model_id):
            return None
    except BaseException:
        shutil.rmtree(tmp, ignore_errors=True)
        raise
    _log.info("stt.model_published", model=model_id, revision=rev, folder=str(final))
    return final


def fetch(snap: Snapshot, root: Path | None = None) -> Path:
    """The share's ``snap`` copied to ``~/.maktaba/models/<org>__<name>/<revision>/``
    (once: a complete copy there is used as it is), every file checked against the
    manifest's size and sha256 as it is copied. Raises :class:`ShareModelError` on a
    file that does not match (nothing is left behind)."""
    root = local_root() if root is None else root
    root.mkdir(parents=True, exist_ok=True)
    parent = safefs.safe_dir(root, [model_folder(snap.model)])
    final = parent / snap.revision
    for have in local_snapshots(snap.model, root):
        if have.revision == snap.revision:
            return have.path
    tmp = _partial(parent, snap.revision)
    try:
        for f in snap.files:
            parts = _safe_rel(f.path)
            d = safefs.safe_dir(tmp, parts[:-1])
            with safefs.open_read_binary(_file_path(snap.path, f.path)) as fh:
                size, sha = _copy_hashing(fh, d / parts[-1])
            if size != f.size or sha != f.sha256:
                raise ShareModelError(
                    f"{f.path} on the share does not match its manifest "
                    f"({size} bytes, sha256 {sha[:12]}…)"
                )
        safefs.write_replace(
            tmp, MANIFEST, json.dumps({**snap.manifest(), "copied_from": str(snap.path)})
        )
        _install(tmp, final, snap.model)
    except BaseException:
        shutil.rmtree(tmp, ignore_errors=True)
        raise
    _log.info("stt.model_fetched", model=snap.model, revision=snap.revision, folder=str(final))
    for old in local_snapshots(snap.model, root):
        if old.path != final:
            # an older version: the new one replaces it (a process that loaded it holds
            # its weights in memory, not the files)
            shutil.rmtree(old.path, ignore_errors=True)
    return final


def from_share(model_id: str, share: Path | None, root: Path | None = None) -> Path:
    """A local folder holding ``model_id``: the newest complete snapshot on the share,
    copied here once; with the share out of reach (or nothing usable on it), the newest
    copy already here. Raises :class:`ShareModelError` when there is none."""
    errors: list[str] = []
    for snap in share_snapshots(share, model_id):
        try:
            return fetch(snap, root)
        except (OSError, ShareModelError) as exc:
            _log.warning("stt.model_fetch_failed", folder=str(snap.path), error=str(exc))
            errors.append(f"{snap.revision}: {exc}")
    local = local_snapshots(model_id, root)
    if local:
        return local[0].path
    where = "no share here" if share is None else f"no complete copy in {share}"
    raise ShareModelError("; ".join([where, *errors])[:300])


def hf_snapshot(model_id: str) -> Path | None:
    """The folder of ``model_id`` in this machine's Hugging Face cache (no network): at
    its pinned commit when Maktaba fetches it at one (Windows, Linux: a download at a
    commit makes no ``refs/main``), else the one ``main`` names (mlx-audio's, on a Mac)."""
    try:
        from huggingface_hub import snapshot_download
    except ImportError:
        return None
    from .engine import pin_for

    pin = pin_for(model_id)
    for revision in dict.fromkeys([pin.revision if pin else None, None]):
        try:
            return Path(snapshot_download(model_id, revision=revision, local_files_only=True))
        except Exception:  # noqa: BLE001 - not cached here
            continue
    return None


def publish_from_cache(model_id: str, share: Path | None) -> Path | None:
    """:func:`publish` the cached Hugging Face snapshot of ``model_id`` to ``share``
    (nothing when either is missing); errors are logged, never raised."""
    if share is None:
        return None
    snapshot = hf_snapshot(model_id)
    if snapshot is None:
        return None
    try:
        if not safefs.real_dir(share):
            share.mkdir(parents=False, exist_ok=True)  # <share root>/maktaba exists
        return publish(model_id, snapshot, share)
    except Exception:  # noqa: BLE001 - the workers wait for the next try
        _log.warning("stt.model_publish_failed", model=model_id, exc_info=True)
        return None
