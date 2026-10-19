# Files on the share

Every Maktaba machine saves what it collects — its logs, its metrics, every job it ran, its
LLM usage — as plain text files **on your NAS**, in a `maktaba` folder next to your videos.
The home adds the numbers of the whole cluster: the queues and the library. Every machine
can read every machine's files, so you can look at the whole cluster from any computer —
even when the home is off.

These files are the record. The home's database keeps only a short cache of the most recent
metrics and logs, so pages read fast (see [The database cache](#the-database-cache)).

## Where the folder is

Maktaba finds the top of your video share — the folder that holds all your libraries — and
uses `maktaba/cluster` inside it. For example, if your libraries are
`/Volumes/Originals/Channels` and `/Volumes/Originals/Courses`, the folder is
`/Volumes/Originals/maktaba/cluster`.

Each machine reaches it through its own path to the share (its *path map*, shown on
**Admin > Machines**):

| On | The same folder is |
|---|---|
| the Mac home | `/Volumes/Originals/maktaba/cluster` |
| a Linux machine with the share on `/mnt/nas` | `/mnt/nas/maktaba/cluster` |
| a Windows machine with the share on `Y:` | `Y:\maktaba\cluster` |
| a Windows machine using the network path | `\\nas\originals\maktaba\cluster` |

To see where this machine writes, run:

```sh
maktaba-pipeline cluster where
```

It prints the share's top as the home knows it, the folder on this machine, and whether it
is really on the share (and if not, why).

If your libraries have no folder in common (for example they are on different shares),
Maktaba uses the folder of the library with the most videos. To choose the place yourself, set `MAKTABA_SHARE_ROOT` on the home (a path as the home sees
it). `MAKTABA_CLUSTER_DIR` on a machine forces a local folder for that machine.

The scanner ignores the `maktaba` folder: it holds no videos.

## What is inside

One folder per machine, named by its IP address, and inside it one folder per kind of
record and one per day:

```
maktaba/cluster/
  192.168.50.20/                     the home
    processes/                       one small file per worker process: who it is
    logs/2026-10-19/                 what the workers logged that day
    metrics/2026-10-19/              CPU, memory, GPU, disks, network every 10 seconds
    jobs/2026-10-19/                 every job: when it was taken, its progress, how it ended
    llm/2026-10-19/                  LLM requests this machine ran (counts only)
    events/2026-10-19/hub.jsonl      the home only: machines connecting, leaving, refused, commands
    queues/2026-10-19/hub.jsonl      the home only: every queue's numbers every 10 seconds
    library/2026-10-19/hub.jsonl     the home only: the library's numbers every 5 minutes
  192.168.50.31/                     a machine
    processes/  logs/  metrics/  jobs/  llm/
```

- **Days are in UTC**, so all machines start a new day folder at the same moment
  (for example at 03:00 in UTC+3).
- **Each worker process writes its own files** (named after the process), and only those.
  Several processes on one machine never write to the same file — that keeps the files safe
  on SMB and NFS shares.
- The files are **JSON Lines**: one record per line. They open in any text editor.
- A machine whose IP address changes starts a new folder. Each `processes` file names its
  machine, so `cluster ls` still groups them.

| Kind | One line is | Useful for |
|---|---|---|
| `logs` | a log record: time, level, event, details | what went wrong, and where |
| `metrics` | a snapshot of the process and its machine, with its health numbers (NAS speed and timeouts) | load, memory, GPU, disk space, NAS health over time |
| `jobs` | a job event: taken, progress, done, failed, skipped, paused, deferred | how long each step takes on which machine; failures |
| `llm` | one LLM request this machine ran: model, who asked, tokens, time, error | LLM load and speed per machine |
| `events` | the home's hub: a connection, a disconnection, a refusal, a command | who was connected when; refused machines |
| `queues` | every queue: waiting, ready, running, paused, failed, done per minute, oldest wait | the queues over time |
| `library` | per library: videos, hours, size, transcribed, series, tagged, indexed | the library's growth |

What is **never** in these files: passwords, keys, the database login, LLM prompts or
answers.

## Reading the files

### With the pipeline command, on any machine

```sh
maktaba-pipeline cluster ls                          # machines, their processes, the days present
maktaba-pipeline cluster logs --level warning        # today's warnings and errors, all machines
maktaba-pipeline cluster logs --ip 192.168.50.31 --follow     # one machine's log, live
maktaba-pipeline cluster jobs --date 2026-10-18      # every job event of a day
maktaba-pipeline cluster metrics --ip 192.168.50.31  # one machine's metrics
maktaba-pipeline cluster queues                      # the queues through today
maktaba-pipeline cluster library                     # the library snapshots
maktaba-pipeline cluster llm                         # LLM requests run today
maktaba-pipeline cluster events                      # connections and commands on the home
```

Options:

| Option | Meaning |
|---|---|
| `--ip IP` | one machine's folder (default: all) |
| `--date YYYY-MM-DD` | a day (default: today, UTC) |
| `--level LEVEL` | logs at or above `debug`, `info`, `warning` or `error` |
| `--follow` | keep printing new lines as they arrive |
| `--dir PATH` | read another folder (for example a machine's local fallback folder, or a copy) |
| `--archive [DIR]` | read the home's long-term archive instead of the share (`logs`, `jobs`, `llm`, `events`, `ls`); `DIR` defaults to `MAKTABA_ARCHIVE_DIR`, else `~/.maktaba/archive`; not with `--follow` |

The day's files of all processes are merged in time order. These commands read the share
directly, so they work while the home is off.

### The archive, on the home

The home keeps the logs, job events, LLM usage and its own events for a year in its archive
(`MAKTABA_ARCHIVE_DIR`, default `~/.maktaba/archive`, on the home's own disk; see the design,
section 7.10), compressed, one file per process and day. On the home:

```sh
maktaba-pipeline cluster where                       # also shows the archive folder and if it is there
maktaba-pipeline cluster ls --archive                # archived machines and their days
maktaba-pipeline cluster logs --archive --ip 192.168.50.31 --date 2026-03-02 --level error
maktaba-pipeline cluster jobs --archive /mnt/backup/maktaba-archive --date 2026-03-02
```

The archive holds closed days only, so `--follow` does not apply. Like the share readers,
these never follow a link planted in the folder.

### With ordinary tools

The files are text, so any tool works:

```sh
tail -f /mnt/nas/maktaba/cluster/192.168.50.31/logs/2026-10-19/*.jsonl
jq 'select(.level == "error")' /mnt/nas/maktaba/cluster/*/logs/2026-10-19/*.jsonl
```

```powershell
Get-Content Y:\maktaba\cluster\192.168.50.31\logs\2026-10-19\*.jsonl -Wait
```

The last line of a file that is being written may be incomplete for a moment; skip it.

### From the web app

**Admin > Machines** shows each machine's live views, and its history from these files
(pick a kind and a day). Scripts can read the same through the home's API with an admin
token: `GET /api/cluster/files` lists the folders, and
`GET /api/cluster/files/<ip>/<kind>?date=YYYY-MM-DD` returns a day's records. For metrics and
logs of the last day the home answers from its database cache (below); the answer's
`X-Maktaba-Source` header says `cache` or `files`, and the records are the same either way.

## The database cache

The home keeps a copy of the most recent metrics and logs in its database, so its pages and
the API read them quickly instead of opening many files on the NAS.

- **What**: every metrics snapshot (one per process every 10 seconds) and every log record
  the home receives over the machines' live connections; and the health numbers (NAS speed,
  CPU, memory, transcription speed) that the Processing page and the transcription report
  show.
- **For how long**: the metrics and logs copies keep the last 24 hours
  (`MAKTABA_CLUSTER_CACHE_HOURS` on the home; `0` turns them off). The health numbers are kept
  as before.
- **Who writes it**: only the home. Machines never write to it.
- **Emptying it loses nothing**: everything in it is also in the files. The home also reads
  the files into it by itself — once at start and then every minute, picking up where it
  stopped — so it holds what the live connections missed (the home's own downtime, a busy
  moment), and refills completely after a new install or a database crash.
- **A home on SQLite** (a single machine) has no cache; its history comes from the files.
- **When the database is slow or stopped**, the home skips the cache for a while and counts
  what it skipped; the live views and the files are not affected, and history for that time
  is read from the files.

## The archive on the home

The share keeps two weeks; the home keeps the **long-term archive** on its own disk.

- **What**: every machine's `logs`, `jobs` and `llm` files and the home's `events`, each
  compressed (about ten to one). Metrics, queue and library snapshots are not archived.
- **When**: once a day has closed (UTC), within the hour; a record that arrives late for that
  day is added the next hour. After the home was off, it archives at start every day the
  share still holds.
- **Where**: `~/.maktaba/archive` of the user that runs the home's server (with Docker, a
  volume), never on the share; the same layout as the share, each file `.jsonl.gz`, plus a
  `.manifest.json` per day. Only the home writes it; folders are `0750`, files `0640`.
- **Reading**: Admin > Machines offers the archive's days next to the share's; once the share
  has deleted a day, the page reads it from the archive (the answer is the same). On the home,
  `maktaba-pipeline cluster logs --archive` reads it too, and so do `zcat` and `zgrep`.
- **When it cannot write** (disk full, folder not writable), it says so on Admin > Machines
  and tries again every hour; nothing else is affected.

| Setting (on the home) | Default | Meaning |
|---|---|---|
| `MAKTABA_ARCHIVE_DIR` | `~/.maktaba/archive` | where the archive is |
| `MAKTABA_ARCHIVE_KINDS` | `logs,jobs,llm,events` | what is archived |
| `MAKTABA_ARCHIVE_DAYS` | 365 | days kept; `0` keeps them all (up to the size cap) |
| `MAKTABA_ARCHIVE_MAX_GB` | 20 | the archive's size cap; past it, the oldest days go |

## How long they are kept, and how big they get

| Setting (on the home) | Default | Meaning |
|---|---|---|
| `MAKTABA_CLUSTER_RETENTION_DAYS` | 14 | day folders older than this are deleted |
| `MAKTABA_CLUSTER_FILE_MB` | 256 | the most one file may grow in a day; past it, records are counted, not written |
| `MAKTABA_CLUSTER_METRICS_FILE_SEC` (any machine) | 10 | how often a metrics snapshot is saved to the file; the live view still gets one every 2 seconds |

The home sends the first two to every machine; setting them on a machine overrides them for
that machine. Each machine deletes its expired day folders (whichever of its processes wrote
them) and, in its local fallback folder, the records of processes gone longer than that; the
home also cleans up after processes that are gone on the share.

You can delete any of these files or folders at any time; they are a record, Maktaba does
not need them to work. Do not edit files that are being written.

## When the share is not reachable

If a machine cannot reach the share (it is not mounted, or the `maktaba` folder cannot be
created), it writes the same files on its own disk, in `~/.maktaba/cluster` (Windows:
`%USERPROFILE%\.maktaba\cluster`), says so on **Admin > Machines**, and checks again every
5 minutes. When the share is back it writes there again. What it wrote locally stays on
that machine; read it with `maktaba-pipeline cluster logs --dir ~/.maktaba/cluster`.

If the share is full or read-only, the records that cannot be written are counted and the
machine keeps working.

## Who can read them

Anyone who can open the share can read these files — that is what lets every machine see
every other. The share's own permissions decide who that is. The logs mention video titles,
paths and errors; if that matters to you, limit who can open the share.
