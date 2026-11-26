### Fixed

- **Release feed freshness (#1668).** A signed release feed older than eight days (up to the 21-day
  hard expiry) is still read so releases and withdrawals are not missed, but no longer counts as a
  fresh check: automatic updates also check GitHub's release list, or wait and report `feed_old`
  in English and Arabic across web, Apple, and Android when GitHub cannot be reached.
- **Kept schema release across Down rollbacks and restores (#1666).** The database guard record now
  preserves `schema_release` separately from `kept_for` (and `dump_schema_release` beside
  `dump_kept_for`), so intermediate releases between `kept_for` and `schema_release` remain allowed
  after a Down rollback or a restore back to a kept schema while staying compatible with older
  records.
- **Replication pause signalling and TCP keepalives (#1667).** Homes now report `copies_paused`
  through `cluster_copies_paused()` and `cluster_node_replica_grant()` (migration slot 0239) so
  joined machines re-check every few seconds while migrations pause copies instead of counting
  login refusals and backing off up to 30 minutes, and server-side TCP keepalives
  (`tcp_keepalives_idle=30`, `tcp_keepalives_interval=10`, `tcp_keepalives_count=3`) end stuck
  replication `COPY` sessions from sleeping machines promptly.
- **Update copy-wait wording, hold reset, refusals, and audit accuracy (#1753).** Clarified
  `machine_copying` hold wording in English and Arabic (web and Android), cleared the 20-hour copy
  hold when a copy ends outside the quiet window so separate days' copies do not share one budget,
  localized `tv_not_held`, gone-run 404s (`no_such_run`), Check now 502/503 responses, 500s without
  a code, and Live TV's `HeldReleaseCard`, and recorded `copy_wait_over` only once the restart
  actually proceeds with the time spent waiting for copies (excluding viewer waits).
