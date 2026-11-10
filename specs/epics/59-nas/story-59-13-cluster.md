# Story 59.13 — A NAS in a cluster: home, full node or storage; one path for each share

> Epic 59 · Phase 4 · Depends on: 59.03, 59.04 · Needed by: 59.14

## Why

The cluster (Epics 31, 35, 37) has one home with Postgres and the queue, plus full nodes with a
replica that can take over. It treats a NAS as storage: every machine reads media through its own
mount, matched by canonical share paths (`smb://server/share`, story 31.03).

When the NAS becomes the home, its own files are local, at `/volume1/Movies` on DSM or
`/mnt/tank/media` on TrueNAS. A Mac node sees the same files at `/Volumes/Movies` through SMB.
Two mount styles give different results:
- **One catch-all mount** (`${MAKTABA_MEDIA}:/media`), as in the current Docker compose: the
  container sees `/media/Movies`, which matches nothing, and every node needs a path map.
- **Each share at its own host path:** the existing resolver already maps `/volume<N>/<share>` to
  `smb://<nas>/<share>`.

A weak NAS should also not take node duties it cannot carry.

## Description

**Mount at the host path.** Every NAS compose file and template mounts each share at the same
path inside the container (59.09–59.11). The NAS's own paths then go through the existing
canonical resolver:

| Platform | Path in the container | Canonical |
|---|---|---|
| DSM | `/volume1/Movies` | `smb://<nas>/Movies` (resolver rule exists) |
| TrueNAS | `/mnt/tank/media` | the SMB share whose path is `/mnt/tank/media`, as the household names it at setup (TrueNAS share names are not derivable from the path) |
| Unraid | `/mnt/user/Movies` | `smb://<nas>/Movies` (new rule: `/mnt/user/<share>`) |
| Other | any | asked at setup |

- **Setup's share step** (on a NAS home): for each media root it shows the canonical share it
  worked out and asks the household to confirm it or type the share name. When the root has no
  SMB or NFS share, it records "local only" and says nodes cannot work on that library.
- **Mounts at other paths** (a `/media` catch-all): setup asks each root's share name, so the
  canonical path is still right. Settings shows "These libraries use a mount at a different path
  from the NAS's own; nodes will match them by the share name you gave."
- `<nas>` is the NAS's mDNS name or IP, as the nodes mount it. Setup offers both and keeps them
  as aliases. The resolver already accepts several server names for one share.

**Roles by tier:**
- **NAS as home:** the default, on any tier.
- **NAS as a full node of a home elsewhere:** allowed on Standard and Strong. On Light, joining
  shows "A Light NAS should be the Maktaba home or just storage. As a node it would slow the NAS
  down without helping the home much." The household can still continue with an explicit choice;
  nothing hard-blocks.
- **NAS as storage only:** unchanged.

**Taking work off a NAS home.**
- When a node with a GPU joins a NAS home, transcription and transcode jobs prefer the node
  (existing scheduling by capability).
- Quiet hours (59.02) do not hold jobs the node takes.
- Stages that run only on the home (the vector index, thumbnails, review pages; Epic 31 R4.2)
  stay on the NAS and follow its tier.

**Switchover.**
- A full node taking over from a NAS home (Epic 37) works as today.
- The NAS's libraries stay reachable through their canonical paths, so the new home reads them
  over SMB, the same way the node already did.

## Acceptance criteria

- **Given** a DSM home with Movies mounted at `/volume1/Movies`, **when** a Mac joins as a full
  node with `/Volumes/Movies` mounted, **then** the Mac transcribes items from Movies with no path
  map.
- **Given** a TrueNAS home with `/mnt/tank/media` and the share named at setup, **then** a node
  with `smb://truenas/media` matches it.
- **Given** a `/media` catch-all with share names given at setup, **then** nodes match too, and
  Settings shows the note.
- **Given** a Light NAS joining as a node, **then** the warning shows, and joining needs the
  explicit choice.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | pipeline unit | `/volume1/Movies` on a DSM home | canonical | `smb://<nas>/Movies` |
| T02 | pipeline unit | `/mnt/user/Movies` on Unraid | canonical | `smb://<nas>/Movies` |
| T03 | pipeline unit | `/mnt/tank/media` + setup name `media` | canonical | `smb://<nas>/media` |
| T04 | pipeline unit | aliases `diskstation.local`, `192.168.1.20` | match a node's mount | matched |
| T05 | Go e2e (cluster rehearsal) | NAS-shaped home container + node container sharing a volume at different paths | job on node | runs, reads the file |
| T06 | web unit | share step | render | worked-out share, confirm or edit, local-only text |
| T07 | Go unit | Light tier, join as node | join | warning, explicit choice needed |

## Strings

| Key | English | Arabic |
|---|---|---|
| `setup.share.confirm` | Maktaba found this library as the share {share} on {server}. Is that how your other computers open it? | وجد Maktaba هذه المكتبة على أنها المشاركة {share} على {server}. هل هكذا تفتحها حواسيبك الأخرى؟ |
| `setup.share.local_only` | This folder isn't shared on the network, so other Maktaba computers can't work on it. | هذا المجلد غير مشارك على الشبكة، لذا لا تستطيع حواسيب Maktaba الأخرى العمل عليه. |
| `settings.share.other_path` | These libraries use a mount at a different path from the NAS's own; nodes will match them by the share name you gave. | تستخدم هذه المكتبات نقطة ربط بمسار يختلف عن مسار الخادم نفسه، وستطابقها العُقد باسم المشاركة الذي أدخلته. |
| `cluster.join.light_nas` | A Light NAS should be the Maktaba home or just storage. As a node it would slow the NAS down without helping the home much. | يُستحسن أن يكون الخادم من الفئة الخفيفة جهاز Maktaba الرئيسي أو مجرد مساحة تخزين. أما بصفته عقدة فسيبطئ الخادم دون أن يفيد الجهاز الرئيسي كثيرًا. |

## Edge cases

- **The NAS's SMB share has a different name from its folder** (DSM allows it rarely): the
  household edits it in the share step.
- **NFS-only households:** canonical `nfs://<nas>/<export>`, with the same resolver rules.
- **The NAS's IP changes (DHCP):** aliases include the mDNS name, so matching keeps working.

## Build plan

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/sharepath/` | Unraid rule; TrueNAS from setup names; aliases |
| `cmd/maktaba-server/setup_steps.go` | share step on a NAS home |
| `cmd/maktaba-server/internal/cluster/` | Light-tier join warning |
| compose files (59.09–59.11) | per-share mounts at host paths |
| `cloud/installers/docker/docker-compose.yml` | drop the `/media` catch-all in favour of per-share lines |
