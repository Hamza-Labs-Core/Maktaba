# The Windows app's server payload (Epic 38, story 38.11)

The Windows app (`Maktaba.exe`, the Tauri shell) carries the whole server in `<install>\server`,
so one Windows install runs everything. This directory builds the payload and the installer.

| File | What it does |
|---|---|
| `payload.sh` | builds `build/win-app/server` (layout below), writes the tauri overlay `build/win-app/tauri.win-app.conf.json` |
| `check.sh` | smoke test of the payload and, with `--installer`, a silent install over a client-only app, a reinstall and an uninstall of the installer |
| `test-hooks.sh` | the installer hooks on both kinds of `Maktaba.exe`, with stand-ins (no app build needed, only makensis) |
| `run-watched.ps1` | runs an installer for `check.sh` in a job object and reports every process it starts |
| `build-app.sh` | payload → `tauri build --bundles nsis` → check. `make win-app` runs it |
| `pins.env` | the third-party downloads: version, URL, SHA-256 |
| `payload_tool.py` | reads PE import tables: the DLLs Postgres needs, and the check that nothing loads from outside the payload (`test_payload_tool.py`) |
| [`apps/desktop/src-tauri/windows/installer-hooks.nsh`](../../../apps/desktop/src-tauri/windows/installer-hooks.nsh), `knows-server-flags.ps1` | the NSIS installer's hooks, and how the installer tells from the shell's source that the `Maktaba.exe` it packs takes their flags (below) |

```bash
make win-app            # the NSIS installer, unsigned, then a test install + uninstall
make win-app-payload    # only the payload, then check.sh
```

Run in Git Bash (Git for Windows) on Windows x64. Needs Go, uv, pnpm (web deps installed), Rust
(MSVC), GNU make (windows-latest: MinGW's `make`; on a PC for example Strawberry Perl's: `gmake
win-app`) and Visual Studio or its Build Tools with the C++ redistributable
(`Microsoft.VisualStudio.Component.VC.Redist.14.Latest`, part of the C++ tools Rust needs).
Output: `<target>/release/bundle/nsis/Maktaba_<version>_x64-setup.exe`, with `<target>` =
`apps/desktop/src-tauri/target` or `$CARGO_TARGET_DIR`.

## Layout

```
%LOCALAPPDATA%\Maktaba\        the NSIS install: current user, no administrator
  Maktaba.exe  uninstall.exe
  server\
    bin\       maktaba-server.exe (web UI embedded) maktaba-api.exe maktaba-streaming.exe
               maktaba-launcher.exe maktaba-cloudlink.exe
    postgres\  bin\{postgres,initdb,pg_ctl,pg_dump,pg_dumpall,pg_restore,psql,pg_isready,
                    pg_upgrade,pg_controldata,pg_resetwal,pg_basebackup}.exe
                   + the DLLs they and the extensions load + the Visual C++ runtime
               lib\  share\
    ffmpeg\    ffmpeg.exe ffprobe.exe
    python\    CPython 3.12 (python.exe), stdlib precompiled
    pipeline\  maktaba_pipeline-<v>-py3-none-any.whl  constraints.txt
    migrations\  the database's schema (shared/db/migrations): maktaba-api finds it beside bin\
    licenses\  postgresql*.txt msvc-runtime.txt ffmpeg*.txt python.txt
    payload.json   versions, sources, SHA-256s (the supervisor compares it across updates)
  server.toml  keys\  data\  postgres\  venv\  logs\ …   the state: never the installer's
```

Not in the app: the CUDA / torch wheels and the models. The first run builds the venv from
`pipeline\` with the bundled Python and downloads them (38.7).

Size (2026-10-21, main 0e106fa5): payload 432 MB on disk, 3956 files — ffmpeg 195 MB (two
static programs), Go binaries 89 MB, Postgres 87 MB (ICU's data 28 MB, message translations
22 MB), Python 61 MB, pipeline 1.6 MB. The installer (NSIS, solid LZMA) is 115 MiB; making
it takes makensis about 5 minutes.

## Where each piece comes from

| Piece | Source | Why | Licence |
|---|---|---|---|
| Go binaries | this repo, `make server build-go` (windows/amd64, `-tags embed_web`, release ldflags) + the launcher, copied to `.exe` names | the same envelope as `release.yml` | this repo's |
| PostgreSQL 16.15 | [theseus-rs/postgresql-binaries](https://github.com/theseus-rs/postgresql-binaries) `x86_64-pc-windows-msvc` zip (EDB's Windows build), SHA-256 from the release asset's digest | every contrib extension, OpenSSL and ICU beside the programs, pg_dump/psql/pg_basebackup; the archive pgbin pins for windows/amd64 (Epic 37). Major 16 = compose and CI | PostgreSQL Licence; the DLLs' (OpenSSL Apache-2.0, ICU, zlib, libxml2/libxslt, lz4, zstd, gettext / libiconv / winpthreads LGPL) in `postgresql-third-party.txt` |
| Visual C++ runtime | the build machine's Visual Studio: `VC\Redist\MSVC\<v>\x64\Microsoft.VC14x.CRT` (vswhere), checked to be signed by Microsoft; the version is in `payload.json` | Postgres's DLLs are built with Visual C++ and a PC without the Redistributable has no `msvcp140.dll` (ICU's). python-build-standalone carries `vcruntime140.dll` only, and the set must come from one release. App-local, as [Microsoft documents](https://learn.microsoft.com/cpp/windows/determining-which-dlls-to-redistribute) | Visual Studio's Distributable Code |
| ffmpeg / ffprobe 8.1.2 | [gyan.dev](https://www.gyan.dev/ffmpeg/builds/) "essentials" build from its GitHub mirror [GyanD/codexffmpeg](https://github.com/GyanD/codexffmpeg), SHA-256 from the release asset's digest | static; has what the TV runs on Windows: `h264_nvenc`, `-hwaccel cuda` (`scale_cuda`), libx264, loudnorm, subtitles. The "full" build only adds codecs Maktaba does not use (its zip is 266 MB, not 110) | **GPLv3** (`--enable-gpl --enable-version3`) — see below |
| CPython 3.12.15 | [astral-sh/python-build-standalone](https://github.com/astral-sh/python-build-standalone) `install_only_stripped`, SHA-256 from the release asset's digest | relocatable, what uv itself installs; the Mac's release | PSF; the bundled libraries' in `python.txt` |
| pipeline wheel + constraints | `deploy/packaging/node/build-release.sh` (the node release's; it makes its Windows zip with Python's zipfile where there is no `zip`, as in Git for Windows) | one build of the pipeline for every install | this repo's |

Left out of the Postgres archive: StackBuilder and its wxWidgets DLLs, `include\`, the import
libraries, the client tools and test programs, and PL/Perl, PL/Python and PL/Tcl (their
runtimes are not in the payload, so they could never load). `payload.sh` finds the DLLs the
programs and the extensions need by reading their import tables (`payload_tool.py closure`),
and `check.sh` fails on any PE file that imports what neither the payload nor Windows has.
Left out of Python: Tcl/Tk, IDLE, turtledemo.

Bumping a piece: change `pins.env` (version, URL, SHA-256 from the publisher's release),
`make win-app`, and the CI gate re-runs `check.sh`.

**GPL (ffmpeg).** As on the Mac: ffmpeg runs as separate programs, so Maktaba's own code is
not affected, but distributing the binaries carries the GPL's source obligation.
`licenses\ffmpeg.txt` points at ffmpeg's source and the commit the build was made from, and
`ffmpeg-build.txt` lists every library linked in; mirroring `ffmpeg-8.1.2.tar.xz` as a release
asset settles it for good (follow-up). The LGPL DLLs of the Postgres build (libintl, libiconv,
winpthreads) are dynamically linked and replaceable; their sources are upstream's.

## The installer and its hooks

`payload.sh`'s overlay makes the NSIS installer current-user (`%LOCALAPPDATA%\Maktaba`, no
administrator), puts the payload in `server\` and includes `installer-hooks.nsh` (Epic 38's
`contract-agent.md`, "Installer hooks"):

| Hook | Runs | Why |
|---|---|---|
| `NSIS_HOOK_PREINSTALL` | `"$INSTDIR\Maktaba.exe" --server-stop`, when the installed one takes the flags | the payload's files are in use while the server runs |
| `NSIS_HOOK_POSTINSTALL` | `"$INSTDIR\Maktaba.exe" --server-start`, when the new one takes them | starts the server from the new payload (nothing on a first install: setup registers the task) |
| `NSIS_HOOK_PREUNINSTALL` | `--server-stop`, then `--server-remove`, when the installed one takes them; not during an update (`$UpdateMode` 1) | no sign-in task is left pointing at a removed program |

The uninstaller removes only the files the installer wrote; the state beside them stays
(`check.sh --installer` installs into a folder that holds one and checks it is untouched).

**Which `Maktaba.exe` takes the flags.** Only a shell with story 38.3 does. One from before it
(the client-only app, which installs to the same `%LOCALAPPDATA%\Maktaba` under the same name,
and this branch's own shell until 38.3 is merged) opens its window for any argument and never
exits, so a hook would hold the install until someone closed it. So the hooks ask only a
`Maktaba.exe` known to take them:

- the one this installer packs: decided when makensis compiles the installer, from the
  shell's source in this tree (`knows-server-flags.ps1`: the quoted literals `"--server-start"`,
  `"--server-stop"` and `"--server-remove"` in the code, not a comment, of
  `apps/desktop/src-tauri/src/*.rs`), which the same `tauri build` compiled into that
  `Maktaba.exe`. Not from the program: a release build need not hold the flags' text at all
  (LLVM compiles `arg == "--server-remove"` into a length and integer compares), so reading
  the binary says "no" for a build that takes them;
- the one already installed (before an install, an update, an uninstall): the installer that
  put a flag-taking `Maktaba.exe` there recorded its size and time as `MaktabaServerFlags` in
  the uninstall key, and the hook compares them with the file there now. A client-only
  install has no record; a `Maktaba.exe` replaced since (a client-only installer over a server
  install) no longer matches it; the uninstaller deletes the key, so the record goes with the
  install. A reinstall of the same build, and an update between two flag-taking builds, find
  their record.

A file in the payload (`server\payload.json`) would not do: it tells a server build from the
client-only app, but not a server build whose shell predates 38.3 (this branch's today).

`test-hooks.sh` tests the hooks on both kinds of `Maktaba.exe` with stand-ins and stand-in
sources (a fresh install, a reinstall, an update to a build from before the flags, the
client-only app there with or without a stale record, an uninstall, the uninstall an update
runs); `make win-app` runs it after the build. `check.sh --installer` decides as the installer
does, from this tree's source, and checks that against the program: the installed
`Maktaba.exe --server-start` must exit by itself when the source takes the flags and keep
running (its window) when it does not. It installs over a client-only app (a stand-in
`Maktaba.exe` that takes no flag and never exits, with its registration and a stale record),
reinstalls and uninstalls, and fails on any hook still running after 20 s; where the shell
takes the flags, on a missing record or when the hooks did not run `Maktaba.exe` with exactly
the flags each step calls for (`run-watched.ps1` runs each installer in a job object, which
tells of every process it starts, so even a hook run of a few milliseconds is seen with its
command line); where it takes none, on any run of `Maktaba.exe` by a hook at all.

`check.sh --installer` changes this user's registry while it runs (the uninstall entry, the
`maktaba://` links, `HKCU\Software\<publisher>\Maktaba` for the last install folder) and puts
it back. It refuses to run where Maktaba is installed for the user, its sign-in task exists or
`maktaba://` is registered, and installs with `/NS` (no shortcuts). The hooks' app runs with
`LOCALAPPDATA` and WebView2's data in its scratch folder; an empty
`%LOCALAPPDATA%\com.hamzalabs.maktaba` a shell window would make is removed afterwards.

## Signing

Not done: Authenticode signing is the owner's decision (Epic 38, "Owner decisions"). Unsigned,
SmartScreen warns on the first run of a downloaded installer ("More info" → "Run anyway"), and
some antivirus tools are stricter with unsigned programs that start at sign-in. A local build
runs unsigned.

What signing takes: an OV code-signing certificate (or Azure Trusted Signing), given to
`desktop-release.yml` as `WINDOWS_CERTIFICATE` (base64 .pfx) and
`WINDOWS_CERTIFICATE_PASSWORD`; tauri then signs `Maktaba.exe`, the installer and the
uninstaller (`bundle.windows.certificateThumbprint` + `timestampUrl`, or `signCommand` for
Trusted Signing), and the payload's own programs (`server\bin\*.exe`) are signed with the same
certificate before bundling (signtool). The third-party programs are unsigned upstream (the
Visual C++ runtime is Microsoft's, signed). The updater key
(`TAURI_SIGNING_PRIVATE_KEY(_PASSWORD)`, `plugins.updater.pubkey`) is separate from this.
