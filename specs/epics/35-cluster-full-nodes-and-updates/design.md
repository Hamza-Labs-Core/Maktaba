# Design — Full-Maktaba cluster machines and automatic updates (Epic 35)

Requirements and decisions: [README.md](README.md). Stories `story-35-01` … `story-35-15`.

## 1. Shape

Two halves that depend on each other:

- **Full nodes.** Every joined machine runs the same `maktaba-server` as the home, in node mode. The
  home stays the only holder of the database schema, the sessions, the secrets and the TV front door.
  A node's API serves what it can from the database through its limited role, proxies what needs the
  home, and resolves every session through the home.
- **Automatic updates.** Because every machine now runs the same release, one update flow covers the
  cluster: the home finds a signed release, installs it on itself through a resumable state machine
  that never interrupts TV, then orders each machine over the cluster channel to install the same
  release, one by one, verifying each.

Process tree on every machine after this epic:

```
OS service (systemd / launchd / Windows service or logon task)
 └─ maktaba-launcher                         <install>/maktaba-launcher[.exe]   stable, tiny
     └─ maktaba-server serve                 <install>/bin/releases/<current>/maktaba-server
         ├─ api          bin/releases/<v>/maktaba-api         (home: migrates; node: node mode)
         ├─ pipeline     bin/releases/<v>/venv/…/python -m maktaba_pipeline
         ├─ streaming    bin/releases/<v>/maktaba-streaming   (home: front door; node: only if enabled)
         ├─ tv-slot ×1–3 bin/releases/<slot release>/maktaba-streaming slot (Epic 33)
         └─ web          in process (supervisor)
```

## 2. Install layout and the launcher (story 35.2)

### 2.1 Layout

```
<install>/                                     macOS ~/Library/Application Support/Maktaba/server (agent)
  maktaba-launcher[.exe]                              or /usr/local/var/maktaba (daemon)
  current            "1.5.0\n"                 Linux /var/lib/maktaba (system) or ~/.local/share/maktaba (user)
  previous           "1.4.2\n"                 Windows %ProgramData%\Maktaba (service) or %LOCALAPPDATA%\Maktaba (task)
  pending            {"version":"1.5.0","run_id":"…","since":"…"}   (only while unconfirmed)
  watermark          "1.5.0\n"                 highest version ever committed on this machine
  bin/releases/1.5.0/
      maktaba-server  maktaba-api  maktaba-streaming   (from the platform archive)
      release.json  checksums.txt  checksums.txt.sigstore.json
      venv/                                     pipeline environment (wheel + hash-checked deps)
      .verified       {"sha256":{…}, "verified_at":"…", "identity":"…"}
  bin/releases/1.4.2/ …
  updates/journal.json  updates/downloads/<version>/  updates/logs/<run_id>.log
```

`bin/releases/<version>/` is the Epic 33 §3.3 directory; `ResolveReleaseBinary`
(`cmd/maktaba-server/internal/supervisor/release.go`) already resolves slot binaries there. The
supervisor's `Build` gains a `ReleaseDir` so every role is started from the current release's
directory, never from `PATH` or a sibling of the launcher. Configuration (`server.toml`,
`secrets.key`, `node.json`) stays where it is today, outside the layout.

Pointer files are written atomically: write `current.tmp`, `fsync`, rename over `current`, `fsync`
the directory (Windows: `MoveFileExW(MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)`).

### 2.2 The launcher

`cmd/maktaba-launcher` (new module, no dependencies beyond the standard library and
`golang.org/x/sys`; < 1 000 lines; its own `launcher_protocol` integer):

1. Read `current`; validate the directory has `.verified` and the `maktaba-server` digest matches
   it (a tampered release directory is not started; falls to `previous`).
2. Start `bin/releases/<current>/maktaba-server serve` with the launcher's argv after `--`, the
   environment, and `MAKTABA_LAUNCHER=1`, `MAKTABA_RELEASE_DIR=…`.
3. Pass SIGTERM/SIGINT (Windows: service stop, console ctrl) to it and wait.
4. On exit:
   - **75** (`EX_TEMPFAIL`, "restart me"): start again at once, re-reading `current` (this is how
     the supervisor restarts itself on a new release, §8).
   - **0** after a stop request: exit 0.
   - anything else: restart with backoff (1 s doubling to 30 s).
5. **Boot-loop guard.** While `pending` exists (a version not yet confirmed), the launcher counts
   starts of that version; 3 exits other than 75 within 10 minutes, or no confirmation within
   `pending_timeout` (15 min), make it write `current = previous`, remove `pending`, append a
   `launcher_rollback` line to `updates/launcher.log`, and start the previous release. The
   supervisor confirms by removing `pending` when the orchestrator commits (§5). This guard is what
   survives a power loss: nothing in memory is needed to get back to a release that boots.
6. `maktaba-launcher --version`, `--status` (prints current, previous, pending, last rollback) and
   `--replace-self <new file>` (§8.3).

The launcher never downloads, verifies signatures or touches the database.

### 2.3 Services per OS

| OS | Unit | ExecStart | Restart on 75 / failure |
|---|---|---|---|
| Linux (system) | `/etc/systemd/system/maktaba-server.service` (`User=maktaba`, existing hardening, `ReadWritePaths=/var/lib/maktaba /var/log/maktaba`) | `/var/lib/maktaba/maktaba-launcher serve` | `Restart=always`, `RestartForceExitStatus=75`, `KillMode=mixed` (the launcher's children get SIGTERM from it, not from systemd, so a supervisor restart does not kill them, §8) |
| Linux (user) | `~/.config/systemd/user/maktaba-server.service` + linger (as Epic 31 nodes) | `~/.local/share/maktaba/maktaba-launcher serve` | same |
| macOS (home) | `/Library/LaunchDaemons/com.maktaba.server.plist` (existing, Program changes) | `…/maktaba-launcher serve` | `KeepAlive` = true; `AbandonProcessGroup` = true |
| macOS (node) | `~/Library/LaunchAgents/com.maktaba.server.plist` (user session: the share and node config are there, Epic 31) | same | same |
| Windows (task, default for nodes) | Scheduled task "Maktaba" at logon of the user (Epic 31 default: mapped drives are per user) | `%LOCALAPPDATA%\Maktaba\maktaba-launcher.exe serve` | the task restarts on failure every 1 min (3 tries); the launcher itself handles 75 |
| Windows (service) | Service `Maktaba` (the launcher implements `golang.org/x/sys/windows/svc`; no NSSM) | `%ProgramData%\Maktaba\maktaba-launcher.exe serve` | SCM recovery: restart after 5 s, reset after 1 day; launcher handles 75 |

`maktaba-server setup --service [--user|--system]` writes the unit for the OS; the node installer
(`deploy/packaging/node`) gains `--full` that does the same for a node.

### 2.4 Migrating existing installs

`maktaba-server setup --self-managed` (and the first start of a launcher-less install that finds
itself in a writable location) converts:

| From | To |
|---|---|
| Archive / manual (`/usr/local/bin/maktaba-server` and siblings) | copies the running binaries into `bin/releases/<running version>/` (digest recorded as `.verified` with `identity: "migrated"`), writes `current`, installs the launcher, rewrites the service unit. |
| `.deb` / `.rpm` | the next package version ships `/usr/bin/maktaba-launcher` and puts its binaries in `/var/lib/maktaba/bin/releases/<v>/` (postinstall writes `current` only when it is newer than `watermark`); the package manager owns the launcher, the updater owns releases. `apt upgrade` and the updater then never fight: a package older than the watermark only refreshes the launcher. |
| Homebrew | not converted automatically (brew owns the Cellar). The Updates page shows "managed by Homebrew — automatic updates off" and the command `maktaba-server setup --self-managed`, which moves to `~/Library/Application Support/Maktaba/server`. |
| Docker | unchanged: Epic 28.3's 409 with instructions. A Docker home cannot run the cluster rollout's home half; nodes may still be self-managed. |
| Worker-only node (Epic 31 installer) | §9.4. |

A machine whose release came from a migration (`identity: migrated`) is updated normally; the
migrated directory is never chosen as a rollback target once a verified release is installed after
it, unless it is `previous`.

## 3. Releases and verification (story 35.1)

### 3.1 What the release publishes

`release.yml` (`sign-and-release`) gains:

1. `release.json` — generated from the build, listed in `checksums.txt`:

```json
{
  "v": 1,
  "version": "1.5.0",
  "tag": "v1.5.0",
  "commit": "<40 hex>",
  "channel": "stable",
  "published": "2026-10-20T10:00:00Z",
  "schema_rev": 131,
  "min_node_schema": 128,
  "breaks_previous_home": false,
  "min_upgrade_from": "1.3.0",
  "edge_protocol": 3,
  "launcher_protocol": 1,
  "agent_protocol": 2,
  "assets": {
    "darwin/arm64":  {"archive": "maktaba-server-1.5.0-darwin-arm64.tar.gz", "sha256": "…",
                      "files": {"maktaba-server": "…", "maktaba-api": "…", "maktaba-streaming": "…", "maktaba-launcher": "…"}},
    "linux/amd64":   {…}, "linux/arm64": {…}, "darwin/amd64": {…}, "windows/amd64": {…}
  },
  "pipeline": {"wheel": "maktaba_pipeline-1.5.0-py3-none-any.whl", "wheel_sha256": "…",
               "constraints": "maktaba-pipeline-1.5.0-constraints.txt", "constraints_sha256": "…",
               "python": "3.12"},
  "app_postgres_major": {"mac-app": 16, "win-app": 16, "linux-app": 16}
}
```

   `schema_rev` is the highest migration slot shipped (equal to `SCHEMA_REV` / `SchemaRev`, a CI
   check fails if they differ); `min_node_schema` is what `maktaba_min_node_schema()` will return
   after its migrations (computed from the migrations' MANIFEST "Breaking for older nodes" rows,
   Epic 31 §5.1); `breaks_previous_home` is set when any migration since the previous release's
   `schema_rev` carries the MANIFEST marker "Breaking for the previous release" (a new rule, §6.3).
   `files` are the digests of the binaries inside the archive, so an extracted directory can be
   re-verified without the archive. `app_postgres_major` (absent before v0.3.2) is each app
   payload's bundled PostgreSQL major, from its pins (`tools/release/<payload>/pins.env`): a
   machine whose bundled database was made by another major refuses the release before its app
   installs it (`pg_major_change`, a person's; the release is not blocked). Without the field,
   the release's start halts at its database step and goes back.

2. `checksums.txt.sigstore.json` — `cosign sign-blob --bundle checksums.txt.sigstore.json
   --new-bundle-format` (in addition to today's `.sig` / `.pem`, kept for humans). The bundle holds
   the certificate, the signature and the Rekor inclusion proof, so verification needs no network.

### 3.2 Verification (`shared/go/releaseverify`, new module)

One Go package used by the API's Updater (to mark a release verified), the supervisor's orchestrator,
the node's machine agent, `maktaba-server update` and the launcher's `--verify` (offline re-check of
a directory). Built on `github.com/sigstore/sigstore-go`.

```
Verify(release dir or downloaded files, tag) error:
  1. bundle := parse(checksums.txt.sigstore.json)
  2. sigstore-go verify(bundle, artifact = checksums.txt,
        trusted root   = embedded trusted_root.json (refreshed, §3.3),
        identity       = issuer "https://token.actions.githubusercontent.com",
                         SAN exactly "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/" + tag,
        extensions     = SourceRepositoryURI "https://github.com/Hamza-Labs-Core/Maktaba",
                         SourceRepositoryRef "refs/tags/" + tag, BuildTrigger "push",
        tlog           = at least 1 inclusion proof, integrated time within the cert validity)
  3. sums := parse(checksums.txt) (sha256sum format; duplicate names or bad hex → error)
  4. release.json digest == sums["release.json"]; parse; version == strip_v(tag); tag == tag
  5. every file to install: sha256(file) == sums[name]  (archive, wheel, constraints)
  6. extracted binaries: sha256 == release.json.assets[platform].files[name]
  7. constraints: every requirement line has --hash=sha256:… (PR #94 rule)
```

Errors are typed (`ErrSignature`, `ErrIdentity`, `ErrTlog`, `ErrChecksum`, `ErrManifest`,
`ErrNoHashes`) and carry the file name; the Updates page and the notification show them.
`MAKTABA_UPDATE_REPO` (Epic 28.2, forks) changes the repository **and** the expected identity
together; it is a startup setting, never an API field.

Epic 28.3's `selfupdate.go` and `cmd/maktaba-server/internal/selfupdate` lose their own download and
checksum code and call this package; the CLI stops reading `releases.maktaba.app/manifest.json`
(Epic 25.34's mirror stays a possible future source behind the same verification).

**Releases published before this story** have no Sigstore bundle. They are verified with their
`checksums.txt.sig` + `.pem` through the same pinned identity (sigstore-go accepts a detached
signature and certificate, with an online Rekor lookup for the inclusion proof) and have no
`release.json`, so they are never chosen as an automatic target: they can be installed only by the
migration of §2.4 (what is running already) or by an explicit `maktaba-server update --version`.
Automatic updates start with the first release that carries `release.json`.

### 3.3 Trusted root

`trusted_root.json` for the public-good Sigstore instance is embedded at build time. The API's
Updater refreshes it daily through Sigstore's TUF repository using `sigstore-go`'s TUF client with
the embedded root as the anchor and keeps the result in `<data_dir>/sigstore/`; nodes receive the
refreshed root from the home's mirror and verify its TUF metadata themselves. Verification never
fails open: a root that cannot be refreshed keeps the last good one (TUF expiry rules apply; an
expired root is a critical notification, not a silent skip).

### 3.4 The pipeline environment

Staging a release builds `bin/releases/<v>/venv` the way the node installer does today (PR #70/#94,
`deploy/packaging/node`): uv when present (it brings Python 3.12), else the system Python 3.12+;
`uv pip install --require-hashes -c constraints.txt <wheel>` (pip: hash-checked constraints first,
then the wheel); extras by platform (`stt` on Apple Silicon, as the installer). Packages come from
PyPI (or `MAKTABA_PIP_INDEX_URL`) and are accepted only by hash. The shared logic moves from the
shell/PowerShell installers into Go (`cmd/maktaba-server/internal/pyenv`), and the installers call
`maktaba-server pyenv build`, so there is one implementation. A machine without internet access
does not need it: **the home hosts a wheelhouse** (`wheelhouse = true`). During its own `stage` step
the home runs `pip download --only-binary=:all: --require-hashes -c constraints.txt --platform <tag>
--python-version 3.12` for every platform in `cluster_nodes.platform` into
`updates/downloads/<v>/wheelhouse/<platform>/` and serves it on the artifact mirror; nodes install with
`--no-index --find-links <local copy>` and the same hashes, so the source of a file never matters. A
dependency without a binary wheel for a platform falls back to PyPI on that node (logged); a node with
neither fails with `pypi_unreachable`.

### 3.5 Watermark and downgrades

- `watermark` (local file, §2.1) and `update_watermark` (home database) hold the highest committed
  version. A release with `version < watermark` is never staged; `version == watermark` with a
  different `checksums.txt` digest is refused (`ErrReplay`) and notified.
- A release whose integrated Rekor time is older than the current release's (a re-signed old commit)
  is refused.
- A rollback (§5.6) to `previous` does not lower the watermark; it is the only way to run a version
  below it.

## 4. Policy, discovery and scheduling (story 35.3)

The API's `Updater` (Epic 28.2, `api/internal/system/updater.go`) stays the discoverer:

- Polls every 6 h (was 24 h; still `MAKTABA_UPDATE_*` env with `rules.updates` override), keeps
  every release of the channel newer than the watermark in `release_catalog`.
- For each new entry, downloads `checksums.txt`, its bundle and `release.json` only (small), verifies
  steps 1–4 of §3.2 and records `verify_state`. A failure is a critical notification at once (a bad
  signature on a published release is an incident whatever the policy).
- Re-reads the GitHub release just before a run (R4.2): gone, now a prerelease on `stable`, or a
  different `checksums.txt` → `withdrawn_at`, not installed.

**Choosing the target** (pure function, table-tested):

```
candidates = catalog where verify_state = verified and not withdrawn and not blocked
             and channel allowed and version > watermark and version not in skipped_versions
             and (pinned_version == "" or version <= pinned_version)
             and min_upgrade_from <= current
target = max(candidates)          (semver; numeric, not lexical)
if target.breaks_previous_home → state "held: needs approval", notify once
```

When `min_upgrade_from > current` (a cluster that skipped too many releases), the target becomes the
highest candidate whose `min_upgrade_from <= current`, and the next window continues from there
(stepping stones).

**Scheduling.** A ticker in the API (every minute) starts a run when: `auto_install`, a target
exists, it is older than `min_release_age_hours`, now is inside `window` and at least 30 minutes
before its end, `deferred_until` passed, no run is active, no TV change is in `staged…swapping`
(Epic 33 R9.4; otherwise wait — the binary change would queue anyway), and the home is healthy
(every role `running`, no crash in the last 10 min). Starting = `POST /update/start` to the
supervisor's loopback admin with the run id after inserting `update_runs(state=planned)`. "Update
now" inserts the run with `trigger = manual` and skips the window and age conditions only.

## 5. The home's update (story 35.4)

### 5.1 Who runs it

The **supervisor** (`maktaba-server`, package `cmd/maktaba-server/internal/updater`) owns the run:
it is the only process that survives the restart of every role, and the launcher covers its own
restart. The API schedules, displays and relays admin actions; it may restart in the middle of a
run without effect on it. The supervisor writes `update_runs` / `update_run_steps` with the home's
own database URL (it already has it) and appends to `updates/logs/<run_id>.log`.

### 5.2 Journal

`updates/journal.json`, rewritten atomically (§2.1) at every step boundary, before the step's side
effects begin and after they end:

```json
{"run_id":"…","version":"1.5.0","from":"1.4.2","step":"restarting_roles",
 "done":["preflight","download","verify","stage","smoke","backup","rehearse","drain","migrate","tv"],
 "roles_done":["api"],"tv_change_id":"…","backup":"…/pre-1.5.0-20261021T030112.dump",
 "started_at":"…","updated_at":"…"}
```

On supervisor start, a journal with a run that is neither committed nor rolled back means
**resume** (§5.5). The database rows are a mirror for the UI; the journal decides.

### 5.3 Steps

| # | Step | Does | Idempotent / resume | Failure → |
|---|---|---|---|---|
| 1 | `preflight` | target still chosen (§4), disk: 3 × archive + venv estimate + backup estimate (`pg_database_size`) free; DB reachable; home healthy; no other run; write permission on `<install>` | pure checks | `failed` (nothing changed) |
| 2 | `download` | archive for this platform, wheel, constraints, `checksums.txt`, bundle, `release.json` into `updates/downloads/<v>/` (HTTP range resume, 3 retries) | re-download what is missing or wrong-sized | `failed` |
| 3 | `verify` | §3.2 all steps | re-verify | `failed` + critical notification, version `blocked` |
| 4 | `stage` | extract into `bin/releases/<v>.tmp/`, verify extracted digests, build `venv` (§3.4), write `.verified`, rename `<v>.tmp` → `<v>` | a `<v>.tmp` is deleted and rebuilt; a `<v>` with a valid `.verified` is kept | `failed` |
| 5 | `smoke` | run `bin/releases/<v>/maktaba-api selfcheck` (new CLI subcommand beside `migrate` in `api/selfcheck_cli.go`) (version, config parse, DB reachable read-only, `migrate status` lists pending slots and checks the DB's applied version ≤ release `schema_rev`) and `maktaba-streaming --version`, `python -m maktaba_pipeline --version` | pure | `failed` |
| 6 | `backup` | §6.1 (only when `migrate status` lists pending migrations) | a verified backup of this run is reused | `failed` (nothing changed) |
| 7 | `rehearse` | §6.2 (pending migrations and `rehearse_migrations`) | scratch DB dropped and redone | `failed`, version `blocked` |
| 8 | `drain` | the home's pipeline: `cmd drain` to its worker sessions (Epic 31 agent), wait for running jobs up to `drain_timeout_minutes` (transcriptions checkpoint and are re-queued, Epic 31 R4.4) | re-send drain | (timeout is not a failure; remaining jobs are released) |
| 9 | `migrate` | `bin/releases/<v>/maktaba-api migrate up` (the old API keeps serving; migrations are additive) | goose skips applied slots; NO TRANSACTION files are idempotent (§6.3) | `failed_migrate`: undrain, old binaries keep running, critical notification, version `blocked` |
| 10 | `tv` | §7 | resumes by `tv_change_id` state | `failed_tv` → rollback (§5.6) of what was restarted: nothing yet; undrain |
| 11 | `restart_roles` | one at a time: `api` → `pipeline` → front door (only if `edge_protocol` changed, §7.3); each: stop gracefully, start from `bin/releases/<v>/`, wait for health (§5.4) | `roles_done` in the journal | `rolling_back` |
| 12 | `restart_supervisor` | §8: write `pending`, swap `previous`/`current`, hand over, exit 75; the new supervisor re-adopts, reads the journal, continues at 13 | the launcher's boot-loop guard | launcher rollback → the old supervisor finds the journal and completes `rolling_back` |
| 13 | `watch` | 10 min: no crash loop of any role, `/readyz` green, TV routing epoch stable | resumes the remaining time | `rolling_back` |
| 14 | `commit` | remove `pending`, set watermarks, undrain, prune `bin/releases` to `keep_versions` and backups to `backups_keep`, `update_runs.state = home_committed`, notify | pure | — |
| 15 | `rollout` | §13 | per target | per policy |

**Cancel** (`POST /api/cluster/updates/runs/{id}/cancel`, supervisor `/update/abort`) is accepted only
while the run is before `migrating`; afterwards it answers 409 `past_migrate` and the run completes or
rolls back on its own.

### 5.4 Health of a restarted role

A role is healthy when, within 2 minutes of its start: its liveness probe passes (22.9 probes),
`/readyz` returns 200, `/api/system/version` (API) or its admin `/version` reports the new version,
and (pipeline) its agent session reconnected with the new version in `hello`. The front door adds:
SSDP answers, `discover.json` lists the same `DeviceID`, an HLS playlist of a live channel advances.

### 5.5 Resume after power loss or crash

| Journal step at boot | What the supervisor does |
|---|---|
| before `migrate` | restarts the step (idempotent); a run older than `window_overrun_minutes` past the window is marked `failed` (`interrupted`) and retried next window |
| `migrate` | runs `migrate up` again (goose sees what is applied; §6.3 makes partial NO TRANSACTION files safe) |
| `tv` | reads the TV change: `live` → next step; `failed` → `failed_tv`; in progress → waits for the controller |
| `restart_roles` | roles in `roles_done` start from the new release, the rest from the old one; continues |
| `restart_supervisor` / `watch` | the launcher started either the new supervisor (continue) or `previous` after its guard (finish `rolling_back`) |

### 5.6 Rollback (binaries)

Order is the reverse of the restarts: front door (if restarted), `pipeline`, `api` back to
`bin/releases/<from>/`; TV: an Epic 33.9 rollback of the binary change (warm while the previous slot
is warm, else cold) — before 33.9 is built, the §7.2 fallback restarts `streaming` on the previous
release's binary (same viewer rule); supervisor: `current = previous` and exit 75. The schema stays (§6). Allowed
only when `from`'s `schema_rev ≥ maktaba_min_node_schema()` and no migration in this run was
`breaks_previous_home` (such runs are held for approval before they start, §4). The run ends
`rolled_back`; the version is `blocked` (no automatic retry) and the admin is notified with the
failing check.

## 6. Database safety (story 35.5)

### 6.1 Backup

Postgres: `pg_dump --format=custom --compress=zstd:3` (Postgres 16+; else gzip 6) of the home's
database with the API's migration user, to `<data_dir>/backups/pre-<version>-<utc>.dump` (mode 0600,
never the share); then `pg_restore --list` must parse it and list every table of the schema; size and
SHA-256 recorded in `update_runs.backup`. `pg_dump` is the one matching the server's major version
(`pg_config`/bundled; a mismatch fails the step with `pg_dump_version`). SQLite homes (tests,
single-machine): `VACUUM INTO`. Kept: `backups_keep` (3), oldest pruned after a commit.

### 6.2 Rehearsal

When the release has pending migrations: `CREATE DATABASE maktaba_rehearse_<run8>` (the migration
user needs `CREATEDB`; without it the step is skipped with a warning), `pg_restore` the backup into it,
run `bin/releases/<v>/maktaba-api migrate up` against it with a statement timeout of 30 min, then
`maktaba-api selfcheck --deep` against it (opens every repository, runs the read queries of the
smoke suite), then `DROP DATABASE`. It also measures the migration's duration, recorded and shown
(a migration that took 20 minutes in rehearsal tells the admin why the window was long). Skipped
above `rehearse_max_db_gb`. Its failure blocks the version: nothing ran on the real database.

### 6.3 Migration rules (enforced by tests in `shared/db/migrations`)

- **Additive by default** (existing README convention): new tables, nullable or defaulted columns,
  new functions, new indexes (`CONCURRENTLY` in NO TRANSACTION files).
- **NO TRANSACTION files are idempotent**: every statement is `IF NOT EXISTS` / `IF EXISTS` /
  `CREATE OR REPLACE`, so re-running after a crash completes them. A test parses each NO TRANSACTION
  file and fails on a statement that is not.
- **Breaking older nodes** (Epic 31 §5.1): the migration `CREATE OR REPLACE`s
  `maktaba_min_node_schema()` and its MANIFEST row says "Breaking for older nodes".
- **Breaking the previous release** (new): a contract step (drop a column, tighten a constraint that
  old code violates) must come at least one release after the code stopped using it; if a migration
  cannot honour that, its MANIFEST row says "Breaking for the previous release" and the release is
  `breaks_previous_home` (§3.1), held for approval (§4).
- CI: the previous release's API test suite runs against the new schema (`make compat-prev`: checks
  out the last tag's `api` and runs its repository tests on a database migrated by HEAD). A failure
  without the MANIFEST marker fails the build.

### 6.4 Restore (manual)

`maktaba-server restore --backup <file> --confirm` (home only): stops every role except the
supervisor, drains and disconnects nodes (`cmd disconnect`), `pg_restore --clean --if-exists` into
the database, sets `current` to the release whose `schema_rev` matches the backup's
`goose_db_version`, restarts. Never automatic; audited; the Updates page shows the command and the
backups list, not a button (a restore loses every write since the backup).

## 7. TV during updates (story 35.6)

### 7.1 With the two-slot runtime (Epic 33.1, 33.7)

Step 10 asks the change controller (`api/internal/tvchange`) for a `binary` change:
`source = {kind: "auto_update", run_id}`, `release` = live release with `streaming = {version,
sha256}` from `bin/releases/<v>/` (`.verified`). The controller treats `source.kind = auto_update` as
`auto_rule = immediate` for the `binary` kind only (the override, README decisions; Epic 33
`auto_promote_binary` stays `approval` for every other source). The change is staged (staging slot on
the new binary), verified (33.5), swapped (33.7, `swap_wait = boundary`), and reaches `live`. The
exemption applies only to a change whose kinds are exactly `[binary]`: the updater never mixes kinds,
and a binary change that a rebase merged with another kind (Epic 33 R9.4) waits for approval like any
other. The
orchestrator waits for `live` or a terminal state, with `boundary_max_wait_minutes` + 10 as its
timeout. A verification failure → `failed_tv` (§5.3).

If another TV change is `ready` waiting for approval, the binary change queues behind it per
Epic 33 R9.4 (rebased); the orchestrator does not wait for an approval it cannot get: it waits until
the window's end + overrun, then fails the run with `tv_change_pending` (retried next window).

*As built:* every home migrates at its new release's start ([install-kinds.md](install-kinds.md)),
so this step runs in the new release's supervisor during its watch, and the release commits once
the change is live; a failure rolls the release back (`failed_tv`, `tv_change_pending`,
`tv_timeout`, `tv_change_cancelled`), and a rollback takes the change back first
([install-kinds.md](install-kinds.md), "The TV step").

### 7.2 Before the two-slot runtime (today)

One streaming process: the front door and the engine are the same. Step 10 becomes "restart
`streaming` on the new binary" when the number of viewers (`channel_runtime` sessions excluding
probes) is ≤ `tv_staging.quiet_max_viewers` (0); otherwise wait inside the window and retry every 5
minutes; at the window's end the run fails with `tv_busy` (retried next window). Pushed channels
reconnect (same video id, Epic 32 §7.4). This fallback is selected when `tv_slots` has no `staging`
capable runtime (`GET /api/tv/slots` reports one process).

### 7.3 Front door and edge protocol

`edge_protocol` (an integer constant in `streaming/internal/frontdoor`) is compared between the
running front door and `release.json`. Equal: the front door is not restarted at all (its binary
stays the previous release's until the next edge change; `keep_versions` never prunes a version a
running process uses). Different: it restarts in step 11 after the TV swap, inside the window
(story 33.14 AC4).

### 7.4 Streaming nodes

A node with slots (Epic 33 node mode) gets a per-host `binary` change for its slots (story 33.14
AC7) during its own apply (§13.3); a streaming node without slots is drained through Epic 32.4
(channels move to other hosts at their wall-clock position) before its restart.

## 8. Supervisor and launcher self-update (story 35.7)

### 8.1 Handover

The supervisor restarts itself without stopping the processes that serve TV:

1. Write `updates/handover.json`: for each child that survives (front door, tv slots), its role,
   pid, process-group id (POSIX) or job name (Windows), admin and probe URLs, start time.
2. Stop the in-process web server (the other restarting roles were restarted in step 11 already).
   The web port is closed for the few seconds the new supervisor needs to start; the SPA's API client
   retries, and TV clients never use the web port's process for anything but proxying (the front door
   and slots keep running). Accepted: a short web-UI blip at 03:00.
3. Exit 75 without signalling the surviving children.
4. The launcher starts `bin/releases/<current>/maktaba-server serve`; it reads `handover.json` and
   **adopts** each listed child: verifies the pid is alive and its probe answers (POSIX `kill(pid, 0)`
   plus `/proc/<pid>/stat` start time or `proc_pidinfo` on macOS, so a reused pid is not adopted),
   then supervises it by probe and by polling (it is not the parent, so it cannot `wait`); when an
   adopted child dies or must restart, it is replaced by a normal child.

POSIX: children already lead their own process groups (22.9 AC12); with systemd `KillMode=mixed`
and launchd `AbandonProcessGroup`, the service manager does not kill them when the supervisor
exits. A full service stop still stops them: the launcher, on SIGTERM, reads `handover.json` and
signals every adopted group.

### 8.2 Windows

Children live in the supervisor's Job Object with `KILL_ON_JOB_CLOSE` (22.9 AC12): when the
supervisor exits, the job closes and they die. For surviving children the supervisor creates a
**named job** `Local\Maktaba-tv-<boot id>` and duplicates its handle into the launcher
(`DuplicateHandle` into the launcher's process, the launcher's pid is in `MAKTABA_LAUNCHER_PID`), so
the job outlives the supervisor; the new supervisor opens it by name and adopts its processes
(`QueryInformationJobObject` lists pids). The launcher closes its handle — killing the job — only on
a service stop. If the duplicate fails, the update degrades to "restart children in order" in the
quiet window with TV viewers ≤ `quiet_max_viewers` (story 33.14 EC2), and says so in the run log.

As built (install-kinds.md, "TV handover"): the job's name carries the install's hash in place
of the boot id (another install, or a test, never meets it; a named object ends with its last
handle), each surviving child also has a named job of its own nested in it (what ends that child
with its descendants after a handover; its handle is duplicated into the launcher too), a
handover is made only onto a release whose `release.json` says `handover_protocol`, and a
launcher that speaks the handover keeps the job it runs every server in across the restart, so
the children stay there without leaving it (a logon task's or a CI runner's job may refuse a
breakaway); the boot's job is the way under a launcher from before it.

### 8.3 Launcher replacement

Rare (`launcher_protocol` changed). In the quiet window, after the home committed: the supervisor
copies `bin/releases/<v>/maktaba-launcher` to `<install>/maktaba-launcher.new`, runs
`maktaba-launcher.new --replace-self`, which renames the running launcher to `.old` (allowed for a
running exe on Windows), renames itself into place, and asks the service manager for a restart
(`systemctl restart` for user units, exit 75 + `RestartForceExitStatus` for system units, launchd
KeepAlive, Windows: `sc` restart through the SCM recovery path). This **is a full restart** of every
process, TV included; it is scheduled only when viewers ≤ `quiet_max_viewers` and announced on the
Updates page beforehand. `.old` is deleted at the next start.

## 9. Full nodes (story 35.8)

### 9.1 Roles in node mode

`maktaba-server serve` decides node mode from the node config (`config.LocalNodeConfig().Joined()`,
already used by `supervisor.ChildEnv` for streaming). In node mode:

| Role | Node behaviour |
|---|---|
| `api` | env `MAKTABA_NODE_MODE=1`, `DATABASE_URL` = the node's role, `MAKTABA_HOME_STREAM_URL` (the home's streaming port, §11.3), `MAKTABA_AUTO_MIGRATE` unset (and the API refuses to migrate in node mode), `MAKTABA_HOME_API_URL` from node config, delegation key from the machine agent (§10), schema guard (§9.3), route classes (§11). No hub, no scheduler (Updater, TV controller, publishers), no secrets key: those are home components; their constructors are skipped in node mode and their routes are `home` class. |
| `web` | in process, as on the home; `/api` → the node API; `/stream`, `/xmltv.xml`, `/lineup.m3u` → the home's front door (§11.3). |
| `pipeline` | `--node` (Epic 31, unchanged). |
| `streaming` | always, for **VOD** (Epic 08): it reads media from the NAS through the node's own mounts and the canonical share-path resolver (story 31.3). TV stations only when the node is an Epic 32 streaming host (`tv_cluster.nodes_stream` and per-machine switch), unchanged from Epic 32.1. |

Ports are the home's defaults (API 8080, web 8088, streaming 8081) and configurable in the node's
`server.toml`; the node writes its web URL into its `cluster_node_report` so the Machines page can
link to it.

The API in node mode refuses to start when its database role is a superuser or has `CREATE` on the
schema (`SELECT rolsuper, has_schema_privilege(current_user, 'public', 'CREATE')`): a node must
never be able to run DDL by mistake.

### 9.2 The machine agent

A Go client of the cluster channel in the node's supervisor (`cmd/maktaba-server/internal/machineagent`),
protocol v2 handshake with the node's key (Epic 31 design-agent §4.2), `kind: "node"`,
`process: "supervisor"`, `worker_id: "sup-<node8>"`. It carries: update commands (§13), `keys`
(delegation key, §10.2), `auth.revoked` (forwarded to the local API over loopback), and the node's
`metrics` (its supervisor's process table). The node's API talks to it on the supervisor's loopback
admin with a per-boot token (`<data_dir>/supervisor/local.token`, 0600). The Go client shares the
vectors in `shared/testdata/cluster/agent_vectors.json` with the hub and the Python agent.

### 9.3 Schema guard in Go

`api/internal/cluster/schemaguard` mirrors `streaming/internal/cluster/schema.go` (same table as
Epic 31 §5.1, same reason texts) with `SchemaRev` = the API's. Paused: `any` routes answer 503
`{code: "paused", reason}` and the web serves the paused page; `local` routes work; `home` routes are
still proxied (the home's API is the newer side). Re-checked every 30 s while paused, every 5 min
otherwise.

### 9.4 Converting worker-only nodes

`maktaba-server setup --node` (or `install-node.sh --full` / `install-node.ps1 -Full`): keeps
`node.json` (identity, key, credentials, home), installs the layout (§2) with the home's current
release (downloaded from the home's mirror and verified), builds the venv, registers the launcher
service, removes the old worker units (`com.maktaba.node*`, `maktaba-node*.service`, the
`maktaba-node` task) after the new service runs, and reports `full_node = true`. Extra worker
instances (`--instances N`) become `[pipeline] instances = N` in the node's `server.toml`. Worker-only
nodes are **converted automatically on their next update** (owner decision): the rollout sends their
pipeline agent `cmd update.convert {run_id, version}` (a new command in the pipeline release that ships
35.8). The Python agent downloads the platform archive from the home's mirror, verifies it with
`sigstore-python` against the same pinned identity and the checksums (both hash-pinned in the
pipeline's constraints), and runs `bin/releases/<v>/maktaba-server setup --node --converting`, which
does the steps above and reports through the new machine agent. A worker-only node too old to know
`update.convert` is shown "worker-only — re-run the installer once" (its installer run converts it).
As built: the conversion is the machine's turn in the rollout (drained first, one machine at a
time, inside the window; target state `converting`), and a catch-up converts the worker-only
machines in the window. The worker downloads from the artifact mirror with a download token
the job carries (`download_token`: HMAC-SHA256 by a home-held key kept in the mirror's folder,
naming the machine and the release, four hours; the mirror's artifact route alone takes it, for
a machine still approved; never the wheelhouse or TUF routes, which setup does not use), never
sending it to GitHub (a bearer token: whoever holds it fetches that release's public files as
that machine until it expires or the machine is revoked; never logged, never persisted). An
installed full node's start removes leftover Epic 31 worker units (a conversion that missed its
health wait, which then exits 0, or stopped before removing them), so no second pipeline stays;
setup run again resumes (the release installed already). The worker verifies with the Sigstore root the server compiles in (a
copy in the pipeline package, refreshed with it by `trusted-root.yml`), runs setup outside its
own service (a `systemd-run --scope`, a `Win32_Process.Create` process outside the scheduled
task's job, a new session on macOS) and answers each later `update.convert` of the run with how
far it got; the target is healthy once the machine's new agent reports the release. Setup keeps
the worker's extra instances (`maktaba-node@N`) as `[pipeline] instances`.
This PC (the Windows worker node joined today) converts on the first rollout after 35.8 ships if its
pipeline has `update.convert`, else on its next installer run.

## 10. Auth across machines (story 35.9)

### 10.1 What a node may know

A node's database role has no access to `users`, `web_sessions`, `refresh_tokens`,
`personal_access_tokens`, `password_reset_tokens` (Epic 31 §3.3), and keeps none. The home's API is
the only authority; the node asks.

### 10.2 Delegation key

After the channel's `welcome`, the hub sends `keys {delegation: {id, key (32 bytes, b64), not_after}}`
sealed on the channel; the node keeps it in memory (and in `node.json` for restarts while the channel
is down, 0600). The home stores it sealed in `home_secrets` (`cluster.node.<id>.delegation`, current
and previous for a 10-minute overlap) and rotates daily. Revoking the node deletes both at once.

Signature on every node → home HTTP request:

```
X-Maktaba-Node:      <node id>
X-Maktaba-Node-Key:  <key id>
X-Maktaba-Node-Ts:   <unix seconds>
X-Maktaba-Node-Sig:  b64(HMAC-SHA256(key, method ‖ "\n" ‖ path?query ‖ "\n" ‖ ts ‖ "\n" ‖ sha256(body) ‖ "\n" ‖ delegated-session-hash))
```

`X-Maktaba-Delegated-Session` carries the user's session id (bearer tokens pass through as
`Authorization`); the signature covers its SHA-256 (`delegated-session-hash`, empty when absent), so a
signed request cannot be re-used with another session. Signature vectors:
`shared/testdata/cluster/delegation_vectors.json`, checked by the home's and the node's tests.

The home rejects ±60 s skew, an unknown or revoked node, a used `(node, ts, sig)` within the window
(replay cache), and logs every rejection. Streaming bodies (uploads) sign `UNSIGNED-PAYLOAD` and are
limited to the routes that allow it.

### 10.3 Resolving a request on a node

```
browser ──cookie maktaba_session=<sid>──► node API middleware
   cache hit (sid → principal, ≤ 30 s, not evicted) ──► principal
   miss ──► POST home /api/cluster/delegate/introspect {session: sid} (signed)
            home: sessions.Lookup(sid) (revoked/expired → 401), user active, roles,
                  csrf_token hash, expires_at; TouchLastSeen (debounced, as today)
        ◄── {principal:{user_id, roles, is_admin, …}, csrf_sha256, expires_at, origin_node_id}
   CSRF (mutating methods): sha256(X-CSRF-Token) == csrf_sha256 (double submit as today)
```

Bearer access tokens (RS256, Story 10.6) are verified locally with the home's JWKS (fetched through
the home, cached 5 min, refreshed on an unknown `kid`); refresh, PATs and anything that needs a
database row are introspected. Negative answers are cached 5 s (a burst of bad cookies does not
flood the home). The cache is keyed by `sha256(sid)`, never the raw id.

**Revocation.** Every place on the home that revokes a session (sign-out, password change, user
disabled, admin revoke, session expiry sweep) calls `cluster.NotifyRevoked(sessionIDs | userID)`; the
hub sends `auth.revoked` to every machine agent, which forwards it to its API; the API evicts. With
the channel down the TTL bounds staleness.

**A home outage** degrades a node to read-only instead of taking it down, on the node's own live
copy of the home's database (Epic 37, `specs/epics/37-node-replica`, which replaced PR #145's
last answers): a credential the home cannot be asked about is looked up on the copy, by the home's
own code; a sign-in is checked there too and makes a session valid on that machine only. Reads
(GET, HEAD) and `local` routes (this machine's status, VOD playback, its own TV) are served, marked
`X-Maktaba-Home: unreachable`, and the web app shows a notice over the pages; a change through the
home's database gets 503 `home_unreachable`. With no usable copy, a `local` route answers anonymously
and anything else is a 503 with `readable: false` (the full "cannot reach its home" page). After
`delegate_unreachable_grace_seconds` (30 s) the node stops asking the home per request and refuses
`home` routes at once, except the session probe and the sign-in, which it answers from its copy. A
video the node cannot read is "not available while the home is unreachable" (503
`video_unavailable_offline`). The node's pipeline pauses on its own signal,
the home's database not answering its beat (two in a row): it claims nothing (the schema guard's
gate), lets jobs in flight finish unless the outage nears the 90 s claim lease (then stops them:
the home would hand them to another machine), and at the first beat answered releases what its ended
jobs left claimed and resumes; the pause shows in its log and its local agent status
(`maktaba-pipeline cluster status`).

**The home's database down too.** The node reads its own copy of the home's database (Epic 37):
the library, playback (a video's library, path and length; the probe rows; the share map) and its
own TV channels, for as long as the home's database does not answer; a session opened meanwhile is
recorded on the node (the copy is read only), so its owner can close it. Both roles start with the
home's database down: the API's role check waits for it (a role that can run DDL then stops the API,
as at start), the streaming role's pool connects when it answers. A node with no copy plays nothing
and serves its own routes only meanwhile.

### 10.4 Signing in on a node

`POST /api/auth/login` (and the other auth routes: logout, password reset, MFA) are `home` class:
the node proxies them to the home with `X-Maktaba-Origin-Node: <node id>` (signed). The home creates
the session row with `origin_node_id` and returns `Set-Cookie` without a `Domain` attribute, so the
browser stores it for the node's host. Cookie names and attributes are the same as the home's
(`httpsec.SecureCookie`: HttpOnly, SameSite=Lax, Secure unless `MAKTABA_DEV=1`); the CSRF cookie is
per host too. Each host is its own origin: CORS lists do not change (the browser never calls another
machine's API cross-origin; the cluster sign-in uses top-level redirects).

### 10.5 Cluster sign-in (one sign-in for every machine)

```
user on node B, not signed in ──► web shows "Continue as … (signed in on <home>)" when the home is reachable
  B: verifier = random 32 bytes, challenge = S256(verifier), state cookie on B
  302 → http://<home>/api/cluster/sso/authorize?node=B&challenge=…&return=/library
  home: signed-in session? (else its own login page, then continue)
        node B approved and full? return path relative?
        code = random 32 bytes; store sha256(code), node, user, parent session, challenge, 60 s
  302 → http://<B>/api/cluster/sso/callback?code=…&state=…
  B: state matches cookie → POST home /api/cluster/sso/exchange {code, verifier} (signed by B)
  home: code unused, unexpired, node == B, S256(verifier) == challenge → mark used,
        create web_session(parent_session_id = home session, origin_node_id = B)
  B: Set-Cookie for B's host, 302 → /library
```

Signing out on the home revokes the session and its children (FK cascade + `auth.revoked`). Signing
out on a node revokes only that child. Two-step sign-in, password changes and disabled users all
apply at the home's `authorize`.

### 10.6 ACL

The node's API runs the home's middleware chain unchanged with the resolved principal: role checks,
`principal.IsAdmin`, per-user library ACLs (they are data rows the node role can read), rate limits
(per node, per user). Admin-only `home` routes are proxied only after the node's own admin check, and
the home checks again (it resolves the forwarded session itself; it never trusts a node's claim about
a user).

## 11. Routing on a node (story 35.10)

### 11.1 Placement classes

`api/internal/router` gains `Class(route) ∈ {local, any, home}` declared next to each mount
(`r.With(placement.Any).Get(…)`). The router test walks every registered route and fails on one
without a class. On the home every class is served locally. On a node:

| Class | Served | Examples |
|---|---|---|
| `local` | by the node | `/api/system/version`, `/api/system/processes`, `/api/system/health`, `/healthz`, `/api/cluster/self`, `/api/cluster/sso/callback` |
| `any` | by the node from the database (schema guard must pass) | library browse and video detail, collections, people, chapters, subtitles list and files (on the share), queue and job reads, TV guide and channel list reads, settings reads |
| `home` | proxied to the home | auth, users and sessions, watch progress and history (device and viewing tables are revoked from the node role), search (the vector index), thumbnails/posters/sprites/review pages (home caches), every admin write that touches home-only resources (settings writes, extensions, Plex, Cloudflare, TV changes, cluster, updates, backups), the hub WebSocket `/api/cluster/ws`, uploads, LLM and service doors |

Writes that the node role could technically do (metadata edits) stay `home` in v1: one writer path
keeps audit, NOTIFY and cache invalidation on the home; `any` writes are a later optimisation.

### 11.2 The home proxy

`api/internal/cluster/homeproxy`: `httputil.ReverseProxy` to `MAKTABA_HOME_API_URL` with the
signature headers (§10.2), `X-Maktaba-Delegated-Session` (the cookie's session id or the
`Authorization` header passed through), `X-Forwarded-For`, `X-Forwarded-Host`, and
`X-Maktaba-Via: node`. WebSocket upgrades are proxied (Go 1.12+ ReverseProxy handles `Upgrade`).
`FlushInterval: -1` for SSE and streams. Response `Set-Cookie` headers lose any `Domain` attribute.
The home's middleware sees `X-Maktaba-Via: node` + a valid signature, resolves the delegated session
itself and serves the request as that user; the relay marker `X-Maktaba-Via: relay` keeps its
meaning (a node's proxy strips an incoming one, as the web proxy does).

### 11.3 TV and images

- `/stream/…` on a node's web port: the node's stream gate checks the node session (as
  `stream_gate.go` does), then proxies to the home's front door (`MAKTABA_HOME_STREAM_URL`, the home's
  streaming port) — the front door stays the one place that routes channels (Epic 32 R3).
  `/xmltv.xml` and `/lineup.m3u` are proxied as is; the URLs inside them name the home (its
  `PUBLIC_BASE_URL`), so Plex and VLC always use the home.
- Images from home-only caches go through the proxy with a disk cache on the node:
  `<data_dir>/cache/proxy/<sha256(url)>` with the response's `ETag`/`Cache-Control`; immutable
  content-addressed art (Epic 33 art digests) is cached forever, the rest revalidated; LRU eviction
  at `proxy_cache_mb`.
- Video on demand (Epic 08): **served by the node itself** — its streaming role reads the file from
  the NAS through its own mounts (story 31.3 resolver) and transcodes locally; nothing passes through
  the home. Fallback in §11.5.

### 11.4 Pages on a node

| Page | On a node |
|---|---|
| Home, Library, Video detail, Collections, People | `any` reads, local; images via the proxy cache |
| Player (VOD) | local: the node's streaming role reads the NAS itself (fallback §11.5) |
| Live TV player, Guide | through the home's front door (§11.3) |
| Search | `home` (vector index) |
| Continue watching, history, favourites | `home` |
| Processing, Machines, Logs | `home` (hub WebSocket proxied) |
| Admin settings, Extensions, Plex, Cloudflare, Users | `home` |
| Live TV Changes, On air | `home` (controller on the home) |
| Updates | `home`; the node's own row comes from `local` too when the home is unreachable |
| System health | `local` for this machine's processes, `home` for the cluster |

The SPA is the node's build; when the node is behind the home (during a rollout) a `home` route may
answer with fields the SPA ignores (additive APIs). A node whose code is paused shows the paused page
(R6.4): "This machine is waiting for its update (v1.4.2 → v1.5.0). Use Maktaba on <home name> —
http://…" with an automatic reload when the node's version changes.

### 11.5 Media access and the VOD fallback

Media is read where it is needed. Every machine reads library files from the NAS through its own
mounts and the canonical share-path resolver (story 31.3, #81): the node's VOD playback and
transcoding, scanning, thumbnails (home-only stage, on the home), transcription and channel encoding.
None of it travels through the home or the cluster link (§18), which carry only control,
database-adjacent calls, the secret-holding service door and machine-to-machine TV streams.

Fallback, only when a machine's storage check (Epic 31 §6) says `read: error`: VOD on that machine is
served by **a ticketed stream from a machine that can read the share** — the home if its storage check
is good, else the least-loaded full node whose check is good. The requesting node asks the home on its
control channel (`vod.fallback {video_id, profile}`); the home picks the server, starts the session
there (on itself, or by a command on that node's control channel) and returns a stream ticket; the
requesting node opens the stream (`purpose: vod`) and serves the bytes to its browser as if local.
When the serving machine is another node, the home splices the two ticketed streams (nodes never
accept connections from each other). Redirecting the browser to the other machine was rejected: it
changes the address the user opened, needs a sign-in there, and breaks the player's same-origin
session. The Machines page shows "VOD served by <machine>: share not readable here" on the affected
machine, with its storage check detail; the fallback stops as soon as the check is good again.

## 12. Discovery (story 35.11)

`cmd/maktaba-server/internal/mdns` (the supervisor's advertiser) and `api/internal/discovery/mdns.go`
(the API's, Epic 31) converge: in node mode only the supervisor advertises. TXT keys added:
`role=home|node`, `home=<home instance id 8>`, `web=<web port>`, `name=<machine name>`; `id` (this
machine's own 8 hex: the home's instance id or the node id) and `protocol=1` are kept from Epic 31;
`join=` stays home-only. Clients (desktop, mobile, TV apps' discovery, `maktaba-pipeline join`) group results by
`home`; `join` keeps ignoring `role=node` entries (one cannot join a node). The apps' server picker
shows "<home name> (3 machines)" and connects to the home when reachable, else to the first
reachable machine of the same cluster (same `home` id); a stored server is remembered by home
instance id, not by address.

## 13. The rollout to machines (story 35.12)

### 13.1 Commands and frames

The fixed command list (Epic 31 design-agent §5.4) gains, for machine agents only:

| `cmd.name` | `args` | Node does |
|---|---|---|
| `update.prepare` | `run_id`, `version`, `manifest_sha256`, `mirror` (home URL path) | download from the mirror (signed GETs; fallback GitHub after 3 failures), §3.2 verify, stage (§2.1, §3.4); replies `cmd.result` at once (`accepted`), progress as `update.progress`, ends with `update.state {prepared_version}` |
| `update.apply` | `run_id`, `version` | §13.3 |
| `update.abort` | `run_id` | stop preparing; delete `<v>.tmp` |
| `update.rollback` | `run_id` | `current = previous`, exit 75 (the home decided, e.g. admin pressed Roll back) |

`update.progress {run_id, step, state, pct?, detail}` and `update.state {version, prepared_version,
platform, layout: "self_managed"|"package"|"worker_only"}` are C→S frames. A node accepts `update.*`
only on its authenticated v2 session to the home in its node config; `version` must equal the
`release.json` it verified (the home cannot make it install something unsigned). Each node keeps its
own journal (§5.2 shape) so a power loss during its apply resumes or rolls back.

### 13.2 The mirror

`GET /api/cluster/updates/artifacts/{version}/{name}`: the files of a release the home verified,
kept under `<data_dir>/updates/mirror/<version>/`. Each is taken once: from the home's own staged
release when it holds a copy that verifies (its `bin/releases/<version>/` keeps the signed files and
the pipeline's wheel and constraints; the home keeps extracted programs, not archives), else from the
release's GitHub asset, ahead of the machines' prepare for each platform the machines run (and on a
machine's first ask). A file is kept only when its SHA-256 is the one the signed `checksums.txt`
lists (`checksums.txt` itself: the digest whose signature the catalog verified), written through a
synced temporary file with its `.ok` marker last, and checked again once in each process: a copy a
power cut or a damaged block spoilt is fetched again, never served for ever. The trusted root
reaches the machines through `/api/cluster/updates/tuf/` (Sigstore's TUF repository passed on,
verified by each machine's TUF client, §3.3). Node-signed (§10.2), range requests, at most 3
downloads at once per machine. The home verifies what it mirrors, the node verifies again; a node
never trusts the home's verification, and a file whose mirror copy failed its verification is
taken from GitHub on the stage's next try.

### 13.3 Sequence

```
home_committed
  ├─ for each online full node (in parallel, at most 3 downloads at once): update.prepare → prepared
  └─ for each target in order (non-streaming first, then streaming; then by name):
       wait: target prepared, policy allows its platform, window (+overrun) for streaming nodes
       update.apply ──► node:
          drain: pipeline (`drain` cmd to its workers, wait ≤ drain_timeout), streaming (§7.4)
          write pending; previous = current; current = <v>; exit 75 (launcher restarts)
          new supervisor: roles start from bin/releases/<v>/
          checks: schema guard fits (the home is at the new schema already), api /readyz,
                  pipeline /healthz + agent session with the new version, streaming /readyz,
                  machine agent reconnected
          remove pending; watermark = <v>; undrain; update.state {version}
       home: target healthy within node_health_timeout? next : failure (§13.4)
done | done_needs_person (only machines a person must see to) | done_with_failures | held
```

### 13.4 Failures

A node that fails to prepare (download, verify, venv) is `failed_prepare`: nothing changed on it, the
home retries the prepare once at the next target and the policy below applies. A node that fails a check
during apply rolls back by itself (§2.2 guard or its own orchestrator: `current =
previous`, exit 75), stays drained, reports `rolled_back` with the failed check. If the previous code
does not fit the schema (a release that raised `min_node_schema`), it comes up **paused** (Epic 31
§5.1) — paused, reported, never running old code on a schema that breaks it. Then
`node_failure_policy`:

- `hold_platform` (default): other nodes of the same `platform` (e.g. `windows/amd64`) are `held`;
  others continue. Held targets show "held: <node> failed on this platform" with Retry / Skip.
- `halt`: all remaining targets held.
- `continue`: next target regardless.

A node that does not answer within `node_health_timeout` is treated as failed (it may still finish
and report later; a late `healthy` is accepted and recorded).

### 13.5 Offline nodes and catch-up

A node offline at rollout time is `offline`. When its machine agent reconnects, its `hello` /
`update.state` reports its version; if it is behind the watermark and a committed run exists, the
home creates a `catch_up` run with that one target (immediately, or in the window for streaming nodes
or when `catch_up = window`). A node paused by the schema guard is caught up immediately regardless.

### 13.6 Skew rules

| Situation | Rule |
|---|---|
| Node version > home | Never by the updater (R8.6). A hand-installed newer node: its schema guard pauses it while the home's schema is behind; the Updates page flags "ahead of the home". |
| Node behind, `min_node_schema ≤ node schema_rev` | Runs (additive schema), stages whose definition moved on stay off (Epic 31 reason 2); the page shows "behind — update pending". |
| Node behind, `min_node_schema > node schema_rev` | Paused (Epic 31 §5.1) until updated; caught up immediately. |
| Agent protocol | v2 additive frames only; a node older than the update frames is "worker-only/legacy — convert". |
| Rollback target | Only `previous`, only if `previous.schema_rev ≥ maktaba_min_node_schema()`. |

## 14. Failure modes

| Failure | Behaviour |
|---|---|
| Power loss on the home before migrate | Boot: journal resumes the step; window passed → `failed (interrupted)`, next window. |
| Power loss during migrate | Boot: `migrate up` again; transactional files rolled back by Postgres; NO TRANSACTION files idempotent. |
| Power loss after `current` flipped | Launcher starts the new version with `pending`; it confirms or the guard falls back; the journal finishes the run either way. |
| Home update fails a check | Before migrate: nothing changed. After: binaries roll back, schema stays; nodes untouched (rollout starts only after commit). Version blocked; admin notified. |
| GitHub unreachable | No new release; the last check time shows; a 14-day stale check is a warning notification (freeze-attack hint). |
| Signature or checksum invalid | Never installed; critical notification; version blocked. |
| Release withdrawn after being seen | Re-check before the run: not installed; already installed: notification "v1.5.0 was withdrawn upstream" (no automatic downgrade). |
| Disk full | Preflight refuses; mid-run: the step fails, staging directories are removed; old release untouched. |
| TV change pending approval | The binary change queues; run fails at window end with `tv_change_pending`; retried next window. |
| Viewers watching during the fallback restart | Wait in the window; `tv_busy` at its end; retried next window. |
| Node offline during rollout | `offline` target; catch-up on reconnect. |
| Node fails | Rolls back, drained, reported; policy decides the rest. |
| Node loses the home mid-apply | Its own journal: finish the restart and checks it can; schema guard and agent check need the home → after `node_health_timeout` it rolls back. |
| Home unreachable for a node's API | Past the grace (30 s) the node runs read-only on its own copy of the home's database (Epic 37): sign-ins, reads, playback and TV go on, a sign-out is made here and handed to the home later, changes get 503 `home_unreachable`; with no current copy only its own routes answer. |
| Supervisor handover fails (Windows job) | Degrades to an ordered restart in the window (TV interruption only if allowed by viewers ≤ max). |
| Launcher crash-loops (corrupt launcher) | The service manager restarts it; the launcher is replaced only after a run committed, its previous file kept as `.old` until the next good start; the runbook covers restoring it by hand. |
| Two runs at once | The supervisor's lock (`updates/lock`, flock / `LockFileEx`) and `update_runs` partial unique index on active states. |

## 15. Security summary

Trust anchors: the embedded Sigstore root and the pinned workflow identity (releases); the home's
cluster key (channel, Epic 31 v2); per-node delegation keys (HTTP). A node installs only what it
verified itself; the home cannot make it install an unsigned or older build. The home resolves every
user session; nodes cache short and, with the home gone, act on its last answer only within a
bounded window, read-only (§10.3). Backups stay on the home's disk. Nothing in an API
request names a file path or URL to install from. The update flow's admin actions are audited.
Residual risks: the legacy plain-HTTP paths while `cluster.link.mode` is `prefer` (closed by
`require`, §18), browsers on plain HTTP to each machine's web UI (unchanged; HTTPS optional), and a
compromised GitHub
workflow with tag-push rights (mitigated by branch/tag protection and the 24 h release age).

## 16. Observability

Metrics (`maktaba_update_`): `runs_total{result}`, `run_duration_seconds`, `step_duration_seconds{step}`,
`migration_duration_seconds`, `backup_bytes`, `targets{state}`, `node_apply_duration_seconds`,
`verify_failures_total{reason}`, `machines_behind`, `last_check_timestamp`. Events: `update_planned`,
`update_step_started|finished|failed`, `update_home_committed`, `update_rolled_back`,
`update_node_prepared|applied|failed|held|caught_up`, `launcher_rollback`, `release_verify_failed`,
`release_withdrawn`. The hub publishes the run on a topic `updates` for the Updates page and the
control centre (Epic 33.15 health samples gain `update.state`).

## 17. Testing approach

- **Unit (Go)**: `releaseverify` with fixture bundles (valid; wrong SAN; wrong issuer; another tag;
  tampered `checksums.txt`; missing tlog; expired root) — fixtures produced once by a test workflow
  and committed; target selection table (channel, pin, skip, age, watermark, min_upgrade_from,
  contract); journal resume table (every step × crash before/after); launcher guard (fake child
  exits); pointer atomicity; handover adoption (fake children, pid reuse); delegation signature
  (vectors shared with the node), replay cache; introspection cache and revocation; route class
  walk; proxy (cookies, WS upgrade, SSE flush, signature headers).
- **Unit (Python)**: nothing new in the pipeline except `--version` and drain handling already
  covered; the venv builder moves to Go.
- **Integration**: a fake GitHub (httptest) serving a real signed fixture release; a Postgres; the
  supervisor with fake children; a full home run from v0.0.1-test to v0.0.2-test including backup,
  rehearsal and migrate with two test migrations (one NO TRANSACTION); a failing `/readyz` to force
  rollback.
- **E2E (CI, nightly)**: three VMs/runners (macOS arm64, Ubuntu, Windows) joined into a cluster from
  two locally built and signed (staging identity) releases; the rollout end to end; TV client
  connected through the update (TS + HLS, story 33.14 TC5).
- **Chaos**: kill -9 the supervisor at each step; power-cut (VM reset) during migrate and after the
  pointer flip; drop the node's network mid-apply; corrupt a staged binary; replay an older signed
  release; flood the delegation endpoint.

## 18. The cluster link: machine-to-machine TLS (story 35.16)

Owner decisions (2026-10-19): include machine-to-machine TLS now; use **encrypted sockets rather than
many HTTP requests**; WebSockets over mutual TLS on the existing API port; **"auth first, then stream
on another"**: one control channel per machine, and every data stream on its own connection opened
with a ticket from the control channel.

### 18.1 What exists today and what moves

| Flow today | Transport today | On the link |
|---|---|---|
| Cluster agent of every pipeline process (Epic 31 phase 2) | `ws://home:8080/api/cluster/agent`, frames sealed with the v2 handshake keys | Full nodes: the machine's **control channel** carries the machine agent; local pipeline processes connect to the supervisor's loopback agent endpoint, which forwards their sessions as control-channel sessions (their v2 sealing is kept end to end, so the Python agent does not change). Worker-only nodes keep the direct v2 WebSocket until converted. |
| Service door (story 31.R) `svc.*` | agent WebSocket | control channel (small requests); answers > 1 MiB move to a data stream (`purpose: svc_body`) |
| Front door → streaming node (Epic 32 §4, TS/HLS pulls; 33.8 relay source pulls) | HTTP to the node's streaming port, HMAC with the cluster stream key | one **data stream** per pull, the home asking the node (§18.5); the node's streaming port binds loopback once `require` |
| Art fetches by streaming nodes (Epic 32 R1.5) | HTTP to the home | control channel request (small, cached) |
| Session checks, `home` route proxy, SSO exchange (35.9, 35.10) | signed HTTP (delegation HMAC) | control channel requests; the HMAC path stays only for machines not on the link (`prefer`) |
| Release mirror, wheelhouse (35.12, §3.4), backups | HTTP | one data stream per file |
| Postgres | TCP, `sslmode=prefer` (Epic 31) | TCP with TLS `verify-full` + client certificate (§18.8) |
| Browser ↔ any machine's web UI | HTTP | unchanged (optional HTTPS, §18.9) |
| Library media (NAS) | each machine's own mounts | unchanged: never on the link (§11.5) |

### 18.2 The cluster CA

- ECDSA P-256 root, 10 years, created by the home's API at first start after this story
  (`cluster-ca.key`, 0600, beside `secrets.key`; a sealed copy in `home_secrets` under
  `cluster.ca.key` so a restore of the database and the secrets key recovers it). Never on a node,
  never on the share, excluded from diagnostics bundles.
- Machine certificates: ECDSA P-256 key generated **on the machine** (never leaves it), CSR sent to
  the home; leaf valid 30 days (`cluster.tls.cert_days`), `not_before` = now − 5 min (clock skew),
  `KeyUsage` digitalSignature, `ExtKeyUsage` clientAuth + serverAuth, SAN URI
  `maktaba://node/<node id>`, CN `maktaba_node_<32 hex>` (= its database role, §18.8). Renewed when a
  third of its life is left.
- The home's own certificates: `server` (SANs: every address of the home — IPs, host name, `.local`
  name, the addresses nodes use in `node.json`), `pg_server` (the same SANs, for Postgres), `client`
  (`maktaba://home/<instance id>`, used when the home's front door is the TLS client of a stream it
  asked for). Re-issued when the home's address set changes.
- Every issued certificate is a `cluster_certs` row (slot S4).

### 18.3 Issuance at join and migration of joined machines

- **New joins:** the join request (Epic 31 §3.1) gains `csr`; at approval the sealed bundle gains
  `tls: {ca_cert, ca_sha256, cert}` — the CA fingerprint is pinned through the pairing code's
  protection, so no trust-on-first-use.
- **Already joined machines** (this PC included): after updating to the release with this story, the
  machine sends `cert.csr {csr}` on its existing v2 agent session — authenticated both ways by the
  pairing key and the home's cluster key, so the CA certificate returned on it is as trustworthy as
  the join bundle. No re-join. A machine joined before the v2 handshake has no `home.cluster_pub` and
  must re-join (as Epic 31 already says).
- **Renewal:** `cert.renew {csr}` on the control channel; the new certificate is used for new
  connections, the old one until its connections close. A machine whose certificate expired (offline
  for over 30 days) falls back to the v2 agent session to renew (allowed in `prefer`; in `require` the
  v2 session is accepted only for `cert.*`).

### 18.4 Control channel

```
node ──TLS 1.3 (client cert)──► home API port (8080)
        ClientHello sniffed on the port: TLS → TLS listener (VerifyClientCertIfGiven, cluster CA);
        plain HTTP → the existing HTTP server, unchanged for browsers and apps
     ──GET /api/cluster/link (Upgrade: websocket)──►
        home: cert chains to the cluster CA, not expired, serial not revoked,
              SAN node id is an approved cluster_nodes row whose cert_serial matches
     ◄── hello {session, settings, max_streams}  (text frame)
        node: server cert chains to the pinned CA and names the address it dialled
```

- The first byte decides TLS vs plain HTTP on the API's existing port (`0x16` = TLS handshake), so
  no new port and no firewall change; browsers and apps keep plain HTTP until `web_https` (§18.9).
- Authentication is the TLS client certificate bound to the node id; nothing else is sent before it.
  The Epic 31 node identity check (approved, not revoked, same pairing key on record) runs on the
  certificate's node id.
- **Per-frame sealing dropped on the link.** TLS 1.3 already gives confidentiality, integrity,
  replay protection, mutual authentication and forward secrecy with per-machine keys from a CA whose
  key never leaves the home. Sealing again would double the crypto, add a second protocol to keep
  correct in Go, and its strict per-session counters would serialise the multiplexing. Sealing stays
  where TLS is absent: the legacy direct WebSocket, and the pipeline processes' own v2 sessions that
  the supervisor forwards (kept end to end because it costs nothing and needs no Python change).
- **Framing on the control channel** (binary WebSocket messages): `{id u32, kind u8 (req|resp|chunk|end|cancel|event), class u8, len}` + payload ≤ 16 KiB. Classes and order of service by the sender's
  scheduler: `control` (agent frames, commands, `auth.revoked`, `cert.*`, tickets) first, then `rpc`
  (session checks, proxied `home` API requests, service door, art). A request body or answer larger
  than 1 MiB is not sent here: the home answers with a stream ticket and it moves to a data stream.
  HTTP over the channel: the node's home proxy (§11.2) uses a `RoundTripper` that sends the request
  as `req` + `chunk`s and reads `resp` + `chunk`s, so the proxy code is unchanged; WebSocket upgrades
  of proxied routes (the hub socket) become a data stream with `purpose: ws_proxy`.
- Keepalive: WebSocket ping every 15 s; 3 missed → closed.

### 18.5 Data streams and tickets

Every data stream is its own `wss://` with mutual TLS (same certificate), opened only with a ticket:

```
node ──control: stream.open {purpose, resource}──► home  (or the home decides: stream.offer {…} to the node)
home: machine's control session live? purpose allowed for it? under max_streams? resource allowed?
     ◄── stream.ticket {ticket, url: "/api/cluster/link/stream", expires_at}
node ──TLS (client cert) ──GET /api/cluster/link/stream  Authorization: Maktaba-Ticket <ticket>──► home
home: ticket valid, unused, unexpired, issued to THIS certificate's node id, purpose/resource match → 101
```

Ticket: 32 random bytes (base64url) handed out on the control channel; the home keeps, in memory,
`sha256(ticket) → {node_id, cert_serial, control_session, purpose, resource, nonce, issued_at,
expires_at = +30 s, used}`. Single use; a second presentation or one by another certificate is refused
and logged (`link_ticket_reuse`, an alert after 3 in 10 min). Purposes: `tv_pull` (resource: channel
id + slot + epoch), `relay_pull`, `artifact` (version + file name), `wheelhouse`, `backup`, `vod`
(fallback session id, §11.5), `svc_body`, `ws_proxy`.

Who opens: always the **node** (nodes never accept connections; the home never dials a node). When
the home needs bytes from a node — the front door pulling a channel the node encodes — the home sends
`stream.offer {purpose: tv_pull, resource, ticket}` on the node's control channel and the node dials
the stream and serves the slot's TS/HLS on it; the front door reads it as its upstream (Epic 32
§4.4 replaced). The node's streaming port then binds loopback only.

Each stream is one WebSocket with its own TCP connection and flow control: a busy TV stream cannot
delay another stream or the control channel. Chunks ≤ 64 KiB per message; TS is forwarded as
received. Per-machine limit `cluster.link.max_streams` (32; the home answers `stream.refused
{reason: limit}`); per-purpose limits follow existing settings (Epic 32 capacity for `tv_pull`).

### 18.6 Lifetimes, revocation, reconnect

| Event | Effect |
|---|---|
| Control channel drops (network, home API restart) | The node reconnects with backoff (1 s doubling to 30 s, ±20 % jitter). In-flight requests fail at once with `link_lost`; idempotent ones (GET, session checks) retry once after reconnect; proxied browser requests get 502 `home_unreachable`. **Open data streams keep running for `stream_grace_seconds` (60)**; if the same machine's control session is back in time they are re-attached to it, otherwise the home closes them (TV: Epic 32/33 failover or restart path; downloads resume by range on a new ticket). No new ticket is issued without a live control session. |
| Machine revoked | The home closes the control channel (`4010 revoked`) and **every stream at once** (no grace), marks the certificate revoked, refuses its serial; Postgres: role `NOLOGIN` (Epic 31). |
| Certificate expires | New connections refused; existing ones closed at expiry; renewal path §18.3. |
| Home CA rotated (manual, rare) | New CA issued; both trusted for 7 days; machines renew under the new CA over their control channel; the old one is removed. |
| Ticket not used within 30 s | Expires; the requester asks again. |

### 18.7 Mode and migration

`cluster.link.mode`: `off` (today's paths), `prefer` (default from this release: a machine with a
certificate uses the link, the others the legacy paths), `require` (legacy paths refused: plain agent
WebSocket from nodes, delegation HMAC over HTTP, front-door HTTP to node streaming ports). The home
switches to `require` by itself when every approved machine has held a control session for 24 h, and
says so on the Machines page; an admin can set it back. The order for the existing node (this PC):
update → `cert.csr` on the v2 session → control channel up → streams move → Postgres DSN switches to
`verify-full` at the next restart (§18.8) → nothing else.

### 18.8 Postgres

Kept as its own TCP connection (pgx / psycopg pools, `LISTEN/NOTIFY`, the hottest path for workers),
with TLS: the home's Postgres gets `ssl = on`, `ssl_cert_file` / `ssl_key_file` = the `pg_server`
certificate, `ssl_ca_file` = the cluster CA (`maktaba-server setup --pg-tls` writes them and runs
`ALTER SYSTEM` + `pg_reload_conf()` when the migration user may; otherwise prints the lines for the
admin), and `pg_hba.conf` `hostssl … clientcert=verify-full` for the `maktaba_node_*` roles once
`require`. Nodes connect with `sslmode=verify-full sslrootcert=<ca> sslcert=<cert> sslkey=<key>`;
the certificate's CN is the node's role, so a stolen password alone is useless.
**Tunnelling Postgres through the link** was weighed and not chosen: it would put the database behind
the same TCP connection as control traffic (or need one more stream per pooled connection), add a hop
and a failure mode to every query, and break the drivers' own reconnect and `LISTEN` behaviour, for
the benefit of one fewer port. It can be revisited (follow-up).

### 18.9 Browsers (optional HTTPS)

Unchanged by default. With `cluster.tls.web_https`, each machine also serves its web UI on 8443 with a
`web` certificate from the cluster CA (SANs: its addresses); `GET /api/cluster/ca.crt` (public) gives
the CA certificate, and the Machines page shows its SHA-256 fingerprint to compare before installing
it. The docs explain installing it on macOS (Keychain, "Always Trust"), Windows (Trusted Root store),
Linux (`/usr/local/share/ca-certificates` + `update-ca-certificates`), iOS/Android (profile / user
CA), and that browsers then accept every machine's HTTPS address; `Secure` cookies work without
`MAKTABA_DEV`.

### 18.10 Comparison of transports (why WebSockets over mTLS)

| | `wss://` + mTLS, control channel + ticketed streams (chosen) | Raw TLS + a multiplexer (yamux) on one socket | QUIC |
|---|---|---|---|
| Port / firewall | the API's existing port (TLS sniffed) | a new port | UDP, often blocked |
| Reuse | the Epic 31 hub, agent framing, Go and Python WebSocket libraries, proxies | new listener and dialer | new stack |
| Head-of-line blocking | none between streams (one TCP connection each); control isolated | one TCP connection for everything: a busy stream delays control unless rate-limited | none |
| Auth of each stream | mTLS + single-use ticket from a live control session | implicit in the session | implicit |
| Cost | our framing on the control channel, one TLS handshake per stream (resumption keeps it cheap) | mature mux, but one shared fate | complexity, Windows/NAT quirks |

### 18.11 Tests

Unit: CA issue/renew/revoke, SAN and CN rules, expired and wrong-CA certificates refused, the TLS
sniffer (TLS and plain on one port), ticket single use / expiry / wrong node / wrong purpose /
reuse alert, `max_streams`, control framing (16 KiB cap, class order: a flood of `rpc` never delays
a `control` frame beyond one message), the HTTP-over-control `RoundTripper` (bodies, cancel, large
answer moved to a stream). Integration: a node and a home with Postgres in `verify-full`; kill the
control channel with a TV stream open (stream survives 60 s, re-attaches; after 60 s closes and the
front door fails over); revoke during streams (all closed at once); migrate a v2-only joined node
without re-join; `prefer` → `require` switch; legacy paths refused in `require`. Load, in two
halves: the integration tier checks it works (20 TV pulls plus a release download on one machine,
every control frame and request answered, none near a stall: a 2 s ceiling, which a runner's load
never reaches and a stalled channel always does); the perf tier (`make perf-ci`, `-tags perf`)
measures the latency: control-channel p99 < 50 ms, control frames and requests alike, as the
link's own cost over the machine's own loopback round trip measured in the same window (CPU shared
with other processes slows every round trip, the link's included), with the numbers reported.
