"""Maktaba TV releases, schedule generations and TV changes (Epic 33, stories 33.2,
33.3, 33.10 and 33.12).

* :mod:`.config` - the ``tv_staging`` rules;
* :mod:`.snapshot` - the live lineup / settings snapshot and the release digest;
* :mod:`.build` - a release from the live state plus a change's pending edits;
* :mod:`.generation` - schedule generation N+1 (copy to the boundary, regenerate after);
* :mod:`.store` - TV change rows and their events;
* :mod:`.stage` - producers (``write_channels``, the scheduler's stale flag) writing TV
  changes instead of the air;
* :mod:`.commit` - the swap commit (lineup, settings, routing flip in one transaction);
  until the two-slot runtime lands (33.1 / 33.7) it is the interim swap;
* :mod:`.gc` - the daily garbage collection.

The modules are imported where used: :mod:`..rules` imports :mod:`.config` only.
"""
