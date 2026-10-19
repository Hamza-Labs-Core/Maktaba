# Story 31.3 — Canonical share paths

## Description

*As the owner of a Maktaba cluster (a Mac home, a Windows node, Linux machines), I want each
media path stored once, in a form every machine works out for itself from the share's network
address, so that a file stored by one machine opens on any other without anyone typing a
path map ("for it to be cross platform, shouldn't all use ip with full path").*

**Today.** `videos.path`, the library roots and the other path columns hold the home's local
paths. The Mac home stores `/Volumes/Originals/Backup/Youtube/...` for 148,593 videos. Each
node guesses a prefix map (`{"/Volumes/Originals/": "O:/"}`) by looking for sample files under
its mounts (`cluster/storage.py`, design §6). That has three problems:

- The map depends on which machine scanned the library.
- A guess can be wrong or missing. A new node needs files to find before it works.
- Names the Mac stored NFD (decomposed) do not open on Windows, which only opens the NFC
  name the share holds (the live node failed thumbnails on
  `اخسأ، فلن تعدو قدرك 🔥🔥🔥.mp4`; fixed at open time first, in PR #79).

The NAS is a Synology (`192.168.50.94`). Windows reaches it as `\\Home-NAS\Originals` (`O:`)
and `\\Home-NAS\Originals\Backup\Youtube` (`Y:`). The Mac reaches it as
`//user@192.168.50.94/Originals` on `/Volumes/Originals`. Plex runs on the NAS and sees
`/volume1/Originals`.

Design: [design.md](design.md) §6 (shared storage). Plan: [plan-31-03](plan-31-03-canonical-share-paths.md).

## The canonical form

```
smb://<server>/<share>/<path>      a file on a network share (whatever the mount: SMB or NFS)
nfs://<server>/<export>/<path>     an NFS export that has no share name
file:///<absolute path>            a file on one machine's own disk
```

Example: `smb://home-nas/Originals/Backup/Youtube/AntiShubohat/a.mp4`.

- **Not URL-encoded.** Spaces, `%`, `#` and Arabic letters are kept as they are, so a stored
  path stays readable and `LIKE 'smb://home-nas/Originals/%'` still works.
- **The server is lower case. The share keeps the server's spelling.** Both are matched
  case-insensitively, the way SMB treats them. The rest of the path keeps its case (SMB
  ignores case when opening, but NFS and Plex on the NAS do not).
- **Text is Unicode NFC.** Windows, Linux and the NAS store names composed; macOS hands
  them out decomposed over SMB. Every value written goes through `normalise_rel`, and the
  data migration makes every value NFC, including those it leaves in local form.
- **`/` separators, no `.`, `..` or empty segments.** A share root has no trailing slash
  (`smb://home-nas/Originals`).
- **The NFS mount of a share uses the share's name.** For example `/volume1/Originals`
  becomes `smb://home-nas/Originals`, through the server's `exports` table or the Synology
  `/volumeN/<share>` rule. One file therefore has one canonical path whichever way a machine
  mounts it. `nfs://` is only for an export no share name covers.

### Server identity, not the literal IP (recommended)

The user asked for "ip with full path". Two options:

| | Stored form | IP change | Machines that use a host name (Windows: `Home-NAS`) | Several addresses (LAN IP, `.local`, a second NIC) |
|---|---|---|---|---|
| A. literal IP | `smb://192.168.50.94/Originals/...` | every stored path is rewritten (148k rows, and Plex maps) | must translate the name back to that IP | one only |
| **B. server record** | `smb://home-nas/Originals/...` | **one edit** of `storage_servers.addresses` | matched by name or by the IPs it resolves to | all of them |

**Decision: B.** A `storage_servers` row holds the server's **name**, which is what paths
store. It also holds its **addresses**: the IP the user asked for, plus host names and the
mDNS name (`192.168.50.94`, `Home-NAS`, `home-nas.local`). A mount matches a server when its
host is one of those addresses, case-insensitively. Failing that, it matches when the host
resolves to one of the IPs the addresses resolve to. The IP is still the anchor, as the
user asked: `storage server-add --from-mounts` creates the record from the mount's IP. But
the stored form does not change when the IP does. With a DHCP lease or a new router, the
admin edits one row (Machines page, "Storage servers") and every machine follows within 5
minutes.

### Characters a client cannot show (catia / SFM)

Windows cannot hold `" * : < > ? \ |` in a name. An SMB server or client maps them:

- Samba `vfs_catia` (Synology's default) maps them to their fullwidth twins
  (`"` ↔ `＂` U+FF02).
- macOS, Linux `cifs mapposix` and `vfs_fruit` map them to the private-use range
  (`"` ↔ U+F020).

**The canonical path holds the on-disk name**, the one Plex on the NAS sees.
`storage_servers.char_map` says what an SMB client sees instead:

- `none` (the default, today's behaviour);
- `fullwidth`;
- `sfm`;
- a catia list such as `0x22:0xff02,0x3a:0xff1a` (on-disk character : client character).

The resolver maps both ways: on-disk to client when it opens over SMB, client to on-disk
when it stores a path a client listed. NFS mounts see the on-disk names, so no mapping
applies. When nothing is declared and a name with such a character does not open, the
resolver also tries the fullwidth and SFM spellings.

The default stays `none` because of yt-dlp. On Windows, yt-dlp writes fullwidth characters
*into the real file name* (`？`, `｜`). With a declared `fullwidth` map those genuine `＂` would
read back as `"`. An admin should set it only for a server that really has catia on and
files written through it.

## Where each machine finds the share

| OS | Source | What it gives |
|---|---|---|
| Windows | `WNetGetConnectionW` per drive letter (exact names, not localised), then `net use` (UNC connections without a letter) | `O: → \\Home-NAS\Originals`, `Y: → \\Home-NAS\Originals\Backup\Youtube` |
| Windows | UNC fallback, one per server: `smb://home-nas/ → //Home-NAS/` | any share with no drive letter opens as `\\Home-NAS\Share\...` |
| macOS | `mount` (`//user@192.168.50.94/Originals on /Volumes/Originals (smbfs, ...)`; share names URL-decoded; Bonjour hosts `Home-NAS._smb._tcp.local` → `home-nas.local`) | `/Volumes/Originals` |
| Linux | `/proc/mounts` (`cifs`, `smb3`, `nfs`, `nfs4`; `\040` unescaped) | `/mnt/originals`, `/srv/nfs/originals` |

A share mounted below its root (`Y:` = `Originals/Backup/Youtube`) is an entry for that sub
folder. The longest entry wins, and both drives give back the same canonical path.

**Precedence**, highest first:

1. `--path-map` / `MAKTABA_WORKER_PATH_MAP` given to the process;
2. the admin's "share → local mount" rows on the Machines page (per node:
   `cluster_nodes.path_map`; the home: `instance_identity.path_map`);
3. the mounts found;
4. the Windows UNC fallback;
5. legacy prefixes (the inferred map, for paths stored before the migration).

The resolver is rebuilt at start and with every storage check (5 min). A remounted share or
a new server address is picked up without a restart.

**Opening a file.** After the rewrite, a non-ASCII name that does not exist as written is
tried in NFC, then in NFD. After that, each path component is compared with the directory
listings in NFC, with listings cached for 5 minutes per directory. This is PR #79, which
works for legacy paths too. Then the other side of the character map is tried.

## Acceptance criteria

- **AC1** `storage_servers` (id, name, addresses, char_map, exports). Paths store
  `smb://<server-name>/<share>/...`. An address change is one edit and rewrites no path.
  Workers have SELECT only.
- **AC2** Parse and format round-trip. A stored value is NFC. Server and share compare
  case-insensitively. The char maps work both ways (fullwidth, SFM, catia lists).
- **AC3** Each machine resolves canonical paths to local ones by itself, from its own
  mounts: Windows (drive letters, `net use`, UNC fallback), macOS (`mount`), Linux
  (`/proc/mounts`). Matching is by the server's address set and the share name. Parsers
  are tested on recorded outputs (the Windows one is this repo's node).
- **AC4** An admin override per machine ("share → local mount") on the Machines page, for
  the nodes and for the home.
- **AC5** `maktaba-pipeline storage canonicalize` converts the stored paths using the home's
  mount table:
  - The default is a dry run reporting per column: rows, to convert, already canonical, on
    no share (with samples), collisions, and a sampled check that converted paths resolve
    to existing files.
  - `--apply` writes each change with its old value under a batch id.
  - It is idempotent.
  - `storage revert` restores a batch where the column still holds the new value.
  - Columns: `videos.path`, `libraries.roots`, `library_roots.path` / `path_canonical`,
    `subtitle_files.path`, `video_repairs.backup_path`, `purge_log.path`.
  - The home's caches (`poster_path`, `sprite_path`, `audio_cache.path`, channel art,
    review pages) are not on a share and are left alone.
- **AC6** Both forms keep working during the transition. Canonical paths resolve through
  the shares; legacy paths resolve through the legacy prefixes.
- **AC7** Everything that opens a file uses the resolver:
  - pipeline stages (`ctx.local_path`), the probe / extract / thumbnail handlers, the
    scanner (walks the root's local mount, stores canonical), the subtitle manager and the
    gRPC subtitle / transcribe calls;
  - the API: the playable check, and library roots, which are stored canonical when on a
    share and checked through the mount;
  - streaming: VOD direct play, sidecar subtitles, transcode input, channel blocks, logo
    and look assets;
  - Plex: its `path_map` is keyed by canonical share, e.g.
    `smb://home-nas/Originals/=/volume1/Originals/`, and is a pure rewrite;
  - the mediaintel importer: `--path-map` stays, output is canonical.
- **AC8** The storage check reports per share whether it is reachable and writable on each
  machine, through which mount, or "not mounted here". This shows on the Machines page for
  every node and for the home.
- **AC9** The home does all of AC5 by itself, with no SSH and no command: see
  [What happens automatically](#what-happens-automatically). Rules
  `storage.auto_canonicalize` (on by default) switches it off.

## What happens automatically

Nobody runs `storage canonicalize` on the Mac any more. The home's pipeline does it, as
the home-only queue stage **`storage_canonicalize`** ("Canonical share paths", a visible
run on the Processing page). Code: `pipeline/src/maktaba_pipeline/sharepath/auto.py`.

**When it runs.** The home's agent enqueues the stage:

- once at start;
- with every storage check (5 min), when the paths still in local form, the mounts or the
  storage servers changed since the last look;
- every `storage.check_minutes` (60) while the last look was *blocked* or *failed*;
- within 30 s of an admin's request on the Machines page;
- at once when a run was cut short (the worker stopped mid-way).

**What one run does.**

1. **Registers the storage servers** the home mounts (macOS `mount`, Linux
   `/proc/mounts`, Windows `WNetGetConnectionW` + `net use`) that no record names
   (`sharepath/autoreg.py`):
   - mounts whose hosts resolve to a common IP are one server;
   - the name comes from a host name the mount uses, else the reverse DNS name of its IP,
     else its NetBIOS name (`smbutil status` / `nmblookup -A` / `nbtstat -A`), else `nas`.
     It is lower case, the first label, and unique (`Home-NAS.lan` -> `home-nas`);
   - the addresses are every host the mounts use, every name found and every IP;
   - an existing record is never changed, so the admin's edits stand. A host whose record
     the admin removed is not registered again.
2. **Checks** that it can convert. It waits (*blocked*, with the reason on the Machines
   page) when any of these fails:
   - every network mount that stored paths are on resolves to a server;
   - every library root still in local form is on a mounted share, or exists here (a root
     that is gone is an unmounted share);
   - the dry run's sampled paths (`storage.verify_samples`, 20) open here, with at most
     `storage.allowed_missing` (0) missing.

   Collisions are counted and left as they are (PR #92).
3. **Plex path map.** Every local prefix of the Plex extension's `path_map` that is on a
   share gets its canonical entry beside it:
   `/Volumes/Originals/=/volume1/Originals/` -> also
   `smb://home-nas/Originals/=/volume1/Originals/`.
4. **Backs up** every value of every path column (AC5's list) into
   `storage_path_backups`, inside the database. No shell and no `pg_dump` are needed. The
   last `storage.backups_kept` (3) are kept.
5. **Pauses only the stages that read or write the shares**: the stages of the pools in
   `storage.pause_pools` (`nas_read`, `nas_write`, `repairs`), with
   `job_stages.paused_by = 'storage_canonicalize'`. It waits up to
   `storage.drain_wait_minutes` (10) for their running jobs. The rest of the pipeline, the
   API and streaming keep running. A stage the admin had paused stays paused.
6. **Applies** in chunks of `storage.batch_size` (2000), one transaction each, under one
   batch id, reporting progress on the Machines page.
7. **Verifies**: a second dry run finds nothing left (a late writer's paths are taken
   into the same batch), and sampled converted paths open.
8. **Resumes** the paused stages and reports *done* (values, time, batch).

It is idempotent: a run with nothing to convert changes nothing. A run cut short keeps
its phase, batch and backup in `storage_canonicalize.state`. The next run carries on with
the same batch, so one revert still undoes all of it. A failure resumes the paused stages,
reports the error and is retried later.

**On the Machines page** (home card, "Canonical paths", en + ar):

- the phase and why it waits;
- the run's progress and the last conversion;
- the servers and the Plex entries it added;
- the buttons **Check now**, **Revert last conversion** and **Convert automatically
  again**.

A revert pauses the share stages, runs `storage revert` on the last batch and resumes
them. Automatic conversion then **holds** until the admin resumes it.

The API side: `GET` / `POST /api/cluster/storage/canonicalize` (`{action: run | revert |
resume}`), and `home.canonicalize` in `GET /api/cluster`.

**On the Mac's next update:**

1. Migration 0131 adds the state and backup tables.
2. At start the pipeline registers `home-nas` from `//…@192.168.50.94/Originals`. Then it
   waits for the check, backs up, pauses the share stages, converts the paths and resumes.
3. The Windows node then resolves `smb://home-nas/Originals/...` through `O:` / `Y:`.
   Add the node's host name to the server on the Machines page if the home's lookups did
   not find it.

## Manual fallback

Run these on the home, in the environment its pipeline runs with (`MAKTABA_DATABASE_URL`
set as for `maktaba-pipeline run`). Nothing here changes a media file.

- `maktaba-pipeline storage status` shows the automatic conversion's state.
  `storage auto` runs one automatic run now, in the foreground.
- `maktaba-pipeline storage mounts` shows the mounts and the shares they resolve to.
  `storage servers` lists the records, and `storage server-add home-nas --address
  192.168.50.94 --address Home-NAS` adds or extends one (or edit it on the Machines page).
- `maktaba-pipeline storage canonicalize` is the dry run; `--apply` converts. This is the
  original command. Stop the writers first (Machines page → Drain, or stop the pipeline).
- `maktaba-pipeline storage batches` lists the conversions. `storage revert [--batch
  <id>]` undoes one value by value, wherever the column still holds the new value.
- `maktaba-pipeline storage backups` lists the backups kept in the database.
  `storage restore-backup [--backup <id>]` writes one back (the last resort). Its batch
  then counts as reverted.
- Plex: the canonical entries are added by themselves. Keep the old
  `/Volumes/Originals/=/volume1/Originals/` entry alongside them until the next release.
- Streaming and the API read the home's share map from its storage report within
  5 minutes. Restart them to pick it up at once.

## Out of scope

- Moving or renaming media files.
- Writing `file:///` for local-disk paths by default (`--include-local` does it on request).
- Mounting a share that is not mounted. A machine reports "not mounted here"; a Windows node
  opens it by UNC.
- In the Go services (API, streaming), the character mapping and the per-directory Unicode
  lookup. They apply the share map the pipeline reports, which covers the stored, already
  NFC, paths.
