"""Retired names: what a release removed from the code while its migrations kept the rows.

Maktaba replaces Plex: the Plex sync stages and the dizqueTV extension are gone, and slot
0215 drops none of their rows (a release's migrations lose no data, so a rollback runs the
Down scripts instead of restoring the dump). Their ``job_stages``, ``processing_jobs``,
``worker_pools``, ``extensions`` and component rows stay until a later cleanup migration
deletes them. The worker never claims their jobs (it claims only its graph's stages); the
readers that list every row leave these names out.

``shared/components/retired.go`` holds the same names for the Go side (a test checks they
agree).
"""

from __future__ import annotations

#: the stages whose rows remain
RETIRED_STAGES: frozenset[str] = frozenset(
    {
        "dizquetv_audit",
        "dizquetv_channels",
        "dizquetv_show",
        "plex_collection",
        "plex_collections_import",
        "plex_groups",
        "plex_index",
    }
)

#: the worker pools only retired stages used
RETIRED_POOLS: frozenset[str] = frozenset({"audits", "network_tv"})

#: the ``extensions`` rows of removed extensions
RETIRED_EXTENSIONS: frozenset[str] = frozenset({"dizquetv"})

#: the component rows (components, component_overrides, component_status,
#: component_restarts) of removed components
RETIRED_COMPONENTS: frozenset[str] = frozenset({"ext.dizquetv"})
