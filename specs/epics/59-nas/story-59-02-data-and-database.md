# Story 59.02 — The data volume and the database on a NAS: local, SSD first, tuned for spinning disks, quiet hours

> Epic 59 · Phase 1 · Depends on: 59.01, 59.04 · Needed by: 59.09, 59.10, 59.11

## Why

The bundled Postgres lives on the data volume. On a NAS that volume can be:
- an SSD pool;
- an HDD RAID array;
- a network share someone mounted by mistake.

Postgres has three problems on a NAS:
- **Wrong tuning:** it is sized for a cluster (`max_connections=300`, 40 replication slots) and
  tuned for SSDs.
- **No sleep:** on an HDD array, browsing and search are slow, and the array never sleeps.
- **Bad timing:** heavy jobs (a first scan, a transcription backlog, convert) run as soon as they
  are queued, while the NAS is also serving files to the household.

## Description

**Where data may live.**
- `target_not_local` (`internal/storage/disk.go`) already refuses network, FAT and exFAT. This
  story adds:
  - FUSE mounts (`fuse.*` types, Unraid's `/mnt/user` shfs among them) as a **warning**, not a
    refusal: "Your data folder is on a merged share (/mnt/user). Put it on a cache pool or a
    single disk (/mnt/cache/appdata/maktaba) for a reliable database."
  - Each refusal names the filesystem type it found.
- Setup reads the block device under the data dir: `stat` → major:minor → `/sys/dev/block/…`,
  walking up to the whole disk and through md, dm and ZFS vdevs where readable.
  - It records `rotational` (any member rotational counts as rotational) and shows it in Settings
    → This machine.
  - Unknown (a ZFS pool whose vdevs are not visible in the container) is treated as not rotational
    and shown as "unknown".

**Tuning per disk and tier.** On spinning disks:
- Postgres gets `random_page_cost=4`, `effective_io_concurrency=2`, `checkpoint_timeout=15min`,
  `wal_compression=on`;
- setup shows the SSD advice from the README (§6.3).

Memory per tier (59.04):

| Tier | `shared_buffers` | `work_mem` | `maintenance_work_mem` | `max_connections` |
|---|---|---|---|---|
| Light | 256MB | 4MB | 64MB | 60 |
| Standard | 512MB | 8MB | 128MB | 100 |
| Strong | 25% of the memory limit, ≤ 4GB | 16MB | 256MB | 300 (today) |

- Joining a node raises `max_connections` and creates the replication slots (Epic 37). The value
  is stored, so it survives restarts.
- The values go in `postgresql.auto.conf` through `pgbundle`. A household's own `postgresql.conf`
  lines still win.

**Quiet hours.**
- New setting `[schedule] quiet_hours = "01:00-06:00"`, on by default on Light and Standard,
  empty on Strong.
- Queued jobs marked heavy wait for the window unless an admin presses **Run now** on that job or
  library. Heavy jobs are:
  - a library's first scan;
  - transcription of a backlog over 10 items;
  - convert and dedup;
  - thumbnail sprites for over 50 items.
- Playback, a single new file's thumbnail and its transcript (when speech is on) never wait.
- The queue page shows "Waiting for quiet hours (01:00)".

**Disk sleep.**
- On a NAS (detected by 59.04 or `MAKTABA_NAS=1`), the default `sweep_interval_sec` for new
  libraries is 21600 (6 h). Existing libraries keep theirs.
- When the data dir is on an SSD and no job is running, Maktaba reads nothing from media disks.
  The design keeps that true; the device checklist checks it with the NAS's own disk-activity
  view.

## Acceptance criteria

- **Given** a data dir on a rotational disk, **then** setup shows the SSD advice, and `SHOW
  random_page_cost` returns 4.
- **Given** the Light tier, **then** Postgres starts with the table's values and stays under
  512 MB RSS with an idle home of 20,000 items.
- **Given** quiet hours 01:00–06:00 at 14:00, **when** a library with 2,000 files is added,
  **then** its first scan waits, the queue says so, and Run now starts it at once.
- **Given** a data dir on `/mnt/user/...` (shfs), **then** setup warns with the cache-pool advice
  and continues.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | fake `/sys` tree: sda rotational=1, md0 over it | detect | rotational |
| T02 | Go unit | nvme0n1 rotational=0 | detect | not rotational |
| T03 | Go unit | ZFS dataset, no vdevs visible | detect | unknown, shown as unknown |
| T04 | Go Postgres test | Light tier | start pgbundle | settings match the table |
| T05 | Go unit | household `postgresql.conf` sets `shared_buffers` | start | the household's value wins |
| T06 | pipeline unit | quiet hours, clock at 14:00 | enqueue a first scan | `waiting_window`; Run now → runs |
| T07 | pipeline unit | clock at 01:00 | waiting job | runs |
| T08 | Go unit | `fuse.shfs` mount | data-dir check | warning, not refusal |
| T09 | CI container (59.15) | `--memory=4g`, Light | first run + 20k-item fixture | Postgres RSS < 512 MB |

## Strings

| Key | English | Arabic |
|---|---|---|
| `setup.data.rotational` | The database is on spinning disks. Maktaba will work, but browsing and search are faster on an SSD, and the disks will not sleep while Maktaba runs. | قاعدة البيانات على أقراص دوّارة. سيعمل Maktaba، لكن التصفح والبحث أسرع على قرص SSD، ولن تدخل الأقراص في وضع السكون ما دام Maktaba يعمل. |
| `setup.data.fuse` | Your data folder is on a merged share ({path}). Put it on a cache pool or a single disk for a reliable database. | مجلد بياناتك على مشاركة مدمجة ({path}). ضعه على مجمّع التخزين المؤقت أو على قرص واحد لتكون قاعدة البيانات موثوقة. |
| `queue.waiting_quiet_hours` | Waiting for quiet hours ({time}) | بانتظار ساعات الهدوء ({time}) |
| `queue.run_now` | Run now | شغّل الآن |
| `settings.quiet_hours` | Quiet hours: heavy work runs only in this window | ساعات الهدوء: لا تعمل المهام الثقيلة إلا في هذه الفترة |

## Edge cases

- **Quiet hours across midnight** (22:00–06:00) work. An empty value means none.
- **A home with nodes:** heavy jobs that a node takes are not held. Quiet hours protect the NAS,
  not the cluster. The hold applies only to jobs the NAS itself would run.
- **Time zone:** quiet hours use the home's time zone (`TZ`, or the NAS's `/etc/localtime` when
  mounted). The compose files pass `TZ`.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/storage/disk.go` | FUSE warning; rotational detection |
| `cmd/maktaba-server/internal/pgbundle/pgbundle.go` | tier and disk settings in `postgresql.auto.conf` |
| `cmd/maktaba-server/internal/config/config.go` | `[schedule] quiet_hours` |
| `pipeline/src/maktaba_pipeline/engine/` | `waiting_window` state; heavy-job marking; Run now |
| `api/internal/handlers/jobs/` | Run now endpoint (admin; role test in 59.16) |
| `web/` | queue label, Run now, Settings line |
