"""``maktaba-pipeline join``: ask the home to take this machine (design sections 3, 7, 8).

1. Keep (or make) this machine's identity: a node id and an X25519 key pair, saved in
   the node config before anything is sent, so asking again is the same machine.
2. Find the home (:mod:`.discover`, or ``--home URL``).
3. ``POST /api/cluster/join`` with the node id, name, OS, architecture, versions,
   capabilities, the storage it sees and its public key; the home answers with its own
   public key for this request.
4. Show the six-digit pairing code both sides derive; the admin types it on the home.
5. Poll ``GET /api/cluster/join/{node_id}?pubkey=`` every 2 s (up to 15 min); once
   approved, open the sealed credentials and store them in the node config.
"""

from __future__ import annotations

import json
import os
import platform
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections.abc import Callable
from dataclasses import replace
from datetime import UTC, datetime
from pathlib import Path
from typing import IO, Any

from .. import __version__
from ..engine.workers import stable_hostname
from . import config as node_config
from .capabilities import capabilities
from .config import HomeRef, NodeConfig
from .crypto import (
    b64d,
    format_code,
    generate_keypair,
    keypair_from_private,
    open_sealed,
    sas_code,
    seal_key,
    shared_secret,
)
from .discover import Home, discover_homes
from .storage import candidate_roots

__all__ = ["JoinError", "join", "join_request_body", "pick_home"]

POLL_SEC = 2.0
WAIT_SEC = 15 * 60.0
HTTP_TIMEOUT = 10.0

_MESSAGES = {
    "joins_closed": "The home is not accepting new machines. On the home, open Admin > "
    "Machines and turn on 'Accept new machines', then run this again.",
    "already_member": "The home already has this machine. Remove it on the home's Machines "
    "page to join it again.",
    "rate_limited": "The home is refusing requests for a moment (too many). Try again in a minute.",
    "pending_elsewhere": "The home already has a request for this machine made with another "
    "key (another copy of its settings?). It expires within 15 minutes; or reject it on the "
    "home's Machines page, then run this again.",
}


class JoinError(RuntimeError):
    """The join did not complete; the message is for the person at the terminal."""


def _http(method: str, url: str, body: dict[str, Any] | None = None) -> tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:  # noqa: S310
            status, raw = resp.status, resp.read()
    except urllib.error.HTTPError as exc:
        status, raw = exc.code, exc.read()
    except (urllib.error.URLError, OSError) as exc:
        raise JoinError(f"cannot reach the home at {url}: {exc}") from exc
    try:
        payload = json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        payload = {}
    return status, payload if isinstance(payload, dict) else {}


def _error_code(payload: dict[str, Any]) -> str:
    """The API's error code; ``{"error": "joins_closed"}`` or ``{"code": ...}`` or
    ``{"error": {"code": ...}}``."""
    err = payload.get("error")
    if isinstance(err, dict):
        return str(err.get("code") or err.get("message") or "")
    return str(err or payload.get("code") or "")


def join_request_body(cfg: NodeConfig, public_b64: str) -> dict[str, Any]:
    caps = capabilities()
    return {
        "node_id": cfg.node_id,
        "name": cfg.name,
        "os": caps["os"],
        "arch": caps["arch"],
        "versions": {"pipeline": __version__, "python": platform.python_version()},
        "capabilities": caps,
        # what it can see before it knows the library: the mount points it would try
        "storage": {"candidates": candidate_roots()},
        "pubkey": public_b64,
    }


def pick_home(homes: list[Home], *, ask: Callable[[str], str] | None, out: IO[str]) -> Home:
    if not homes:
        raise JoinError(
            "No Maktaba home found on this network. Pass its address: "
            "maktaba-pipeline join --home http://<home-ip>:8080"
        )
    if len(homes) == 1 or ask is None:
        open_ones = [h for h in homes if h.join is not False]
        return (open_ones or homes)[0]
    out.write("Maktaba homes on this network:\n")
    for i, h in enumerate(homes, 1):
        out.write(f"  {i}. {h.label()}\n")
    while True:
        answer = ask(f"Join which one? [1-{len(homes)}] ").strip()
        if answer.isdigit() and 1 <= int(answer) <= len(homes):
            return homes[int(answer) - 1]
        out.write("Type the number of a home.\n")


def _identity(name: str | None, path: Path | None) -> NodeConfig:
    """The saved identity, or a new one (saved before anything is sent)."""
    existing = node_config.load(path)
    if existing is not None:
        keypair_from_private(b64d(existing.private_key))  # a broken key fails here
        return replace(existing, name=name) if name else existing
    kp = generate_keypair()
    cfg = NodeConfig(
        node_id=str(uuid.uuid4()),
        name=name or stable_hostname(),
        private_key=kp.private_b64,
    )
    node_config.save(cfg, path)
    return cfg


REJOIN_COMMAND = "maktaba-pipeline join --rejoin"


def _start_over(
    path: Path,
    *,
    name: str | None,
    out: IO[str],
    ask: Callable[[str], str] | None,
    assume_yes: bool,
) -> None:
    """``join --rejoin``: keep the old node config as ``node.json.bak``, forget it."""
    old = node_config.load(path)
    if old is None:
        return  # nothing to start over from: a plain join
    where = old.home.name or old.home.url or "its home"
    question = (
        f"This machine is {old.name} (node {old.node_id}) of {where}. Join again as a new"
        " machine? [y/N] "
    )
    if not assume_yes:
        if ask is None:
            raise JoinError("re-joining needs a confirmation: run it again with --yes")
        if ask(question).strip().lower() not in ("y", "yes"):
            raise JoinError("re-join cancelled; nothing changed")
    backup = path.with_name(path.name + ".bak")
    os.replace(path, backup)
    out.write(
        f"Kept the old settings as {backup}.\n"
        "The home will see this machine as a new one: remove the old record"
        f" ({old.name}, node {old.node_id}) on the home's Machines page, and set this"
        " machine's path map and queue switches again once it is approved.\n"
    )


def join(
    *,
    home_url: str | None = None,
    name: str | None = None,
    path: Path | None = None,
    out: IO[str] | None = None,
    ask: Callable[[str], str] | None = None,
    discover: Callable[[str | None], list[Home]] = discover_homes,
    sleep: Callable[[float], None] = time.sleep,
    clock: Callable[[], float] = time.monotonic,
    poll_sec: float = POLL_SEC,
    wait_sec: float = WAIT_SEC,
    rejoin: bool = False,
    assume_yes: bool = False,
) -> NodeConfig:
    """Run the join; returns the saved, joined node config. Raises :class:`JoinError`.

    ``rejoin``: a machine that already joined (or joined before the home had a cluster
    key) starts over — after a confirmation (``ask``, or ``assume_yes``) its node config
    is kept as ``node.json.bak`` and a fresh identity asks to join."""
    out = out or sys.stdout
    path = path or node_config.config_path()
    if rejoin:
        _start_over(path, name=name, out=out, ask=ask, assume_yes=assume_yes)
    cfg = _identity(name, path)
    if cfg.joined:
        out.write(
            f"This machine has already joined {cfg.home.name or cfg.home.url}"
            f" (node {cfg.node_id}).\n"
        )
        if not cfg.home.cluster_pub:
            out.write(
                "It joined before the home had a cluster key, so it has no live channel:"
                " run maktaba-pipeline join --rejoin\n"
            )
        return cfg
    kp = keypair_from_private(b64d(cfg.private_key))

    out.write("Looking for the Maktaba home on this network...\n")
    home = pick_home(discover(home_url), ask=ask, out=out)
    base = home.url.rstrip("/")
    out.write(f"Asking {home.name or base} to take this machine ({cfg.name})...\n")

    status, resp = _http("POST", f"{base}/api/cluster/join", join_request_body(cfg, kp.public_b64))
    if status == 403:
        raise JoinError(_MESSAGES["joins_closed"])
    if status == 409:
        code = _error_code(resp)
        raise JoinError(_MESSAGES.get(code) or _MESSAGES["already_member"])
    if status == 429:
        raise JoinError(_MESSAGES["rate_limited"])
    if status not in (200, 201, 202):
        code = _error_code(resp)
        raise JoinError(_MESSAGES.get(code) or f"the home refused the request ({status} {code})")
    try:
        home_pub = b64d(str(resp["home_pubkey"]))
    except (KeyError, ValueError) as exc:
        raise JoinError("the home's answer has no usable key") from exc
    home_info = resp.get("home") or {}

    shared = shared_secret(kp.private, home_pub)
    code = sas_code(shared, kp.public, home_pub)
    bar = "=" * 44
    out.write(
        f"\n{bar}\n  Pairing code:   {format_code(code)}\n{bar}\n\n"
        "On the home, open Admin > Machines, find this machine"
        f" ({cfg.name}) and enter this code.\nWaiting for approval (up to 15 minutes)...\n"
    )
    out.flush()

    poll_url = (
        f"{base}/api/cluster/join/{urllib.parse.quote(cfg.node_id)}"
        f"?pubkey={urllib.parse.quote(kp.public_b64, safe='')}"
    )
    deadline = clock() + wait_sec
    while True:
        status, resp = _http("GET", poll_url)
        state = str(resp.get("state") or "")
        if status == 200 and state == "approved" and resp.get("sealed"):
            break
        if state in ("rejected", "expired", "revoked"):
            raise JoinError(f"the home {state} this request")
        if status in (403, 404, 410) and not state:
            raise JoinError(f"the home no longer knows this request ({status})")
        if clock() >= deadline:
            raise JoinError("no approval within 15 minutes; run join again for a new code")
        sleep(poll_sec)

    try:
        plain = open_sealed(seal_key(shared, kp.public, home_pub), cfg.node_id, resp["sealed"])
        payload = json.loads(plain)
    except Exception as exc:  # noqa: BLE001 - InvalidTag, bad JSON: the same answer
        raise JoinError(
            "the home's credentials could not be opened with this machine's key"
        ) from exc
    if str(payload.get("node_id", cfg.node_id)) != cfg.node_id or not payload.get("database_url"):
        raise JoinError("the home's credentials are not for this machine")
    sealed_home = payload.get("home") or {}
    joined = replace(
        cfg,
        database_url=str(payload["database_url"]),
        home=HomeRef(
            url=str(sealed_home.get("api_url") or base),
            instance_id=str(sealed_home.get("instance_id") or home_info.get("instance_id") or ""),
            name=str(sealed_home.get("name") or home_info.get("name") or home.name),
            cluster_pub=str(sealed_home.get("cluster_pub") or ""),
        ),
        joined_at=datetime.now(UTC).isoformat().replace("+00:00", "Z"),
    )
    saved = node_config.save(joined, path)
    out.write(
        f"Approved. This machine joined {joined.home.name or base}; settings saved to {saved}\n"
    )
    return joined
