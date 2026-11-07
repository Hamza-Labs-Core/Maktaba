"""The runner's own trust check (Epic 54, story 54.22 §2, 54.23 §1 step 1).

The API trusts only the first-party bundle built into its binary; the runner checks its own
copy before it imports any plugin: the bundle and every file of its group's plugin packages
must be the bytes the release's signed pipeline wheel installed (each file's sha256 in the
wheel's ``RECORD``). A changed byte refuses the runner (``plugin_trusted_unsigned``) and
nothing of its group runs. The worker then checks the bundle's digest the runner reports
in ``hello`` against its own (:func:`maktaba_pipeline.plugins.native.hello`).

A developer checkout (an editable install, whose files are the working tree) has no RECORD
to check against: the check answers ``developer`` and logs it; a release never installs one.
"""

from __future__ import annotations

import base64
import hashlib
import json
from importlib import metadata
from pathlib import Path

__all__ = ["Untrusted", "check"]

DIST = "maktaba-pipeline"


class Untrusted(RuntimeError):
    def __init__(self, code: str, detail: str) -> None:
        super().__init__(f"{code}: {detail}")
        self.code = code
        self.detail = detail


def _editable(dist: metadata.Distribution) -> bool:
    raw = dist.read_text("direct_url.json")
    if not raw:
        return False
    try:
        return bool(json.loads(raw).get("dir_info", {}).get("editable"))
    except ValueError:
        return False


def check(packages: list[str]) -> str:
    """``release`` when every file is the installed one, ``developer`` for a checkout;
    raises :class:`Untrusted` otherwise. ``packages`` are top-level package names
    (``maktaba_fp_classify``); the bundle is always checked."""
    try:
        dist = metadata.distribution(DIST)
    except metadata.PackageNotFoundError as exc:
        raise Untrusted("plugin_trusted_unsigned", "the pipeline is not installed") from exc
    if _editable(dist):
        return "developer"
    wanted = {"maktaba_pipeline/plugins/data/firstparty.json"}
    record = {str(f): f for f in dist.files or ()}
    for pkg in packages:
        wanted |= {k for k in record if k.startswith(pkg + "/") and not k.endswith(".pyc")}
    for name in sorted(wanted):
        f = record.get(name)
        if f is None or f.hash is None:
            raise Untrusted("plugin_trusted_unsigned", f"{name} is not in the release's wheel")
        if f.hash.mode != "sha256":
            raise Untrusted("plugin_trusted_unsigned", f"{name}: {f.hash.mode}")
        data = Path(str(f.locate())).read_bytes()
        got = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
        if got != f.hash.value:
            raise Untrusted("plugin_trusted_unsigned", f"{name} changed since it was installed")
    return "release"
