"""The canonical form of a media path (Epic 31, story 31.3).

A path in the database names the file by the share it lives on, never by where one
machine happens to mount that share::

    smb://<server>/<share>/<path>      a file on a network share (SMB or NFS mount)
    nfs://<server>/<export>/<path>     an NFS export that has no share name
    file:///<absolute path>            a file on a machine's own disk

``<server>`` is the *name* of a ``storage_servers`` record (``nas``), not an address:
the record lists the server's addresses (IPs, host names, the mDNS name), so a new IP
is one edit and no stored path changes. The form is URI-shaped but **not
URL-encoded**: spaces, ``%``, ``#`` and non-ASCII letters are kept as they are, so a
stored path stays readable and ``LIKE 'smb://nas/Originals/%'`` still works.

Rules (all applied by :func:`format_canonical` / :func:`normalise_rel`):

* the server name is lower case; the share keeps the server's spelling and is matched
  case-insensitively (SMB shares and paths are case-insensitive);
* separators are ``/``; no ``.``/``..`` segments, no empty segments, no trailing ``/``;
* text is Unicode NFC (macOS hands out NFD names over SMB; NFC is what Windows,
  Linux and the NAS itself use);
* characters a client cannot show in a name are stored as the server stores them on
  its disk; :class:`CharMap` converts between that and the form a client sees over
  SMB (Samba's ``catia`` mapping or the Services-for-Mac private-use range).
"""

from __future__ import annotations

import posixpath
import re
import unicodedata
from dataclasses import dataclass
from functools import cached_property

__all__ = [
    "SCHEMES",
    "CharMap",
    "SharePath",
    "format_canonical",
    "is_canonical",
    "nfc",
    "normalise_rel",
    "parse_canonical",
    "share_prefix",
]

#: the schemes a canonical path may have
SCHEMES = ("smb", "nfs", "file")

_CANON_RE = re.compile(r"^(smb|nfs)://([^/]+)/([^/]+)(?:/(.*))?$", re.IGNORECASE)


def nfc(text: str) -> str:
    """Unicode NFC; a no-op on ASCII (the common case, checked first)."""
    return text if text.isascii() else unicodedata.normalize("NFC", text)


def normalise_rel(rel: str) -> str:
    """A relative path in canonical shape: ``/`` separators, NFC, no empty / ``.``
    segments, ``..`` refused (it never appears in a stored media path)."""
    parts: list[str] = []
    for seg in rel.replace("\\", "/").split("/"):
        if seg in ("", "."):
            continue
        if seg == "..":
            raise ValueError(f"'..' in a media path: {rel!r}")
        parts.append(nfc(seg))
    return "/".join(parts)


@dataclass(frozen=True, slots=True)
class SharePath:
    """A parsed canonical path. For ``file`` the server and share are empty and
    ``rel`` is the absolute path (``/Users/me/a.mp4`` or ``C:/Videos/a.mp4``)."""

    scheme: str
    server: str
    share: str
    rel: str

    def __str__(self) -> str:
        return format_canonical(self.scheme, self.server, self.share, self.rel)

    @property
    def prefix(self) -> str:
        """``smb://nas/Originals/`` (``file:///`` for a local file)."""
        return share_prefix(self.scheme, self.server, self.share)


def share_prefix(scheme: str, server: str, share: str) -> str:
    scheme = scheme.lower()
    if scheme == "file":
        return "file:///"
    share = share.strip("/")
    return f"{scheme}://{server.lower()}/{share}/"


def format_canonical(scheme: str, server: str, share: str, rel: str = "") -> str:
    """``smb://nas/Originals/Backup/a.mp4``; ``file:///C:/Videos/a.mp4``."""
    scheme = scheme.lower()
    if scheme not in SCHEMES:
        raise ValueError(f"unknown scheme {scheme!r}")
    if scheme == "file":
        p = rel.replace("\\", "/")
        drive = re.match(r"^/?([A-Za-z]):(?:/|$)", p)
        if drive:
            body = normalise_rel(p[drive.end() :])
            return f"file:///{drive.group(1).upper()}:/{body}".rstrip("/")
        return "file:///" + normalise_rel(p)
    if not server or not share:
        raise ValueError("a share path needs a server and a share")
    # an NFS export may have several segments (/volume1/media): keep them, normalised
    share_n = normalise_rel(share)
    body = normalise_rel(rel)
    out = f"{scheme}://{server.lower()}/{share_n}"
    return f"{out}/{body}" if body else out


def parse_canonical(path: str) -> SharePath | None:
    """The parts of a canonical path; ``None`` for anything else (a legacy local
    path such as ``/Volumes/Originals/a.mp4`` or ``Y:/a.mp4``)."""
    if not path:
        return None
    low = path[:7].lower()
    if low.startswith("file://"):
        rest = path[7:]
        if not rest.startswith("/"):
            return None  # file://host/... is not a form we write
        body = rest[1:]
        if re.match(r"^[A-Za-z]:(?:/|$)", body):
            return SharePath("file", "", "", body)
        return SharePath("file", "", "", "/" + body)
    m = _CANON_RE.match(path)
    if not m:
        return None
    return SharePath(m.group(1).lower(), m.group(2).lower(), m.group(3), m.group(4) or "")


def is_canonical(path: str | None) -> bool:
    return bool(path) and parse_canonical(path or "") is not None


# -- characters a client cannot use in a name ---------------------------------------

#: Samba ``vfs_catia`` style table used by Synology and many NAS defaults: the
#: Windows-illegal ASCII characters as their fullwidth twins (``"`` -> ``＂`` U+FF02)
FULLWIDTH: dict[str, str] = {
    '"': "\uff02",
    "*": "\uff0a",
    ":": "\uff1a",
    "<": "\uff1c",
    ">": "\uff1e",
    "?": "\uff1f",
    "\\": "\uff3c",
    "|": "\uff5c",
}

#: Services for Macintosh / ``vfs_fruit`` ``encoding = private`` / Linux ``cifs``
#: ``mapposix``: the private-use range U+F0xx
SFM: dict[str, str] = {
    '"': "\uf020",
    "*": "\uf021",
    ":": "\uf022",
    "<": "\uf023",
    ">": "\uf024",
    "?": "\uf025",
    "\\": "\uf026",
    "|": "\uf027",
}

PRESETS: dict[str, dict[str, str]] = {"none": {}, "fullwidth": FULLWIDTH, "sfm": SFM}


@dataclass(frozen=True)
class CharMap:
    """How a server's on-disk names differ from what an SMB client sees.

    ``spec`` is a preset (``none`` - the default, nothing is changed; ``fullwidth``;
    ``sfm``) or a catia mapping list ``0x22:0xff02,0x3a:0xff1a`` (on-disk character :
    client character). Canonical paths hold the **on-disk** form (what Plex on the NAS
    sees under ``/volume1``); :meth:`to_client` gives the name a client opens over
    SMB, :meth:`to_disk` turns a name a client listed back into the stored form.
    """

    spec: str = "none"

    @cached_property
    def table(self) -> dict[str, str]:
        return parse_char_map(self.spec)

    @property
    def active(self) -> bool:
        return bool(self.table)

    def to_client(self, name: str) -> str:
        t = self.table
        if not t:
            return name
        return "".join(t.get(c, c) for c in name)

    def to_disk(self, name: str) -> str:
        t = self.table
        if not t:
            return name
        back = {v: k for k, v in t.items()}
        return "".join(back.get(c, c) for c in name)


def parse_char_map(spec: str | None) -> dict[str, str]:
    """A preset name or ``0x22:0xff02,...``; raises ``ValueError`` on anything else."""
    s = (spec or "none").strip()
    if s.lower() in PRESETS:
        return dict(PRESETS[s.lower()])
    out: dict[str, str] = {}
    for part in s.split(","):
        part = part.strip()
        if not part:
            continue
        a, sep, b = part.partition(":")
        if not sep:
            raise ValueError(f"char map entry {part!r} is not disk:client")
        disk, client = chr(int(a, 0)), chr(int(b, 0))
        if disk == "/" or client == "/":
            raise ValueError("'/' cannot be mapped")
        out[disk] = client
    return out


def join_rel(prefix: str, rel: str) -> str:
    """``smb://nas/Originals/`` + ``a/b.mp4``."""
    if not rel:
        return prefix.rstrip("/") if prefix != "file:///" else prefix
    return prefix.rstrip("/") + "/" + rel if prefix != "file:///" else prefix + rel


def parent(path: str) -> str:
    """The directory of a canonical (or POSIX-style) path."""
    return posixpath.dirname(path.rstrip("/"))
