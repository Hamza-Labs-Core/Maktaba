"""Where the shared storage is mounted here, and whether it works (design section 6).

**Path-map inference.** The database keeps paths as the home's scanner saw them
(``Y:/Channel/a.mp4``, ``/Volumes/nas/Channel/a.mp4``, ``/volume1/media/...``). For each
library this takes up to five videos (path, size) and, for every candidate mount root
of this OS, tries the root joined with ever-shorter suffixes of the sample's path; a
file that exists with the same size gives ``db_prefix -> local_prefix``. The mapping
most samples agree on wins, and it needs two agreeing samples (or the library's only
video). A library whose paths already resolve here as they are needs no entry.

**Storage check.** Read: the samples, through the map, stat with the right size.
Write: create and delete ``.maktaba-<node8>.tmp`` in a library root.
"""

from __future__ import annotations

import contextlib
import os
import platform
import re
import string
import subprocess
from collections import Counter
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from ..engine.paths import PathMap
from ..engine.sql import loads

__all__ = [
    "LibrarySample",
    "candidate_roots",
    "check_shares",
    "check_storage",
    "infer_path_map",
    "load_samples",
    "network_mounts",
]

#: file systems in /proc/mounts that are network or user-space mounts of a share
NETWORK_FS = frozenset({"nfs", "nfs4", "cifs", "smb3", "smbfs", "fuse.sshfs", "9p", "fuse.rclone"})

# Mount parents per OS; module attributes so tests can point them at temp dirs.
MAC_VOLUMES = "/Volumes"
LINUX_PARENTS: tuple[str, ...] = ("/mnt", "/srv")
LINUX_MEDIA = "/media"
PROC_MOUNTS = "/proc/mounts"

SizeOf = Callable[[str], int | None]


@dataclass(frozen=True, slots=True)
class LibrarySample:
    library_id: str
    roots: tuple[str, ...]
    #: (database path, size in bytes), at most five
    videos: tuple[tuple[str, int], ...]


def _size_of(path: str) -> int | None:
    try:
        st = os.stat(path)
    except (OSError, ValueError):
        return None
    return st.st_size if os.path.isfile(path) else None


def _slash(p: str) -> str:
    return p.replace("\\", "/")


def _subdirs(parent: str) -> list[str]:
    try:
        return sorted(
            str(e.path) for e in os.scandir(parent) if e.is_dir() and not e.name.startswith(".")
        )
    except OSError:
        return []


def network_mounts(text: str) -> list[str]:
    """Mount points of network / fuse file systems in ``/proc/mounts`` text."""
    out: list[str] = []
    for line in text.splitlines():
        parts = line.split()
        if len(parts) < 3 or parts[2] not in NETWORK_FS:
            continue
        # /proc/mounts escapes spaces and friends as octal (\040)
        out.append(re.sub(r"\\([0-7]{3})", lambda m: chr(int(m.group(1), 8)), parts[1]))
    return out


def _unc_from_net_use(text: str) -> list[str]:
    return [_slash(m) for m in re.findall(r"(\\\\[^\s\\]+\\[^\s\\]+)", text)]


def _net_use() -> str:
    try:
        return subprocess.run(
            ["net", "use"], capture_output=True, text=True, timeout=10, check=False
        ).stdout
    except (OSError, subprocess.SubprocessError):
        return ""


def _drive_exists(letter: str) -> bool:
    return os.path.exists(f"{letter}:\\")


def _db_root(path: str) -> str | None:
    """``Y:/x`` -> ``Y:/``; ``//nas/share/x`` -> ``//nas/share/``."""
    p = _slash(path)
    if re.match(r"^[A-Za-z]:/", p):
        return p[:3]
    m = re.match(r"^(//[^/]+/[^/]+)/", p)
    return m.group(1) + "/" if m else None


def candidate_roots(
    *,
    system: str | None = None,
    env: Mapping[str, str] | None = None,
    home: Path | None = None,
    db_paths: Iterable[str] = (),
    proc_mounts: str | None = None,
    net_use: str | None = None,
) -> list[str]:
    """Directories that may be the share's mount here, most specific first, unique."""
    system = system or platform.system()
    env = os.environ if env is None else env
    out: list[str] = []
    hints = env.get("MAKTABA_STORAGE_HINTS", "")
    out += [h for h in hints.split(os.pathsep) if h.strip()]
    if system == "Darwin":
        out += _subdirs(MAC_VOLUMES)
        home = home or Path.home()
        with contextlib.suppress(OSError):
            for entry in sorted(home.iterdir()):
                if entry.is_symlink() and entry.is_dir():
                    out.append(str(entry))
    elif system == "Windows":
        out += [f"{d}:/" for d in string.ascii_uppercase if _drive_exists(d)]
        out += _unc_from_net_use(_net_use() if net_use is None else net_use)
        out += [r for r in (_db_root(p) for p in db_paths) if r]
    else:
        if proc_mounts is None:
            try:
                proc_mounts = Path(PROC_MOUNTS).read_text(encoding="utf-8")
            except OSError:
                proc_mounts = ""
        out += network_mounts(proc_mounts)
        for parent in LINUX_PARENTS:
            out += _subdirs(parent)
        for user_dir in _subdirs(LINUX_MEDIA):
            out += _subdirs(user_dir)
    seen: set[str] = set()
    unique: list[str] = []
    for c in out:
        key = _slash(c).rstrip("/").lower() if system == "Windows" else _slash(c).rstrip("/")
        if key and key not in seen:
            seen.add(key)
            unique.append(c)
    return unique


def _join(root: str, rel: str) -> str:
    return _slash(root).rstrip("/") + "/" + rel


def _norm_prefix(p: str) -> str:
    p = _slash(p)
    return p if p.endswith("/") else p + "/"


def _match(
    db_path: str, size: int, candidates: Sequence[str], size_of: SizeOf
) -> tuple[str, str] | None:
    """``(db_prefix, local_prefix)`` for one sample, longest suffix first."""
    p = _slash(db_path)
    parts = p.split("/")
    # parts[0] is '' for a POSIX path, 'Y:' for a drive; UNC is '', '', server, share:
    # the server and share stay in the prefix
    # (a canonical smb://server/share/... likewise)
    first = 4 if p.startswith("//") or re.match(r"^[a-z]+://", p) else 1
    for start in range(first, len(parts)):
        rel = "/".join(parts[start:])
        if not rel:
            continue
        for root in candidates:
            if size_of(_join(root, rel)) == size:
                db_prefix = "/".join(parts[:start]) + "/"
                return _norm_prefix(db_prefix), _norm_prefix(root)
    return None


def infer_path_map(
    samples: Sequence[LibrarySample],
    candidates: Sequence[str],
    *,
    size_of: SizeOf = _size_of,
) -> dict[str, str]:
    """The inferred map, ``{db_prefix: local_prefix}``; empty when nothing is needed or
    nothing was found."""
    out: dict[str, tuple[str, int]] = {}
    for lib in samples:
        if not lib.videos:
            continue
        # already reachable as stored: this library needs no mapping
        if sum(size_of(p) == s for p, s in lib.videos) >= min(2, len(lib.videos)):
            continue
        votes: Counter[tuple[str, str]] = Counter()
        for path, size in lib.videos:
            hit = _match(path, size, candidates, size_of)
            if hit is not None:
                votes[hit] += 1
        if not votes:
            continue
        (db_prefix, local_prefix), n = votes.most_common(1)[0]
        if n < min(2, len(lib.videos)):
            continue
        prev = out.get(db_prefix)
        if prev is None or n > prev[1]:
            out[db_prefix] = (local_prefix, n)
    return {k: v for k, (v, _) in sorted(out.items())}


async def load_samples(db: Any, *, per_library: int = 5) -> list[LibrarySample]:
    libs = await db.fetch("SELECT id, roots FROM libraries ORDER BY id")
    out: list[LibrarySample] = []
    for lib in libs:
        roots = lib["roots"]
        roots_list = loads(roots) if isinstance(roots, str) else list(roots or [])
        rows = await db.fetch(
            "SELECT path, size_bytes FROM videos WHERE library_id = $1 AND deleted_at IS NULL"
            " AND state <> 'missing' ORDER BY path LIMIT $2",
            lib["id"],
            per_library,
        )
        out.append(
            LibrarySample(
                library_id=str(lib["id"]),
                roots=tuple(str(r) for r in roots_list or []),
                videos=tuple((str(r["path"]), int(r["size_bytes"])) for r in rows),
            )
        )
    return out


def check_storage(
    samples: Sequence[LibrarySample],
    path_map: PathMap,
    node8: str,
    *,
    size_of: SizeOf = _size_of,
) -> dict[str, Any]:
    """``{read: ok|error, write: ok|error|skipped, checked_at, detail: str|None}``.

    Read is ``ok`` when every library with videos has at least one sample readable here
    with its size, or present when its size is not known yet (0) (a library without
    videos has nothing to check); write is
    ``skipped`` when no library root is known.
    """
    detail: list[str] = []
    read = "ok"
    for lib in samples:
        if not lib.videos:
            continue
        # a size of 0 is not known yet (a file found but not probed): it only has to be there
        if not any(
            (got := size_of(path_map.to_local(p))) is not None and (s <= 0 or got == s)
            for p, s in lib.videos
        ):
            read = "error"
            detail.append(f"no sample of library {lib.library_id} is readable here")
    write = "skipped"
    for lib in samples:
        for root in lib.roots:
            local = path_map.to_local(_norm_prefix(root))
            probe = _join(local, f".maktaba-{node8}.tmp")
            try:
                with open(probe, "wb") as fh:
                    fh.write(b"maktaba")
                os.remove(probe)
                write = "ok"
                break
            except OSError as exc:
                write = "error"
                detail.append(f"cannot write in {local}: {exc.strerror or exc}")
        if write == "ok":
            break
    if write == "ok":
        detail = [d for d in detail if not d.startswith("cannot write")]
    return {
        "read": read,
        "write": write,
        "checked_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "detail": "; ".join(detail) or None,
    }


def _probe_write(local_dir: str, node8: str) -> tuple[str, str | None]:
    probe = _join(local_dir, f".maktaba-{node8}.tmp")
    try:
        with open(probe, "wb") as fh:
            fh.write(b"maktaba")
        os.remove(probe)
    except OSError as exc:
        return "error", f"cannot write in {local_dir}: {exc.strerror or exc}"
    return "ok", None


def _readable_dir(local_dir: str) -> tuple[str, str | None]:
    try:
        with os.scandir(local_dir) as it:
            next(it, None)
    except OSError as exc:
        return "error", f"cannot read {local_dir}: {exc.strerror or exc}"
    return "ok", None


def check_shares(
    samples: Sequence[LibrarySample],
    resolver: PathMap,
    node8: str,
    *,
    write: bool = True,
) -> list[dict[str, Any]]:
    """Per share (story 31.3): which of the shares the libraries live on are reachable
    and writable here, and through which local mount.

    The shares checked are the canonical prefixes of the library roots (and of the
    sampled videos), each as a ``{share, local, source, read, write, detail}`` row;
    a share no mount reaches has ``local: null`` and ``read: error``.
    """
    from ..sharepath.canonical import parse_canonical

    shares_of = getattr(resolver, "shares", ())
    wanted: dict[str, list[str]] = {}
    for lib in samples:
        for value in [*lib.roots, *(p for p, _ in lib.videos[:1])]:
            sp = parse_canonical(value)
            if sp is None or sp.scheme == "file":
                continue
            wanted.setdefault(f"{sp.scheme}://{sp.server}/{sp.share}", []).append(
                value if value in lib.roots else value.rsplit("/", 1)[0]
            )
    rows: list[dict[str, Any]] = []
    for share, dirs in sorted(wanted.items()):
        local_root = resolver.to_local(share)
        mapped = local_root != share
        via = next(
            (
                s
                for s in sorted(shares_of, key=lambda x: -len(x.prefix))
                if (share + "/").lower().startswith(s.prefix.lower())
                or s.prefix.lower().startswith((share + "/").lower())
            ),
            None,
        )
        row: dict[str, Any] = {
            "share": share,
            "local": local_root if mapped else None,
            "source": via.source if via is not None else ("map" if mapped else None),
            "read": "error",
            "write": "skipped",
            "detail": None,
        }
        if not mapped:
            row["detail"] = "not mounted here"
            rows.append(row)
            continue
        # a share mounted only below its root (Y: = Originals/Backup/Youtube): check there
        target = resolver.to_local(dirs[0]) if dirs else local_root
        row["read"], row["detail"] = _readable_dir(target)
        if write and row["read"] == "ok":
            row["write"], row["detail"] = _probe_write(target, node8)
        rows.append(row)
    return rows
