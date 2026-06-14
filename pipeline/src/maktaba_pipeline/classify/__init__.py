"""Epic 26 — Content Intelligence: the ``classify`` stage.

This package holds the local-only, network-free classifiers that run in
the new ``classify`` pipeline stage (Story 26.7), between ``index`` and
``thumbnail``:

- :mod:`.title_parser` (Story 26.1) — filename → structured metadata.
- :mod:`.topic_extractor` (Story 26.2) — transcript → content type,
  entities, topic + language distribution.
- :mod:`.series_detector` (Story 26.3) — library-level grouping of
  episodes into series.
- :mod:`.auto_collections` (Story 26.4) — library-level smart-collection
  proposals.

Each module follows the established pipeline convention (see
``library_mgmt/content_type.py`` and ``library_mgmt/topics.py``): a pure,
deterministic algorithmic core with a ``*_VERSION`` constant, no I/O, no
DB, no network — so the stage that wraps them owns persistence and the
core stays trivially unit-testable.
"""

from __future__ import annotations
