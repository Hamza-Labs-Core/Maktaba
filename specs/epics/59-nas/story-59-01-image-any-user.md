# Story 59.01 — One image for every NAS: any user, never root, first start offline

> Epic 59 · Phase 1 · Depends on: — · Needed by: 59.02, 59.03, 59.05, 59.06, 59.07, 59.09, 59.10, 59.11, 59.12

## Why

`ghcr.io/hamza-labs-core/maktaba-server` runs as uid/gid 1000, fixed in the Dockerfile
(`useradd --uid 1000`).
- **TrueNAS** starts apps as 568 and expects ACLs for that user.
- **Unraid** uses 99:100.
- **Synology** households run containers as a DSM user (uid 1026 and up, group 100) that they
  grant shares to.

When the image is started as another uid:
- `HOME` points at a directory that uid may not own;
- the volume it cannot write fails deep inside setup, with no clear message.

First start also builds the pipeline venv from PyPI (`venv_bootstrap.py`). On a NAS without
outbound internet that fails, and on a slow CPU it takes many minutes.

## Description

**Runs as any uid:gid, as given.**
- The Dockerfile keeps a default user (1000) for plain `docker run`. Nothing in the image depends
  on it:
  - `HOME` and `XDG_*` stay under `/var/lib/maktaba`;
  - `/etc/passwd` is not needed. The server reads its uid from `os.Getuid()` and its name from
    `$USER`, or uses `maktaba` when neither exists;
  - every path under `/opt/maktaba` is world-readable and executable, and nothing there is
    written.
- `maktaba-server container` starts with a **write check** of the volume, before anything else:
  it creates and removes `/var/lib/maktaba/.write-test`. On failure it prints one message and
  exits 78, so the NAS shows the container as stopped with a reason, not a restart loop:

  > Maktaba cannot write to its data folder /var/lib/maktaba as user 568 (group 568). Give this
  > user Read/Write on the folder you mounted there, then start Maktaba again.

  The same text is in Arabic on the next line. The NAS-specific hint comes from 59.09/59.10 when
  the platform is detected.
- The existing **root refusal** stays: uid 0 exits 78 with "Maktaba does not run as root. Set
  the container's user to a normal user (for example 1000:1000)."
- The image has **no PUID/PGID logic** and no root entrypoint.

**First start offline.**
- The release builds the pipeline venv (or a wheelhouse plus a hash-locked constraints file) for
  each architecture inside the image, at `/opt/maktaba/server/venv-seed/`.
- `venv_bootstrap` copies or installs from it with `--no-index`. PyPI is used only when the
  seed is missing, which is never the case in a release image.
- The `runtime.ml` pack (speech wheels, ~1–3 GB) stays a separate download, fetched when speech
  to text is turned on (59.06). The image stays small enough for a NAS: one gigabyte or less,
  compressed, without ML.

**Security floor**, checked at start and refused with a message:
- privileged mode, detected through the capability set (`CapEff` full) and writable `/sys`;
- the Docker socket mounted at a known path.

Added capabilities beyond Docker's default set are reported in Settings → This machine, not
refused.

## Acceptance criteria

- **Given** the image started as 1000, 568, 99:100 and 1026:100, each with a volume owned by that
  uid, **when** setup runs, **then** it completes and every process runs as that uid.
- **Given** a volume the uid cannot write, **then** the container exits 78 within 5 seconds with
  the message in both languages.
- **Given** no network beyond the registry pull (`--network none` after the image is present),
  **when** the container's first start runs with speech to text off, **then** setup completes
  and the plugin runners start.
- **Given** `--privileged` or `/var/run/docker.sock` mounted, **then** the container refuses to
  start with its message.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI container matrix (59.15) | uid ∈ {1000, 568, 99:100, 1026:100} × {amd64, arm64} | first start + setup | ready; `ps` shows that uid only |
| T02 | CI | volume owned by root, mode 755 | start as 568 | exit 78, both languages, path and uid named |
| T03 | CI | `--network none` | first start | venv from the seed; no PyPI request in the log |
| T04 | CI | `--privileged` | start | refused, message |
| T05 | CI | `-v /var/run/docker.sock:/var/run/docker.sock` | start | refused, message |
| T06 | Go unit | no passwd entry for the uid | user name lookup | `maktaba`, no panic |
| T07 | Release check | built image | `docker image inspect` size, compressed | ≤ 1 GB per architecture without ML |

## Strings

| Key | English | Arabic |
|---|---|---|
| `container.write_denied` | Maktaba cannot write to its data folder {path} as user {uid} (group {gid}). Give this user Read/Write on the folder you mounted there, then start Maktaba again. | لا يستطيع Maktaba الكتابة في مجلد بياناته {path} بصفته المستخدم {uid} (المجموعة {gid}). امنح هذا المستخدم صلاحية القراءة والكتابة على المجلد الذي ربطته هناك، ثم شغّل Maktaba من جديد. |
| `container.root_refused` | Maktaba does not run as root. Set the container's user to a normal user (for example 1000:1000). | لا يعمل Maktaba بصلاحيات الجذر. اضبط مستخدم الحاوية على مستخدم عادي (مثل 1000:1000). |
| `container.privileged_refused` | Maktaba does not run in a privileged container or with the Docker socket. Remove that setting and start it again. | لا يعمل Maktaba في حاوية ذات امتيازات كاملة ولا مع مقبس Docker. أزل هذا الإعداد وشغّله من جديد. |

## Edge cases

- **The volume is a fresh named volume, owned by root:** Docker creates it with the image's
  directory ownership (1000). Started as another uid, the write check fails with the message. The
  guides (59.09–59.11) use host paths the household owns.
- **An existing 1000-owned volume, now started as 568:** the write check fails. The message says
  to give 568 access or keep running as 1000. Nothing is chowned.
- **Group-writable volume (TrueNAS Apps preset, group 568 Modify):** works. The check is a real
  write, not a mode comparison.

## Build plan

| File | Change |
|---|---|
| `deploy/docker/server/Dockerfile` | world-readable `/opt/maktaba`; venv seed per architecture |
| `cmd/maktaba-server/container.go` | write check, root and privileged refusal, exit 78 |
| `cmd/maktaba-server/internal/userinfo/` (new) | uid/name without passwd |
| `pipeline/src/maktaba_pipeline/.../venv_bootstrap.py` | `--no-index` from the seed |
| `.github/workflows/release.yml` | build the seed in `build-server-image` |
| `cloud/installers/docker/docker-compose.yml` | `user:` line with a comment; no `/media` catch-all (59.13) |
