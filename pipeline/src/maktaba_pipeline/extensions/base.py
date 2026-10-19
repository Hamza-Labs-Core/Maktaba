"""The server-side extension contract (plan 06-11 notes N3).

An extension adds stages to the queue graph. Core never imports an extension:
the extension imports core, declares a :class:`Manifest`, and in
:meth:`Extension.register` adds its stages plus the edges *from* core stages
that trigger them.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import TYPE_CHECKING, Protocol, runtime_checkable

if TYPE_CHECKING:
    from ..engine.graph import GraphBuilder
    from ..engine.stages.registry import StageImpl

__all__ = ["Extension", "Manifest", "SettingField"]


@dataclass(frozen=True, slots=True)
class SettingField:
    """One entry of an extension's settings schema."""

    key: str
    label: str
    secret: bool = False
    required: bool = False
    default: str = ""
    #: environment variable used only to bootstrap the first stored value
    env: str | None = None
    kind: str = "string"
    #: used by the home's API only, never by a stage (the Cloudflare API token)
    home_only: bool = False
    #: home_secrets name of a secret field; None means ``ext.<extension>.<key>``
    secret_name: str | None = None


@dataclass(frozen=True, slots=True)
class Manifest:
    name: str
    version: str
    description: str
    settings: tuple[SettingField, ...] = ()
    #: stage names this extension contributes
    stages: tuple[str, ...] = ()

    def secret_keys(self) -> frozenset[str]:
        return frozenset(f.key for f in self.settings if f.secret)

    def secret_name(self, key: str) -> str:
        """Where a secret setting is kept in ``home_secrets`` (slot 0120)."""
        for f in self.settings:
            if f.key == key and f.secret_name:
                return f.secret_name
        return f"ext.{self.name}.{key}"


@runtime_checkable
class Extension(Protocol):
    manifest: Manifest

    def register(self, graph: GraphBuilder) -> None:
        """Add stages and trigger edges to the graph."""

    def impls(self) -> Mapping[str, StageImpl]:
        """Handlers and fingerprints keyed by stage name."""

    def is_configured(self, settings: Mapping[str, str]) -> bool:
        """True when the settings are enough to talk to the external system."""
