"""TLS for the pipeline's Postgres connections, as a DSN asks it (libpq's ``sslmode``).

asyncpg reads ``sslmode`` / ``sslrootcert`` / ``sslcert`` / ``sslkey`` from a URL itself,
but a URL is also where a mode that tries TLS and then plain (``prefer``) hides: a node
whose home requires its certificate then logged in in plain text and was refused ("no
pg_hba.conf entry … no encryption"). So a DSN that asks for TLS (``require``,
``verify-ca``, ``verify-full``) is connected with an :class:`ssl.SSLContext` made here
from it, passed as ``ssl=`` to every connect and pool: TLS or no connection, never a plain
retry. ``verify-ca`` checks the server's certificate against the CA, ``verify-full`` its
name too; ``require`` verifies when the DSN names a CA (as libpq does) and only encrypts
otherwise. The client certificate (``sslcert``, its key ``sslkey``) is offered whenever
the DSN names it. Any other mode is left to asyncpg as the DSN says.
"""

from __future__ import annotations

import ssl
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

__all__ = ["TLSConfigError", "connect_args"]

#: the URL's TLS settings this module turns into the context
_TLS_KEYS = ("sslmode", "sslrootcert", "sslcert", "sslkey")
_STRICT = ("require", "verify-ca", "verify-full")


class TLSConfigError(ValueError):
    """The DSN asks for TLS this machine cannot set up (no CA for a verify mode)."""


def connect_args(dsn: str) -> tuple[str, ssl.SSLContext | None]:
    """``dsn`` without its TLS settings and the context they make (None, and the dsn as
    it is, when it asks for no TLS that must hold)."""
    parts = urlsplit(dsn)
    query = parse_qsl(parts.query, keep_blank_values=True)
    opts = {k: v for k, v in query if k in _TLS_KEYS}
    mode = opts.get("sslmode", "").strip().lower()
    if mode not in _STRICT:
        return dsn, None
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_2
    root = opts.get("sslrootcert", "").strip()
    if mode == "require" and not root:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    else:
        if not root:
            raise TLSConfigError(f"sslmode={mode} names no CA (sslrootcert)")
        ctx.check_hostname = mode == "verify-full"
        ctx.verify_mode = ssl.CERT_REQUIRED
        ctx.load_verify_locations(cafile=root)
    cert = opts.get("sslcert", "").strip()
    if cert:
        ctx.load_cert_chain(cert, keyfile=opts.get("sslkey", "").strip() or None)
    rest = [(k, v) for k, v in query if k not in _TLS_KEYS]
    return urlunsplit(parts._replace(query=urlencode(rest))), ctx
