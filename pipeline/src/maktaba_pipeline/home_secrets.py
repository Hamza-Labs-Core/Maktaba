"""The home's sealed secrets (slot 0120 ``home_secrets``, Epic 32 story 32.6).

The same format as the home's API (``api/internal/secrets``): AES-256-GCM under a
32-byte key file in the home's config directory (``secrets.key``, mode 0600), the
row name as the additional data, a fresh 12-byte nonce per value, and ``key_id`` =
hex of ``sha256(key)[:8]``. The key never enters the database.

Only the home reads these: the machines' group role (``maktaba_worker``) has no right
on the table and no key file, so on a node every read answers ``None`` and the
stages that need a secret stay on the home (``placement="home"``).

Names: ``ext.<extension>.<field>`` for an extension's secret setting (the Plex
token is ``ext.plex.token``); ``cf_api_token`` and ``cf_signing_key`` belong to the
API's Cloudflare integration and are never read here.
"""

from __future__ import annotations

import hashlib
import os
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from .engine.sql import now, to_ts
from .log import get_logger

__all__ = [
    "KEY_FILE_NAME",
    "KEY_SIZE",
    "WrongKeyError",
    "delete_secret",
    "extension_secret_name",
    "get_secret",
    "key_id",
    "key_path",
    "load_key",
    "open_sealed",
    "put_secret",
    "seal",
]

KEY_SIZE = 32
KEY_FILE_NAME = "secrets.key"

_log = get_logger()


class WrongKeyError(Exception):
    """The value was sealed with another key (or for another name, or changed)."""


def extension_secret_name(extension: str, field: str) -> str:
    return f"ext.{extension}.{field}"


def key_path(env: Mapping[str, str] | None = None) -> Path:
    """``MAKTABA_SECRETS_KEY``, else beside ``MAKTABA_CONFIG`` (server.toml), else the
    platform's (what ``secrets.KeyPath`` resolves in Go): on Windows maktaba-server's
    keys directory, ``%LOCALAPPDATA%\\Maktaba\\keys`` (Epic 38), or the
    ``%APPDATA%\\Maktaba\\secrets.key`` of an install from before while there is none in
    the new place (maktaba-server copies it there and keeps the original); elsewhere,
    macOS too, the XDG config directory (the developer stack's key, which
    migrate-from-dev moves into Maktaba.app)."""
    env = os.environ if env is None else env
    explicit = env.get("MAKTABA_SECRETS_KEY", "").strip()
    if explicit:
        return Path(explicit)
    cfg = env.get("MAKTABA_CONFIG", "").strip()
    if cfg:
        return Path(cfg).parent / KEY_FILE_NAME
    home = Path.home()
    if sys.platform == "win32":
        local = env.get("LOCALAPPDATA", "")
        cur = (Path(local) if local else home / "AppData" / "Local") / "Maktaba" / "keys"
        if (cur / KEY_FILE_NAME).is_file():
            return cur / KEY_FILE_NAME
        appdata = env.get("APPDATA", "")
        old = (Path(appdata) if appdata else home / "AppData" / "Roaming") / "Maktaba"
        return (old if (old / KEY_FILE_NAME).is_file() else cur) / KEY_FILE_NAME
    xdg = env.get("XDG_CONFIG_HOME", "")
    base = Path(xdg) / "maktaba" if xdg else home / ".config" / "maktaba"
    return base / KEY_FILE_NAME


def key_id(key: bytes) -> str:
    return hashlib.sha256(key).digest()[:8].hex()


def load_key(path: Path | None = None, *, create: bool = False) -> bytes | None:
    """The key, or None when the file is absent (and ``create`` is false) or unusable.

    A file of the wrong size is never replaced: that would make every stored secret
    unreadable."""
    path = path or key_path()
    try:
        data = path.read_bytes()
    except FileNotFoundError:
        if not create:
            return None
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        raw = os.urandom(KEY_SIZE)
        try:
            fd = os.open(
                path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_BINARY", 0), 0o600
            )
        except FileExistsError:  # created by the API (or another worker) meanwhile
            return load_key(path)
        with os.fdopen(fd, "wb") as fh:
            fh.write(raw)
        return raw
    except OSError as exc:
        _log.warning("secrets_key_unreadable", path=str(path), error=str(exc))
        return None
    if len(data) != KEY_SIZE:
        _log.warning("secrets_key_wrong_size", path=str(path), size=len(data))
        return None
    return data


def seal(key: bytes, name: str, plain: bytes) -> tuple[bytes, bytes]:
    nonce = os.urandom(12)
    return AESGCM(key).encrypt(nonce, plain, name.encode()), nonce


def open_sealed(key: bytes, name: str, ciphertext: bytes, nonce: bytes) -> bytes:
    try:
        return AESGCM(key).decrypt(bytes(nonce), bytes(ciphertext), name.encode())
    except Exception as exc:  # InvalidTag, wrong nonce size
        raise WrongKeyError(name) from exc


async def get_secret(db: Any, name: str, key: bytes | None = None) -> str | None:
    """The secret's value, or None: no row, no key file, sealed with another key, or
    the table not readable by this role (a node)."""
    try:
        row = await db.fetchrow(
            "SELECT ciphertext, nonce, key_id FROM home_secrets WHERE name = $1", name
        )
    except Exception as exc:  # noqa: BLE001 - a node's role is refused; an old schema has no table
        _log.info("home_secrets_unreadable", name=name, error=type(exc).__name__)
        return None
    if row is None:
        return None
    key = key if key is not None else load_key()
    if key is None:
        _log.warning("home_secret_without_key", name=name)
        return None
    if str(row["key_id"]) != key_id(key):
        _log.warning("home_secret_other_key", name=name)
        return None
    try:
        return open_sealed(key, name, row["ciphertext"], row["nonce"]).decode()
    except (WrongKeyError, UnicodeDecodeError):
        _log.warning("home_secret_unopenable", name=name)
        return None


async def put_secret(db: Any, name: str, value: str, key: bytes) -> None:
    ct, nonce = seal(key, name, value.encode())
    await db.execute(
        "INSERT INTO home_secrets (name, ciphertext, nonce, key_id, updated_at)"
        " VALUES ($1, $2, $3, $4, $5)"
        " ON CONFLICT (name) DO UPDATE SET ciphertext = EXCLUDED.ciphertext,"
        " nonce = EXCLUDED.nonce, key_id = EXCLUDED.key_id, updated_at = EXCLUDED.updated_at",
        name,
        ct,
        nonce,
        key_id(key),
        to_ts(db, now()),
    )


async def delete_secret(db: Any, name: str) -> None:
    await db.execute("DELETE FROM home_secrets WHERE name = $1", name)
