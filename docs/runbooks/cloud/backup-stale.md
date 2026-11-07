# Runbook: a backup is late, or the WAL archive is failing

> Epic 50 stories 50.09, 50.12.

## When

- `backup_stale` (**SEV2**): no good backup for 26 hours.
- `backup_never` (**SEV2**): no backup ever.
- `wal_archive_failing` (**SEV2**): the newest archived WAL is over 5 minutes old on the primary.

## Check

1. On the leader (`patronictl -c /etc/patroni/patroni.yml list`): `sudo -u postgres pgbackrest --stanza=cloud info` and `journalctl -u maktaba-backup --since -2d`, and the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines).
2. **Which repository?** A backup goes to repo1 and repo2 in turn; the WAL archive to both at once. `pgbackrest --stanza=cloud --repo=1 check` and `--repo=2 check` name the one that fails.

## Do

1. **The bucket is unreachable** (Hetzner Object Storage or R2 down, a key revoked): nothing is lost while the other repository works, and Postgres keeps the WAL it couldn't push (watch the disk: `df -h /var/lib/postgresql`). When it is back the archive catches up by itself; then `sudo -u postgres maktaba-backup full` starts a fresh chain.
2. **A passphrase or key was changed** in the config but not in the repository: put the old one back (the password manager has it). A repository is never re-created over its backups.
3. **The timer didn't run** (`backup_never`): `systemctl status maktaba-backup.timer` on both nodes; `setup.sh` enables it when `REPO1_ENDPOINT` is set.

## Done when

`pgbackrest info` shows a backup from today in both repositories, and `wal_archive_failing` is quiet.

## Tell users

Nothing: no user sees it.
