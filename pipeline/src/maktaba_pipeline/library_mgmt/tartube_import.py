"""Read what a Tartube database says was downloaded (Epic 47, story 47.12).

The home's YouTube downloads before Maktaba were made with Tartube. Its database
(``tartube.db``) is a Python pickle of Tartube's own classes; beside it are Tartube's JSON
exports. The import:

1. reads the **JSON export** first: plain data, nothing is unpickled. Both shapes are read:
   Tartube's own export (``{"file_type": "db_export", "db_dict": {dbid: {"type": "video",
   "vid": ..., "db_dict": {...}}}}``, nested channel -> playlist/folder -> video) and
   Make-It-Offline's (``{"videos": [{"videoId": ..., "isDownloaded": ...}]}``);
2. falls back to the database **only for what the export lacks** (Tartube's export has no
   ``dl_flag``), through :class:`StubUnpickler`: it never imports a module. Every global
   in the pickle becomes an inert stub class, except a short allowlist of plain data types;
   anything that could run code (``os``, ``subprocess``, ``builtins.eval``, ...) is refused
   and the load stops. Run it only on a file the owner names, on the home;
3. counts a video as downloaded when ``dl_flag`` is set and its file exists (a file whose
   folder is not on this machine at all is taken on ``dl_flag``'s word, and counted apart).

Nothing here writes: :func:`downloaded_ids` returns the ids, and
:func:`maktaba_pipeline.library_mgmt.youtube_sources.seed_done` records them.
"""

from __future__ import annotations

import codecs
import datetime as _dt
import json
import os
import pickle
from collections import OrderedDict
from collections.abc import Callable, Iterable, Iterator, Mapping
from dataclasses import dataclass, field
from typing import Any

__all__ = [
    "ImportReport",
    "TartubeRecord",
    "TartubeRefused",
    "StubUnpickler",
    "downloaded_ids",
    "load_pickle",
    "read_export",
    "records_from_pickle",
]

#: Tartube's video ids are YouTube's 11 characters
_ID_LEN = 11
MAX_JSON_BYTES = 2 * 1024**3


class TartubeRefused(Exception):
    """The pickle names something that is not plain data: the load stopped."""


# ------------------------------------------------------------------ the stub-only unpickler


class _Stub:
    """An inert object: it keeps the attributes the pickle gives it and does nothing."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        self.__dict__["_args"] = args
        self.__dict__.update(kwargs)

    def __setstate__(self, state: Any) -> None:
        if isinstance(state, tuple) and len(state) == 2:
            state = {**(state[0] or {}), **(state[1] or {})}
        if isinstance(state, dict):
            self.__dict__.update({str(k): v for k, v in state.items()})
        else:
            self.__dict__["_state"] = state

    def __reduce__(self) -> Any:  # never pickled again
        raise TypeError("a Tartube stub is not picklable")


def _safe_reconstructor(cls: Any, base: Any, state: Any) -> Any:
    """``copyreg._reconstructor`` for stubs only (protocol 0/1 objects)."""
    if not (isinstance(cls, type) and issubclass(cls, _Stub)):
        raise TartubeRefused(f"copyreg._reconstructor of {cls!r}")
    return cls.__new__(cls)


def _safe_codecs_encode(text: str, encoding: str = "utf-8", errors: str = "strict") -> bytes:
    """``_codecs.encode`` as protocol 2 uses it for bytes (latin-1 only)."""
    if encoding not in ("latin1", "latin-1") or not isinstance(text, str):
        raise TartubeRefused(f"_codecs.encode({encoding!r})")
    return codecs.encode(text, "latin1", errors if errors == "strict" else "strict")


#: plain data a pickle may name
_ALLOWED: dict[tuple[str, str], Any] = {
    ("builtins", "set"): set,
    ("builtins", "frozenset"): frozenset,
    ("builtins", "list"): list,
    ("builtins", "dict"): dict,
    ("builtins", "tuple"): tuple,
    ("builtins", "bytearray"): bytearray,
    ("builtins", "object"): object,
    ("__builtin__", "set"): set,
    ("__builtin__", "frozenset"): frozenset,
    ("__builtin__", "object"): object,
    ("collections", "OrderedDict"): OrderedDict,
    ("datetime", "datetime"): _dt.datetime,
    ("datetime", "date"): _dt.date,
    ("datetime", "time"): _dt.time,
    ("datetime", "timedelta"): _dt.timedelta,
    ("datetime", "timezone"): _dt.timezone,
    ("copy_reg", "_reconstructor"): _safe_reconstructor,
    ("copyreg", "_reconstructor"): _safe_reconstructor,
    ("_codecs", "encode"): _safe_codecs_encode,
}

#: modules whose globals are never even stubbed: a pickle naming one is not Tartube's
_REFUSED_MODULES = frozenset(
    {
        "builtins", "__builtin__", "os", "posix", "nt", "posixpath", "ntpath", "sys",
        "subprocess", "shutil", "socket", "importlib", "imp", "runpy", "code", "codeop",
        "pickle", "_pickle", "marshal", "types", "functools", "operator", "ctypes", "io",
        "_io", "pty", "platform", "webbrowser", "multiprocessing", "threading", "signal",
        "tempfile", "pathlib", "codecs", "_codecs", "copyreg", "copy_reg", "inspect",
        "asyncio", "urllib", "http", "requests", "pdb", "timeit", "trace", "commands",
        "popen2", "glob", "zipimport", "pkgutil", "site", "sysconfig", "gc", "weakref",
        "builtins.eval", "numpy", "torch", "dill", "cloudpickle", "yaml",
    }
)  # fmt: skip


class StubUnpickler(pickle.Unpickler):
    """An unpickler that imports nothing (MIO's ``convert_tartube_db.py`` without its
    real-import path): plain data from :data:`_ALLOWED`, an inert stub class for every
    other global, and a refusal for anything from :data:`_REFUSED_MODULES`."""

    def __init__(self, file: Any) -> None:
        super().__init__(file)
        self._stubs: dict[tuple[str, str], type] = {}
        self.stubbed: set[str] = set()

    def find_class(self, module: str, name: str) -> Any:
        key = (module, name)
        if key in _ALLOWED:
            return _ALLOWED[key]
        top = module.split(".", 1)[0]
        if top in _REFUSED_MODULES or module in _REFUSED_MODULES:
            raise TartubeRefused(f"the pickle names {module}.{name}: not loaded")
        if not name.isidentifier() or name.startswith("__"):
            raise TartubeRefused(f"the pickle names {module}.{name}: not loaded")
        stub = self._stubs.get(key)
        if stub is None:
            stub = type(name, (_Stub,), {"__module__": f"tartube_stub.{module}"})
            self._stubs[key] = stub
            self.stubbed.add(f"{module}.{name}")
        return stub

    def persistent_load(self, pid: Any) -> Any:
        raise TartubeRefused("persistent ids are not loaded")


def load_pickle(path: str) -> Any:
    """Load ``path`` with :class:`StubUnpickler` (raises :class:`TartubeRefused`)."""
    with open(path, "rb") as f:
        return StubUnpickler(f).load()


# ------------------------------------------------------------------ records


@dataclass(frozen=True, slots=True)
class TartubeRecord:
    vid: str
    dbid: int | None = None
    #: None: the source does not say (Tartube's JSON export)
    dl_flag: bool | None = None
    file_dir: str | None = None
    file_name: str | None = None
    file_ext: str | None = None
    #: the closest channel or playlist above it: (name, source url)
    parent: tuple[str | None, str | None] = (None, None)

    def file_path(self) -> str | None:
        if not self.file_dir or not self.file_name:
            return None
        name = self.file_name + (self.file_ext or "")
        return os.path.join(self.file_dir, name)


def _vid(value: Any) -> str | None:
    if isinstance(value, str) and len(value) == _ID_LEN:
        return value
    return None


def _flag(value: Any) -> bool | None:
    return value if isinstance(value, bool) else None


def _str(value: Any) -> str | None:
    return value if isinstance(value, str) and value else None


def _split_file(value: Any) -> tuple[str | None, str | None, str | None]:
    """A ``file`` value (a name or a path) as (dir, stem, .ext)."""
    s = _str(value)
    if s is None:
        return None, None, None
    d, base = os.path.split(s)
    stem, ext = os.path.splitext(base)
    return (d or None), stem, (ext or None)


def _walk_native(
    tree: Mapping[str, Any], parent: tuple[str | None, str | None]
) -> Iterator[TartubeRecord]:
    for key, node in tree.items():
        if not isinstance(node, dict):
            continue
        kind = node.get("type")
        if kind == "video":
            vid = _vid(node.get("vid"))
            if vid is None:
                continue
            d, stem, ext = _split_file(node.get("file"))
            yield TartubeRecord(
                vid=vid,
                dbid=node.get("dbid") if isinstance(node.get("dbid"), int) else _int(key),
                dl_flag=_flag(node.get("dl_flag")),
                file_dir=_str(node.get("file_dir")) or d,
                file_name=_str(node.get("file_name")) or stem,
                file_ext=_str(node.get("file_ext")) or ext,
                parent=parent,
            )
        children = node.get("db_dict")
        if isinstance(children, dict):
            here = parent
            if kind in ("channel", "playlist"):
                here = (_str(node.get("name")), _str(node.get("source")))
            yield from _walk_native(children, here)


def _int(value: Any) -> int | None:
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _mio_records(doc: Mapping[str, Any]) -> Iterator[TartubeRecord]:
    parents: dict[Any, tuple[str | None, str | None]] = {}
    for key in ("channels", "playlists"):
        for c in doc.get(key) or []:
            if isinstance(c, dict):
                parents[c.get("dbid")] = (_str(c.get("name")), _str(c.get("url")))
    for v in doc.get("videos") or []:
        if not isinstance(v, dict):
            continue
        vid = _vid(v.get("videoId"))
        if vid is None:
            continue
        d, stem, ext = _split_file(v.get("fileName"))
        ext = ("." + v["fileExtension"]) if _str(v.get("fileExtension")) else ext
        pl = v.get("playlistDbids") or []
        parent = parents.get(v.get("channelDbid")) or (parents.get(pl[0]) if pl else None)
        yield TartubeRecord(
            vid=vid,
            dbid=v.get("dbid") if isinstance(v.get("dbid"), int) else None,
            dl_flag=_flag(v.get("isDownloaded")),
            file_dir=d,
            file_name=stem,
            file_ext=ext,
            parent=parent or (None, None),
        )


def read_export(path: str) -> list[TartubeRecord]:
    """The video records of a Tartube (or Make-It-Offline) JSON export."""
    if os.path.getsize(path) > MAX_JSON_BYTES:
        raise ValueError(f"{path} is larger than {MAX_JSON_BYTES} bytes")
    with open(path, encoding="utf-8") as f:
        doc = json.load(f)
    if not isinstance(doc, dict):
        raise ValueError(f"{path} is not a Tartube export")
    if isinstance(doc.get("db_dict"), dict):
        return list(_walk_native(doc["db_dict"], (None, None)))
    if isinstance(doc.get("videos"), list):
        return list(_mio_records(doc))
    raise ValueError(f"{path} is not a Tartube export (no db_dict or videos)")


def _attrs(obj: Any) -> dict[str, Any]:
    d = getattr(obj, "__dict__", None)
    return d if isinstance(d, dict) else {}


def _kind(obj: Any) -> str | None:
    name = type(obj).__name__.lower()
    for k in ("video", "channel", "playlist", "folder"):
        if k in name:
            return k
    return None


def records_from_pickle(root: Any) -> list[TartubeRecord]:
    """The video records of a loaded ``tartube.db`` (its ``media_reg_dict``)."""
    reg: Any = None
    if isinstance(root, dict):
        for key in ("media_reg_dict", "media_registry", "media_dict"):
            if isinstance(root.get(key), dict):
                reg = root[key]
                break
    if not isinstance(reg, dict):
        raise ValueError("no media registry in the Tartube database")
    out: list[TartubeRecord] = []
    for key, obj in reg.items():
        if _kind(obj) != "video":
            continue
        a = _attrs(obj)
        vid = _vid(a.get("vid"))
        if vid is None:
            continue
        parent: tuple[str | None, str | None] = (None, None)
        p = a.get("parent_obj")
        for _ in range(8):
            if p is None:
                break
            if _kind(p) in ("channel", "playlist"):
                pa = _attrs(p)
                parent = (_str(pa.get("name")), _str(pa.get("source")))
                break
            p = _attrs(p).get("parent_obj")
        out.append(
            TartubeRecord(
                vid=vid,
                dbid=a.get("dbid") if isinstance(a.get("dbid"), int) else _int(key),
                dl_flag=bool(a.get("dl_flag")),
                file_dir=_str(a.get("file_dir")),
                file_name=_str(a.get("file_name")),
                file_ext=_str(a.get("file_ext")),
                parent=parent,
            )
        )
    return out


# ------------------------------------------------------------------ what was downloaded


@dataclass(slots=True)
class ImportReport:
    records: int = 0
    downloaded: int = 0
    #: dl_flag set and the file is there
    verified: int = 0
    #: dl_flag set, its folder is not on this machine: taken on the flag's word
    unverified: int = 0
    #: dl_flag set, the folder is here and the file is not
    missing: int = 0
    not_downloaded: int = 0
    #: the export said nothing and the database was not read
    unknown: int = 0
    filtered: int = 0
    used_pickle: bool = False
    stubbed: list[str] = field(default_factory=list)
    by_parent: dict[str, int] = field(default_factory=dict)

    def as_json(self) -> dict[str, Any]:
        return {
            "records": self.records,
            "downloaded": self.downloaded,
            "verified": self.verified,
            "unverified": self.unverified,
            "missing": self.missing,
            "not_downloaded": self.not_downloaded,
            "unknown": self.unknown,
            "filtered": self.filtered,
            "used_pickle": self.used_pickle,
            "by_parent": dict(sorted(self.by_parent.items())[:200]),
        }


def _merge(json_recs: list[TartubeRecord], db_recs: Iterable[TartubeRecord]) -> list[TartubeRecord]:
    """The export's records with what the database adds (``dl_flag``, the file)."""
    by_dbid = {r.dbid: r for r in db_recs if r.dbid is not None}
    by_vid = {r.vid: r for r in by_dbid.values()}
    out: list[TartubeRecord] = []
    for r in json_recs:
        d = by_dbid.get(r.dbid) if r.dbid is not None else None
        if d is None or d.vid != r.vid:
            d = by_vid.get(r.vid)
        if d is None:
            out.append(r)
            continue
        out.append(
            TartubeRecord(
                vid=r.vid,
                dbid=r.dbid,
                dl_flag=r.dl_flag if r.dl_flag is not None else d.dl_flag,
                file_dir=r.file_dir or d.file_dir,
                file_name=r.file_name or d.file_name,
                file_ext=r.file_ext or d.file_ext,
                parent=r.parent if r.parent != (None, None) else d.parent,
            )
        )
    return out


def _matches(rec: TartubeRecord, wanted: Iterable[str]) -> bool:
    name, url = rec.parent
    for w in wanted:
        w = w.strip()
        if w and ((name and w.casefold() == name.casefold()) or (url and w in url)):
            return True
    return False


def downloaded_ids(
    path: str,
    *,
    database: str | None = None,
    channels: Iterable[str] = (),
    to_local: Callable[[str], str] = lambda p: p,
) -> tuple[list[str], ImportReport]:
    """The YouTube ids Tartube downloaded, read from ``path`` (a JSON export, or
    ``tartube.db`` itself). ``database``: the ``tartube.db`` to fall back to for what the
    export lacks (default: ``tartube.db`` beside the export, when there is one).
    ``channels``: only videos under these channels or playlists (a name or part of the
    URL); empty: all."""
    rep = ImportReport()
    is_json = path.lower().endswith(".json")
    if is_json:
        recs = read_export(path)
        lacks = any(r.dl_flag is None for r in recs)
        db_path = database or os.path.join(os.path.dirname(path), "tartube.db")
        if lacks and os.path.isfile(db_path):
            unp = _load(db_path, rep)
            recs = _merge(recs, records_from_pickle(unp))
    else:
        recs = records_from_pickle(_load(path, rep))
    rep.records = len(recs)
    wanted = [c for c in channels if c and c.strip()]
    ids: dict[str, None] = {}
    for r in recs:
        if wanted and not _matches(r, wanted):
            rep.filtered += 1
            continue
        if r.dl_flag is None:
            rep.unknown += 1
            continue
        if not r.dl_flag:
            rep.not_downloaded += 1
            continue
        fp = r.file_path()
        if fp is not None:
            local = to_local(fp)
            if os.path.isfile(local):
                rep.verified += 1
            elif os.path.isdir(os.path.dirname(local)):
                rep.missing += 1
                continue
            else:
                rep.unverified += 1
        else:
            rep.unverified += 1
        if r.vid not in ids:
            ids[r.vid] = None
            label = r.parent[0] or r.parent[1] or "(none)"
            rep.by_parent[label] = rep.by_parent.get(label, 0) + 1
    rep.downloaded = len(ids)
    return list(ids), rep


def _load(path: str, rep: ImportReport) -> Any:
    # safe by construction: StubUnpickler imports nothing and refuses code-running globals
    with open(path, "rb") as f:
        u = StubUnpickler(f)
        root = u.load()
    rep.stubbed = sorted(u.stubbed)
    rep.used_pickle = True
    return root
