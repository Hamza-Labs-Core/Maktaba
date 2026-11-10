# Story 59.03 — Media permissions: check, explain, and work with read-only libraries

> Epic 59 · Phase 1 · Depends on: 59.01, 59.04 (platform detection) · Needed by: 59.07, 59.09, 59.10, 59.11, 59.13

## Why

On a NAS, the container's user sees each share through the NAS's ACLs. Many households will
give Maktaba read-only access to their originals, and some shares will not be readable at all.
Today Maktaba assumes it can write into every library:
- subtitles are written beside the video;
- convert, dedup, YouTube downloads and repair write into libraries;
- the trash renames files into `<root>/.maktaba/trash`.

A missing permission today shows up as jobs failing one at a time with `EACCES`, long after
setup. Nothing tells the household which folder, which user or what to change.

## Description

**The check.**
- For each library root, `maktaba-server` runs `mediaaccess.Check(root)`, which returns one of
  three results. The probe is a temporary directory created under `<root>/.maktaba/` and removed
  at once; `.maktaba` is created if it is missing, and left in place only if it already existed.

| Result | Meaning |
|---|---|
| `rw` | read and write |
| `ro` | can list and read, cannot write |
| `none` | cannot list the root |

- It also samples up to 50 files across the tree (breadth-first, stopping at 2 seconds) and
  reports `partial` when some are unreadable, with up to 5 example paths.
- It runs at setup, when a library is added or edited, from the library page's **Check access**
  button, and after a write fails with a permission error. It does **not** run at every start:
  a probe write would wake a sleeping disk. The result is stored per library
  (`libraries.access`, `access_checked_at`).
- **Read-only should be a mount, not just an ACL.** Inside a container without Landlock (59.07),
  only a `:ro` mount guarantees nothing writes. The guides mount read-only libraries `:ro`, and
  when the check finds `ro` the library page says whether the mount itself is read-only (from
  `/proc/self/mountinfo`).

**Explaining it.** The library page and setup show the state and who Maktaba runs as ("user 568,
group 568"). When the platform is detected (59.04: DSM, TrueNAS, Unraid, other), they show the
fix for it:
- **DSM:** "In Control Panel → Shared Folder → {share} → Edit → Permissions, give the user
  {name} Read/Write (or Read only if you prefer)."
- **TrueNAS:** "In Datasets → {dataset} → Permissions, add an ACL entry for user {uid} with
  Modify (or Read)."
- **Unraid:** "In Shares → {share}, set the share's export and check that user nobody (99) can
  read it."
- **Other:** "Give user {uid} read (and write, if you want Maktaba to save subtitles and
  conversions there) on {path}."

The share or dataset name is worked out from the path where possible (`/volume1/<share>`,
`/mnt/<pool>/<dataset>`). Otherwise the path is shown.

**Read-only libraries are supported.**
- The plugin host resolves `files.write` grants per library. A stage whose manifest writes
  `library.*` is not scheduled on a read-only library, except for subtitles. Each feature's
  behaviour is in the README (§6.2):
  - **Subtitles:** on a read-only library, `library.subtitles` writes go to a new database table,
    `subtitle_files` (video id, language, format, text). Subtitle files are small, and keeping them
    in the database means node replicas and a node taking over (Epic 37) have them too, which a
    folder on the home would not. Streaming's subtitle lookup
    (`streaming/internal/handlers/subtitle.go`, `sidecar.go`) checks the table after the
    side-car path, so players see the same tracks. Existing side-car subtitles beside the video
    are still read.
  - **Convert, dedup, YouTube into this library, repair:** the action is disabled on the library
    with "This library is read-only for Maktaba" and the fix. Admin API calls return 409
    `library_read_only`. The convert list (the Queue button) leaves these files out and says how
    many it left out.
  - **Delete:** unavailable. The API returns 409 `library_read_only`, and the web hides the
    button with a tooltip giving the reason.
- A library whose access changes (`ro` → `rw` after the household's fix) is picked up by
  **Check access**. Nothing needs re-adding.
- **The cluster's folder and the model copy** (`<share>/maktaba/`, `stt/model_share.py`) need a
  writable share. On a NAS home whose libraries are all read-only, setup asks for one writable
  folder for them (59.13). Without one, nodes download models themselves, and pairing a node
  says why.

**Folders the walker skips.** The walker already skips `@eaDir` and every dot-folder
(`scanner/walker.py`), which covers `.zfs`, `.snapshot`, `.Trash-*` and `.AppleDouble`. This
story adds `#recycle`, `#snapshot` (DSM) and `lost+found`, in the same list, which the convert
list shares.

## Acceptance criteria

- **Given** a library mounted `:ro`, **then** it shows Read only with the fix for the platform. A
  new video in it gets subtitles stored in the data dir, and the web player shows them.
- **Given** a read-only library, **when** an admin calls convert or delete on one of its items,
  **then** 409 `library_read_only`, and no file is touched.
- **Given** a root the user cannot list, **then** the library shows No access, and no scan runs
  for it.
- **Given** a share with `#recycle`, `#snapshot` and `lost+found`, **then** none appears in the
  library.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | temp dirs with modes 755/555/000 as a non-root user | Check | rw / ro / none |
| T02 | Go unit | 3 of 50 sampled files mode 000 | Check | partial + 3 examples |
| T03 | Go unit | `.maktaba` absent, ro | Check | nothing created |
| T04 | pipeline unit | ro library, subtitles stage | run | a `subtitle_files` row; nothing written in the library |
| T05 | Go handler (streaming) | subtitle lookup with both a side-car file and a row | GET | side-car first, then the row |
| T06 | Go handler (59.16) | ro library | convert, delete, YouTube target | 409 `library_read_only` |
| T07 | pipeline unit | walker over a tree with `#recycle`, `#snapshot`, `lost+found` | scan | none indexed |
| T10 | Go unit | library check at server start | start | no probe write |
| T08 | CI container (59.15) | media mounted `:ro` | full first run | ready; subtitles in the table; no EACCES in logs |
| T09 | web unit | ro library page | render | Read only, fix text, buttons disabled with reason |

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.access.rw` | Read and write | قراءة وكتابة |
| `library.access.ro` | Read only | قراءة فقط |
| `library.access.none` | No access | لا وصول |
| `library.access.partial` | Some files can't be read | بعض الملفات لا يمكن قراءتها |
| `library.access.runs_as` | Maktaba runs as user {uid}, group {gid} | يعمل Maktaba بصفته المستخدم {uid} والمجموعة {gid} |
| `library.access.check` | Check access | افحص الصلاحيات |
| `library.read_only` | This library is read-only for Maktaba | هذه المكتبة للقراءة فقط بالنسبة إلى Maktaba |
| `library.fix.dsm` | In Control Panel → Shared Folder → {share} → Edit → Permissions, give the user {name} Read/Write (or Read only if you prefer). | من لوحة التحكم ← المجلد المشترك ← {share} ← تعديل ← الأذونات، امنح المستخدم {name} صلاحية القراءة والكتابة (أو القراءة فقط إن شئت). |
| `library.fix.truenas` | In Datasets → {dataset} → Permissions, add an ACL entry for user {uid} with Modify (or Read). | من مجموعات البيانات ← {dataset} ← الأذونات، أضف إدخال ACL للمستخدم {uid} بصلاحية التعديل (أو القراءة). |
| `library.fix.unraid` | In Shares → {share}, check that user nobody (99) can read it. | من المشاركات ← {share}، تأكد من أن المستخدم nobody ‏(99) يستطيع قراءتها. |
| `library.fix.other` | Give user {uid} read (and write, if you want Maktaba to save subtitles and conversions there) on {path}. | امنح المستخدم {uid} صلاحية القراءة (والكتابة إن أردت أن يحفظ Maktaba الترجمات والتحويلات هناك) على {path}. |
| `convert.skipped_read_only` | Left out because their libraries are read-only: {n} | استُبعدت لأن مكتباتها للقراءة فقط: {n} |
| `library.access.mount_ro` | The folder is mounted read-only in the container. | المجلد مربوط للقراءة فقط داخل الحاوية. |
| `job.permission_denied` | Maktaba couldn't write {path}. See this library's access. | تعذّر على Maktaba الكتابة في {path}. راجع صلاحيات هذه المكتبة. |

## Edge cases

- **Root squash on an NFS mount inside the NAS:** the check sees what the container sees.
  Nothing is special-cased.
- **Write allowed on the root, denied in subfolders:** a write that fails later is reported
  against that library as `partial` with the path. The job's failure shows `job.permission_denied`
  and links to the library's access panel.
- **ACL changes while running:** the next check picks them up. No restart is needed.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/mediaaccess/` (new) | Check, sampling, platform fix text |
| `api/internal/handlers/libraries/` | access fields, Check access endpoint, 409s |
| `streaming/internal/handlers/subtitle.go`, `sidecar.go` | `subtitle_files` fallback lookup |
| `pipeline/src/maktaba_plugin_runner/` | `library.*` grant resolution per library access |
| `pipeline/src/maktaba_pipeline/scanner/walker.py` | the skip list |
| migration | `libraries.access`, `access_checked_at`; `subtitle_files` |
| `pipeline/src/maktaba_pipeline/stt/model_share.py`, cluster folder | the writable-folder choice on a NAS home |
| `web/src/i18n/{en,ar}.json`, `web/` | strings; access panel, disabled actions with reasons |
