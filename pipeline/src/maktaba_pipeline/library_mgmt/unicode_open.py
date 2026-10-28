"""Open a file whose name a network share spells in another Unicode form (Epic 47.3).

Ported from FileUtilities' ``TryOpenFileWithNormalization`` (``EncodeService.cs``): the
NAS keeps Arabic file names in one normalisation form and the Mac asks in another, so an
open of the path as stored can fail although the file is there. Tried in order:

1. the path as given;
2. its NFC form;
3. its NFD form;
4. on macOS, ``ditto`` (which goes through the Finder's own name handling) copies each
   spelling into a work folder, and the copy is opened instead.
"""

from __future__ import annotations

import os
import subprocess
import sys
import unicodedata
from collections.abc import Callable
from dataclasses import dataclass

__all__ = ["OpenedPath", "candidates", "open_fallback"]


@dataclass(frozen=True, slots=True)
class OpenedPath:
    #: the path that opened (one of the spellings, or the copy in the work folder)
    path: str
    #: how: "as_given", "nfc", "nfd" or "ditto"
    how: str

    @property
    def copied(self) -> bool:
        """The file is a copy in the work folder: the caller deletes it when done."""
        return self.how == "ditto"


def candidates(path: str) -> list[tuple[str, str]]:
    """``(how, spelling)`` in the order tried; a spelling equal to an earlier one is
    left out."""
    out: list[tuple[str, str]] = [("as_given", path)]
    for how, cand in (
        ("nfc", unicodedata.normalize("NFC", path)),
        ("nfd", unicodedata.normalize("NFD", path)),
    ):
        if all(cand != p for _, p in out):
            out.append((how, cand))
    return out


def _can_open(path: str) -> bool:
    try:
        with open(path, "rb") as f:
            f.read(1)
        return True
    except OSError:
        return False


def _ditto(src: str, dest: str) -> bool:
    try:
        res = subprocess.run(["ditto", src, dest], capture_output=True, timeout=3600, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return False
    return res.returncode == 0 and os.path.isfile(dest)


def open_fallback(
    path: str,
    work_dir: str | None = None,
    *,
    can_open: Callable[[str], bool] = _can_open,
    ditto: Callable[[str, str], bool] = _ditto,
    platform: str = sys.platform,
) -> OpenedPath:
    """A path to ``path``'s file that opens (see the module doc).

    ``work_dir`` is where the macOS ``ditto`` copy goes (no copy without one). Raises
    :class:`FileNotFoundError` when no spelling opens and no copy could be made.
    """
    spellings = candidates(path)
    for how, cand in spellings:
        if can_open(cand):
            return OpenedPath(cand, how)
    if platform == "darwin" and work_dir is not None:
        os.makedirs(work_dir, exist_ok=True)
        dest = os.path.join(work_dir, unicodedata.normalize("NFC", os.path.basename(path)))
        for _, cand in spellings:
            if ditto(cand, dest) and can_open(dest):
                return OpenedPath(dest, "ditto")
    raise FileNotFoundError(f"cannot open {path} in any Unicode form")
