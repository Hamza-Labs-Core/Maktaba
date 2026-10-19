"""Which network shares this machine has mounted, and where (story 31.3).

Each OS has its own source; the parsers take the text so tests run on recorded
outputs, :func:`discover` runs the live commands:

* **Windows** - every drive letter's remote name (``WNetGetConnectionW``, so share
  names with spaces and localised ``net use`` headers are no problem), then the
  ``net use`` list for UNC connections without a letter;
* **macOS** - ``mount`` (``//user@192.168.50.94/Originals on /Volumes/Originals
  (smbfs, ...)``; share names arrive URL-encoded);
* **Linux** - ``/proc/mounts`` (``cifs`` / ``smb3`` / ``nfs`` / ``nfs4``; mount points
  escape spaces as ``\\040``).

A :class:`Mount` is ``(protocol, host, share, local)``: ``host`` exactly as the mount
names it (an IP, a host name or an mDNS name - the resolver matches it against the
addresses of a ``storage_servers`` record), ``share`` the SMB share (``Originals``,
possibly followed by a sub folder: ``Originals/Backup``) or the NFS export path
(``/volume1/Originals``), ``local`` the mount point, drive (``O:/``) or UNC root
(``//192.168.50.94/Originals/``).
"""

from __future__ import annotations

import contextlib
import platform
import re
import string
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import unquote

__all__ = [
    "Mount",
    "discover",
    "parse_mac_mount",
    "parse_net_use",
    "parse_proc_mounts",
    "windows_drive_mounts",
]

SMB_FS = frozenset({"cifs", "smb3", "smbfs", "smb"})
NFS_FS = frozenset({"nfs", "nfs4"})
PROC_MOUNTS = "/proc/mounts"


@dataclass(frozen=True, slots=True)
class Mount:
    protocol: str  #: ``smb`` | ``nfs``
    host: str
    share: str
    local: str  #: forward slashes, trailing ``/``
    source: str  #: ``drive`` | ``net use`` | ``mount`` | ``proc``

    def as_json(self) -> dict[str, str]:
        return {
            "protocol": self.protocol,
            "host": self.host,
            "share": self.share,
            "local": self.local,
            "source": self.source,
        }


def _dir(p: str) -> str:
    p = p.replace("\\", "/")
    return p if p.endswith("/") else p + "/"


def _host(raw: str) -> str:
    """``user;domain@host:445`` / ``[fe80::1]`` -> the bare host, lower case."""
    h = raw.rsplit("@", 1)[-1]
    if h.startswith("["):
        h = h[1:].split("]", 1)[0]
    elif h.count(":") == 1:
        h = h.split(":", 1)[0]
    h = h.strip().lower()
    # macOS names a share it found by Bonjour "Home-NAS._smb._tcp.local"
    m = re.match(r"^(.+?)\._(?:smb|afpovertcp|nfs)\._tcp\.(local)\.?$", h)
    return f"{m.group(1)}.{m.group(2)}" if m else h.rstrip(".")


def _unc(remote: str) -> tuple[str, str] | None:
    """``\\\\host\\share\\sub`` -> ``(host, share/sub)``."""
    r = remote.replace("\\", "/")
    m = re.match(r"^//([^/]+)/(.+?)/?$", r)
    if not m:
        return None
    return _host(m.group(1)), m.group(2)


def parse_net_use(text: str) -> list[Mount]:
    """``net use`` output: drive mappings and UNC connections without a letter.

    Lines look like ``OK           O:        \\\\192.168.50.94\\Originals  Microsoft
    Windows Network`` (the status and the network name columns are localised, the
    drive and the remote are not). A remote with spaces is cut at the first run of
    two spaces; :func:`windows_drive_mounts` gives exact names for lettered drives.
    """
    out: list[Mount] = []
    for line in text.splitlines():
        m = re.search(r"(?:^|\s)([A-Za-z]:)?\s+(\\\\[^\\\s]+\\\S.*?)(?:\s{2,}|$)", line)
        if not m:
            continue
        parsed = _unc(m.group(2).strip())
        if parsed is None:
            continue
        host, share = parsed
        if share.rstrip("$").upper() == "IPC" or share.endswith("$"):
            continue  # IPC$ / admin shares: not media
        drive = m.group(1)
        local = _dir(f"{drive.upper()}/") if drive else _dir(f"//{host}/{share}")
        out.append(Mount("smb", host, share, local, "net use"))
    return out


def parse_mac_mount(text: str) -> list[Mount]:
    """macOS ``mount`` output (smbfs and nfs lines; afp and local disks ignored)."""
    out: list[Mount] = []
    for line in text.splitlines():
        m = re.match(r"^(.+?) on (.+) \(([^,)]+)", line.strip())
        if not m:
            continue
        spec, point, fs = m.group(1), m.group(2), m.group(3).strip().lower()
        if fs in SMB_FS:
            sm = re.match(r"^//([^/]+)/(.+)$", spec)
            if not sm:
                continue
            out.append(
                Mount(
                    "smb", _host(unquote(sm.group(1))), unquote(sm.group(2)), _dir(point), "mount"
                )
            )
        elif fs in NFS_FS:
            host, sep, export = spec.partition(":/")
            if sep:
                out.append(Mount("nfs", _host(host), "/" + export, _dir(point), "mount"))
    return out


def _unescape_octal(s: str) -> str:
    return re.sub(r"\\([0-7]{3})", lambda m: chr(int(m.group(1), 8)), s)


def parse_proc_mounts(text: str) -> list[Mount]:
    """``/proc/mounts`` (or ``/proc/self/mountinfo``-free ``mount`` lines) on Linux."""
    out: list[Mount] = []
    for line in text.splitlines():
        parts = line.split()
        if len(parts) < 3:
            continue
        spec, point, fs = _unescape_octal(parts[0]), _unescape_octal(parts[1]), parts[2].lower()
        if fs in SMB_FS:
            parsed = _unc(spec)
            if parsed:
                out.append(Mount("smb", parsed[0], parsed[1], _dir(point), "proc"))
        elif fs in NFS_FS:
            host, sep, export = spec.partition(":/")
            if sep:
                out.append(Mount("nfs", _host(host), "/" + export, _dir(point), "proc"))
    return out


def _wnet_remote(letter: str) -> str | None:
    """The UNC a drive letter is mapped to (``WNetGetConnectionW``), ``None`` if local."""
    if sys.platform != "win32":
        return None
    import ctypes
    from ctypes import wintypes

    mpr = ctypes.WinDLL("mpr")  # type: ignore[attr-defined,unused-ignore]
    fn = mpr.WNetGetConnectionW
    fn.argtypes = [wintypes.LPCWSTR, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)]
    fn.restype = wintypes.DWORD
    size = wintypes.DWORD(1024)
    buf = ctypes.create_unicode_buffer(size.value)
    if fn(f"{letter}:", buf, ctypes.byref(size)) != 0:
        return None
    return buf.value or None


def windows_drive_mounts(remote_of: object = None) -> list[Mount]:
    """Mapped drive letters through ``WNetGetConnectionW`` (``remote_of`` for tests:
    a callable ``letter -> UNC | None``)."""
    get = remote_of if callable(remote_of) else _wnet_remote
    out: list[Mount] = []
    for letter in string.ascii_uppercase:
        try:
            remote = get(letter)
        except OSError:
            remote = None
        if not remote:
            continue
        parsed = _unc(str(remote))
        if parsed:
            out.append(Mount("smb", parsed[0], parsed[1], f"{letter}:/", "drive"))
    return out


def _run(cmd: list[str]) -> str:
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=10, check=False).stdout
    except (OSError, subprocess.SubprocessError):
        return ""


def discover(system: str | None = None) -> list[Mount]:
    """This machine's network mounts now; unique by local root, drives first."""
    system = system or platform.system()
    found: list[Mount] = []
    if system == "Windows":
        found += windows_drive_mounts()
        found += parse_net_use(_run(["net", "use"]))
    elif system == "Darwin":
        found += parse_mac_mount(_run(["/sbin/mount"]) or _run(["mount"]))
    else:
        text = ""
        with contextlib.suppress(OSError):
            text = Path(PROC_MOUNTS).read_text(encoding="utf-8", errors="replace")
        found += parse_proc_mounts(text)
    seen: set[str] = set()
    out: list[Mount] = []
    for m in found:
        key = m.local.lower() if system == "Windows" else m.local
        if key not in seen:
            seen.add(key)
            out.append(m)
    return out
