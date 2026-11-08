---
schema: 1
kind: decision
title: Plex sync and dizqueTV removed; Plex Live TV stays
date: 2026-11-08
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- A rehearsal failure in a Plex sync stage (`plex_groups`) surfaced that Maktaba still carried four Plex sync stages and the whole dizqueTV extension, moved into plugin runners in Epic 54 wave 8.
- The owner set the direction: Maktaba replaces Plex, it does not work alongside it. The only Plex integration that stays is Plex Live TV (`plex_livetv`), which shows Maktaba's own channels through its HDHomeRun tuner. dizqueTV played from Plex and needed `plex_index` and `plex_collection`, so it went too.
- The first draft dropped tables in its migration, which would have held the release for approval and made a rollback restore the whole dump. It was reworked so this release changes no data: 0216 only rewrites the plugin roles' grants, the leftover rows and tables stay, and the code ignores them through one retired list.

## What we learned
- The product direction (replace Plex) was never written down, so features against it were built, moved and hardened across several epics. A direction that rules features out belongs in the docs.
- A removal is two releases: first the code goes and ignores the leftovers (a lossless migration, so rollback runs the Down scripts), then a later migration deletes the data once no release that uses it can be rolled back to.

## Keep doing
- Check a dependency map before removing a feature: dizqueTV depended on two of the four Plex stages.

## Open
- The cleanup migration: delete the seven stages' rows, the two pools, the dizqueTV extension and component rows, the settings keys and `external_links` targets; drop `plex_items`, `channel_audits` and `channel_audit_channels`; drop the `maktaba_fp_dizquetv` role; remove the retired lists and the `rules.legacy_tv_live` fallback.
- The mediaintel scripts still write Plex collections on their own.
