"""Named statements (Epic 54, story 54.24 §1).

Each first-party plugin keeps its SQL in ``queries/<name>.sql``, one statement per file,
with a header the build checks and the API enforces::

    -- name: promo_links.run.2
    -- mode: write                 (read | write)
    -- scope: library $1           (a check, or several: subject $1, season $2; or catalog)
    -- tables: promo_links          (comma separated; ``table*`` marks a table written)
    DELETE FROM promo_links WHERE library_id = $1

The text after the header is the statement exactly as the plugin's code passes it, with
its ``$n`` placeholders: the code keeps calling ``db.fetch(text, …)``, and the native
database (:class:`maktaba_plugin_sdk.native.StatementDB`) sends only the statement's
**name** and its arguments; the API runs the text it registered under that name from the
release, never text the plugin sent.

``scope`` names the argument the API checks against the job's token: ``subject $N`` must be
the job's subject, ``library $N`` one of the token's libraries, ``settings $N`` the key of
one of the ``rules.*`` sections the manifest lists; any other kind (``season $N``,
``transcript $N``, ``youtube_channel $N``, …: :data:`ROW_SCOPES`) an id of a row the API
looks up, whose library (and video) must be the job's. A write lists every check its rows
need, and a read a check for every ``$n`` it compares an id column to, comma separated
(``subject $1, season $4``), and the API runs each one; the build
(``tools/plugins/statements_build.py``) works them out from the statement's parse tree and
``tools/plugins/scope_tables.toml``. ``catalog`` statements read or write rows that belong
to no one subject (the series catalog, a plan). A forged argument is ``403 plugin_scope``.
"""

from __future__ import annotations

import hashlib
import re
from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from typing import Any

__all__ = [
    "ROW_SCOPES",
    "SCOPES",
    "Registry",
    "Statement",
    "StatementError",
    "key",
    "parse_file",
    "parse_scope",
    "render_file",
    "render_scope",
]

#: kinds of ids the API resolves to a row's library (and video) with a lookup of its own
#: (api/internal/plugins/statements/resolve.go): one per kind in tools/plugins/scope_tables.toml
ROW_SCOPES = (
    "audio_cache",
    "audio_track",
    "clip_group",
    "collection",
    "dup_group",
    "file_action",
    "job",
    "job_subject",
    "segment",
    "season",
    "series",
    "topic_collection",
    "transcript",
    "youtube_channel",
    "youtube_playlist",
    "youtube_source",
    "youtube_video",
)
SCOPES = ("subject", "library", "settings", "catalog", *ROW_SCOPES)
_HEADER = re.compile(r"^--\s*([a-z]+):\s*(.*?)\s*$")
_NAME = re.compile(r"^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$")


class StatementError(ValueError):
    """A statement file that does not follow the header rules."""


def key(text: str) -> str:
    """The lookup key of a statement's text: the text without leading or trailing space."""
    return text.strip()


def digest(text: str) -> str:
    return hashlib.sha256(key(text).encode()).hexdigest()


@dataclass(frozen=True, slots=True)
class Statement:
    name: str
    text: str
    mode: str = "read"
    scope: str = "catalog"
    #: the 1-based ``$n`` the scope checks (0 for catalog)
    scope_arg: int = 0
    reads: tuple[str, ...] = ()
    writes: tuple[str, ...] = ()
    plugin: str = ""
    #: the checks after the first: ``((kind, n), …)``, each run as the first is
    also: tuple[tuple[str, int], ...] = ()

    @property
    def checks(self) -> tuple[tuple[str, int], ...]:
        """Every check the API runs before the statement (none for ``catalog``)."""
        if self.scope == "catalog":
            return ()
        return ((self.scope, self.scope_arg), *self.also)

    @property
    def tables(self) -> tuple[str, ...]:
        return tuple(sorted(set(self.reads) | set(self.writes)))

    def to_json(self) -> dict[str, Any]:
        out: dict[str, Any] = {
            "name": self.name,
            "mode": self.mode,
            "scope": self.scope,
            "scope_arg": self.scope_arg,
            "reads": list(self.reads),
            "writes": list(self.writes),
            "text": key(self.text),
            "sha256": digest(self.text),
        }
        if self.also:
            out["also"] = [{"scope": k, "arg": n} for k, n in self.also]
        return out

    @classmethod
    def from_json(cls, d: Mapping[str, Any], plugin: str = "") -> Statement:
        return cls(
            name=str(d["name"]),
            text=str(d["text"]),
            mode=str(d.get("mode", "read")),
            scope=str(d.get("scope", "catalog")),
            scope_arg=int(d.get("scope_arg", 0)),
            reads=tuple(d.get("reads", ())),
            writes=tuple(d.get("writes", ())),
            plugin=plugin,
            also=tuple((str(x["scope"]), int(x["arg"])) for x in d.get("also", ())),
        )


def parse_file(text: str, *, path: str = "") -> Statement:
    """A ``queries/*.sql`` file, header and statement."""
    lines = text.split("\n")
    head: dict[str, str] = {}
    i = 0
    while i < len(lines) and lines[i].startswith("--"):
        m = _HEADER.match(lines[i])
        if m is None:
            raise StatementError(f"{path}:{i + 1}: header line is not '-- key: value'")
        head[m.group(1)] = m.group(2)
        i += 1
    body = "\n".join(lines[i:])
    if body.endswith("\n"):
        body = body[:-1]
    for k in ("name", "mode", "scope", "tables"):
        if k not in head:
            raise StatementError(f"{path}: header has no '{k}'")
    unknown = set(head) - {"name", "mode", "scope", "tables"}
    if unknown:
        raise StatementError(f"{path}: unknown header keys {sorted(unknown)}")
    name = head["name"]
    if not _NAME.match(name):
        raise StatementError(f"{path}: bad name {name!r}")
    mode = head["mode"]
    if mode not in ("read", "write"):
        raise StatementError(f"{path}: mode must be read or write, not {mode!r}")
    checks = parse_scope(head["scope"], body, path=path)
    scope, scope_arg = checks[0] if checks else ("catalog", 0)
    reads: list[str] = []
    writes: list[str] = []
    for t in (x.strip() for x in head["tables"].split(",")):
        if not t:
            continue
        if t.endswith("*"):
            writes.append(t[:-1])
        else:
            reads.append(t)
    if mode == "read" and writes:
        raise StatementError(f"{path}: a read statement writes {writes}")
    if not body.strip():
        raise StatementError(f"{path}: no statement after the header")
    return Statement(
        name=name,
        text=body,
        mode=mode,
        scope=scope,
        scope_arg=scope_arg,
        reads=tuple(sorted(reads)),
        writes=tuple(sorted(writes)),
        also=tuple(checks[1:]),
    )


def parse_scope(value: str, body: str = "", *, path: str = "") -> list[tuple[str, int]]:
    """A ``scope:`` value: ``catalog``, or checks ``kind $N`` separated by commas."""
    if value.strip() == "catalog":
        return []
    out: list[tuple[str, int]] = []
    for part in value.split(","):
        scope, _, arg = part.strip().partition(" ")
        if scope not in SCOPES or scope == "catalog":
            raise StatementError(f"{path}: scope must be one of {SCOPES}, not {scope!r}")
        m2 = re.fullmatch(r"\$([1-9][0-9]?)", arg.strip())
        if m2 is None:
            raise StatementError(f"{path}: scope {scope} needs the argument it checks ($N)")
        n = int(m2.group(1))
        if body and not re.search(rf"\${n}(?![0-9])", body):
            raise StatementError(f"{path}: scope argument ${n} is not in the statement")
        if (scope, n) in out:
            raise StatementError(f"{path}: scope {scope} ${n} twice")
        out.append((scope, n))
    if not out:
        raise StatementError(f"{path}: scope is empty")
    return out


def render_scope(checks: Iterable[tuple[str, int]]) -> str:
    parts = [f"{k} ${n}" for k, n in checks]
    return ", ".join(parts) if parts else "catalog"


def render_file(st: Statement) -> str:
    scope = render_scope(st.checks)
    tables = ", ".join([*st.reads, *(f"{t}*" for t in st.writes)])
    return (
        f"-- name: {st.name}\n-- mode: {st.mode}\n-- scope: {scope}\n-- tables: {tables}\n"
        f"{key(st.text)}\n"
    )


@dataclass
class Registry:
    """One plugin's statements, by name and by text."""

    plugin: str
    by_name: dict[str, Statement] = field(default_factory=dict)
    by_key: dict[str, Statement] = field(default_factory=dict)

    @classmethod
    def of(cls, plugin: str, statements: Iterable[Statement]) -> Registry:
        r = cls(plugin)
        for st in statements:
            if st.name in r.by_name:
                raise StatementError(f"{plugin}: two statements named {st.name}")
            k = key(st.text)
            if k in r.by_key:
                raise StatementError(f"{plugin}: {st.name} repeats {r.by_key[k].name}")
            r.by_name[st.name] = st
            r.by_key[k] = st
        return r

    def lookup(self, text: str) -> Statement | None:
        return self.by_key.get(key(text))
