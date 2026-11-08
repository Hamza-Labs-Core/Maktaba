"""What the ``plex_livetv`` stage needs besides the database: the Plex client factory, a clock."""

from __future__ import annotations

import time
from collections.abc import Callable, Mapping

from ...integrations.plex import PlexClient

__all__ = ["ClientFactory", "PlexService"]

ClientFactory = Callable[[Mapping[str, str]], PlexClient]


class PlexService:
    """The client factory (direct with the token on the home, the relay on a node) and clock."""

    def __init__(self, client_factory: ClientFactory, clock: Callable[[], float] = time.monotonic):
        self._factory = client_factory
        self._clock = clock

    def client(self, settings: Mapping[str, str]) -> PlexClient:
        return self._factory(settings)
