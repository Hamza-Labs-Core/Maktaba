"""home_secrets from the pipeline (slot 0120, Epic 32 story 32.6): the API's format, the
extension store keeping secrets out of ``extensions.settings``, and a node that cannot
read them."""

from __future__ import annotations

import os
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline import home_secrets as hs
from maktaba_pipeline.engine.graph import GraphBuilder
from maktaba_pipeline.engine.sql import loads, now, to_ts
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.base import Manifest, SettingField
from maktaba_pipeline.extensions.store import REDACTED, ensure_rows, load_state, save_settings
from maktaba_pipeline.runtime import Database

# the vector api/internal/secrets TestCrossLanguageVector opens too
_KEY = bytes(range(32))
_CT = bytes.fromhex("18579b3e54ba13dd6c270b6eee5432d33e9955c1a6f6d4333e9f9d")
_NONCE = bytes.fromhex("6465666768696a6b6c6d6e6f")


@pytest.mark.unit
def test_same_format_as_the_api() -> None:
    assert hs.key_id(_KEY) == "630dcd2966c43366"
    assert hs.open_sealed(_KEY, "ext.plex.token", _CT, _NONCE) == b"PLEX-SECRET"
    with pytest.raises(hs.WrongKeyError):
        hs.open_sealed(_KEY, "cf_api_token", _CT, _NONCE)  # another name
    with pytest.raises(hs.WrongKeyError):
        hs.open_sealed(bytes(32), "ext.plex.token", _CT, _NONCE)  # another key
    ct, nonce = hs.seal(_KEY, "x", b"v")
    assert len(nonce) == 12 and hs.open_sealed(_KEY, "x", ct, nonce) == b"v"
    assert hs.seal(_KEY, "x", b"v")[1] != nonce


@pytest.mark.unit
def test_key_path_and_load(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    assert hs.key_path({"MAKTABA_SECRETS_KEY": "/k/secrets.key"}) == Path("/k/secrets.key")
    assert (
        hs.key_path({"MAKTABA_CONFIG": str(tmp_path / "server.toml")}) == tmp_path / "secrets.key"
    )
    # neither set, Windows: maktaba-server's keys directory (Epic 38 story 38.1: never
    # the roaming profile), or the roaming profile's key of an install from before
    # while there is none in the new place
    monkeypatch.setattr(sys, "platform", "win32")
    win = {"LOCALAPPDATA": str(tmp_path / "L"), "APPDATA": str(tmp_path / "R")}
    cur = tmp_path / "L" / "Maktaba" / "keys" / "secrets.key"
    old = tmp_path / "R" / "Maktaba" / "secrets.key"
    assert hs.key_path(win) == cur
    for p in (old, cur):
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(bytes(32))
        assert hs.key_path(win) == p
    # macOS keeps the XDG path, like Linux: the developer stack's key, which
    # migrate-from-dev moves into Maktaba.app
    for platform in ("darwin", "linux"):
        monkeypatch.setattr(sys, "platform", platform)
        assert (
            hs.key_path({"XDG_CONFIG_HOME": str(tmp_path / "x")})
            == tmp_path / "x" / "maktaba" / "secrets.key"
        )
        assert hs.key_path({}) == Path.home() / ".config" / "maktaba" / "secrets.key"
    monkeypatch.undo()
    path = tmp_path / "c" / "secrets.key"
    assert hs.load_key(path) is None  # the pipeline never makes the key by default
    key = hs.load_key(path, create=True)
    assert key is not None and len(key) == 32 and hs.load_key(path) == key
    if os.name != "nt":
        assert (path.stat().st_mode & 0o777) == 0o600
    bad = tmp_path / "bad.key"
    bad.write_bytes(b"short")
    assert hs.load_key(bad) is None and bad.read_bytes() == b"short"


class TokenExt:
    manifest = Manifest(
        name="tokext",
        version="1",
        description="test",
        settings=(
            SettingField("url", "URL", required=True, env="TOKEXT_URL"),
            SettingField("token", "Token", secret=True, required=True, env="TOKEXT_TOKEN"),
            SettingField("api", "API", secret=True, home_only=True, secret_name="tok_api"),
        ),
    )

    def register(self, graph: GraphBuilder) -> None:
        pass

    def impls(self) -> Mapping[str, StageImpl]:
        return {}

    def is_configured(self, settings: Mapping[str, str]) -> bool:
        return bool(settings.get("url")) and bool(settings.get("token"))


async def _row(db: Any) -> dict[str, Any]:
    r = await db.fetchrow("SELECT settings FROM extensions WHERE name = 'tokext'")
    return dict(loads(r["settings"]) or {})


@pytest.mark.asyncio
async def test_store_keeps_secrets_in_home_secrets(
    db: Database,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    key = hs.load_key(hs.key_path(), create=True)  # the API made it (tmp, conftest)
    assert key is not None
    monkeypatch.setenv("TOKEXT_URL", "http://env")
    monkeypatch.setenv("TOKEXT_TOKEN", "envtoken")
    ext = TokenExt()
    await ensure_rows(db, [ext])
    assert "token" not in await _row(db)  # the env token went to home_secrets
    assert await hs.get_secret(db, "ext.tokext.token") == "envtoken"
    monkeypatch.delenv("TOKEXT_TOKEN")
    st = await load_state(db, ext)
    assert st.configured and st.settings["token"] == "envtoken"

    await save_settings(db, ext, {"url": "http://db", "token": "new"})
    assert await _row(db) == {"url": "http://db"}
    assert (await load_state(db, ext)).settings["token"] == "new"
    await save_settings(db, ext, {"token": REDACTED})
    assert (await load_state(db, ext)).settings["token"] == "new"

    # home-only secrets (the Cloudflare token) are never loaded into a worker
    await hs.put_secret(db, "tok_api", "cf-secret", key)
    assert "api" not in (await load_state(db, ext)).settings


@pytest.mark.asyncio
async def test_a_token_still_in_the_row_is_used_until_moved(
    db: Database,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.delenv("TOKEXT_URL", raising=False)
    monkeypatch.delenv("TOKEXT_TOKEN", raising=False)
    await db.execute(
        "INSERT INTO extensions (name, enabled, settings, updated_at) VALUES ($1, $2, $3, $4)",
        "tokext",
        True,
        '{"url":"http://p","token":"legacy"}',
        to_ts(db, now()),
    )
    st = await load_state(db, TokenExt())
    assert st.configured and st.settings["token"] == "legacy"


@pytest.mark.asyncio
async def test_without_the_key_a_secret_is_absent(
    db: Database,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    """A node: no key file (and on Postgres no right on the table): the token is absent,
    so the extension is unconfigured there."""
    await hs.put_secret(db, "ext.tokext.token", "t", _KEY)
    await db.execute(
        "INSERT INTO extensions (name, enabled, settings, updated_at) VALUES ($1, $2, $3, $4)",
        "tokext",
        True,
        '{"url":"http://p"}',
        to_ts(db, now()),
    )
    monkeypatch.setenv("MAKTABA_SECRETS_KEY", str(tmp_path / "none.key"))
    monkeypatch.delenv("TOKEXT_TOKEN", raising=False)
    st = await load_state(db, TokenExt())
    assert not st.configured and not st.settings.get("token")
    with pytest.raises(RuntimeError):
        await save_settings(db, TokenExt(), {"token": "x"})
