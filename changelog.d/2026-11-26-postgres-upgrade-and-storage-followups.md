### Fixed

- **Bundled PostgreSQL upgrades and storage moves.** Retrying the upgrade preserves pending
  restore requests and cleans up abandoned upgrade files. Storage moves report set-aside
  database folders they leave behind, and backups cannot be placed inside a bundled database folder.
