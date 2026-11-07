# Runbook: a backup is late, or the WAL archive is failing

> Epic 50 story 50.09. Alerts: `backup_stale` (no good backup for 26 h), `backup_never`,
> `wal_archive_failing` (the newest archived WAL is over 5 minutes old on the primary).

1. On the leader (`patronictl -c /etc/patroni/patroni.yml list`):
   `sudo -u postgres pgbackrest --stanza=cloud info` and
   `journalctl -u maktaba-backup --since -2d`.
2. **Which repository?** A backup goes to repo1 and repo2 in turn; the WAL archive to both at
   once. `pgbackrest --stanza=cloud --repo=1 check` and `--repo=2 check` name the one that fails.
3. **The bucket is unreachable** (Hetzner Object Storage or R2 down, a key revoked): nothing is
   lost while the other repository works and Postgres keeps the WAL it could not push (watch the
   disk: `df -h /var/lib/postgresql`; the disk alert is at 80 %). When it is back, the archive
   catches up by itself; run `sudo -u postgres maktaba-backup full` to start a fresh chain.
4. **A passphrase or key was changed** in the config but not in the repository: put the old one
   back (the password manager has it). A repository is never re-created over its backups.
5. **The timer did not run** (`backup_never`): `systemctl status maktaba-backup.timer` on both
   nodes; `setup.sh` enables it when `REPO1_ENDPOINT` is set.
