# Story 59.02 — The data volume and the database on a NAS: local, SSD first, tuned for spinning disks, quiet hours

> Epic 59 · Phase 1 · Depends on: 59.01, 59.04 · Needed by: 59.09, 59.10, 59.11

## Why

The bundled Postgres lives on the data volume. On a NAS that volume can be:
- an SSD pool;
- an HDD RAID array;
- a network share someone mounted by mistake.

Postgres has three problems on a NAS:
- **Wrong tuning:** its memory settings are Postgres's defaults, the same on a 4 GB NAS as on a
  64 GB workstation, and nothing changes for spinning disks.
- **No sleep:** on an HDD array, browsing and search are slow, and the array never sleeps.
- **Bad timing:** heavy jobs (a first scan, a transcription backlog, convert) run as soon as they
  are queued, while the NAS is also serving files to the household.

## Description

**Where data may live.**
- `target_not_local` (`internal/storage/disk_linux.go`) already refuses network, FAT, exFAT
  and FUSE (Unraid's `/mnt/user` shfs is not in its list of local filesystems). That stays. This
  story makes each refusal name the filesystem type it found, and, for a FUSE merged share, say:
  "Your data folder is on a merged share ({path}). Put it on a cache pool or a single disk (for
  example /mnt/cache/appdata/maktaba)."
- Setup finds the disk under the data dir from `/proc/self/mountinfo`, not from `stat`: btrfs
  (most Synology volumes) and ZFS report an anonymous device number (major 0), so `stat` leads
  nowhere.
  - The mount's source (`/dev/mapper/cachedev_0` on DSM, `/dev/md2`, `/dev/sdb1`) is followed
    through `/sys/class/block/<dev>/slaves/` across dm and md layers to the disks, and their
    `queue/rotational` is read. Any rotational member counts as rotational.
  - A ZFS source (`tank/apps`) has no device to follow in a container. Setup then reads the
    pool's vdevs from `/proc/spl/kstat/zfs/<pool>/` where present; otherwise it **asks**: "Is
    Maktaba's data folder on an SSD?", defaulting to No.
  - The result (`rotational`, `ssd`, or `asked`) is shown in Settings → This machine.

**Tuning per disk and tier.** On spinning disks:
- Postgres gets `random_page_cost=4`, `effective_io_concurrency=2`, `checkpoint_timeout=15min`,
  `wal_compression=on`;
- setup shows the SSD advice from the README (§6.3).

Memory per tier (59.04):

| Tier | `shared_buffers` | `work_mem` | `maintenance_work_mem` |
|---|---|---|---|
| Light | 256MB | 4MB | 64MB |
| Standard | 512MB | 8MB | 128MB |
| Strong | 25% of the memory limit, ≤ 4GB | 16MB | 256MB |

- `max_connections` stays 300 on every tier. `pgbundle.go` records that the home alone opens
  about 130 connections and that 100 did not fit. Lowering it is out of this story: it would
  need every pool sized per tier and measured first.
- The values are passed as `-c` arguments with the existing server arguments, as
  `max_connections` already is. That keeps them from being overridden by
  `postgresql.auto.conf`, which outranks `postgresql.conf`.
- **The household's override:** `[postgres] settings = { shared_buffers = "1GB" }` in
  `server.toml`, applied last, wins over the tier and the disk rules.

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
- Today's default is no periodic sweep (`sweep_interval_sec: 0`, `library_mgmt/config.py`), and
  it stays. Scans run when asked, on schedule, or when an app reports a new file. When a
  household sets a sweep on a NAS, the library page says "A regular scan keeps the disks of this
  library awake."
- The media access check (59.03) does not run at every start, for the same reason.
- When the data dir is on an SSD and no job is running, Maktaba reads nothing from media disks.
  The design keeps that true; the device checklist checks it with the NAS's own disk-activity
  view.

## Acceptance criteria

- **Given** a data dir on a rotational disk, **then** setup shows the SSD advice, and `SHOW
  random_page_cost` returns 4.
- **Given** the Light tier, **then** Postgres starts with the table's values, and its memory
  (the sum of PSS over its processes) stays under 512 MB with an idle home of 20,000 items.
- **Given** quiet hours 01:00–06:00 at 14:00, **when** a library with 2,000 files is added,
  **then** its first scan waits, the queue says so, and Run now starts it at once.
- **Given** a data dir on `/mnt/user/...` (shfs), **then** setup refuses, naming `fuse.shfs` and
  giving the cache-pool advice.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | fake mountinfo (btrfs on `/dev/mapper/cachedev_0`) + `/sys` tree: dm → md2 → sda, sdb rotational=1 | detect | rotational |
| T02 | Go unit | ext4 on nvme0n1p1, rotational=0 | detect | ssd |
| T03 | Go unit | ZFS source, no vdevs visible | detect | asks; default No → rotational settings |
| T04 | Go Postgres test | Light tier | start pgbundle | settings match the table; `max_connections` 300 |
| T05 | Go unit | `[postgres] settings` sets `shared_buffers` | start | the household's value wins |
| T06 | pipeline unit | quiet hours, clock at 14:00 | enqueue a first scan | `waiting_window`; Run now → runs |
| T07 | pipeline unit | clock at 01:00 | waiting job | runs |
| T08 | Go unit | `fuse.shfs` mount | data-dir check | refused; message names the type and the cache pool |
| T09 | CI container (59.15) | `--memory=4g`, Light | first run + 20k-item fixture | Postgres PSS sum < 512 MB; container `memory.current` under the limit |

## Strings

| Key | English | Arabic |
|---|---|---|
| `setup.check.note.data_rotational` | The database is on spinning disks. Maktaba will work, but browsing and search are faster on an SSD, and the disks will not sleep while Maktaba runs. | قاعدة البيانات على أقراص دوّارة. سيعمل Maktaba، لكن التصفح والبحث أسرع على قرص SSD، ولن تدخل الأقراص في وضع السكون ما دام Maktaba يعمل. |
| `setup.check.note.data_fuse` | Your data folder is on a merged share ({path}). Put it on a cache pool or a single disk (for example /mnt/cache/appdata/maktaba). | مجلد بياناتك على مشاركة مدمجة ({path}). ضعه على مجمّع التخزين المؤقت أو على قرص واحد (مثل ‎/mnt/cache/appdata/maktaba). |
| `setup.check.note.data_fs_refused` | Maktaba can't keep its database on {fstype} ({path}). Choose a folder on a local disk. | لا يستطيع Maktaba حفظ قاعدة بياناته على نظام الملفات {fstype} ‏({path}). اختر مجلدًا على قرص محلي. |
| `machine.disk.ask` | Is Maktaba's data folder on an SSD? | هل مجلد بيانات Maktaba على قرص SSD؟ |
| `library.sweep_keeps_awake` | A regular scan keeps the disks of this library awake. | الفحص الدوري يُبقي أقراص هذه المكتبة مستيقظة. |
| `queue.waiting.quiet_hours` | Waiting for quiet hours ({at}) | بانتظار ساعات الهدوء ({at}) |
| `queue.run_now` | Run now | شغّل الآن |
| `settings.quiet_hours` | Quiet hours: heavy work runs only in this window | ساعات الهدوء: لا تعمل المهام الثقيلة إلا في هذه الفترة |

## Edge cases

- **Quiet hours across midnight** (22:00–06:00) work. An empty value means none.
- **A home with nodes:** heavy jobs that a node takes are not held. Quiet hours protect the NAS,
  not the cluster. The hold applies only to jobs the NAS itself would run.
- **Time zone:** quiet hours use the home's time zone (`TZ`, or the NAS's `/etc/localtime` when
  mounted). The compose files pass `TZ`.

## As built

- A held job is a `paused` job with `paused_reason = 'quiet_hours:<HH:MM>'` and `not_before` at
  the window's start (the engine's `JobDeferred`, so claiming it again is no new attempt),
  not a new job state: the queue's waiting reason `quiet_hours` names it. Heavy is decided
  when a worker claims the job (`engine/quiet.py`), from the window in
  `MAKTABA_QUIET_HOURS`. Run now sets `run_now` in the job's payload and clears the wait
  (`POST /api/queues/jobs/{id}/run-now`, `POST /api/libraries/{id}/run-now`).
- When the data disk cannot be read, setup says so and Settings → This machine asks
  (`[server] data_disk`); until then the database is tuned for spinning disks.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/storage/disk_linux.go` | refusal names the type; rotational detection from mountinfo |
| `cmd/maktaba-server/internal/pgbundle/pgbundle.go` | tier and disk settings as `-c` server arguments; the household override |
| `cmd/maktaba-server/internal/config/config.go` | `[schedule] quiet_hours`, `[postgres] settings` |
| `pipeline/src/maktaba_pipeline/engine/` | `waiting_window` state; heavy-job marking; Run now |
| `api/internal/handlers/jobs/` | Run now endpoint (admin; its role test ships with it, 59.16) |
| `web/` | queue label, Run now, Settings line |
